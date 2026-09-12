// services.go：/services + /services/{id}/targets/*（T034，AC-003/UF-2）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/servicesvc"
)

func (h *Handler) ListService(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.Services.List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetService(w http.ResponseWriter, r *http.Request) {
	sv, err := h.Services.Get(r.Context(), urlParam(r, "id"))
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "服务"))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, sv)
}

func (h *Handler) CreateService(w http.ResponseWriter, r *http.Request) {
	var in servicesvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	sv, apiErr := h.Services.Create(r.Context(), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, sv)
}

func (h *Handler) UpdateService(w http.ResponseWriter, r *http.Request) {
	var in servicesvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	sv, apiErr := h.Services.Update(r.Context(), urlParam(r, "id"), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, sv)
}

func (h *Handler) EnableService(w http.ResponseWriter, r *http.Request) {
	h.setServiceEnabled(w, r, true)
}
func (h *Handler) DisableService(w http.ResponseWriter, r *http.Request) {
	h.setServiceEnabled(w, r, false)
}

func (h *Handler) setServiceEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	var in struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	_ = queryutil.DecodeStrict(r.Body, &in)
	sv, apiErr := h.Services.SetServiceEnabled(r.Context(), urlParam(r, "id"), enabled, in.ExpectedVersion, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, sv)
}

func (h *Handler) DeleteService(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Services.Delete(r.Context(), urlParam(r, "id"), actorID(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Targets ----

func (h *Handler) ListTargets(w http.ResponseWriter, r *http.Request) {
	ts, err := h.Services.Targets(r.Context(), urlParam(r, "id"))
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "服务"))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, map[string]any{"items": ts})
}

func (h *Handler) AddTarget(w http.ResponseWriter, r *http.Request) {
	var in servicesvc.TargetInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	t, apiErr := h.Services.AddTarget(r.Context(), urlParam(r, "id"), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, t)
}

func (h *Handler) UpdateTarget(w http.ResponseWriter, r *http.Request) {
	var in servicesvc.TargetInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	t, apiErr := h.Services.UpdateTarget(r.Context(), urlParam(r, "targetId"), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, t)
}

func (h *Handler) DeleteTarget(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Services.DeleteTarget(r.Context(), urlParam(r, "targetId"), actorID(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) EnableTarget(w http.ResponseWriter, r *http.Request) {
	h.setTargetEnabled(w, r, true)
}
func (h *Handler) DisableTarget(w http.ResponseWriter, r *http.Request) {
	h.setTargetEnabled(w, r, false)
}

func (h *Handler) setTargetEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	var in struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	_ = queryutil.DecodeStrict(r.Body, &in)
	// 仅翻 enabled：URL 留空触发同 URL 更新路径前先取原值——TargetInput.URL 空即 400，
	// 故这里经 UpdateTarget 的 fields 通道（url 必填校验在 Normalize；启用/禁用不动 url）
	t, apiErr := h.Services.SetTargetEnabled(r.Context(), urlParam(r, "targetId"), enabled, in.ExpectedVersion, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, t)
}
