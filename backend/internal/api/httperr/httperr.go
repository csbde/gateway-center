// Package httperr 统一错误模型（T014，contracts/README.md）。
// 结构：{error:{code,message,request_id,details[{field,hint}]}}
// 每条错误信息含资源定位与修复建议（NFR-USE-01）。
package httperr

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

type Detail struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message,omitempty"`
	ID      string `json:"id,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

type Body struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	RequestID string   `json:"request_id"`
	Details   []Detail `json:"details,omitempty"`
}

// APIError 携带 HTTP 状态与契约错误码。
type APIError struct {
	Status  int
	Code    string
	Message string
	Details []Detail
	Err     error // 内部原因（仅日志）
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }
func (e *APIError) Unwrap() error { return e.Err }

func newErr(status int, code, msg string, details []Detail) *APIError {
	return &APIError{Status: status, Code: code, Message: msg, Details: details}
}

// ---- 构造器（与 contracts/README.md 错误模型表一一对应）----

func ValidationFailed(msg string, details ...Detail) *APIError {
	return newErr(http.StatusBadRequest, "VALIDATION_FAILED", msg, details)
}
func Unauthenticated() *APIError {
	return newErr(http.StatusUnauthorized, "UNAUTHENTICATED", "登录状态无效或已过期，请重新登录", nil)
}
func Forbidden(what string) *APIError {
	return newErr(http.StatusForbidden, "FORBIDDEN", "当前角色无权执行："+what, nil)
}
func NotFound(what string) *APIError {
	return newErr(http.StatusNotFound, "NOT_FOUND", what+"不存在或已被删除", nil)
}
func Conflict(code, msg string, details ...Detail) *APIError {
	return newErr(http.StatusConflict, code, msg, details)
}
func ConcurrentEdit(currentVersion int64) *APIError {
	return newErr(http.StatusConflict, "CONCURRENT_EDIT",
		"该资源已被他人修改，请刷新后重试",
		[]Detail{{Field: "expected_version", Hint: "服务器当前 row_version=" + itoa(currentVersion)}})
}
func DependencyBlocked(resource string, referrers []Detail) *APIError {
	return newErr(http.StatusConflict, "DEPENDENCY_BLOCKED",
		resource+" 正被其他资源引用，无法删除；请先解除依赖或按 Disable→Archive→软删 处置",
		referrers)
}
func DeployInProgress(nodeID string) *APIError {
	return newErr(http.StatusConflict, "DEPLOY_IN_PROGRESS",
		"节点 "+nodeID+" 已有进行中的部署，请等待其结束", nil)
}
func PipelineBlocked(msg string, details ...Detail) *APIError {
	return newErr(http.StatusUnprocessableEntity, "PIPELINE_BLOCKED", msg, details)
}
func Newf(status int, code, msg string) *APIError {
	return newErr(status, code, msg, nil)
}
func GatewayUnreachable(nodeID string, err error) *APIError {
	return &APIError{Status: http.StatusBadGateway, Code: "GATEWAY_UNREACHABLE",
		Message: "无法连接节点 " + nodeID + " 的 Traefik API", Err: err}
}
func Internal(err error) *APIError {
	return &APIError{Status: http.StatusInternalServerError, Code: "INTERNAL",
		Message: "服务内部错误，请通过 request_id 联系管理员", Err: err}
}

// Write 将错误以统一体输出；requestID 由中间件注入 ctx。
func Write(w http.ResponseWriter, r *http.Request, e *APIError, requestID string) {
	if requestID == "" {
		requestID = uuid.NewString()
	}
	if e.Status >= 500 {
		slog.Error("request failed", "request_id", requestID, "code", e.Code, "err", e.Err)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.Status)
	json.NewEncoder(w).Encode(map[string]any{"error": Body{
		Code: e.Code, Message: e.Message, RequestID: requestID, Details: e.Details,
	}})
}

// From 将任意 error 归一化为 APIError（未知错误→500）。
func From(err error) *APIError {
	if err == nil {
		return nil
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae
	}
	return Internal(err)
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
