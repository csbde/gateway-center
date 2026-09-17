// proxyhosts.go：/proxy-hosts 网站代理 CRUD + 启停（向 NPM 学习）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/proxyhostsvc"
)

func (h *Handler) ListProxyHosts(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.ProxyHosts.List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetProxyHost(w http.ResponseWriter, r *http.Request) {
	host, apiErr := h.ProxyHosts.Get(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, host)
}

func (h *Handler) CreateProxyHost(w http.ResponseWriter, r *http.Request) {
	var in proxyhostsvc.CreateProxyHostInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	host, apiErr := h.ProxyHosts.Create(r.Context(), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, host)
}

func (h *Handler) UpdateProxyHost(w http.ResponseWriter, r *http.Request) {
	var in proxyhostsvc.UpdateProxyHostInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	host, apiErr := h.ProxyHosts.Update(r.Context(), urlParam(r, "id"), in, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, host)
}

func (h *Handler) EnableProxyHost(w http.ResponseWriter, r *http.Request) {
	host, apiErr := h.ProxyHosts.SetEnabled(r.Context(), urlParam(r, "id"), true, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, host)
}

func (h *Handler) DisableProxyHost(w http.ResponseWriter, r *http.Request) {
	host, apiErr := h.ProxyHosts.SetEnabled(r.Context(), urlParam(r, "id"), false, actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, host)
}

func (h *Handler) DeleteProxyHost(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.ProxyHosts.Delete(r.Context(), urlParam(r, "id"), actorID(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
