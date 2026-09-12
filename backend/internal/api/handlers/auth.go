// auth.go：/auth/login /auth/refresh /auth/logout /me（T015）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
)

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := queryutil.DecodeStrict(r.Body, &body); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	rid := requestIDOf(r)
	toks, u, apiErr := h.Auth.Login(r.Context(), body.Username, body.Password, clientIP(r), r.UserAgent(), rid, "")
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, map[string]any{
		"access_token": toks.AccessToken, "refresh_token": toks.RefreshToken,
		"expires_in": toks.ExpiresIn,
		"user":       map[string]any{"id": u.ID, "username": u.Username, "display_name": u.DisplayName, "role": u.Role},
	})
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := queryutil.DecodeStrict(r.Body, &body); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	toks, apiErr := h.Auth.Refresh(r.Context(), body.RefreshToken)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, toks)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := queryutil.DecodeStrict(r.Body, &body); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	if apiErr := h.Auth.Logout(r.Context(), body.RefreshToken); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	u, apiErr := h.Auth.Me(r.Context(), actorID(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, map[string]any{
		"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
		"role": u.Role, "status": u.Status, "last_login_at": u.LastLoginAt,
	})
}
