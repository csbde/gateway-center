// certificates.go：/certificates 资产库 CRUD + parse + probe（向 NPM 学习）。
// 私钥永不出响应，仅元数据、状态及脱敏指纹（宪章 VIII）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/certsvc"
)

func (h *Handler) ListCertificates(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	items, total, err := h.Certs.List(r.Context(), q, status)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetCertificate(w http.ResponseWriter, r *http.Request) {
	c, apiErr := h.Certs.Get(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, c)
}

func (h *Handler) CreateCertificate(w http.ResponseWriter, r *http.Request) {
	var in certsvc.CreateCustomInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	c, apiErr := h.Certs.CreateCustom(r.Context(), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, c)
}

func (h *Handler) ParseCertificate(w http.ResponseWriter, r *http.Request) {
	var in certsvc.ParseInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	res, apiErr := h.Certs.Parse(in)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, res)
}

func (h *Handler) ProbeCertificate(w http.ResponseWriter, r *http.Request) {
	c, apiErr := h.Certs.Probe(r.Context(), urlParam(r, "id"), actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, c)
}

func (h *Handler) DeleteCertificate(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Certs.Delete(r.Context(), urlParam(r, "id"), actorID(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
