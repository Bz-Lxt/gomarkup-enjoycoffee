package flavorscore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

// Score 结构体本身没有 JSON 标签，早期它被直接塞进响应，
// 前端会收到 AcidityX10 这样的 PascalCase 字段。这个测试锁住修复后的形态。
func TestViewSerializesSnakeCaseOnly(t *testing.T) {
	sc := &Score{
		ID:           7,
		BrewID:       42,
		BeanID:       3,
		AcidityX10:   85,
		SweetX10:     70,
		AromaX10:     90,
		AftertoneX10: 65,
		BodyX10:      75,
		BitterX10:    30,
		Note:         "柑橘明亮",
		ScoredAt:     time.Date(2026, 3, 14, 9, 30, 0, 0, domain.Beijing),
	}

	raw, err := json.Marshal(sc.View())
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	for key := range got {
		if strings.ToLower(key) != key {
			t.Errorf("字段 %q 含大写字母，违反全站 snake_case 契约", key)
		}
	}

	// 抽查几个前端一定会用到的字段确实存在，避免"全小写"因为字段被删光而空过。
	for _, want := range []string{
		"brew_id", "acidity_x10", "acidity_text", "total_x10", "total_text", "scored_at",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("缺少字段 %q", want)
		}
	}

	if got["acidity_text"] != "8.5" {
		t.Errorf("acidity_text 应为 \"8.5\"，实际 %v", got["acidity_text"])
	}
	if got["total_x10"] != float64(415) {
		t.Errorf("total_x10 应为 415，实际 %v", got["total_x10"])
	}
	if got["total_text"] != "41.5" {
		t.Errorf("total_text 应为 \"41.5\"，实际 %v", got["total_text"])
	}
	// 展示时刻走项目统一格式，不是 RFC3339
	if got["scored_at"] != "2026-03-14 09:30:00" {
		t.Errorf("scored_at 格式不符，实际 %v", got["scored_at"])
	}
}

// 「尚未评分」必须在 JSON 里表达成 null。
// 若 View() 对 nil 返回零值结构体，前端会收到一份全零的假评分，
// 把"没打分"渲染成"打了 0 分"。
func TestNilScoreViewMarshalsToNull(t *testing.T) {
	var sc *Score
	raw, err := json.Marshal(map[string]any{"score": sc.View()})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if string(raw) != `{"score":null}` {
		t.Errorf("nil 评分应序列化为 null，实际 %s", raw)
	}
}

func TestFormatScoreX10(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0.0"},
		{5, "0.5"},
		{100, "10.0"},
		{85, "8.5"},
		{600, "60.0"},
		{415, "41.5"},
		{-25, "-2.5"},
	}
	for _, c := range cases {
		if got := domain.FormatScoreX10(c.in); got != c.want {
			t.Errorf("FormatScoreX10(%d) = %q，期望 %q", c.in, got, c.want)
		}
	}
}
