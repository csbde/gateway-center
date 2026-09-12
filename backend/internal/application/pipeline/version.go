// Package pipeline 发布管线（宪章 IV 强制顺序：Draft→Validation→Generation→Diff→Deploy→Verification）。
// version.go：VersionService——当前期望态→自包含 Snapshot→Generate→ConfigVersion pending→validating→ready（T037，FR-025/027）。
// 无旁路语义：Deploy 仅接受本服务产出的 ready 版本（deploy.go 校验），validate 失败即终止且不留 ready。
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/diff"
	"gateway-center/backend/internal/domain/validate"
	"gateway-center/backend/internal/generate"
	"gateway-center/backend/internal/infrastructure/cryptox"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

// Loader 装载节点全量期望态（集成测试可用 fake；生产用 gorm 实现 LoadGraph）。
type Loader interface {
	LoadGraph(ctx context.Context, nodeID string) (*validate.NodeGraph, error)
}

type GraphLoader struct {
	nodes   *pgstore.NodeRepo
	domains *pgstore.DomainRepo
	svcs    *pgstore.ServiceRepo
	targets *pgstore.TargetRepo
	routes  *pgstore.RouteRepo
	mws     *pgstore.MiddlewareRepo
}

func NewGraphLoader(nodes *pgstore.NodeRepo, domains *pgstore.DomainRepo,
	svcs *pgstore.ServiceRepo, targets *pgstore.TargetRepo, routes *pgstore.RouteRepo, mws *pgstore.MiddlewareRepo) *GraphLoader {
	return &GraphLoader{nodes: nodes, domains: domains, svcs: svcs, targets: targets, routes: routes, mws: mws}
}

func (l *GraphLoader) LoadGraph(ctx context.Context, nodeID string) (*validate.NodeGraph, error) {
	n, err := l.nodes.Get(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	g := &validate.NodeGraph{Node: n, Targets: map[string][]domain.Target{}, MwBindings: map[string][]string{}}
	if g.Domains, _, err = l.domains.List(ctx, filtered(nodeID)); err != nil {
		return nil, err
	}
	if g.Services, _, err = l.svcs.List(ctx, filtered(nodeID)); err != nil {
		return nil, err
	}
	if g.Routes, err = l.routes.ListByNode(ctx, nodeID); err != nil {
		return nil, err
	}
	if g.Middlewares, _, err = l.mws.List(ctx, filtered(nodeID)); err != nil {
		return nil, err
	}
	for i := range g.Services {
		ts, err := l.targets.ListByService(ctx, g.Services[i].ID)
		if err != nil {
			return nil, err
		}
		g.Targets[g.Services[i].ID] = ts
	}
	for _, rt := range g.Routes {
		ids := make([]string, 0, len(rt.Middlewares))
		for _, rm := range rt.Middlewares {
			ids = append(ids, rm.MiddlewareID)
		}
		g.MwBindings[rt.ID] = ids
	}
	return g, nil
}

func filtered(nodeID string) pgstore.ListQuery {
	return pgstore.ListQuery{Page: 1, PageSize: 0, NodeID: nodeID}
}

// ---- VersionService ----

type VersionService struct {
	loader  Loader
	vers    *pgstore.VersionRepo
	deploys *pgstore.DeploymentRepo
	certs   *pgstore.CertRepo
	cipher  *cryptox.Cipher
	audit   *auditrec.Recorder
}

func NewVersionService(loader Loader, vers *pgstore.VersionRepo, deploys *pgstore.DeploymentRepo,
	certs *pgstore.CertRepo, cipher *cryptox.Cipher, audit *auditrec.Recorder) *VersionService {
	return &VersionService{loader: loader, vers: vers, deploys: deploys, certs: certs, cipher: cipher, audit: audit}
}

// ValidateResult 是 POST /nodes/{id}/validate 的响应（也是 CreateVersion 的第一阶段）。
type ValidateResult struct {
	NodeID     string           `json:"node_id"`
	VersionID  string           `json:"version_id,omitempty"` // dry-run 时为空
	Version    int64            `json:"version,omitempty"`
	Valid      bool             `json:"valid"`
	Issues     []validate.Issue `json:"issues"`
	RouteCount int              `json:"route_count"`
	Summary    map[string]int   `json:"changes_summary,omitempty"`
}

// Validate dry-run：仅六类检查 + 与最近成功版本的 diff 预览（不落版本）。
func (s *VersionService) Validate(ctx context.Context, nodeID string) (*ValidateResult, *httperr.APIError) {
	g, err := s.loader.LoadGraph(ctx, nodeID)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, httperr.NotFound("节点")
		}
		return nil, httperr.Internal(err)
	}
	rep := validate.RunNode(g)
	res := &ValidateResult{NodeID: nodeID, Valid: rep.Valid, Issues: rep.Issues, RouteCount: rep.RouteCount}
	if res.Issues == nil {
		res.Issues = []validate.Issue{}
	}
	snap, apiErr := s.buildSnapshot(ctx, g)
	if apiErr != nil {
		res.Valid = false
		res.Issues = append(res.Issues, validate.Issue{Category: "artifact", Resource: "snapshot",
			Code: "snapshot_build_failed", Message: apiErr.Message, Blocking: true})
		return res, nil
	}
	if prev, err := s.vers.LatestSuccessByNode(ctx, nodeID); err == nil {
		if old, ok := snapshotFrom(prev.Snapshot); ok {
			res.Summary = diff.Summary(diff.SnapshotDiff(old, snap))
		}
	} else if !errors.Is(err, pgstore.ErrNotFound) {
		return nil, httperr.Internal(err)
	}
	return res, nil
}

// CreateVersion 强制管线前半段：validate（失败即 PIPELINE_BLOCKED）→ 生成 → pending→validating→ready。
// 返回 ready 版本供 Deploy 消费（无旁路：未 ready 不可部署）。
func (s *VersionService) CreateVersion(ctx context.Context, nodeID, actorID string) (*domain.ConfigVersion, *ValidateResult, *httperr.APIError) {
	g, err := s.loader.LoadGraph(ctx, nodeID)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, nil, httperr.NotFound("节点")
		}
		return nil, nil, httperr.Internal(err)
	}
	rep := validate.RunNode(g)
	vres := &ValidateResult{NodeID: nodeID, Valid: rep.Valid, Issues: rep.Issues, RouteCount: rep.RouteCount}
	if vres.Issues == nil {
		vres.Issues = []validate.Issue{}
	}
	if !rep.Valid {
		return nil, vres, blocked(rep.Issues)
	}
	snap, apiErr := s.buildSnapshot(ctx, g)
	if apiErr != nil {
		return nil, vres, apiErr
	}
	// 生成产物 + 自检（Generate 内含零明文断言 = artifact 类检查）
	arts, err := generate.Generate(snap)
	if err != nil {
		return nil, vres, blocked([]validate.Issue{{Category: "artifact",
			Resource: "generation", Code: "generate_failed", Message: err.Error(), Blocking: true}})
	}
	snapJSON, err := json.Marshal(snap)
	if err != nil {
		return nil, vres, httperr.Internal(err)
	}
	var snapMap map[string]any
	_ = json.Unmarshal(snapJSON, &snapMap)

	verNum, err := s.vers.NextVersion(ctx, nodeID)
	if err != nil {
		return nil, vres, httperr.Internal(err)
	}
	var parent *string
	var changes map[string]any
	if prev, err := s.vers.LatestSuccessByNode(ctx, nodeID); err == nil {
		pid := prev.ID
		parent = &pid
		if old, ok := snapshotFrom(prev.Snapshot); ok {
			items := diff.SnapshotDiff(old, snap)
			changes = map[string]any{"summary": diff.Summary(items), "items": items}
		}
	} else if !errors.Is(err, pgstore.ErrNotFound) {
		return nil, vres, httperr.Internal(err)
	}
	if changes == nil {
		changes = map[string]any{"summary": map[string]int{"added": len(snap.Routers) + len(snap.Services) + len(snap.Middlewares), "modified": 0, "removed": 0}, "items": []any{}}
	}

	v := &domain.ConfigVersion{
		NodeID: nodeID, Version: verNum, Status: "pending",
		Snapshot: snapMap, ArtifactFiles: generate.ArtifactToFiles(arts),
		ChangesSummary: changes, ParentVersionID: parent, Origin: "forward",
	}
	v.SetActor(actorID)
	if err := s.vers.Create(ctx, v); err != nil {
		return nil, vres, httperr.Internal(err)
	}
	// pending→validating→ready（生成+自检同事务窗口内顺序迁移；失败版本停留 validating 即不可部署）
	if err := s.vers.AdvanceStatus(ctx, v.ID, "pending", "validating"); err != nil {
		return nil, vres, httperr.Internal(err)
	}
	if err := s.vers.AdvanceStatus(ctx, v.ID, "validating", "ready"); err != nil {
		return nil, vres, httperr.Internal(err)
	}
	v.Status = "ready"
	vres.VersionID, vres.Version = v.ID, v.Version
	s.audit.Record(ctx, nil, auditrec.Event{ActorID: actorID, Action: "generate",
		ResourceType: "config_version", ResourceID: v.ID, ResourceName: fmt.Sprintf("%s v%d", g.Node.Name, v.Version),
		After: map[string]any{"node_id": nodeID, "version": v.Version, "changes": changes["summary"]}})
	return v, vres, nil
}

// SnapshotFrom 反序列化 ConfigVersion.Snapshot（回滚/diff 用，导出供 handlers）。
func SnapshotFrom(m map[string]any) (*generate.Snapshot, bool) {
	return snapshotFrom(m)
}

func snapshotFrom(m map[string]any) (*generate.Snapshot, bool) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	var s generate.Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, false
	}
	return &s, true
}

// buildSnapshot 期望态→自包含快照（仅 enabled 资源入产物；draft/disabled/archived 排除，宪章 VI/US7）。
func (s *VersionService) buildSnapshot(ctx context.Context, g *validate.NodeGraph) (*generate.Snapshot, *httperr.APIError) {
	snap := &generate.Snapshot{NodeID: g.Node.ID, NodeName: g.Node.Name, EnvType: string(g.Node.EnvType)}
	domByID := map[string]*domain.Domain{}
	for i := range g.Domains {
		d := &g.Domains[i]
		domByID[d.ID] = d
	}
	svcName := map[string]string{}
	for _, sv := range g.Services {
		if !sv.Enabled {
			continue
		}
		name := slug(sv.Name)
		snap.Services = append(snap.Services, generate.ServiceSnap{ID: sv.ID, Name: name})
		svcName[sv.ID] = name
		// Targets 追加需回填——先建索引
	}
	si := map[string]int{}
	for i := range snap.Services {
		si[snap.Services[i].ID] = i
	}
	for _, sv := range g.Services {
		idx, ok := si[sv.ID]
		if !ok {
			continue
		}
		for _, t := range g.Targets[sv.ID] {
			if !t.Enabled {
				continue
			}
			w := t.Weight
			if w <= 0 {
				w = 1
			}
			snap.Services[idx].Targets = append(snap.Services[idx].Targets, generate.TargetSnap{URL: t.URL, Weight: w})
		}
	}
	mwName := map[string]string{}
	for _, mw := range g.Middlewares {
		if !mw.Enabled {
			continue
		}
		mwName[mw.ID] = slug(mw.Name)
		snap.Middlewares = append(snap.Middlewares, generate.MiddlewareSnap{ID: mw.ID, Name: slug(mw.Name), Type: mw.Type, Params: mw.Params})
	}
	certSeen := map[string]bool{}
	for _, rt := range g.Routes {
		if rt.Status != "enabled" {
			continue
		}
		r := generate.RouterSnap{ID: rt.ID, Name: slug(rt.Name), Mode: rt.Mode,
			ServiceName: svcName[rt.ServiceID], HTTPS: rt.HTTPS, Priority: rt.Priority}
		if r.ServiceName == "" {
			return nil, blocked([]validate.Issue{{Category: "dependency",
				Resource: "route " + rt.Name, Code: "route_service_missing", Message: "启用路由的服务不在快照（服务被禁用？）", Blocking: true}})
		}
		r.EntryPoint = "web"
		if rt.HTTPS {
			r.EntryPoint = "websecure"
		}
		if rt.Mode == "advanced" {
			r.AdvancedRule = rt.AdvancedRule
		} else if rt.DomainID != nil {
			d := domByID[*rt.DomainID]
			if d == nil {
				return nil, blocked([]validate.Issue{{Category: "dependency",
					Resource: "route " + rt.Name, Code: "route_domain_missing", Message: "域名不存在或已删除", Blocking: true}})
			}
			r.DomainName, r.Path, r.MatchType = d.Name, rt.Path, rt.MatchType
			if rt.HTTPS {
				r.CertMode = string(d.HTTPSPolicy)
				r.Resolver = d.CertResolverRef
				if d.HTTPSPolicy == domain.PolicyImported && d.ImportedCertID != nil && !certSeen[*d.ImportedCertID] {
					c, err := s.certs.Get(ctx, *d.ImportedCertID)
					if err != nil {
						if errors.Is(err, pgstore.ErrNotFound) {
							return nil, blocked([]validate.Issue{{Category: "domain",
								Resource: "domain " + d.Name, Code: "cert_missing_blocking", Message: "域名声明 imported 但证书材料缺失", Blocking: true}})
						}
						return nil, httperr.Internal(err)
					}
					snap.Certificates = append(snap.Certificates, generate.CertSnap{
						ID: c.ID, Name: slug(strings.TrimPrefix(d.Name, "*.")), DomainName: d.Name,
						CertPEM: c.CertPEM, PrivateKeyRef: c.ID,
					})
					certSeen[c.ID] = true
				}
			}
		}
		for _, mwID := range g.MwBindings[rt.ID] {
			if nm, ok := mwName[mwID]; ok {
				r.MiddlewareNames = append(r.MiddlewareNames, nm)
			} else {
				// 启用的路由引用了不在快照的中间件（被禁用/删除）——validate 应已阻断，双保险
				return nil, blocked([]validate.Issue{{Category: "dependency",
					Resource: "route " + rt.Name, Code: "route_mw_missing", Message: "路由引用的中间件已禁用或不存在", Blocking: true}})
			}
		}
		if r.MiddlewareNames == nil {
			r.MiddlewareNames = []string{}
		}
		snap.Routers = append(snap.Routers, r)
	}
	// 无任何 router 的快照合法（清空动态配置），由 Diff/确认环节负责人工知晓
	return snap, nil
}

// slug Traefik 资源名：小写字母数字连字符（与迁移 CHECK/mwreg.ValidSlug 同语义）。
func slug(s string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == '.' || r == ' ':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "res"
	}
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

// blocked 将阻断性 Issue 转为 422 PIPELINE_BLOCKED（首条为主消息，全部入 details）。
func blocked(issues []validate.Issue) *httperr.APIError {
	msg := "验证未通过"
	var first *validate.Issue
	for _, i := range issues {
		if i.Blocking {
			first = &i
			msg = i.Resource + ": " + i.Message
			break
		}
	}
	details := make([]httperr.Detail, 0, len(issues))
	for _, i := range issues {
		details = append(details, httperr.Detail{Field: i.Category + "." + i.Code, Message: i.Resource + " " + i.Message, Hint: i.Hint})
	}
	_ = first
	return httperr.PipelineBlocked(msg, details...)
}
