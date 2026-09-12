// rollback.go：一键回滚（T052，FR-031/AC-011/012、SC-003、宪法 V）。
// 回滚 = 以目标版本快照（不回读当前业务表！）创建 origin=rollback 的新版本，复用同一部署管线。
package pipeline

import (
	"context"
	"fmt"
	"time"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/diff"
	"gateway-center/backend/internal/generate"
)

// Rollback 门禁语义与 Deploy 相同（confirmed 必填、离线阻断、并发闸、生产 Gate）。
// 新版本内容 = 目标版本快照逐字节复用（产物重生成保证确定性），source_version_id 指回目标。
func (s *DeployService) Rollback(ctx context.Context, nodeID, targetVersionID string, confirmed bool, actorID string, async AsyncRunner) (*DeployResult, *httperr.APIError) {
	node, err := s.nodes.Get(ctx, nodeID)
	if err != nil {
		return nil, mapNotFound(err, "节点")
	}
	if !node.Enabled {
		return nil, httperr.PipelineBlocked("节点 " + node.Name + " 已禁用，禁止一切部署")
	}
	target, err := s.vers.Get(ctx, targetVersionID)
	if err != nil {
		return nil, mapNotFound(err, "配置版本")
	}
	if target.NodeID != node.ID {
		return nil, httperr.ValidationFailed("回滚目标不属于该节点", httperr.Detail{Field: "version_id"})
	}
	// 回滚对象必须是曾成功部署的版本（AC-011：历史成功版本）
	if apiErr := s.wasSuccessful(ctx, target); apiErr != nil {
		return nil, apiErr
	}
	if !confirmed {
		return nil, httperr.Newf(422, "DEPLOY_NOT_CONFIRMED", "回滚前必须勾选变更确认（confirmed=true）")
	}
	if apiErr := s.assertOnline(ctx, node); apiErr != nil {
		return nil, apiErr
	}
	active, err := s.deploys.ActiveForNode(ctx, node.ID)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if active != nil {
		return nil, httperr.DeployInProgress(node.Name)
	}

	// 快照复用（宪章 V：绝不回读当前业务表——目标快照即全部事实来源）
	snap, ok := snapshotFrom(target.Snapshot)
	if !ok {
		return nil, httperr.PipelineBlocked("回滚目标快照损坏，无法复用")
	}
	arts, err := generate.Generate(snap)
	if err != nil {
		return nil, httperr.PipelineBlocked("回滚产物重生成失败: " + err.Error())
	}
	snapMap := target.Snapshot // 逐字节复用原快照 JSONB

	verNum, err := s.vers.NextVersion(ctx, node.ID)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	pid := target.ID
	changes := map[string]any{}
	if cur, err := s.vers.LatestSuccessByNode(ctx, node.ID); err == nil {
		if old, ok := snapshotFrom(cur.Snapshot); ok {
			items := diff.SnapshotDiff(old, snap)
			changes = map[string]any{"summary": diff.Summary(items), "items": items, "rollback_from": cur.Version}
		}
	}
	v := &domain.ConfigVersion{
		NodeID: node.ID, Version: verNum, Status: "pending",
		Snapshot: snapMap, ArtifactFiles: generate.ArtifactToFiles(arts),
		ChangesSummary:  changes,
		ParentVersionID: &pid, Origin: "rollback", SourceVersionID: &pid,
	}
	v.SetActor(actorID)
	if err := s.vers.Create(ctx, v); err != nil {
		return nil, httperr.Internal(err)
	}
	if err := s.vers.AdvanceStatus(ctx, v.ID, "pending", "validating"); err != nil {
		return nil, httperr.Internal(err)
	}
	if err := s.vers.AdvanceStatus(ctx, v.ID, "validating", "ready"); err != nil {
		return nil, httperr.Internal(err)
	}

	now := time.Now().UTC()
	d := &domain.Deployment{
		NodeID: node.ID, ConfigVersionID: v.ID, Trigger: "rollback",
		Status: "pending", ConfirmedAt: &now, ConfirmedBy: &actorID,
	}
	d.SetActor(actorID)
	if err := s.deploys.Create(ctx, d); err != nil {
		return nil, httperr.DeployInProgress(node.Name)
	}
	s.audit.Record(ctx, nil, auditrec.Event{ActorID: actorID, Action: "rollback",
		ResourceType: "deployment", ResourceID: d.ID,
		ResourceName: fmt.Sprintf("%s v%d ← v%d", node.Name, v.Version, target.Version),
		After:        map[string]any{"node_id": node.ID, "new_version_id": v.ID, "source_version_id": target.ID}})

	exec := context.WithoutCancel(ctx)
	async(func() { _, _ = s.run(exec, node, v, d) })
	return &DeployResult{DeploymentID: d.ID, Status: "pending"}, nil
}

// wasSuccessful 目标版本须存在 success 部署记录（AC-011）。
func (s *DeployService) wasSuccessful(ctx context.Context, v *domain.ConfigVersion) *httperr.APIError {
	if cur, err := s.vers.LatestSuccessByNode(ctx, v.NodeID); err == nil && cur.ID == v.ID {
		return nil // 当前即最新成功版——回滚到自己，合法但无意义（前端会提示）
	}
	cnt, err := s.deploys.CountSuccess(ctx, v.ID)
	if err != nil {
		return httperr.Internal(err)
	}
	if cnt == 0 {
		return httperr.PipelineBlocked("只能回滚到曾经部署成功的版本（v" + fmt.Sprint(v.Version) + " 无成功部署记录）")
	}
	return nil
}
