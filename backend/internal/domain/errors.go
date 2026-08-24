// Package domain 定义跨层共享的领域错误、枚举与时间基准。
package domain

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Kind 是领域错误的分类，决定 HTTP 状态码映射。
type Kind string

const (
	// KindValidation 输入不满足业务约束，属调用方错误。
	KindValidation Kind = "VALIDATION"
	// KindNotFound 请求的资源不存在。
	KindNotFound Kind = "NOT_FOUND"
	// KindConflict 与现有数据状态冲突，如唯一约束、树环检测。
	KindConflict Kind = "CONFLICT"
	// KindPrecondition 前置条件不满足，如缺少 TDS 却要求测量模式判定。
	KindPrecondition Kind = "PRECONDITION_FAILED"
	// KindComputation 数学计算失败，如除零、定点数溢出。
	KindComputation Kind = "COMPUTATION_ERROR"
	// KindInternal 服务端内部故障，对外不暴露细节。
	KindInternal Kind = "INTERNAL"
)

// Error 是携带机器可读错误码与字段级明细的领域错误。
//
// 设计取舍：不使用裸 errors.New 冒泡到 handler 再靠字符串匹配判断状态码。
// 字符串匹配在需求演进中极其脆弱，且无法向前端提供可编程的错误码表。
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  []FieldError
	cause   error
}

// FieldError 定位到具体输入字段的校验失败，供前端在对应输入框下方渲染红字。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *Error) Error() string {
	var sb strings.Builder
	sb.WriteString(string(e.Kind))
	sb.WriteString("/")
	sb.WriteString(e.Code)
	sb.WriteString(": ")
	sb.WriteString(e.Message)
	for _, f := range e.Fields {
		sb.WriteString(fmt.Sprintf(" [%s: %s]", f.Field, f.Reason))
	}
	if e.cause != nil {
		sb.WriteString(" <- ")
		sb.WriteString(e.cause.Error())
	}
	return sb.String()
}

func (e *Error) Unwrap() error { return e.cause }

// HTTPStatus 把领域错误分类映射为 HTTP 状态码。
func (e *Error) HTTPStatus() int {
	switch e.Kind {
	case KindValidation:
		return http.StatusBadRequest
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindPrecondition:
		return http.StatusPreconditionFailed
	case KindComputation:
		// 计算失败的根因几乎总是非法输入（粉量为 0、量值超出物理范围），
		// 归到 422 而非 500，避免把用户错误伪装成服务故障。
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

// WithCause 挂载底层错误，仅用于服务端日志，不进入 API 响应。
func (e *Error) WithCause(err error) *Error {
	e.cause = err
	return e
}

// WithField 追加一条字段级明细。
func (e *Error) WithField(field, reason string) *Error {
	e.Fields = append(e.Fields, FieldError{Field: field, Reason: reason})
	return e
}

// ---- 构造器 ----

func Validation(code, msg string) *Error {
	return &Error{Kind: KindValidation, Code: code, Message: msg}
}

func NotFound(resource string, id any) *Error {
	return &Error{
		Kind:    KindNotFound,
		Code:    "RESOURCE_NOT_FOUND",
		Message: fmt.Sprintf("%s 不存在: %v", resource, id),
	}
}

func Conflict(code, msg string) *Error {
	return &Error{Kind: KindConflict, Code: code, Message: msg}
}

func Precondition(code, msg string) *Error {
	return &Error{Kind: KindPrecondition, Code: code, Message: msg}
}

func Computation(code, msg string) *Error {
	return &Error{Kind: KindComputation, Code: code, Message: msg}
}

func Internal(msg string) *Error {
	return &Error{Kind: KindInternal, Code: "INTERNAL_ERROR", Message: msg}
}

// AsDomain 提取链上的领域错误。非领域错误统一包装为 Internal，
// 以保证 handler 永远不会把底层实现细节（SQL 语句、连接串）泄露给客户端。
func AsDomain(err error) *Error {
	if err == nil {
		return nil
	}
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	return Internal("服务内部错误").WithCause(err)
}
