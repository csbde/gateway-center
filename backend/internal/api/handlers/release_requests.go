// release_requests.go：/release-requests 全端点（T076，FR-037 生产发布审批）。
// submit（版本须 ready）→ pending；approve/reject（自批守卫）；cancel（仅提交人）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
)

type releaseRequestCreateInput struct {
	NodeID          string `json:"node_id"`
	ConfigVersionID string `json:"config_version_id"`
	Comment         string `json:"comment"`
}

type approvalDecisionInput struct {
	Comment string `json:"comment"`
}

func (h *Handler) ListReleaseRequests(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	items, total, err := h.Approvals.List(r.Context(), q, status)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) CreateReleaseRequest(w http.ResponseWriter, r *http.Request) {
	var in releaseRequestCreateInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	a := actorOf(r)
	rr, apiErr := h.Approvals.Submit(r.Context(), in.NodeID, in.ConfigVersionID, a.ID, in.Comment)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, rr)
}

func (h *Handler) ApproveReleaseRequest(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	var in approvalDecisionInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	a := actorOf(r)
	rr, apiErr := h.Approvals.Approve(r.Context(), id, a.ID, in.Comment)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, rr)
}

func (h *Handler) RejectReleaseRequest(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	var in approvalDecisionInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	a := actorOf(r)
	rr, apiErr := h.Approvals.Reject(r.Context(), id, a.ID, in.Comment)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, rr)
}

func (h *Handler) CancelReleaseRequest(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	a := actorOf(r)
	rr, apiErr := h.Approvals.Cancel(r.Context(), id, a.ID)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, rr)
}
