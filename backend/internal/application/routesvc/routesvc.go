// Package routesvc 路由用例（T035，AC-004、FR-014）。
// 简单模式：用户零 Traefik 语法，保存即回显 generated_rule_preview（与 generate/validate 同源）。
// 高级模式写路径在 US3 T060 接线（仅 gateway_admin+）。
package routesvc

import (
	"context"
	"errors"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/state"
	"gateway-center/backend/internal/generate"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	routes *pgstore.RouteRepo
	doms   *pgstore.DomainRepo
	svcs   *pgstore.ServiceRepo
	mws    *pgstore.MiddlewareRepo
	nodes  *pgstore.NodeRepo
	audit  *auditrec.Recorder
}

func New(routes *pgstore.RouteRepo, doms *pgstore.DomainRepo, svcs *pgstore.ServiceRepo,
	mws *pgstore.MiddlewareRepo, nodes *pgstore.NodeRepo, audit *auditrec.Recorder) *Service {
	return &Service{routes: routes, doms: doms, svcs: svcs, mws: mws, nodes: nodes, audit: audit}
}

type Input struct {
	NodeID          string   `json:"node_id"`
	Name            string   `json:"name"`
	Mode            string   `json:"mode"` // simple/advanced（advanced 写路径 US3）
	DomainID        string   `json:"domain_id,omitempty"`
	Path            string   `json:"path,omitempty"`
	MatchType       string   `json:"match_type,omitempty"` // exact/prefix
	ServiceID       string   `json:"service_id"`
	HTTPS           bool     `json:"https"`
	AdvancedRule    string   `json:"advanced_rule,omitempty"`
	MiddlewareIDs   []string `json:"middleware_ids,omitempty"`
	ExpectedVersion int64    `json:"expected_version,omitempty"`
}

// View 是路由 API 视图：实体 + 规则预览（FR-014 零语法）。
type View struct {
	*domain.Route
	GeneratedRulePreview string `json:"generated_rule_preview"`
}

func (s *Service) List(ctx context.Context, q pgstore.ListQuery) ([]domain.Route, int64, error) {
	return s.routes.List(ctx, q)
}

func (s *Service) Get(ctx context.Context, id string) (*View, *httperr.APIError) {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "路由")
	}
	return s.viewOf(ctx, rt)
}

func (s *Service) viewOf(ctx context.Context, rt *domain.Route) (*View, *httperr.APIError) {
	preview := rt.AdvancedRule
	if rt.Mode == "simple" && rt.DomainID != nil {
		if d, err := s.doms.Get(ctx, *rt.DomainID); err == nil {
			preview = generate.RuleForSimple(d.Name, rt.Path, rt.MatchType)
		}
	}
	return &View{Route: rt, GeneratedRulePreview: preview}, nil
}

// Preview 供向导实时回显（POST /routes/preview，不落库）。
func (s *Service) Preview(ctx context.Context, in Input) (string, *httperr.APIError) {
	if in.Mode == "advanced" || in.AdvancedRule != "" {
		return strings.TrimSpace(in.AdvancedRule), nil
	}
	d, err := s.doms.Get(ctx, in.DomainID)
	if err != nil {
		return "", mapNotFound(err, "域名")
	}
	return generate.RuleForSimple(d.Name, in.Path, in.MatchType), nil
}

func (s *Service) Create(ctx context.Context, in Input, actorID string) (*View, *httperr.APIError) {
	if apiErr := s.validateInput(ctx, in); apiErr != nil {
		return nil, apiErr
	}
	taken, err := s.routes.NameTaken(ctx, in.NodeID, in.Name)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if taken {
		return nil, httperr.ValidationFailed("该节点下路由名已存在", httperr.Detail{Field: "name"})
	}
	maxp, err := s.routes.MaxPriority(ctx, in.NodeID)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	rt := &domain.Route{
		NodeID: in.NodeID, Name: strings.TrimSpace(in.Name),
		Mode:     defaultStr(in.Mode, "simple"),
		DomainID: nilIfEmpty(in.DomainID), Path: in.Path,
		MatchType: defaultStr(in.MatchType, "prefix"),
		ServiceID: in.ServiceID, HTTPS: in.HTTPS,
		Priority:     maxp + 10, // 自动递增留间隙（data-model §6）
		AdvancedRule: strings.TrimSpace(in.AdvancedRule),
		Status:       "draft", // 新建即草稿，发布链负责 enabled
	}
	rt.SetActor(actorID)
	if err := s.routes.Create(ctx, rt); err != nil {
		if isUnique(err) {
			return nil, httperr.ValidationFailed("该节点下路由名已存在", httperr.Detail{Field: "name"})
		}
		return nil, httperr.Internal(err)
	}
	if len(in.MiddlewareIDs) > 0 {
		if apiErr := s.bindMiddlewares(ctx, rt.ID, in.MiddlewareIDs, in.NodeID); apiErr != nil {
			return nil, apiErr
		}
	}
	v, apiErr := s.Get(ctx, rt.ID)
	if apiErr != nil {
		return nil, apiErr
	}
	s.audit.Record(ctx, nil, ev(actorID, "create", rt.ID, rt.Name, nil, v))
	return v, nil
}

func (s *Service) Update(ctx context.Context, id string, in Input, actorID string) (*View, *httperr.APIError) {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "路由")
	}
	in.NodeID = rt.NodeID
	if apiErr := s.validateInput(ctx, in); apiErr != nil {
		return nil, apiErr
	}
	if in.ExpectedVersion == 0 {
		in.ExpectedVersion = rt.RowVersion
	}
	before := *rt
	fields := map[string]any{
		"name": strings.TrimSpace(in.Name), "mode": rt.Mode,
		"domain_id": nilIfEmpty(in.DomainID), "path": in.Path,
		"match_type": defaultStr(in.MatchType, "prefix"), "service_id": in.ServiceID,
		"https": in.HTTPS, "advanced_rule": strings.TrimSpace(in.AdvancedRule),
		"updated_by": actorID,
	}
	if err := s.routes.Update(ctx, rt, in.ExpectedVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(rt.RowVersion)
		}
		if isUnique(err) {
			return nil, httperr.ValidationFailed("该节点下路由名已存在", httperr.Detail{Field: "name"})
		}
		return nil, httperr.Internal(err)
	}
	if in.MiddlewareIDs != nil {
		if apiErr := s.bindMiddlewares(ctx, id, in.MiddlewareIDs, rt.NodeID); apiErr != nil {
			return nil, apiErr
		}
	}
	fresh, apiErr := s.Get(ctx, id)
	if apiErr != nil {
		return nil, apiErr
	}
	s.audit.Record(ctx, nil, ev(actorID, "update", id, fresh.Name, before, fresh))
	return fresh, nil
}

// SetStatus 草稿→启用 / 禁用 / 归档（Route 状态机守卫，data-model §6）。
func (s *Service) SetStatus(ctx context.Context, id, to string, expected int64, actorID string) (*View, *httperr.APIError) {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "路由")
	}
	if err := state.Route.Can(rt.Status, to); err != nil {
		return nil, httperr.ValidationFailed(err.Error(), httperr.Detail{Field: "status", Hint: "草稿→启用→禁用→归档（终态）"})
	}
	if err := s.routes.Update(ctx, rt, expected, map[string]any{"status": to, "updated_by": actorID}); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(rt.RowVersion)
		}
		return nil, httperr.Internal(err)
	}
	fresh, _ := s.Get(ctx, id)
	s.audit.Record(ctx, nil, ev(actorID, "update", id, rt.Name, map[string]any{"status": rt.Status}, map[string]any{"status": to}))
	return fresh, nil
}

func (s *Service) Delete(ctx context.Context, id, actorID string) *httperr.APIError {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		return mapNotFound(err, "路由")
	}
	if err := s.routes.SoftDelete(ctx, id); err != nil {
		return httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "delete", id, rt.Name, rt, nil))
	return nil
}

// validateInput：草稿态宽松、启用要素完整；跨节点引用即时拒绝。
func (s *Service) validateInput(ctx context.Context, in Input) *httperr.APIError {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 64 {
		return httperr.ValidationFailed("路由名称必填且 ≤64 字符", httperr.Detail{Field: "name"})
	}
	mode := defaultStr(in.Mode, "simple")
	if mode != "simple" && mode != "advanced" {
		return httperr.ValidationFailed("模式仅 simple/advanced", httperr.Detail{Field: "mode"})
	}
	if mode == "advanced" {
		return httperr.Newf(403, "FORBIDDEN", "高级模式路由需 gateway_admin 及以上且经 US3 专用端点保存")
	}
	if in.NodeID == "" {
		return httperr.ValidationFailed("node_id 必填", httperr.Detail{Field: "node_id"})
	}
	if _, err := s.nodes.Get(ctx, in.NodeID); err != nil {
		return mapNotFound(err, "节点")
	}
	if mt := in.MatchType; mt != "" && mt != "exact" && mt != "prefix" {
		return httperr.ValidationFailed("匹配方式仅 exact/prefix", httperr.Detail{Field: "match_type"})
	}
	if in.Path != "" && !strings.HasPrefix(in.Path, "/") {
		return httperr.ValidationFailed("路径必须以 / 开头", httperr.Detail{Field: "path", Hint: "如 /api 或 /crm"})
	}
	sv, err := s.svcs.Get(ctx, in.ServiceID)
	if err != nil {
		return mapNotFound(err, "服务")
	}
	if sv.NodeID != in.NodeID {
		return httperr.ValidationFailed("禁止跨节点引用服务", httperr.Detail{Field: "service_id", Hint: "配置以节点为作用域"})
	}
	if in.DomainID != "" {
		d, err := s.doms.Get(ctx, in.DomainID)
		if err != nil {
			return mapNotFound(err, "域名")
		}
		if d.NodeID != in.NodeID {
			return httperr.ValidationFailed("禁止跨节点引用域名", httperr.Detail{Field: "domain_id", Hint: "配置以节点为作用域"})
		}
	}
	return nil
}

func (s *Service) bindMiddlewares(ctx context.Context, routeID string, mwIDs []string, nodeID string) *httperr.APIError {
	seen := map[string]bool{}
	for _, mwID := range mwIDs {
		if seen[mwID] {
			return httperr.ValidationFailed("中间件重复绑定", httperr.Detail{Field: "middleware_ids"})
		}
		seen[mwID] = true
		mw, err := s.mws.Get(ctx, mwID)
		if err != nil {
			return mapNotFound(err, "中间件")
		}
		if mw.NodeID != nodeID {
			return httperr.ValidationFailed("禁止跨节点引用中间件 "+mw.Name, httperr.Detail{Field: "middleware_ids"})
		}
	}
	if err := s.routes.ReplaceMiddlewares(ctx, routeID, mwIDs); err != nil {
		return httperr.Internal(err)
	}
	return nil
}

func ev(actorID, action, rid, name string, before, after any) auditrec.Event {
	return auditrec.Event{ActorID: actorID, Action: action, ResourceType: "route",
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

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func nilIfEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
