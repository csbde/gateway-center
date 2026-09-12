// Package usersvc 用户管理用例（T080，FR-036）。
// 仅 super_admin 可调用（router SuperAdminOnly）；角色变更写 permission_change 审计；
// 禁用即时吊销全部会话（TokenRepo.RevokeAllForUser）；软删亦吊销会话（UserRepo.SoftDelete 内含）。
package usersvc

import (
	"context"
	"errors"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/authsvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	users  *pgstore.UserRepo
	tokens *pgstore.TokenRepo
	audit  *auditrec.Recorder
}

func New(users *pgstore.UserRepo, tokens *pgstore.TokenRepo, audit *auditrec.Recorder) *Service {
	return &Service{users: users, tokens: tokens, audit: audit}
}

type Input struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

type UpdateInput struct {
	Role            string `json:"role"`
	Status          string `json:"status"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (s *Service) List(ctx context.Context, q pgstore.ListQuery) ([]domain.User, int64, error) {
	return s.users.List(ctx, q)
}

func (s *Service) Get(ctx context.Context, id string) (*domain.User, *httperr.APIError) {
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "用户")
	}
	return u, nil
}

// Create 创建用户并分配角色；密码 bcrypt≥12；创建即分配角色记 permission_change。
func (s *Service) Create(ctx context.Context, in Input, actorID, actorUsername string) (*domain.User, *httperr.APIError) {
	if apiErr := authsvc.ValidateUsername(in.Username); apiErr != nil {
		return nil, apiErr
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.DisplayName == "" || len(in.DisplayName) > 64 {
		return nil, httperr.ValidationFailed("显示名必填且 ≤64 字符", httperr.Detail{Field: "display_name", Hint: "用于界面展示，可含中文"})
	}
	if !domain.ValidRole(in.Role) {
		return nil, httperr.ValidationFailed("角色非法", httperr.Detail{Field: "role", Hint: "super_admin/gateway_admin/developer/viewer"})
	}
	// 用户名唯一（GORM 软删自动过滤）
	if existing, err := s.users.GetByUsername(ctx, in.Username); err == nil && existing != nil {
		return nil, httperr.ValidationFailed("用户名已存在", httperr.Detail{Field: "username", Hint: "更换用户名，或恢复已禁用同名账号"})
	} else if err != nil && !errors.Is(err, pgstore.ErrNotFound) {
		return nil, httperr.Internal(err)
	}
	hash, err := authsvc.HashPassword(in.Password) // 返回 *httperr.APIError（强度不足）或 bcrypt error
	if err != nil {
		if apiErr, ok := err.(*httperr.APIError); ok {
			return nil, apiErr
		}
		return nil, httperr.Internal(err)
	}
	u := &domain.User{
		Username:     in.Username,
		DisplayName:  in.DisplayName,
		PasswordHash: hash,
		Role:         domain.Role(in.Role),
		Status:       "active",
	}
	u.SetActor(actorID)
	if err := s.users.Create(ctx, u); err != nil {
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, ActorUsername: actorUsername, Action: "user.create",
		ResourceType: "user", ResourceID: u.ID, ResourceName: u.Username,
		After: userView(u),
	})
	// 创建即分配角色 → permission_change（FR-038 权限变更可审计）
	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, ActorUsername: actorUsername, Action: "permission_change",
		ResourceType: "user", ResourceID: u.ID, ResourceName: u.Username,
		After: map[string]any{"role": u.Role},
	})
	return u, nil
}

// Update 修改角色或状态；角色变更记 permission_change；禁用即时吊销会话。
func (s *Service) Update(ctx context.Context, id string, in UpdateInput, actorID, actorUsername string) (*domain.User, *httperr.APIError) {
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "用户")
	}
	before := userView(u)
	fields := map[string]any{"updated_by": actorID}
	roleChanged := false
	if in.Role != "" && in.Role != string(u.Role) {
		if !domain.ValidRole(in.Role) {
			return nil, httperr.ValidationFailed("角色非法", httperr.Detail{Field: "role", Hint: "super_admin/gateway_admin/developer/viewer"})
		}
		fields["role"] = in.Role
		roleChanged = true
	}
	disabling := false
	changed := false
	if in.Status != "" && in.Status != u.Status {
		if in.Status != "active" && in.Status != "disabled" {
			return nil, httperr.ValidationFailed("状态非法", httperr.Detail{Field: "status", Hint: "active/disabled"})
		}
		fields["status"] = in.Status
		disabling = in.Status == "disabled"
		changed = true
	}
	if !roleChanged && !changed {
		return u, nil // 无可变更字段
	}
	if err := s.users.Update(ctx, u, in.ExpectedVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(u.RowVersion)
		}
		return nil, httperr.Internal(err)
	}
	// 禁用即时吊销全部会话（FR-036/R6：disabled 即时 401）
	if disabling {
		_ = s.tokens.RevokeAllForUser(ctx, u.ID)
	}
	fresh, _ := s.users.Get(ctx, id)
	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, ActorUsername: actorUsername, Action: "user.update",
		ResourceType: "user", ResourceID: id, ResourceName: u.Username,
		Before: before, After: userView(fresh),
	})
	if roleChanged {
		s.audit.Record(ctx, nil, auditrec.Event{
			ActorID: actorID, ActorUsername: actorUsername, Action: "permission_change",
			ResourceType: "user", ResourceID: id, ResourceName: u.Username,
			Before: map[string]any{"role": u.Role}, After: map[string]any{"role": in.Role},
		})
	}
	return fresh, nil
}

// Delete 软删并吊销全部会话（UserRepo.SoftDelete 事务内 revoke refresh_tokens）。
func (s *Service) Delete(ctx context.Context, id, actorID, actorUsername string) *httperr.APIError {
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return mapNotFound(err, "用户")
	}
	if err := s.users.SoftDelete(ctx, id); err != nil {
		return httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, ActorUsername: actorUsername, Action: "user.delete",
		ResourceType: "user", ResourceID: id, ResourceName: u.Username, Before: userView(u),
	})
	return nil
}

func userView(u *domain.User) map[string]any {
	return map[string]any{
		"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
		"role": u.Role, "status": u.Status, "last_login_at": u.LastLoginAt,
		"row_version": u.RowVersion, "created_at": u.CreatedAt, "updated_at": u.UpdatedAt,
	}
}

func mapNotFound(err error, what string) *httperr.APIError {
	if errors.Is(err, pgstore.ErrNotFound) {
		return httperr.NotFound(what)
	}
	return httperr.Internal(err)
}
