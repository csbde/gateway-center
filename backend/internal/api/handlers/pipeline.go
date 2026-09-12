// pipeline.go：/nodes/{id}/validate /nodes/{id}/versions /versions/* /deployments/*（T039，FR-025~032）。
package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/diff"
	"gateway-center/backend/internal/generate"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

// versionRepo/deployRepo 直读仓储（列表/详情端点；写操作全在 pipeline 服务内）。
func (h *Handler) versionRepo() *pgstore.VersionRepo   { return h.Vers }
func (h *Handler) deployRepo() *pgstore.DeploymentRepo { return h.Deps }

// asyncRun 受管理后台执行：WaitGroup 使 graceful shutdown 可等待在途部署。
func (h *Handler) asyncRun(fn func()) {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("部署执行体 panic 已恢复", "recover", rec)
			}
		}()
		fn()
	}()
}

func pipelineSnapshot(v *domain.ConfigVersion) (*generate.Snapshot, bool) {
	return pipeline.SnapshotFrom(v.Snapshot)
}

func errSnapshotCorrupt(id string) error { return errors.New("版本快照损坏: " + id) }

// ValidateNode POST /nodes/{id}/validate —— dry-run 六类检查（200 总返回报告，UI 逐条展示）。
func (h *Handler) ValidateNode(w http.ResponseWriter, r *http.Request) {
	res, apiErr := h.Versions.Validate(r.Context(), urlParam(r, "id"))
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusOK, res)
}

// CreateVersion POST /nodes/{id}/versions —— 强制管线前半段（422=验证不过零副作用）。
func (h *Handler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	v, res, apiErr := h.Versions.CreateVersion(r.Context(), urlParam(r, "id"), actorID(r))
	if apiErr != nil {
		// 422 时把报告装进 details（AC-007：逐条报告且不可生成）
		writeErr(w, r, apiErr)
		return
	}
	_ = res
	queryutil.WriteObject(w, http.StatusCreated, versionView(v, false))
}

// ListVersions GET /nodes/{id}/versions —— 列表剔除大字段（SC-006）。
func (h *Handler) ListVersions(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.versionRepo().ListByNode(r.Context(), urlParam(r, "id"), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	views := make([]map[string]any, 0, len(items))
	for i := range items {
		views = append(views, versionView(&items[i], false))
	}
	queryutil.Respond(w, q, views, total)
}

// GetVersion GET /versions/{id} —— 详情含快照与产物（仍零明文，R7）。
func (h *Handler) GetVersion(w http.ResponseWriter, r *http.Request) {
	v, err := h.versionRepo().Get(r.Context(), urlParam(r, "id"))
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "配置版本"))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, versionView(v, true))
}

// DeleteVersionRejected DELETE /versions/{id} —— 不可变记录，任何角色 403（FR-032/US7-AC3）。
func (h *Handler) DeleteVersionRejected(w http.ResponseWriter, r *http.Request) {
	writeErr(w, r, httperr.Forbidden("配置版本为不可变记录，不允许删除（FR-032）"))
}

// DiffVersion GET /versions/{id}/diff?against= —— 资源级差异（AC-008/US2-AC2）。
func (h *Handler) DiffVersion(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	against := r.URL.Query().Get("against")
	repo := h.versionRepo()
	cur, err := repo.Get(r.Context(), id)
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "配置版本"))
		return
	}
	var old *domain.ConfigVersion
	if against != "" {
		old, err = repo.Get(r.Context(), against)
	} else {
		old, err = repo.LatestSuccessByNode(r.Context(), cur.NodeID)
	}
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "对比版本"))
		return
	}
	oldSnap, ok1 := pipelineSnapshot(old)
	curSnap, ok2 := pipelineSnapshot(cur)
	if !ok1 || !ok2 {
		writeErr(w, r, internalOf(errSnapshotCorrupt(old.ID)))
		return
	}
	items := diff.SnapshotDiff(oldSnap, curSnap)
	queryutil.WriteObject(w, http.StatusOK, map[string]any{
		"from_version": old.Version, "to_version": cur.Version,
		"summary": diff.Summary(items), "items": items,
	})
}

// CreateDeployment POST /deployments —— 门禁同步、执行异步（202 受理）。
func (h *Handler) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	var in pipeline.DeployInput
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	res, apiErr := h.Deploys.Deploy(r.Context(), in, actorID(r), h.asyncRun)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusAccepted, res)
}

// Rollback POST /deployments/rollback（US2 T052 实装前 422 引导）。
func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NodeID     string `json:"node_id"`
		VersionID  string `json:"version_id"`
		Confirmed  bool   `json:"confirmed"`
		ApprovalID string `json:"approval_id,omitempty"`
	}
	if err := queryutil.DecodeStrict(r.Body, &in); err != nil {
		writeErr(w, r, badRequest(err))
		return
	}
	res, apiErr := h.Deploys.Rollback(r.Context(), in.NodeID, in.VersionID, in.Confirmed, actorID(r), h.asyncRun)
	if apiErr != nil {
		writeErr(w, r, apiErr)
		return
	}
	queryutil.WriteObject(w, http.StatusAccepted, res)
}

// ListDeployments GET /deployments（node_id 作用域 + status 过滤）。
func (h *Handler) ListDeployments(w http.ResponseWriter, r *http.Request) {
	q, ok := listQ(w, r)
	if !ok {
		return
	}
	items, total, err := h.deployRepo().List(r.Context(), q)
	if err != nil {
		writeErr(w, r, internalOf(err))
		return
	}
	queryutil.Respond(w, q, items, total)
}

// GetDeployment GET /deployments/{id}。
func (h *Handler) GetDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := h.deployRepo().Get(r.Context(), urlParam(r, "id"))
	if err != nil {
		writeErr(w, r, notFoundOrInternal(err, "部署记录"))
		return
	}
	queryutil.WriteObject(w, http.StatusOK, d)
}

// DeleteDeploymentRejected 部署记录不可删（FR-032 审计链）。
func (h *Handler) DeleteDeploymentRejected(w http.ResponseWriter, r *http.Request) {
	writeErr(w, r, httperr.Forbidden("部署记录为不可变审计链，不允许删除（FR-032）"))
}

func versionView(v *domain.ConfigVersion, full bool) map[string]any {
	out := map[string]any{
		"id": v.ID, "node_id": v.NodeID, "version": v.Version, "status": v.Status,
		"changes_summary": v.ChangesSummary, "parent_version_id": v.ParentVersionID,
		"origin": v.Origin, "source_version_id": v.SourceVersionID,
		"created_by": v.CreatedBy, "created_at": v.CreatedAt, "row_version": v.RowVersion,
	}
	if full {
		out["snapshot"] = v.Snapshot
		out["artifact_files"] = v.ArtifactFiles
	}
	return out
}
