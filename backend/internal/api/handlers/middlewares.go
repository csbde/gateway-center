// middlewares.go：/middlewares CRUD + 启停（T057，AC-005/FR-017/020/021）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/mwsvc"
)

func (h *Handler) ListMiddleware(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.Middlewares.List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetMiddleware(w http.ResponseWriter, r *http.Request) {
	v, apiErr := h.Middlewares.Get(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, v)
}

func (h *Handler) CreateMiddleware(w http.ResponseWriter, r *http.Request) {
	var in mwsvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	v, apiErr := h.Middlewares.Create(r.Context(), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, v)
}

func (h *Handler) UpdateMiddleware(w http.ResponseWriter, r *http.Request) {
	var in mwsvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	v, apiErr := h.Middlewares.Update(r.Context(), urlParam(r, "id"), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, v)
}

func (h *Handler) EnableMiddleware(w http.ResponseWriter, r *http.Request) {
	h.setMiddlewareEnabled(w, r, true)
}
func (h *Handler) DisableMiddleware(w http.ResponseWriter, r *http.Request) {
	h.setMiddlewareEnabled(w, r, false)
}

func (h *Handler) setMiddlewareEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	var in struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	_ = queryutil.DecodeStrict(r.Body, &in)
	v, apiErr := h.Middlewares.SetEnabled(r.Context(), urlParam(r, "id"), enabled, in.ExpectedVersion, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, v)
}

func (h *Handler) DeleteMiddleware(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Middlewares.Delete(r.Context(), urlParam(r, "id"), actorID(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
