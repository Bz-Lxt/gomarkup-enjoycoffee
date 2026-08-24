// Package httpx 提供统一的 HTTP 响应封装、请求解码与中间件。
//
// 存在的理由是"响应形态只有一处定义"。若每个 handler 各自 json.NewEncoder(w).Encode，
// 前端就要面对若干种彼此微妙不同的成功/失败结构，错误处理只能靠猜。
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// Envelope 是全部 API 响应的统一外层结构。
//
// 无论成功失败都带 ok 字段，前端只需判断一个布尔值即可分流，
// 不必依赖 HTTP 状态码（状态码在经过代理、CDN 时并不总是可靠地透传）。
type Envelope struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
	// Warnings 承载"操作成功但有事要告诉你"的信息，如剩余豆量已见底、
	// 风味树深度过深。它不是错误，因此不能塞进 error 字段让前端当失败处理。
	Warnings []string `json:"warnings,omitempty"`
	Meta     *Meta    `json:"meta,omitempty"`
}

// ErrorBody 是错误响应的载荷。
type ErrorBody struct {
	Kind    string              `json:"kind"`
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Fields  []domain.FieldError `json:"fields,omitempty"`
}

// Meta 承载分页与耗时信息。
type Meta struct {
	Total    int   `json:"total,omitempty"`
	Page     int   `json:"page,omitempty"`
	PageSize int   `json:"page_size,omitempty"`
	TookMs   int64 `json:"took_ms,omitempty"`
}

// OK 写出成功响应。
func OK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, Envelope{OK: true, Data: data})
}

// OKWithWarnings 写出带警告的成功响应。
func OKWithWarnings(w http.ResponseWriter, data any, warnings []string) {
	writeJSON(w, http.StatusOK, Envelope{OK: true, Data: data, Warnings: warnings})
}

// OKWithMeta 写出带分页元信息的成功响应。
func OKWithMeta(w http.ResponseWriter, data any, meta *Meta) {
	writeJSON(w, http.StatusOK, Envelope{OK: true, Data: data, Meta: meta})
}

// Created 写出 201 响应。
func Created(w http.ResponseWriter, data any, warnings []string) {
	writeJSON(w, http.StatusCreated, Envelope{OK: true, Data: data, Warnings: warnings})
}

// NoContent 写出 204 响应。
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Fail 把任意 error 转为规范化的错误响应。
//
// 5xx 与 4xx 的日志级别刻意不同：客户端把参数填错不是服务的问题，
// 用 error 级别记录会让真正的故障淹没在噪声里。
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	de := domain.AsDomain(err)
	status := de.HTTPStatus()

	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"code", de.Code,
	}
	if status >= 500 {
		// cause 只进日志不进响应：它可能含 SQL 片段或连接串
		attrs = append(attrs, "detail", de.Error())
		logger.Error("请求处理失败", attrs...)
	} else {
		attrs = append(attrs, "message", de.Message)
		logger.Debug("请求被拒绝", attrs...)
	}

	body := &ErrorBody{
		Kind:    string(de.Kind),
		Code:    de.Code,
		Message: de.Message,
		Fields:  de.Fields,
	}
	if status >= 500 {
		// 内部错误对外只给一句人类可读的话，细节留在服务端日志
		body.Message = "服务内部错误，请稍后重试"
	}
	writeJSON(w, status, Envelope{OK: false, Error: body})
}

// FailWithPayload 写出失败响应，但保留一份诊断数据。
//
// 用于就绪探针这类"请求本身成功了，但被探测的东西没就绪"的场景：
// 状态码必须是 503 才能让编排系统正确决策，而运维需要 data 里的
// 逐项明细才能知道到底是哪一项没起来。
func FailWithPayload(w http.ResponseWriter, status int, code, msg string, data any) {
	writeJSON(w, status, Envelope{
		OK:   false,
		Data: data,
		Error: &ErrorBody{
			Kind:    string(domain.KindPrecondition),
			Code:    code,
			Message: msg,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, env Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 业务响应不应被任何层级缓存：豆子的新鲜度、萃取判定都随时间变化
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(env); err != nil {
		// 走到这里通常是客户端提前断开。响应头已发出，无法再改状态码，
		// 只能记录后放弃。
		logger.Debug("响应写出失败", "error", err.Error())
	}
}

// maxBodyBytes 限制请求体大小。1 MiB 对本项目最大的请求体（一次冲煮的
// 数百个注水节点）仍有十倍以上余量，同时挡住无意或恶意的巨型请求。
const maxBodyBytes = 1 << 20

// DecodeJSON 解码请求体，把各类解码失败统一转为可读的领域校验错误。
//
// 直接把 json.Unmarshal 的原始错误抛给用户会产出
// "json: cannot unmarshal string into Go struct field ... of type int64"
// 这种只有 Go 程序员看得懂的文本。这里把它翻译成字段级提示。
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return domain.Validation("UNSUPPORTED_MEDIA_TYPE", "请求体必须是 application/json")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	// 拒绝未知字段：前端把字段名拼错时，静默忽略会让"我明明填了却没生效"
	// 变成一个极难排查的问题。宁可在第一次请求就报错。
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return translateDecodeError(err)
	}

	// 请求体里不允许跟第二个 JSON 值
	if dec.More() {
		return domain.Validation("MALFORMED_JSON", "请求体只能包含一个 JSON 对象")
	}
	return nil
}

func translateDecodeError(err error) error {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		maxErr    *http.MaxBytesError
	)

	switch {
	case errors.As(err, &syntaxErr):
		return domain.Validation("MALFORMED_JSON", "请求体不是合法的 JSON").
			WithField("_body", "第 "+strconv.FormatInt(syntaxErr.Offset, 10)+" 字节处语法错误")

	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "" {
			field = "_body"
		}
		return domain.Validation("TYPE_MISMATCH", "字段类型不正确").
			WithField(field, "期望 "+typeErr.Type.String()+"，收到 "+typeErr.Value)

	case errors.As(err, &maxErr):
		return domain.Validation("BODY_TOO_LARGE", "请求体过大，上限 1 MiB")

	case errors.Is(err, io.EOF):
		return domain.Validation("EMPTY_BODY", "请求体不能为空")

	case strings.HasPrefix(err.Error(), "json: unknown field "):
		name := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return domain.Validation("UNKNOWN_FIELD", "请求体含未知字段").
			WithField(name, "该字段不被接受，请检查拼写")

	default:
		return domain.Validation("MALFORMED_JSON", "请求体无法解析").WithCause(err)
	}
}
