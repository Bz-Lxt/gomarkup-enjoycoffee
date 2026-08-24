package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/config"
)

// checkOrigin 用给定的白名单构造一个 upgrader，返回它的 Origin 判定函数。
func checkOrigin(t *testing.T, allowlist ...string) func(origin, host string) bool {
	t.Helper()
	h := &Handlers{Cfg: config.Config{CORSOrigins: allowlist}}
	fn := h.newUpgrader().CheckOrigin
	return func(origin, host string) bool {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/ws/brews/1/pour", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return fn(r)
	}
}

// TestSameOriginHandshakeIsAlwaysAllowed 验证同源握手一律放行。
//
// 这是主路径：前端经 nginx 同源代理访问，Origin 与 Host 天然相同。
// 放行它不需要任何配置，因此换主机、换端口、走局域网都不会失效。
func TestSameOriginHandshakeIsAlwaysAllowed(t *testing.T) {
	// 白名单故意留一个与被测来源完全无关的值，确保放行是"同源"判出来的，
	// 而不是恰好命中了白名单。
	allow := checkOrigin(t, "http://example.invalid")

	cases := []struct{ origin, host string }{
		{"http://localhost:8081", "localhost:8081"},          // 交付后的标准端口
		{"http://127.0.0.1:31411", "127.0.0.1:31411"},        // 开发期
		{"http://frontend-user", "frontend-user"},            // 容器内服务名（QA）
		{"http://192.168.1.5:8081", "192.168.1.5:8081"},      // 局域网：手机架在电子秤旁
		{"http://coffee.local:8081", "coffee.local:8081"},    // 自定义域名
		{"https://coffee.example.com", "coffee.example.com"}, // 反代加 TLS
		{"http://LOCALHOST:8081", "localhost:8081"},          // 主机名大小写不敏感
	}
	for _, c := range cases {
		if !allow(c.origin, c.host) {
			t.Errorf("同源握手被拒：origin=%s host=%s —— "+
				"这会让实时注水曲线在该部署方式下完全不可用", c.origin, c.host)
		}
	}
}

// TestLANAccessWasTheRegression 单独钉住曾经的缺陷。
//
// 同源代理改造把 HTTP 侧的主机端口耦合消掉了，却漏掉了 WS 的 Origin 校验：
// 它仍只认 CORS 白名单加 localhost，于是从局域网 IP 访问会被静默拒绝 ——
// 而"把手机架在秤旁边看实时流速"恰恰是这个功能最典型的用法。
// 用户那侧只看到一条画不出来的曲线，真正的原因躲在服务端日志里。
func TestLANAccessWasTheRegression(t *testing.T) {
	// 白名单就是缺陷发生时 docker-compose.yml 里的那两个值
	allow := checkOrigin(t, "http://localhost:31411", "http://127.0.0.1:31411")

	if !allow("http://192.168.1.5:8081", "192.168.1.5:8081") {
		t.Error("从局域网 IP 访问时握手被拒 —— 这正是修复前的行为，不能回归")
	}
}

// TestCrossSiteHandshakeIsStillRejected 验证放宽同源判定没有削弱 CSRF 防护。
//
// 同源策略不适用于 WebSocket 握手：任何网站的脚本都能向本机的 WS 端点
// 发起连接。所以第三方 Origin 必须被拒 —— 否则用户开着咖啡应用时访问
// 恶意站点，对方就能读到实时注水数据、甚至替用户打点。
func TestCrossSiteHandshakeIsStillRejected(t *testing.T) {
	allow := checkOrigin(t, "http://localhost:31411")

	cases := []struct{ origin, host, why string }{
		{"http://evil.example.com", "localhost:8081", "第三方站点"},
		{"https://evil.example.com", "192.168.1.5:8081", "第三方站点（TLS）"},
		// 端口不同就不是同源。攻击者在同一台机器另开一个端口起服务，
		// 不该因为主机名相同就被当成自己人。
		{"http://coffee.local:9999", "coffee.local:8081", "同主机但不同端口"},
		{"http://sub.coffee.local:8081", "coffee.local:8081", "子域名不算同源"},
	}
	for _, c := range cases {
		if allow(c.origin, c.host) {
			t.Errorf("%s 的握手被放行了：origin=%s host=%s —— "+
				"WS 握手不受同源策略保护，放行等于交出实时数据的读写权",
				c.why, c.origin, c.host)
		}
	}
}

// TestLocalhostIsAllowedOnAnyPort 验证本机任意端口仍然放行。
//
// 开发期浏览器在 vite 的 5173 上、后端在另一个端口，属于合法的跨源，
// 把每个端口都写进白名单不现实。这条只对本机地址生效。
func TestLocalhostIsAllowedOnAnyPort(t *testing.T) {
	allow := checkOrigin(t, "http://localhost:31411")

	for _, origin := range []string{
		"http://localhost:5173",
		"http://127.0.0.1:4173",
		"http://[::1]:5173",
	} {
		if !allow(origin, "localhost:31410") {
			t.Errorf("本机来源 %s 被拒，开发期前端将连不上", origin)
		}
	}
}

// TestExplicitAllowlistStillWorks 验证白名单仍是有效的放行途径。
// 生产上若真有跨源前端，配置必须还能兜住。
func TestExplicitAllowlistStillWorks(t *testing.T) {
	allow := checkOrigin(t, "https://coffee.example.com/")

	// 白名单项带尾斜杠也应能匹配上
	if !allow("https://coffee.example.com", "api.example.com") {
		t.Error("白名单内的跨源来源被拒了")
	}
}

// TestMissingOriginIsAllowed 验证不带 Origin 的客户端不被拦。
//
// curl、设备网关、Playwright 的接口用例都不发 Origin。它们不受同源策略
// 约束，Origin 缺失本身不是攻击信号 —— 拦掉它只会挡住智能秤接入。
func TestMissingOriginIsAllowed(t *testing.T) {
	if !checkOrigin(t, "http://localhost:31411")("", "localhost:31410") {
		t.Error("不带 Origin 的非浏览器客户端被拒，智能秤将无法接入")
	}
}

// TestWildcardAllowsEverything 验证显式配了 * 时不再设限。
// 这是运维的逃生阀，配了就该真的生效。
func TestWildcardAllowsEverything(t *testing.T) {
	if !checkOrigin(t, "*")("http://anything.example.com", "localhost:8081") {
		t.Error("配置了 * 却仍拒绝，逃生阀失效")
	}
}

// TestMalformedOriginIsRejected 验证畸形 Origin 不会被误判成同源。
func TestMalformedOriginIsRejected(t *testing.T) {
	allow := checkOrigin(t, "http://localhost:31411")

	for _, origin := range []string{
		"null", // 沙箱 iframe / file:// 页面会发这个
		"://missing-scheme",
		"http://", // 没有 host
	} {
		if allow(origin, "coffee.example.com") {
			t.Errorf("畸形 Origin %q 被放行了", origin)
		}
	}
}

// TestSameOriginHelperComparesHostAndPortTogether 直接钉住比较逻辑本身。
func TestSameOriginHelperComparesHostAndPortTogether(t *testing.T) {
	cases := []struct {
		origin, host string
		want         bool
	}{
		{"http://a.example:8081", "a.example:8081", true},
		{"http://a.example:8081", "a.example:8082", false},
		{"http://a.example", "a.example:80", false}, // 不做默认端口归一化
		{"http://A.Example:8081", "a.example:8081", true},
		{"", "a.example", false},
		{"not a url at all", "a.example", false},
	}
	for _, c := range cases {
		if got := sameOrigin(c.origin, c.host); got != c.want {
			t.Errorf("sameOrigin(%q, %q) = %v，期望 %v", c.origin, c.host, got, c.want)
		}
	}
}
