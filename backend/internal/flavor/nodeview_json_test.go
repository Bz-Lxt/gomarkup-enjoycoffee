package flavor

import (
	"encoding/json"
	"strings"
	"testing"
)

// NodeView 内嵌了 Node 并经 /flavors/search 直接出网。
// 两者最初都没有 JSON 标签，前端会收到 ID / Name / SortOrder 这类
// PascalCase 字段，与全站 snake_case 契约不符。这个测试锁住修复后的形态。
func TestNodeViewSerializesSnakeCaseOnly(t *testing.T) {
	parent := int64(1)
	nv := &NodeView{
		Node: Node{
			ID:        9,
			ParentID:  &parent,
			Name:      "柠檬",
			Color:     "#E8D44D",
			Icon:      "lemon",
			SortOrder: 2,
			Builtin:   true,
		},
		Depth:              1,
		Path:               "柑橘 / 柠檬",
		Children:           []int64{},
		Ancestors:          []int64{1},
		DescendantCount:    0,
		DirectBeanCount:    3,
		AggregateBeanCount: 3,
	}

	raw, err := json.Marshal(nv)
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

	for _, want := range []string{
		"id", "parent_id", "name", "color", "icon", "sort_order", "builtin",
		"depth", "path", "children", "ancestors",
		"descendant_count", "direct_bean_count", "aggregate_bean_count",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("缺少字段 %q", want)
		}
	}

	// 时间戳是存储细节，不该出网 —— 出网就得处理时区与格式，
	// 而前端在搜索结果里完全用不到它。
	for _, unwanted := range []string{"created_at", "updated_at", "CreatedAt", "UpdatedAt"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("字段 %q 不应出现在搜索结果里", unwanted)
		}
	}
}
