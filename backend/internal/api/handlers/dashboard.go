// dashboard.go：GET /dashboard 聚合 + GET /domains/{id}/certificate 只读视图（T068）。
// 均为只读，无审计、无写；证书视图永不含 PEM/私钥（宪章 VIII，certsvc.CertView 已裁剪）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
)

// Dashboard GET /dashboard（FR-041/AC-013：在线/离线/漂移计数、资源数、证书预警、最近发布）。
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := h.Dash.Aggregate(r.Context())
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, d)
}

// DomainCertificate GET /domains/{id}/certificate（FR-033/008：证书状态/到期/SAN 只读观测）。
func (h *Handler) DomainCertificate(w http.ResponseWriter, r *http.Request) {
	v, apiErr := h.Certs.View(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, v)
}
