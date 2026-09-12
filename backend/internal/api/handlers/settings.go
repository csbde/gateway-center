// settings.go：GET/PUT /settings（T025，FR-004 平台参数）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/settingsvc"
)

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	queryutil.WriteObject(w, http.StatusOK, h.Settings.Current(r.Context()))
}

func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	a := actorOf(r)
	s, apiErr := h.Settings.Update(r.Context(), in, a.ID, a.Username, a.IP, a.RequestID)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, s)
}
