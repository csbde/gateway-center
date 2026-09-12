// Package auditrec 提供事务内审计写入（T017，research R12、宪法 X）。
// before/after 快照写入前经统一 redactor 脱敏（R7）；与业务变更同事务提交——
// 「有变更必有审计」；ConfigVersion/Deployment/AuditLog 不提供任何删除通道（FR-032）。
package auditrec

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/redactx"
	"gorm.io/gorm"
)

type Event struct {
	ActorID       string
	ActorUsername string
	Action        string // create/update/delete/enable/disable/deploy/rollback/approve/reject/cert_policy_change/permission_change/advanced_edit/login
	ResourceType  string
	ResourceID    string
	ResourceName  string
	Before, After any // 任意可序列化对象（DTO 层已白名单，此处再兜底脱敏）
	IP, UserAgent string
	RequestID     string
}

type Recorder struct {
	repo *pgstore.AuditRepo
}

func New(repo *pgstore.AuditRepo) *Recorder { return &Recorder{repo: repo} }

// Record 写入一条审计。tx 非 nil 时加入业务事务（同提交，R12）；
// tx 为 nil 用于登录等无业务事务场景（独立提交）。
// 审计失败不阻断业务写（记 error 日志并告警），但合规要求下 DB 异常会连带业务事务回滚。
func (r *Recorder) Record(ctx context.Context, tx *gorm.DB, ev Event) {
	a := &domain.AuditLog{
		OccurredAt:    time.Now().UTC(),
		ActorID:       ev.ActorID,
		ActorUsername: ev.ActorUsername,
		Action:        ev.Action,
		ResourceType:  ev.ResourceType,
		ResourceID:    ev.ResourceID,
		ResourceName:  ev.ResourceName,
		Before:        redactx.RedactMap(toMap(ev.Before)),
		After:         redactx.RedactMap(toMap(ev.After)),
		IP:            ev.IP,
		UserAgent:     ev.UserAgent,
		RequestID:     ev.RequestID,
	}
	if err := r.repo.Append(ctx, tx, a); err != nil {
		slog.Error("审计写入失败", "err", err, "action", ev.Action, "resource", ev.ResourceType)
	}
}

func toMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"value": redactx.MaskValue}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}
