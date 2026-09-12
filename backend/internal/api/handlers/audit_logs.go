// audit_logs.go：/audit-logs 只读检索端点（T079，FR-038 / 宪法 X 仅追加，检索不限）。
// 支持 actor_id/action/resource_type/resource_id/from/to 过滤 + 分页；from/to 为 RFC3339。
package handlers

import (
	"net/http"
	"time"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

func (h *Handler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	f := pgstore.AuditFilter{
		ActorID:      r.URL.Query().Get("actor_id"),
		Action:       r.URL.Query().Get("action"),
		ResourceType: r.URL.Query().Get("resource_type"),
		ResourceID:   r.URL.Query().Get("resource_id"),
	}
	// from/to 解析失败即忽略该过滤项（不阻断检索，降级为无界）。
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = t
		}
	}
	items, total, err := h.Audits.Search(r.Context(), f, q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetAuditLog(w http.ResponseWriter, r *http.Request) {
	a, err := h.Audits.Get(r.Context(), urlParam(r, "id"))
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "审计记录"))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, a)
}
