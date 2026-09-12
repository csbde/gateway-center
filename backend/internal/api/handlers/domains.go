// domains.go：/domains CRUD + /certificates/import（T033，AC-002）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/certsvc"
	"gateway-center/backend/internal/application/domainsvc"
)

func (h *Handler) ListDomain(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.Domains.List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetDomain(w http.ResponseWriter, r *http.Request) {
	d, err := h.Domains.Get(r.Context(), urlParam(r, "id"))
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "域名"))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, d)
}

func (h *Handler) CreateDomain(w http.ResponseWriter, r *http.Request) {
	var in domainsvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	d, apiErr := h.Domains.Create(r.Context(), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, d)
}

func (h *Handler) UpdateDomain(w http.ResponseWriter, r *http.Request) {
	var in domainsvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	d, apiErr := h.Domains.Update(r.Context(), urlParam(r, "id"), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, d)
}

func (h *Handler) EnableDomain(w http.ResponseWriter, r *http.Request) {
	h.setDomainEnabled(w, r, true)
}
func (h *Handler) DisableDomain(w http.ResponseWriter, r *http.Request) {
	h.setDomainEnabled(w, r, false)
}

func (h *Handler) setDomainEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	var in struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	_ = queryutil.DecodeStrict(r.Body, &in)
	d, apiErr := h.Domains.SetEnabled(r.Context(), urlParam(r, "id"), enabled, in.ExpectedVersion, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, d)
}

func (h *Handler) DeleteDomain(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Domains.Delete(r.Context(), urlParam(r, "id"), actorID(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ImportCertificate POST /certificates/import —— 私钥加密入库，响应仅指纹级信息。
func (h *Handler) ImportCertificate(w http.ResponseWriter, r *http.Request) {
	var in certsvc.ImportInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	c, apiErr := h.Certs.Import(r.Context(), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, map[string]any{
		"id": c.ID, "domain_id": c.DomainID, "source": c.Source,
		"not_before": c.NotBefore, "not_after": c.NotAfter, "issuer": c.Issuer,
		"sans": c.Sans, "status": c.Status, "private_key": "****",
	})
}
