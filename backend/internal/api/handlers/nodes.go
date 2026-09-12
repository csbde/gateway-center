// nodes.go：/nodes 全端点（T032，AC-001）。
package handlers

import (
	"net/http"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/nodesvc"
	"gateway-center/backend/internal/domain"
)

func nodeView(n *domain.GatewayNode) map[string]any {
	return map[string]any{
		"id": n.ID, "name": n.Name, "base_url": n.BaseURL, "deploy_root": n.DeployRoot,
		"env_type": n.EnvType, "remark": n.Remark, "enabled": n.Enabled,
		"has_api_auth": len(n.APIAuthEncrypted) > 0, // 私凭据仅回有无，永不回显（R7）
		"row_version":  n.RowVersion, "created_at": n.CreatedAt, "updated_at": n.UpdatedAt,
	}
}

func (h *Handler) ListNode(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.Nodes.List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

func (h *Handler) GetNode(w http.ResponseWriter, r *http.Request) {
	n, err := h.Nodes.Get(r.Context(), urlParam(r, "id"))
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "节点"))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, nodeView(n))
}

func (h *Handler) CreateNode(w http.ResponseWriter, r *http.Request) {
	var in nodesvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	n, apiErr := h.Nodes.Create(r.Context(), in, actorOf(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusCreated, nodeView(n))
}

func (h *Handler) UpdateNode(w http.ResponseWriter, r *http.Request) {
	var in nodesvc.Input
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	n, apiErr := h.Nodes.Update(r.Context(), urlParam(r, "id"), in, actorOf(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, nodeView(n))
}

func (h *Handler) EnableNode(w http.ResponseWriter, r *http.Request) { h.setNodeEnabled(w, r, true) }
func (h *Handler) DisableNode(w http.ResponseWriter, r *http.Request) {
	h.setNodeEnabled(w, r, false)
}

func (h *Handler) setNodeEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	var in struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	_ = queryutil.DecodeStrict(r.Body, &in) // body 可空
	n, apiErr := h.Nodes.SetEnabled(r.Context(), urlParam(r, "id"), enabled, in.ExpectedVersion, actorOf(r))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, nodeView(n))
}

func (h *Handler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Nodes.Delete(r.Context(), urlParam(r, "id"), actorOf(r)); apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// NodeState GET /nodes/{id}/state（FR-002 期望/实际分列的 actual 侧）。
func (h *Handler) NodeState(w http.ResponseWriter, r *http.Request) {
	st, err := h.Nodes.State(r.Context(), urlParam(r, "id"))
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "节点"))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, st)
}
