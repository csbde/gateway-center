// credentials.go：/credentials CRUD + verify（T064，FR-035）。
// 响应体仅含元数据 + fingerprint；DataEncrypted 在模型层 json:"-" 永不出响应（宪章 VIII）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/credsvc"
)

func (h *Handler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.Credentials.List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetCredential(w http.ResponseWriter, r *http.Request) {
	c, apiErr := h.Credentials.Get(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, c)
}

func (h *Handler) CreateCredential(w http.ResponseWriter, r *http.Request) {
	var in credsvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	c, apiErr := h.Credentials.Create(r.Context(), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, c)
}

func (h *Handler) UpdateCredential(w http.ResponseWriter, r *http.Request) {
	var in credsvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	c, apiErr := h.Credentials.Update(r.Context(), urlParam(r, "id"), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, c)
}

func (h *Handler) VerifyCredential(w http.ResponseWriter, r *http.Request) {
	res, apiErr := h.Credentials.Verify(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, res)
}

func (h *Handler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Credentials.Delete(r.Context(), urlParam(r, "id"), actorID(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
