// GatewayNode / NodeState 仓储（T027）。
package pgstore

import (
	"context"
	"errors"

	"gateway-center/backend/internal/domain"
	"gorm.io/gorm"
)

type NodeRepo struct{ db *gorm.DB }

func NewNodeRepo(db *gorm.DB) *NodeRepo { return &NodeRepo{db: db} }

func (r *NodeRepo) List(ctx context.Context, q ListQuery) ([]domain.GatewayNode, int64, error) {
	return listScanned[domain.GatewayNode](ctx, r.db.Model(&domain.GatewayNode{}), q, "name")
}

func (r *NodeRepo) Get(ctx context.Context, id string) (*domain.GatewayNode, error) {
	var n domain.GatewayNode
	if err := r.db.WithContext(ctx).First(&n, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &n, nil
}

func (r *NodeRepo) Create(ctx context.Context, n *domain.GatewayNode) error {
	n.RowVersion = 1
	return r.db.WithContext(ctx).Create(n).Error
}

func (r *NodeRepo) Update(ctx context.Context, n *domain.GatewayNode, expected int64, fields map[string]any) error {
	return UpdateOptimistic(ctx, r.db, n, expected, fields)
}

func (r *NodeRepo) SoftDelete(ctx context.Context, id string) error {
	var n domain.GatewayNode
	n.ID = id
	return SoftDelete(ctx, r.db, &n)
}

func (r *NodeRepo) State(ctx context.Context, nodeID string) (*domain.NodeState, error) {
	var s domain.NodeState
	err := r.db.WithContext(ctx).First(&s, "node_id = ?", nodeID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &domain.NodeState{NodeID: nodeID, Status: "unknown"}, nil // 未探测=unknown（FR-002）
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *NodeRepo) UpsertState(ctx context.Context, s *domain.NodeState) error {
	return r.db.WithContext(ctx).Save(s).Error
}

// UpdateDriftState 列级更新漂移四字段（driftsvc / deploy 成功路径用）。
// 只写 drift/drift_detail/desired_version/actual_version，不触碰 probesvc 负责的
// status/loaded_*/last_probe_at 等列——避免与全行 UpsertState(Save) 的并发写互相覆写。
// Select 强制写入零值（drift=false / drift_detail=nil 清除漂移）。
func (r *NodeRepo) UpdateDriftState(ctx context.Context, nodeID string, drift bool, detail []map[string]any, desiredVer, actualVer int64) error {
	return r.db.WithContext(ctx).Model(&domain.NodeState{}).
		Where("node_id = ?", nodeID).
		Select("drift", "drift_detail", "desired_version", "actual_version").
		Updates(domain.NodeState{
			Drift:          drift,
			DriftDetail:    detail,
			DesiredVersion: desiredVer,
			ActualVersion:  actualVer,
		}).Error
}

// HasDeployments 软删前置检查（UF-1：有部署记录拒删）。
func (r *NodeRepo) HasDeployments(ctx context.Context, nodeID string) (bool, error) {
	var cnt int64
	err := r.db.WithContext(ctx).Model(&domain.Deployment{}).
		Where("node_id = ? AND deleted_at IS NULL", nodeID).Count(&cnt).Error
	return cnt > 0, err
}

// AllEnabledIDs 供调度器枚举探测目标。
func (r *NodeRepo) AllIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&domain.GatewayNode{}).
		Where("deleted_at IS NULL").Pluck("id", &ids).Error
	return ids, err
}

// AllStates 供 Dashboard 聚合在线/离线/漂移计数（T068/T073）。
func (r *NodeRepo) AllStates(ctx context.Context) ([]domain.NodeState, error) {
	var ss []domain.NodeState
	err := r.db.WithContext(ctx).Find(&ss).Error
	return ss, err
}
