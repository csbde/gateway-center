// routes.go：/routes 简单模式 CRUD + 状态迁移（T035，AC-004/FR-014）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/routesvc"
)

func (h *Handler) ListRoute(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.Routes.List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetRoute(w http.ResponseWriter, r *http.Request) {
	v, apiErr := h.Routes.Get(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, v)
}

func (h *Handler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	var in routesvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	v, apiErr := h.Routes.Create(r.Context(), in, principalOf(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, v)
}

func (h *Handler) UpdateRoute(w http.ResponseWriter, r *http.Request) {
	var in routesvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	v, apiErr := h.Routes.Update(r.Context(), urlParam(r, "id"), in, principalOf(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, v)
}

func (h *Handler) EnableRoute(w http.ResponseWriter, r *http.Request) {
	h.routeStatus(w, r, "enabled")
}
func (h *Handler) DisableRoute(w http.ResponseWriter, r *http.Request) {
	h.routeStatus(w, r, "disabled")
}
func (h *Handler) ArchiveRoute(w http.ResponseWriter, r *http.Request) {
	h.routeStatus(w, r, "archived")
}

func (h *Handler) routeStatus(w http.ResponseWriter, r *http.Request, to string) {
	var in struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	_ = queryutil.DecodeStrict(r.Body, &in)
	v, apiErr := h.Routes.SetStatus(r.Context(), urlParam(r, "id"), to, in.ExpectedVersion, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, v)
}

func (h *Handler) DeleteRoute(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Routes.Delete(r.Context(), urlParam(r, "id"), actorID(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ValidateAdvancedRoute POST /routes/validate-advanced —— 高级模式语法验证（T060，FR-015）。
// 纯校验不写入；响应含固定 risk_notice（FR-016）。端点在 GatewayAdminOrAbove 组，服务层再复核。
func (h *Handler) ValidateAdvancedRoute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rule string `json:"rule"`
	}
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	res, apiErr := h.Routes.ValidateAdvanced(r.Context(), in.Rule, principalOf(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, res)
}

// PreviewRoute POST /routes/preview —— 向导实时规则回显（FR-014，零语法）。
func (h *Handler) PreviewRoute(w http.ResponseWriter, r *http.Request) {
	var in routesvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	rule, apiErr := h.Routes.Preview(r.Context(), in)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, map[string]any{"generated_rule_preview": rule})
}
