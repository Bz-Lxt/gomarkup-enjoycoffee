package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func run(t *testing.T, allowed []string, rawQuery string) (int, string) {
	t.Helper()
	called := false
	h := StrictQuery(allowed, func(http.ResponseWriter, *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodGet, "/x?"+rawQuery, nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code == http.StatusOK && !called {
		t.Fatal("返回 200 却没有调用下游 handler")
	}
	if rec.Code != http.StatusOK && called {
		t.Fatal("已经判定参数非法，却仍然调用了下游 handler")
	}
	return rec.Code, rec.Body.String()
}

func TestKnownParamsPassThrough(t *testing.T) {
	code, _ := run(t, []string{"flavor_ids", "match"}, "flavor_ids=1,2&match=ALL")
	if code != http.StatusOK {
		t.Fatalf("合法参数应通过，实际 HTTP %d", code)
	}
}

func TestEmptyQueryPassesThrough(t *testing.T) {
	code, _ := run(t, []string{"flavor_ids"}, "")
	if code != http.StatusOK {
		t.Fatalf("无参数请求应通过，实际 HTTP %d", code)
	}
}

// 这是整个机制存在的理由：把 flavor_ids 写成 node_ids 时，
// 旧行为是静默返回未筛选的全量数据，新行为是当场拒绝。
func TestMisspelledParamIsRejectedInsteadOfIgnored(t *testing.T) {
	code, body := run(t, []string{"flavor_ids", "match"}, "node_ids=1,2&match=ALL")
	if code != http.StatusBadRequest {
		t.Fatalf("未知参数应返回 400，实际 HTTP %d，响应 %s", code, body)
	}
	if !strings.Contains(body, "node_ids") {
		t.Errorf("报错必须点名是哪个参数出错，实际响应: %s", body)
	}
	if !strings.Contains(body, "flavor_ids") {
		t.Errorf("报错应提示正确的参数名 flavor_ids，否则调用方还得翻文档。实际响应: %s", body)
	}
}

func TestAllUnknownParamsAreReportedAtOnce(t *testing.T) {
	// 一次只报一个会让调用方来回试错。请求体校验已经是一次性返回全部字段错误，
	// 查询参数没有理由更差。
	_, body := run(t, []string{"keyword"}, "q=x&stage=y&limit=3")

	var env struct {
		Error struct {
			Code   string `json:"code"`
			Fields []struct {
				Field string `json:"field"`
			} `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	if env.Error.Code != "UNKNOWN_QUERY_PARAM" {
		t.Errorf("错误码应为 UNKNOWN_QUERY_PARAM，实际 %q", env.Error.Code)
	}
	if len(env.Error.Fields) != 3 {
		t.Fatalf("三个未知参数应一次全部报出，实际报了 %d 个: %s", len(env.Error.Fields), body)
	}
	// 顺序必须稳定，否则日志与测试都会随 map 遍历顺序抖动
	want := []string{"limit", "q", "stage"}
	for i, f := range env.Error.Fields {
		if f.Field != want[i] {
			t.Errorf("第 %d 个字段应是 %q（按字母序），实际 %q", i, want[i], f.Field)
		}
	}
}

func TestRouteWithNoParamsRejectsAnything(t *testing.T) {
	code, body := run(t, nil, "cascade=true")
	if code != http.StatusBadRequest {
		t.Fatalf("不接受参数的路径收到参数应报错，实际 HTTP %d", code)
	}
	if !strings.Contains(body, "不接受任何查询参数") {
		t.Errorf("空清单的提示应说明该路径不收参数，实际: %s", body)
	}
}

func TestHintSuggestsNearestNameForRealWorldConfusions(t *testing.T) {
	// 这几组都是本项目真实写错过的组合，提示必须能指到正确的名字
	cases := []struct {
		got     string
		allowed []string
		want    string
	}{
		{"node_ids", []string{"flavor_ids", "match"}, "flavor_ids"},
		{"flavor_node_ids", []string{"flavor_ids", "keyword"}, "flavor_ids"},
		{"stage", []string{"stages", "keyword"}, "stages"},
		{"q", []string{"keyword", "sort"}, ""},
		{"limit", []string{"page", "page_size"}, ""},
		{"match", []string{"flavor_match", "sort"}, "flavor_match"},
	}
	for _, c := range cases {
		got := closest(c.got, c.allowed)
		if got != c.want {
			t.Errorf("closest(%q, %v) = %q，期望 %q", c.got, c.allowed, got, c.want)
		}
	}
}

func TestHintFallsBackToFullListWhenNothingIsClose(t *testing.T) {
	_, body := run(t, []string{"keyword", "sort"}, "utm_source=twitter")
	if !strings.Contains(body, "keyword") || !strings.Contains(body, "sort") {
		t.Errorf("没有相近名字时应列出全部可用参数，实际: %s", body)
	}
}
