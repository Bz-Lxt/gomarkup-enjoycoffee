package httpx

import (
	"net/http"
	"sort"
	"strings"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

// StrictQuery 包装 handler，拒绝它不认识的查询参数。
//
// 为什么需要它：请求体的未知字段早就会被拒（见 respond.go 的
// DisallowUnknownFields → UNKNOWN_FIELD），但查询参数一直是"读不到就用缺省值"。
// 这条不对称非常昂贵 —— 调用方把 flavor_ids 写成 node_ids 时，
// 后端不会报错，只会返回**未经筛选的全量数据**，而调用方以为筛选生效了。
// 类型检查抓不到（参数名是字符串），单测抓不到（后端行为完全正常），
// 只有肉眼比对前后端才能发现。本项目已经因此一次性漏过四处。
//
// 所以：允许清单写在路由旁边，参数名对不上就当场 400，并把正确的名字列出来。
// 拼错一个字符的代价从"数据静默错误"降到"一条明确的报错"。
//
// 允许清单为空表示该路由不接受任何查询参数。这是刻意的严格：
// 一个不该有参数的路径收到参数，说明调用方对契约的理解有偏差，
// 静默忽略只会让这个偏差留到更晚才暴露。
func StrictQuery(allowed []string, h http.HandlerFunc) http.HandlerFunc {
	set := make(map[string]struct{}, len(allowed))
	for _, k := range allowed {
		set[k] = struct{}{}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if raw := r.URL.RawQuery; raw != "" {
			var unknown []string
			for k := range r.URL.Query() {
				if _, ok := set[k]; !ok {
					unknown = append(unknown, k)
				}
			}
			if len(unknown) > 0 {
				// 排序后再报，让同一个错误每次的文案一致 ——
				// map 遍历顺序随机，否则测试和日志都会抖。
				sort.Strings(unknown)
				err := domain.Validation("UNKNOWN_QUERY_PARAM", "查询参数含未知键")
				for _, k := range unknown {
					err = err.WithField(k, unknownHint(k, allowed))
				}
				Fail(w, r, err)
				return
			}
		}
		h(w, r)
	}
}

// unknownHint 给出可操作的提示。
//
// 只说"未知参数"会让调用方去翻文档；把该路由接受的名字直接列出来，
// 大多数情况下（拼写笔误、下划线写法不一致）一眼就能看出该改成什么。
func unknownHint(got string, allowed []string) string {
	if len(allowed) == 0 {
		return "该路径不接受任何查询参数"
	}
	if near := closest(got, allowed); near != "" {
		return "该参数不被接受，是否想写 " + near + "？"
	}
	return "该参数不被接受。本路径接受：" + strings.Join(allowed, "、")
}

// closest 在允许清单里找一个与 got 足够接近的名字。
//
// 刻意不用纯编辑距离。真实写错的参数名几乎都不是敲错字母，而是**换了说法**：
// node_ids ↔ flavor_ids、match ↔ flavor_match、stage ↔ stages。
// 这几组的编辑距离都在 5 以上，任何能覆盖它们的距离阈值都会顺带把
// limit ↔ page_size 这种毫不相干的组合也算作"相近"，提示反而误导人。
//
// 共同点是别的：它们共享下划线切出来的词元，或者一个是另一个的子串。
// 所以按「词元重合 → 子串包含 → 纯拼写笔误」三级判定，都不满足就不给建议。
func closest(got string, allowed []string) string {
	gotTokens := tokenSet(got)

	best, bestScore := "", 0
	bestDist := 1 << 30
	for _, a := range allowed {
		score, dist := 0, editDistance(got, a)

		switch {
		case sharedTokens(gotTokens, tokenSet(a)) > 0:
			// 词元重合越多越可信：flavor_node_ids → flavor_ids 重合两个词元，
			// 比只重合 ids 的候选更可能是用户想写的那个。
			score = 100 + sharedTokens(gotTokens, tokenSet(a))
		case contains(got, a):
			// stage → stages 这类单复数差异。限定最短 4 字符，
			// 否则 q 会和任何含 q 的名字"相近"。
			score = 50
		case dist <= 2:
			// 真正的敲错字母，如 keywrod → keyword
			score = 10
		}

		if score > bestScore || (score == bestScore && score > 0 && dist < bestDist) {
			best, bestScore, bestDist = a, score, dist
		}
	}
	return best
}

func tokenSet(s string) map[string]struct{} {
	parts := strings.Split(s, "_")
	out := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		if p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}

func sharedTokens(a, b map[string]struct{}) int {
	n := 0
	for k := range a {
		if _, ok := b[k]; ok {
			n++
		}
	}
	return n
}

// contains 判断两个名字是否存在子串包含关系，短名字至少 4 字符。
func contains(a, b string) bool {
	if len(a) < 4 || len(b) < 4 {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

func editDistance(a, b string) int {
	// 只在报错路径上跑，行数远比常数因子重要，所以用最朴素的两行滚动数组。
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
