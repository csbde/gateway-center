// users.go：/users CRUD 端点（T080，FR-036；router SuperAdminOnly 守卫，仅 super_admin）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/usersvc"
)

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.Users.List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var in usersvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	a := actorOf(r)
	u, apiErr := h.Users.Create(r.Context(), in, a.ID, a.Username)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, u)
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	u, apiErr := h.Users.Get(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, u)
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	var in usersvc.UpdateInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	a := actorOf(r)
	u, apiErr := h.Users.Update(r.Context(), urlParam(r, "id"), in, a.ID, a.Username)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, u)
}

func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	a := actorOf(r)
	if apiErr := h.Users.Delete(r.Context(), urlParam(r, "id"), a.ID, a.Username); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
