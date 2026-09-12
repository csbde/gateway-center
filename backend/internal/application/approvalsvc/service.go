// Package approvalsvc 生产发布审批用例（T076，FR-037、宪章 X）。
// submit（版本须 ready）→ pending；approve/reject（自批守卫：reviewed_by≠submitted_by，
// 应用层+DB CHECK 双层）；cancel（仅提交人、仅 pending）。
// 审计同时记录提交人与批准人（US6-AC3）。
package approvalsvc

import (
	"context"
	"errors"

	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	approvals *pgstore.ApprovalRepo
	vers      *pgstore.VersionRepo
	rec       *auditrec.Recorder
}

func New(approvals *pgstore.ApprovalRepo, vers *pgstore.VersionRepo, rec *auditrec.Recorder) *Service {
	return &Service{approvals: approvals, vers: vers, rec: rec}
}

// Submit 提交发布申请（版本须 ready，FR-037）。
func (s *Service) Submit(ctx context.Context, nodeID, versionID, submitterID, comment string) (*domain.ReleaseRequest, *httperr.APIError) {
	v, err := s.vers.Get(ctx, versionID)
	if err != nil {
		return nil, mapNotFound(err, "配置版本")
	}
	if v.NodeID != nodeID {
		return nil, httperr.ValidationFailed("版本不属于该节点", httperr.Detail{Field: "node_id"})
	}
	if v.Status != "ready" {
		return nil, httperr.PipelineBlocked("仅 ready 状态的版本可提交发布申请（当前 " + v.Status + "）")
	}
	rr := &domain.ReleaseRequest{
		NodeID:          nodeID,
		ConfigVersionID: versionID,
		SubmittedBy:     submitterID,
		Status:          "pending",
		Comment:         comment,
	}
	if err := s.approvals.Create(ctx, rr); err != nil {
		return nil, httperr.Internal(err)
	}
	s.rec.Record(ctx, nil, auditrec.Event{
		ActorID:      submitterID,
		Action:       "release_request.submit",
		ResourceType: "release_request",
		ResourceID:   rr.ID,
		After: map[string]any{"node_id": nodeID, "config_version_id": versionID,
			"status": "pending", "submitted_by": submitterID},
	})
	return rr, nil
}

// Approve 批准（自批由应用层+DB CHECK 双层守卫，FR-037/US6-AC2）。
func (s *Service) Approve(ctx context.Context, id, reviewerID, comment string) (*domain.ReleaseRequest, *httperr.APIError) {
	return s.review(ctx, id, reviewerID, comment, "approved", "approve")
}

// Reject 驳回。
func (s *Service) Reject(ctx context.Context, id, reviewerID, comment string) (*domain.ReleaseRequest, *httperr.APIError) {
	return s.review(ctx, id, reviewerID, comment, "rejected", "reject")
}

func (s *Service) review(ctx context.Context, id, reviewerID, comment, to, action string) (*domain.ReleaseRequest, *httperr.APIError) {
	rr, err := s.approvals.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "发布申请")
	}
	// 自批守卫（应用层先拦，DB CHECK 兜底）
	if rr.SubmittedBy == reviewerID {
		return nil, httperr.PipelineBlocked("不可审批自己提交的发布申请（FR-037 自批守卫）")
	}
	if err := s.approvals.Review(ctx, id, "pending", to, reviewerID, comment); err != nil {
		// 状态迁移非法（已被处理/并发）或 DB CHECK 违反
		return nil, httperr.PipelineBlocked("审批处理失败：申请可能已被处理或发生并发冲突")
	}
	fresh, apiErr := s.Get(ctx, id)
	if apiErr != nil {
		return nil, apiErr
	}
	// 审计同时记录提交人与批准人（US6-AC3）
	s.rec.Record(ctx, nil, auditrec.Event{
		ActorID:      reviewerID,
		Action:       "release_request." + action,
		ResourceType: "release_request",
		ResourceID:   id,
		Before: map[string]any{"status": "pending", "submitted_by": rr.SubmittedBy},
		After:  map[string]any{"status": to, "reviewed_by": reviewerID, "submitted_by": rr.SubmittedBy},
	})
	return fresh, nil
}

// Cancel 提交人撤回待审申请。
func (s *Service) Cancel(ctx context.Context, id, submitterID string) (*domain.ReleaseRequest, *httperr.APIError) {
	rr, err := s.approvals.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "发布申请")
	}
	if err := s.approvals.Cancel(ctx, id, submitterID); err != nil {
		return nil, httperr.PipelineBlocked("撤回失败：仅提交人可撤回待审状态的申请")
	}
	s.rec.Record(ctx, nil, auditrec.Event{
		ActorID:      submitterID,
		Action:       "release_request.cancel",
		ResourceType: "release_request",
		ResourceID:   id,
		Before:       map[string]any{"status": rr.Status, "submitted_by": rr.SubmittedBy},
		After:        map[string]any{"status": "cancelled"},
	})
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (*domain.ReleaseRequest, *httperr.APIError) {
	rr, err := s.approvals.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "发布申请")
	}
	return rr, nil
}

func (s *Service) List(ctx context.Context, q pgstore.ListQuery, status string) ([]domain.ReleaseRequest, int64, error) {
	return s.approvals.List(ctx, q, status)
}

func mapNotFound(err error, what string) *httperr.APIError {
	if errors.Is(err, pgstore.ErrNotFound) {
		return httperr.NotFound(what)
	}
	return httperr.Internal(err)
}
