package httpx

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

// PathID 解析路径参数中的 int64 ID。
func PathID(r *http.Request, name string) (int64, error) {
	raw := chi.URLParam(r, name)
	if raw == "" {
		return 0, domain.Validation("MISSING_PATH_PARAM", "缺少路径参数 "+name)
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.Validation("INVALID_PATH_PARAM", "路径参数 "+name+" 必须是正整数").
			WithField(name, "收到 "+raw)
	}
	return id, nil
}

// QueryInt 读取整型查询参数，缺省时返回 def。
func QueryInt(r *http.Request, name string, def int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, domain.Validation("INVALID_QUERY_PARAM", "查询参数 "+name+" 必须是整数").
			WithField(name, "收到 "+raw)
	}
	return v, nil
}

// QueryInt64 读取 int64 查询参数，缺省时返回 0。
func QueryInt64(r *http.Request, name string) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, domain.Validation("INVALID_QUERY_PARAM", "查询参数 "+name+" 必须是整数").
			WithField(name, "收到 "+raw)
	}
	return v, nil
}

// QueryInt64List 读取逗号分隔的 int64 列表，如 flavor_ids=3,7,12。
//
// 逗号分隔而非重复键（?id=3&id=7）：前者在浏览器地址栏里可读性好得多，
// 用户手动改 URL 做筛选实验时不容易出错。
func QueryInt64List(r *http.Request, name string) ([]int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil || v <= 0 {
			return nil, domain.Validation("INVALID_QUERY_PARAM",
				"查询参数 "+name+" 必须是逗号分隔的正整数列表").
				WithField(name, "无法解析 "+p)
		}
		out = append(out, v)
	}
	return out, nil
}

// QueryStringList 读取逗号分隔的字符串列表，自动去空白与空项。
func QueryStringList(r *http.Request, name string) []string {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// QueryString 读取字符串查询参数。
func QueryString(r *http.Request, name string) string {
	return strings.TrimSpace(r.URL.Query().Get(name))
}

// QueryBool 读取布尔查询参数。接受 true/1/yes/on 及其反面，其余视为缺省。
func QueryBool(r *http.Request, name string, def bool) bool {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name)))
	switch raw {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return def
	}
}

// Pagination 是解析后的分页参数。
type Pagination struct {
	Page     int
	PageSize int
	Offset   int
	Limit    int
}

const (
	defaultPageSize = 20
	maxPageSize     = 200
)

// ParsePagination 解析 page / page_size。
//
// 上限 200 而非不限：一次返回全部豆子在本项目的数据量下是可行的，
// 但每条豆子的响应里含新鲜度分段与雷达聚合，无上限会让某天数据变多后
// 突然出现一个几 MB 的响应，而且是在前端渲染时才暴露出来。
func ParsePagination(r *http.Request) (Pagination, error) {
	page, err := QueryInt(r, "page", 1)
	if err != nil {
		return Pagination{}, err
	}
	size, err := QueryInt(r, "page_size", defaultPageSize)
	if err != nil {
		return Pagination{}, err
	}

	if page < 1 {
		page = 1
	}
	switch {
	case size < 1:
		size = defaultPageSize
	case size > maxPageSize:
		size = maxPageSize
	}

	return Pagination{
		Page:     page,
		PageSize: size,
		Offset:   (page - 1) * size,
		Limit:    size,
	}, nil
}
