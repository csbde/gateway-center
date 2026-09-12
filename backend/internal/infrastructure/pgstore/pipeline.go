// ConfigVersion / Deployment 仓储（T037/T038 依赖；不可变记录一律无 UPDATE-删除通道语义）。
package pgstore

import (
	"context"
	"errors"
	"time"

	"gateway-center/backend/internal/domain"
	"gorm.io/gorm"
)

type VersionRepo struct{ db *gorm.DB }

func NewVersionRepo(db *gorm.DB) *VersionRepo { return &VersionRepo{db: db} }

// NextVersion 节点级 advisory lock 防并发跳号（data-model §8）。
func (r *VersionRepo) NextVersion(ctx context.Context, nodeID string) (int64, error) {
	var v int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", nodeID).Error; err != nil {
			return err
		}
		var maxv *int64
		if err := tx.Model(&domain.ConfigVersion{}).Where("node_id = ?", nodeID).
			Pluck("MAX(version)", &maxv).Error; err != nil {
			return err
		}
		v = 1
		if maxv != nil {
			v = *maxv + 1
		}
		return nil
	})
	return v, err
}

func (r *VersionRepo) Create(ctx context.Context, v *domain.ConfigVersion) error {
	v.RowVersion = 1
	return r.db.WithContext(ctx).Create(v).Error
}

// AdvanceStatus 仅允许状态单向迁移（不可变快照，仅 status 追加式迁移，data-model §8）。
func (r *VersionRepo) AdvanceStatus(ctx context.Context, id, from, to string) error {
	res := r.db.WithContext(ctx).Model(&domain.ConfigVersion{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("版本状态迁移非法: " + from + "→" + to)
	}
	return nil
}

func (r *VersionRepo) Get(ctx context.Context, id string) (*domain.ConfigVersion, error) {
	var v domain.ConfigVersion
	if err := r.db.WithContext(ctx).First(&v, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &v, nil
}

// GetForUpdate 部署临界区：锁版本行防并发改状态。
func (r *VersionRepo) GetForUpdate(ctx context.Context, tx *gorm.DB, id string) (*domain.ConfigVersion, error) {
	var v domain.ConfigVersion
	if err := tx.WithContext(ctx).Raw("SELECT * FROM config_versions WHERE id = ? FOR UPDATE", id).Scan(&v).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if v.ID == "" {
		return nil, ErrNotFound
	}
	return &v, nil
}

func (r *VersionRepo) ListByNode(ctx context.Context, nodeID string, q ListQuery) ([]domain.ConfigVersion, int64, error) {
	// 列表不回传 snapshot/artifact_files 大字段（SC-006）
	base := r.db.WithContext(ctx).Model(&domain.ConfigVersion{}).
		Select("id", "node_id", "version", "status", "changes_summary", "parent_version_id",
			"origin", "source_version_id", "created_by", "created_at", "updated_at", "row_version").
		Where("node_id = ?", nodeID)
	return listScanned[domain.ConfigVersion](ctx, base, q, "version DESC")
}

// LatestReady / LatestSuccess 供 drift 判定 desired 版本。
func (r *VersionRepo) LatestReady(ctx context.Context, nodeID string) (*domain.ConfigVersion, error) {
	var v domain.ConfigVersion
	err := r.db.WithContext(ctx).Where("node_id = ? AND status = 'ready'", nodeID).
		Order("version DESC").First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *VersionRepo) LatestSuccessByNode(ctx context.Context, nodeID string) (*domain.ConfigVersion, error) {
	var v domain.ConfigVersion
	err := r.db.WithContext(ctx).
		Joins("JOIN deployments d ON d.config_version_id = config_versions.id").
		Where("config_versions.node_id = ? AND d.status = 'success' AND d.deleted_at IS NULL", nodeID).
		Order("config_versions.version DESC").First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &v, err
}

// ---- Deployment ----

type DeploymentRepo struct{ db *gorm.DB }

func NewDeploymentRepo(db *gorm.DB) *DeploymentRepo { return &DeploymentRepo{db: db} }

func (r *DeploymentRepo) Create(ctx context.Context, d *domain.Deployment) error {
	d.RowVersion = 1
	return r.db.WithContext(ctx).Create(d).Error
}

func (r *DeploymentRepo) Get(ctx context.Context, id string) (*domain.Deployment, error) {
	var d domain.Deployment
	if err := r.db.WithContext(ctx).First(&d, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

// SetStatus 迁移 + 追加日志事件（管线阶段时间戳，data-model §9 logs）。
func (r *DeploymentRepo) SetStatus(ctx context.Context, id, from, to, stage string, extra map[string]any) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var d domain.Deployment
		if err := tx.First(&d, "id = ?", id).Error; err != nil {
			return err
		}
		ev := map[string]any{"stage": stage, "at": time.Now().UTC().Format(time.RFC3339Nano)}
		if extra != nil {
			for k, v := range extra {
				ev[k] = v
			}
		}
		fields := map[string]any{"status": to, "row_version": gorm.Expr("row_version + 1")}
		if extra != nil {
			if v, ok := extra["verification_result"]; ok {
				fields["verification_result"] = v
			}
			if v, ok := extra["error_message"]; ok {
				fields["error_message"] = v
			}
			if v, ok := extra["error_code"]; ok {
				fields["error_code"] = v
			}
		}
		updates := tx.Model(&domain.Deployment{}).Where("id = ? AND status = ?", id, from).Updates(fields)
		if updates.Error != nil {
			return updates.Error
		}
		if updates.RowsAffected == 0 {
			return errors.New("部署状态迁移非法: " + from + "→" + to)
		}
		logs := append(d.Logs, ev)
		return tx.Model(&domain.Deployment{}).Where("id = ?", id).Update("logs", logs).Error
	})
}

func (r *DeploymentRepo) List(ctx context.Context, q ListQuery) ([]domain.Deployment, int64, error) {
	return listScanned[domain.Deployment](ctx, r.db.Model(&domain.Deployment{}), q, "created_at DESC")
}

// CountSuccess 该版本是否有成功部署记录（回滚目标合法性，AC-011）。
func (r *DeploymentRepo) CountSuccess(ctx context.Context, versionID string) (int64, error) {
	var cnt int64
	err := r.db.WithContext(ctx).Model(&domain.Deployment{}).
		Where("config_version_id = ? AND status = 'success' AND deleted_at IS NULL", versionID).Count(&cnt).Error
	return cnt, err
}

// ActiveForNode 同节点非终态部署（409 DEPLOY_IN_PROGRESS 判定）。
func (r *DeploymentRepo) ActiveForNode(ctx context.Context, nodeID string) (*domain.Deployment, error) {
	var d domain.Deployment
	err := r.db.WithContext(ctx).
		Where("node_id = ? AND status IN ('pending','validating','ready','deploying')", nodeID).
		First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// MarkInterruptedFailed 重启恢复（R16/NFR-REL-01）：中断中的部署标 failed，待人工确认。
func (r *DeploymentRepo) MarkInterruptedFailed(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Model(&domain.Deployment{}).
		Where("status IN ('pending','validating','ready','deploying')").
		Updates(map[string]any{
			"status":        "failed",
			"error_code":    "INTERRUPTED",
			"error_message": "平台重启导致部署中断；已确认的期望状态保留，请人工重新发起",
			"row_version":   gorm.Expr("row_version + 1"),
		})
	return res.RowsAffected, res.Error
}
