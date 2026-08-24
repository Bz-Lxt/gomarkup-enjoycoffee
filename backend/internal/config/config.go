// Package config 集中管理运行期配置，全部来源于环境变量。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// PourSourceMode 决定注水节点数据的来源通道。
//
// 这是 README §7 所述"模拟与切换"的唯一开关。三种模式共享同一套下游处理逻辑，
// 差异仅在于谁来产生 pour 事件，因此模拟通道不构成对真实实现的替换。
type PourSourceMode string

const (
	// PourSourceManual 仅接受前端手动打点。零依赖，任何环境都可用。
	PourSourceManual PourSourceMode = "manual"
	// PourSourceSimulator 启用内置流速模拟器，按物理模型生成连续注水曲线。
	PourSourceSimulator PourSourceMode = "simulator"
	// PourSourceDevice 对接真实智能秤：设备按公开的 WebSocket 协议直接推流。
	PourSourceDevice PourSourceMode = "device"
)

// SimulatorEnabled 报告该模式下是否应启动内置流速模拟器。
func (m PourSourceMode) SimulatorEnabled() bool { return m == PourSourceSimulator }

// DeviceIngestEnabled 报告该模式下经 WebSocket 进来的注水节点是否来自真实设备。
//
// 这两个判定挂在模式类型上而不是 Config 上，是为了让 ws.Hub 也能用 ——
// Hub 只持有模式而不持有整个 Config（它只需要这一件事）。若各处自己写
// `m == PourSourceDevice`，"每种模式意味着什么"就会散落在多个文件里，
// 加第四种模式时必然漏掉一处。
func (m PourSourceMode) DeviceIngestEnabled() bool { return m == PourSourceDevice }

// Config 是应用的完整运行期配置。
type Config struct {
	Env      string
	LogLevel string
	LogJSON  bool

	HTTPAddr        string
	ShutdownTimeout time.Duration

	DatabaseURL    string
	DBMaxConns     int32
	DBConnTimeout  time.Duration
	MigrateOnStart bool
	SeedDemoData   bool

	CORSOrigins []string

	PourSource PourSourceMode

	// FlavorCacheRefresh 控制风味树内存快照的兜底重建周期。
	// 正常路径是写操作后主动失效，此处只是防御性兜底，避免任何遗漏的写路径
	// 导致缓存永久陈旧。
	FlavorCacheRefresh time.Duration
}

// Load 从环境变量装载配置并做完整性校验。
func Load() (*Config, error) {
	c := &Config{
		Env:                getEnv("APP_ENV", "development"),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
		HTTPAddr:           getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:        getEnv("DATABASE_URL", ""),
		ShutdownTimeout:    getDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
		DBConnTimeout:      getDuration("DB_CONN_TIMEOUT", 30*time.Second),
		FlavorCacheRefresh: getDuration("FLAVOR_CACHE_REFRESH", 5*time.Minute),
		DBMaxConns:         int32(getInt("DB_MAX_CONNS", 10)),
		MigrateOnStart:     getBool("MIGRATE_ON_START", true),
		SeedDemoData:       getBool("SEED_DEMO_DATA", true),
		PourSource:         PourSourceMode(strings.ToLower(getEnv("POUR_SOURCE_MODE", string(PourSourceManual)))),
	}

	c.LogJSON = getBool("LOG_JSON", c.Env == "production")
	c.CORSOrigins = splitAndTrim(getEnv("CORS_ORIGINS", "http://localhost:31411"))

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	var problems []string

	if strings.TrimSpace(c.DatabaseURL) == "" {
		problems = append(problems, "DATABASE_URL 未设置")
	}
	if strings.TrimSpace(c.HTTPAddr) == "" {
		problems = append(problems, "HTTP_ADDR 不能为空")
	}
	switch c.PourSource {
	case PourSourceManual, PourSourceSimulator, PourSourceDevice:
	default:
		problems = append(problems, fmt.Sprintf(
			"POUR_SOURCE_MODE=%q 非法，可选值: manual | simulator | device", c.PourSource))
	}
	if c.DBMaxConns < 1 {
		problems = append(problems, "DB_MAX_CONNS 必须 >= 1")
	}
	if len(c.CORSOrigins) == 0 {
		problems = append(problems, "CORS_ORIGINS 不能为空，否则前端将全部被拒")
	}

	if len(problems) > 0 {
		return errors.New("配置校验失败: " + strings.Join(problems, "; "))
	}
	return nil
}

// IsProduction 用于决定是否屏蔽 debug 输出与详细错误暴露。
func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production")
}

// SimulatorEnabled 报告是否允许启动内置流速模拟器。
func (c *Config) SimulatorEnabled() bool { return c.PourSource.SimulatorEnabled() }

// DeviceIngestEnabled 报告是否有真实设备在经 WebSocket 推流。
func (c *Config) DeviceIngestEnabled() bool { return c.PourSource.DeviceIngestEnabled() }

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getInt(key string, def int) int {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	return n
}

func getBool(key string, def bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	return b
}

func getDuration(key string, def time.Duration) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
