// Package routesvc 路由用例（T035，AC-004、FR-014）。
// 简单模式：用户零 Traefik 语法，保存即回显 generated_rule_preview（与 generate/validate 同源）。
// 高级模式写路径（T060，FR-015/016、宪法 II/X）：仅 gateway_admin+，advrule 白名单文法强校验，
// 每次使用写 advanced_edit 审计（resource_id=route_id）；简单模式全流程不感知高级字段（双模式隔离）。
package routesvc

import (
	"context"
	"errors"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/advrule"
	"gateway-center/backend/internal/domain/state"
	"gateway-center/backend/internal/generate"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

// Principal 调用者身份（handler 从认证上下文装配；rank 用于角色下限判断）。
type Principal struct {
	ID   string
	Role string
}

var roleRank = map[string]int{"viewer": 0, "developer": 1, "gateway_admin": 2, "super_admin": 3}

func (p Principal) atLeast(min string) bool { return roleRank[p.Role] >= roleRank[min] }

// RiskNotice 高级模式固定风险提示（FR-016 四项；validate-advanced 与保存路径同一文案源）。
const RiskNotice = "高级模式直接编写网关匹配表达式：写错会导致流量无法匹配；该表达式不受简单向导约束，" +
	"保存与每次修改都会进入审计并需 gateway_admin 以上权限；请确认你理解 &&/|| 与函数语义后再发布。"

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

func (s *Service) Create(ctx context.Context, in Input, actor Principal) (*View, *httperr.APIError) {
	mode := defaultStr(in.Mode, "simple")
	if mode == "advanced" {
		if apiErr := s.checkAdvanced(ctx, in, actor); apiErr != nil {
			return nil, apiErr
		}
	} else {
		in.AdvancedRule = "" // 宪法 II：简单模式不携带高级字段（双模式隔离）
	}
	if apiErr := s.validateInput(ctx, in); apiErr != nil {
		return nil, apiErr
	}
	taken, err := s.routes.NameTaken(ctx, in.NodeID, in.Name)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if taken {
		return nil, httperr.ValidationFailed("该节点下路由名已存在", httperr.Detail{Field: "name", Hint: "更换名称，或删除/归档同名路由后重试"})
	}
	maxp, err := s.routes.MaxPriority(ctx, in.NodeID)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	rt := &domain.Route{
		NodeID: in.NodeID, Name: strings.TrimSpace(in.Name),
		Mode:     mode,
		DomainID: nilIfEmpty(in.DomainID), Path: in.Path,
		MatchType: defaultStr(in.MatchType, "prefix"),
		ServiceID: in.ServiceID, HTTPS: in.HTTPS,
		Priority:     maxp + 10, // 自动递增留间隙（data-model §6）
		AdvancedRule: strings.TrimSpace(in.AdvancedRule),
		Status:       "draft", // 新建即草稿，发布链负责 enabled
	}
	rt.SetActor(actor.ID)
	if err := s.routes.Create(ctx, rt); err != nil {
		if isUnique(err) {
			return nil, httperr.ValidationFailed("该节点下路由名已存在", httperr.Detail{Field: "name", Hint: "更换名称，或删除/归档同名路由后重试"})
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
	s.audit.Record(ctx, nil, ev(actor.ID, "create", rt.ID, rt.Name, nil, v))
	if mode == "advanced" {
		s.auditAdvanced(ctx, actor.ID, rt.ID, rt.Name, rt.AdvancedRule)
	}
	return v, nil
}

func (s *Service) Update(ctx context.Context, id string, in Input, actor Principal) (*View, *httperr.APIError) {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "路由")
	}
	in.NodeID = rt.NodeID
	// 模式切换（simple↔advanced）也走高级门禁；未提供 mode 时保持原模式。
	targetMode := defaultStr(in.Mode, rt.Mode)
	if targetMode == "advanced" {
		if strings.TrimSpace(in.AdvancedRule) == "" {
			in.AdvancedRule = rt.AdvancedRule // 仅改其他字段时沿用已存表达式（仍复核角色）
		}
		if apiErr := s.checkAdvanced(ctx, in, actor); apiErr != nil {
			return nil, apiErr
		}
	} else {
		in.AdvancedRule = rt.AdvancedRule
		if rt.Mode == "advanced" {
			in.AdvancedRule = "" // advanced→simple：清空高级表达式
		}
	}
	if apiErr := s.validateInput(ctx, in); apiErr != nil {
		return nil, apiErr
	}
	if in.ExpectedVersion == 0 {
		in.ExpectedVersion = rt.RowVersion
	}
	before := *rt
	fields := map[string]any{
		"name": strings.TrimSpace(in.Name), "mode": targetMode,
		"domain_id": nilIfEmpty(in.DomainID), "path": in.Path,
		"match_type": defaultStr(in.MatchType, "prefix"), "service_id": in.ServiceID,
		"https": in.HTTPS, "advanced_rule": strings.TrimSpace(in.AdvancedRule),
		"updated_by": actor.ID,
	}
	if err := s.routes.Update(ctx, rt, in.ExpectedVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(rt.RowVersion)
		}
		if isUnique(err) {
			return nil, httperr.ValidationFailed("该节点下路由名已存在", httperr.Detail{Field: "name", Hint: "更换名称，或删除/归档同名路由后重试"})
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
	s.audit.Record(ctx, nil, ev(actor.ID, "update", id, fresh.Name, before, fresh))
	if targetMode == "advanced" {
		s.auditAdvanced(ctx, actor.ID, id, fresh.Name, fresh.AdvancedRule)
	}
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
	// advanced 模式的角色/表达式校验在 Create/Update 前置的 checkAdvanced 完成；
	// 此处仅确保表达式非空（写入前已被强校验，空值即数据不一致）。
	if in.NodeID == "" {
		return httperr.ValidationFailed("node_id 必填", httperr.Detail{Field: "node_id", Hint: "路由按节点作用域，先选择网关"})
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

// checkAdvanced 高级模式写路径统一前置门禁（T060）：角色 ≥gateway_admin（FR-015）
// + 白名单文法强校验（FR-016「保存即校验」）。错误信息定位到表达式问题本身。
func (s *Service) checkAdvanced(ctx context.Context, in Input, actor Principal) *httperr.APIError {
	if !actor.atLeast("gateway_admin") {
		return httperr.Newf(403, "FORBIDDEN", "高级模式路由仅 gateway_admin 及以上可保存")
	}
	rule := strings.TrimSpace(in.AdvancedRule)
	if rule == "" {
		return httperr.ValidationFailed("高级模式必须提供规则表达式", httperr.Detail{Field: "advanced_rule", Hint: "如 Host(`crm.example.com`) && PathPrefix(`/api`)"})
	}
	if msg := advrule.Validate(rule); msg != "" {
		return httperr.ValidationFailed("高级表达式不合法: "+msg, httperr.Detail{Field: "advanced_rule", Hint: "仅支持 Host/HostRegexp/Path/PathPrefix/Headers/HeadersRegexp/Method 与 &&/||/括号"})
	}
	return nil
}

// AdvancedValidateResult POST /routes/validate-advanced 响应体（纯校验，不写库）。
type AdvancedValidateResult struct {
	Valid             bool             `json:"valid"`
	Issues            []map[string]any `json:"issues"`
	RiskNotice        string           `json:"risk_notice"`
	NormalizedPreview string           `json:"normalized_preview"`
}

// ValidateAdvanced 供实时语法验证端点；同样要求 gateway_admin+（端点在路由组已守卫，
// 服务层再复核一次——宪法 XII 纵深防御）。
func (s *Service) ValidateAdvanced(ctx context.Context, rule string, actor Principal) (*AdvancedValidateResult, *httperr.APIError) {
	if !actor.atLeast("gateway_admin") {
		return nil, httperr.Newf(403, "FORBIDDEN", "高级模式仅 gateway_admin 及以上可用")
	}
	res := &AdvancedValidateResult{RiskNotice: RiskNotice, Issues: []map[string]any{}}
	rule = strings.TrimSpace(rule)
	if msg := advrule.Validate(rule); msg != "" {
		res.Issues = append(res.Issues, map[string]any{"message": msg})
	} else {
		res.Valid = true
		res.NormalizedPreview = advrule.Normalize(rule)
	}
	return res, nil
}

// auditAdvanced 每次使用高级模式写 advanced_edit 审计（FR-016/宪法 X）。
func (s *Service) auditAdvanced(ctx context.Context, actorID, routeID, routeName, rule string) {
	s.audit.Record(ctx, nil, auditrec.Event{ActorID: actorID, Action: "advanced_edit",
		ResourceType: "route", ResourceID: routeID, ResourceName: routeName,
		After: map[string]any{"advanced_rule": rule, "risk_notice_ack": true}})
}

func (s *Service) bindMiddlewares(ctx context.Context, routeID string, mwIDs []string, nodeID string) *httperr.APIError {
	seen := map[string]bool{}
	for _, mwID := range mwIDs {
		if seen[mwID] {
			return httperr.ValidationFailed("中间件重复绑定", httperr.Detail{Field: "middleware_ids", Hint: "同一中间件在同一路由仅可绑定一次"})
		}
		seen[mwID] = true
		mw, err := s.mws.Get(ctx, mwID)
		if err != nil {
			return mapNotFound(err, "中间件")
		}
		if mw.NodeID != nodeID {
			return httperr.ValidationFailed("禁止跨节点引用中间件 "+mw.Name, httperr.Detail{Field: "middleware_ids", Hint: "配置以节点为作用域，选用本节点下的中间件"})
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
