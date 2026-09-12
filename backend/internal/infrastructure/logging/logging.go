// Package logging 初始化 slog 并注册敏感键打码 Handler（T003，research R7）。
// token/password/private_key/authorization 等键在任意 Handler 输出前强制打码。
package logging

import (
	"context"
	"log/slog"
	"os"

	"gateway-center/backend/internal/infrastructure/redactx"
)

// sensitiveAttrHandler 包装任意下游 Handler，对敏感键值替换为掩码。
type sensitiveAttrHandler struct{ next slog.Handler }

func (h *sensitiveAttrHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *sensitiveAttrHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, redactx.RedactText(r.Message), 0)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(h.maskAttr(a))
		return true
	})
	return h.next.Handle(ctx, nr)
}

func (h *sensitiveAttrHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	masked := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		masked[i] = h.maskAttr(a)
	}
	return &sensitiveAttrHandler{next: h.next.WithAttrs(masked)}
}

func (h *sensitiveAttrHandler) WithGroup(name string) slog.Handler {
	return &sensitiveAttrHandler{next: h.next.WithGroup(name)}
}

func (h *sensitiveAttrHandler) maskAttr(a slog.Attr) slog.Attr {
	if redactx.IsSensitiveKey(a.Key) {
		return slog.String(a.Key, redactx.MaskValue)
	}
	if a.Value.Kind() == slog.KindGroup {
		ga := a.Value.Group()
		vals := make([]any, len(ga))
		for i, g := range ga {
			vals[i] = h.maskAttr(g)
		}
		return slog.Group(a.Key, vals...)
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, redactx.RedactText(a.Value.String()))
	}
	return a
}

// New 返回 JSON 结构化日志器（生产形态；测试可注入 io.Discard）。
func New(level slog.Level) *slog.Logger {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(&sensitiveAttrHandler{next: base})
}
