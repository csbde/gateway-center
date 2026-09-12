// Package mwsvc 中间件用例（T057，AC-005/FR-017/020/021）。
// 参数按类型独立校验（mwreg 注册表，NFR-MNT-01 扩展点）；删除受引用守卫（AC-015 前置形态）；
// referenced_by 回显构成前端「谁在用」清单。
package mwsvc

import (
	"context"
	"errors"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/depcheck"
	"gateway-center/backend/internal/domain/mwreg"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	mws   *pgstore.MiddlewareRepo
	nodes *pgstore.NodeRepo
	audit *auditrec.Recorder
}

func New(mws *pgstore.MiddlewareRepo, nodes *pgstore.NodeRepo, audit *auditrec.Recorder) *Service {
	return &Service{mws: mws, nodes: nodes, audit: audit}
}

type Input struct {
	NodeID          string         `json:"node_id"`
	Name            string         `json:"name"`
	Type            string         `json:"type"`
	Params          map[string]any `json:"params"`
	ExpectedVersion int64          `json:"expected_version,omitempty"`
}

// View 实体 + 引用方路由名（FR-020 删除阻断/前端展示同一数据源）。
type View struct {
	*domain.Middleware
	ReferencedBy []string `json:"referenced_by"`
}

func paramErr(msg, field, hint string) *httperr.APIError {
	return httperr.ValidationFailed(msg, httperr.Detail{Field: field, Hint: hint})
}

func (s *Service) List(ctx context.Context, q pgstore.ListQuery) ([]domain.Middleware, int64, error) {
	return s.mws.List(ctx, q)
}

func (s *Service) Get(ctx context.Context, id string) (*View, *httperr.APIError) {
	mw, err := s.mws.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "中间件")
	}
	return s.viewOf(ctx, mw)
}

func (s *Service) viewOf(ctx context.Context, mw *domain.Middleware) (*View, *httperr.APIError) {
	refs, err := s.mws.ReferencingRoutes(ctx, mw.ID)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.Name)
	}
	return &View{Middleware: mw, ReferencedBy: names}, nil
}

// validateInput 类型注册表字段级校验（错误定位 field=参数名，NFR-USE-01）。
func validateInput(in Input) *httperr.APIError {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 64 {
		return paramErr("名称必填且 ≤64 字符", "name", "")
	}
	if !mwreg.ValidSlug(in.Name) {
		return paramErr("名称需为小写字母开头的 slug（a-z0-9-）", "name", "将作为网关内部资源标识，如 strict-csp")
	}
	if in.NodeID == "" {
		return paramErr("node_id 必填", "node_id", "中间件按节点作用域")
	}
	if _, err := mwreg.Get(in.Type); err != nil {
		return paramErr(err.Error(), "type", strings.Join(mwreg.Types(), "/"))
	}
	for _, fe := range mwreg.Validate(in.Type, in.Params) {
		return paramErr("参数 "+fe.Field+" 非法: "+fe.Hint, fe.Field, fe.Hint)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in Input, actorID string) (*View, *httperr.APIError) {
	if apiErr := validateInput(in); apiErr != nil {
		return nil, apiErr
	}
	if _, err := s.nodes.Get(ctx, in.NodeID); err != nil {
		return nil, mapNotFound(err, "节点")
	}
	taken, err := s.mws.NameTaken(ctx, in.NodeID, in.Name)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if taken {
		return nil, paramErr("该节点下中间件名已存在", "name", "同一节点内唯一")
	}
	mw := &domain.Middleware{NodeID: in.NodeID, Name: strings.TrimSpace(in.Name),
		Type: in.Type, Params: in.Params, Enabled: true}
	mw.SetActor(actorID)
	if err := s.mws.Create(ctx, mw); err != nil {
		if isUnique(err) {
			return nil, paramErr("该节点下中间件名已存在", "name", "")
		}
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "create", mw.ID, mw.Name, nil, mw))
	return s.viewOf(ctx, mw)
}

func (s *Service) Update(ctx context.Context, id string, in Input, actorID string) (*View, *httperr.APIError) {
	mw, err := s.mws.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "中间件")
	}
	// 类型不可变更（改名/换型会造成引用语义漂移）；params 按原类型再校验
	merged := in
	merged.NodeID = mw.NodeID
	merged.Type = mw.Type
	if merged.Name == "" {
		merged.Name = mw.Name
	}
	if merged.Params == nil {
		merged.Params = mw.Params
	}
	if apiErr := validateInput(merged); apiErr != nil {
		return nil, apiErr
	}
	if merged.Name != mw.Name {
		taken, err := s.mws.NameTaken(ctx, mw.NodeID, merged.Name)
		if err != nil {
			return nil, httperr.Internal(err)
		}
		if taken {
			return nil, paramErr("该节点下中间件名已存在", "name", "")
		}
	}
	before := *mw
	if in.ExpectedVersion == 0 {
		in.ExpectedVersion = mw.RowVersion
	}
	fields := map[string]any{"name": strings.TrimSpace(merged.Name),
		"params": merged.Params, "updated_by": actorID}
	if err := s.mws.Update(ctx, mw, in.ExpectedVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(mw.RowVersion)
		}
		if isUnique(err) {
			return nil, paramErr("该节点下中间件名已存在", "name", "")
		}
		return nil, httperr.Internal(err)
	}
	fresh, apiErr := s.Get(ctx, id)
	if apiErr != nil {
		return nil, apiErr
	}
	s.audit.Record(ctx, nil, ev(actorID, "update", id, fresh.Name, before, fresh))
	return fresh, nil
}

func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool, expected int64, actorID string) (*View, *httperr.APIError) {
	mw, err := s.mws.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "中间件")
	}
	if err := s.mws.Update(ctx, mw, expected, map[string]any{"enabled": enabled, "updated_by": actorID}); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(mw.RowVersion)
		}
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "update", id, mw.Name,
		map[string]any{"enabled": mw.Enabled}, map[string]any{"enabled": enabled}))
	return s.Get(ctx, id)
}

// Delete 引用守卫：被任何未删路由绑定即 409 + 引用清单（AC-015/US3-AC3）。
func (s *Service) Delete(ctx context.Context, id, actorID string) *httperr.APIError {
	mw, err := s.mws.Get(ctx, id)
	if err != nil {
		return mapNotFound(err, "中间件")
	}
	refs, err := s.mws.ReferencingRoutes(ctx, id)
	if err != nil {
		return httperr.Internal(err)
	}
	rr := depcheck.RouteRefs(refs)
	if depcheck.AnyRefs(rr) {
		return httperr.DependencyBlocked("中间件 "+mw.Name, routeDetails(rr))
	}
	if err := s.mws.SoftDelete(ctx, id); err != nil {
		return httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "delete", id, mw.Name, mw, nil))
	return nil
}

func routeDetails(refs []depcheck.RouteRef) []httperr.Detail {
	out := make([]httperr.Detail, 0, len(refs))
	for _, r := range refs {
		out = append(out, httperr.Detail{Field: "routes", Message: r.Name, ID: r.ID})
	}
	return out
}

func ev(actorID, action, rid, name string, before, after any) auditrec.Event {
	return auditrec.Event{ActorID: actorID, Action: action, ResourceType: "middleware",
		ResourceID: rid, ResourceName: name, Before: before, After: after}
}

func mapNotFound(err error, what string) *httperr.APIError {
	if errors.Is(err, pgstore.ErrNotFound) {
		return httperr.NotFound(what)
	}
	return httperr.Internal(err)
}

func isUnique(err error) bool {
	e := err.Error()
	return strings.Contains(e, "SQLSTATE 23505") || strings.Contains(e, "duplicate key")
}
