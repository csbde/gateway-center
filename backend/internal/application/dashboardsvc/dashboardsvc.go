// Package dashboardsvc Dashboard 聚合用例（T068/T073，FR-008/041、AC-013）。
// 只读汇总：节点在线/离线/漂移计数、资源计数、证书到期预警、最近发布。
// 数据源自各 repo 的现态（探测/漂移由 probesvc/driftsvc 写入 node_states，本服务只读）。
package dashboardsvc

import (
	"context"
	"sort"
	"time"

	"gateway-center/backend/internal/application/certsvc"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	nodes    *pgstore.NodeRepo
	doms     *pgstore.DomainRepo
	svcs     *pgstore.ServiceRepo
	routes   *pgstore.RouteRepo
	mws      *pgstore.MiddlewareRepo
	certs    *pgstore.CertRepo
	deploys  *pgstore.DeploymentRepo
	settings *pgstore.SettingsRepo
}

func New(nodes *pgstore.NodeRepo, doms *pgstore.DomainRepo, svcs *pgstore.ServiceRepo,
	routes *pgstore.RouteRepo, mws *pgstore.MiddlewareRepo, certs *pgstore.CertRepo,
	deploys *pgstore.DeploymentRepo, settings *pgstore.SettingsRepo) *Service {
	return &Service{nodes: nodes, doms: doms, svcs: svcs, routes: routes,
		mws: mws, certs: certs, deploys: deploys, settings: settings}
}

type Dashboard struct {
	NodesOnline   int             `json:"nodes_online"`
	NodesOffline  int             `json:"nodes_offline"`
	NodesDegraded int             `json:"nodes_degraded"`
	NodesDrift    int             `json:"nodes_drift"`
	Counts        CountSummary    `json:"counts"`
	ExpiringCerts []CertSummary   `json:"expiring_certificates"`
	RecentDeploy  []DeploySummary `json:"recent_deployments"`
}

type CountSummary struct {
	Nodes       int `json:"nodes"`
	Domains     int `json:"domains"`
	Services    int `json:"services"`
	Routes      int `json:"routes"`
	Middlewares int `json:"middlewares"`
}

type CertSummary struct {
	DomainID   string     `json:"domain_id"`
	DomainName string     `json:"domain_name"`
	Status     string     `json:"status"`
	NotAfter   *time.Time `json:"not_after,omitempty"`
	Issuer     string     `json:"issuer,omitempty"`
}

type DeploySummary struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	NodeName  string    `json:"node_name"`
	Status    string    `json:"status"`
	Trigger   string    `json:"trigger"`
	CreatedAt time.Time `json:"created_at"`
}

// count 调用各 repo.List 取 total（listScanned 返回 items,total,err），PageSize=1 只取计数。
func count[T any](items []T, total int64, err error) int {
	if err != nil {
		return 0
	}
	return int(total)
}

func (s *Service) Aggregate(ctx context.Context) (*Dashboard, error) {
	d := &Dashboard{Counts: CountSummary{}}

	states, err := s.nodes.AllStates(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range states {
		switch st.Status {
		case "online":
			d.NodesOnline++
		case "offline":
			d.NodesOffline++
		case "degraded":
			d.NodesDegraded++
		}
		if st.Drift {
			d.NodesDrift++
		}
	}

	// 资源计数（全量 total；deleted_at 已被软删除过滤）
	q := pgstore.ListQuery{Page: 1, PageSize: 1}
	d.Counts.Nodes = count(s.nodes.List(ctx, q))
	d.Counts.Domains = count(s.doms.List(ctx, q))
	d.Counts.Services = count(s.svcs.List(ctx, q))
	d.Counts.Routes = count(s.routes.List(ctx, q))
	d.Counts.Middlewares = count(s.mws.List(ctx, q))

	// 证书到期预警：仅 expiring_soon / expired（FR-008）。阈值优先平台设置。
	warnDays := 30
	if ps, err := s.settings.Get(ctx); err == nil && ps.ExpiryWarnDays > 0 {
		warnDays = ps.ExpiryWarnDays
	}
	allCerts, err := s.certs.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	for i := range allCerts {
		c := &allCerts[i]
		st := certsvc.StatusOf(c, warnDays)
		if st == "expiring_soon" || st == "expired" {
			d.ExpiringCerts = append(d.ExpiringCerts, CertSummary{
				DomainID: c.GetDomainID(), DomainName: s.domainName(ctx, c.GetDomainID()),
				Status: st, NotAfter: c.NotAfter, Issuer: c.Issuer,
			})
		}
	}
	sort.Slice(d.ExpiringCerts, func(i, j int) bool {
		a, b := d.ExpiringCerts[i].NotAfter, d.ExpiringCerts[j].NotAfter
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return a.Before(*b)
	})

	// 最近发布（最新 5 条；listScanned 默认 created_at DESC）
	recent, _, err := s.deploys.List(ctx, pgstore.ListQuery{Page: 1, PageSize: 5})
	if err != nil {
		return nil, err
	}
	for _, dep := range recent {
		d.RecentDeploy = append(d.RecentDeploy, DeploySummary{
			ID: dep.ID, NodeID: dep.NodeID, NodeName: s.nodeName(ctx, dep.NodeID),
			Status: dep.Status, Trigger: dep.Trigger, CreatedAt: dep.CreatedAt,
		})
	}
	return d, nil
}

func (s *Service) domainName(ctx context.Context, id string) string {
	if d, err := s.doms.Get(ctx, id); err == nil {
		return d.Name
	}
	return ""
}

func (s *Service) nodeName(ctx context.Context, id string) string {
	if n, err := s.nodes.Get(ctx, id); err == nil {
		return n.Name
	}
	return ""
}
