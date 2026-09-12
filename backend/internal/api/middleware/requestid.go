// Package middleware HTTP 中间件（T016/T017）。
package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey string

const (
	CtxRequestID ctxKey = "request_id"
	CtxPrincipal ctxKey = "principal"
	CtxActor     ctxKey = "actor" // Actor{ID,Username,Role}
)

// RequestID 注入链路 ID（审计 request_id 关联，contracts/README.md 错误模型）。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), CtxRequestID, id)))
	})
}

func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(CtxRequestID).(string)
	return v
}
