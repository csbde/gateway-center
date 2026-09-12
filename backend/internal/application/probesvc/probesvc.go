// Package probesvc 节点健康探测（T040，FR-002/003；完整 offline/degraded 判定 US5 T071）。
// 每周期 GET /api/overview + 资源集合摘要回写 node_states（desired/actual 分列，宪章 XI 的 actual 侧数据源）。
// 状态判定：连续失败达阈值→offline（last_online_at 保留）；可达但启用 Target 失败/加载错误→degraded。
package probesvc

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gateway-center/backend/internal/application/driftsvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/traefikapi"
)

type ClientFactory func(n *domain.GatewayNode) (*traefikapi.Client, error)

type Service struct {
	nodes    *pgstore.NodeRepo
	targets  *pgstore.TargetRepo
	settings Settings
	factory  ClientFactory
	drift    *driftsvc.Service // 探测成功后比对漂移（T072；nil=跳过）
	mu       sync.Mutex        // 同节点探测不重叠
	running  map[string]bool
}

// Settings 探测参数（platform_settings 快照）。
type Settings interface {
	ProbeInterval() time.Duration
	FailureThreshold() int
}

// SettingsAdapter 把 settingsvc.Service 的快照适配为 probesvc.Settings。
type SettingsAdapter struct {
	Snap func() *domain.PlatformSettings
}

func (a SettingsAdapter) ProbeInterval() time.Duration {
	return time.Duration(a.Snap().ProbeIntervalSec) * time.Second
}
func (a SettingsAdapter) FailureThreshold() int { return a.Snap().OfflineThreshold }

func New(nodes *pgstore.NodeRepo, targets *pgstore.TargetRepo, settings Settings, factory ClientFactory, drift *driftsvc.Service) *Service {
	return &Service{nodes: nodes, targets: targets, settings: settings, factory: factory,
		drift: drift, running: map[string]bool{}}
}

// RunOnce 探测全部节点（并发上限由 scheduler 的 sem 保证外层；此处仅防单节点重入）。
func (s *Service) RunOnce(ctx context.Context) {
	ids, err := s.nodes.AllIDs(ctx)
	if err != nil {
		slog.Error("枚举节点失败", "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		s.mu.Lock()
		if s.running[id] {
			s.mu.Unlock()
			continue
		}
		s.running[id] = true
		s.mu.Unlock()
		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.running, nodeID)
				s.mu.Unlock()
			}()
			s.probe(ctx, nodeID)
		}(id)
	}
	wg.Wait()
}

func (s *Service) probe(ctx context.Context, nodeID string) {
	n, err := s.nodes.Get(ctx, nodeID)
	if err != nil {
		return
	}
	st, err := s.nodes.State(ctx, nodeID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	st.NodeID = nodeID
	st.LastProbeAt = &now
	if !n.Enabled {
		st.Status = "unknown" // 禁用节点不探测（部署同样冻结）
		_ = s.nodes.UpsertState(ctx, st)
		return
	}
	c, err := s.factory(n)
	if err != nil {
		// 客户端构造失败亦计入连续失败（任何探测未达均算一次，FR-003）
		s.markFailure(st, now)
		_ = s.nodes.UpsertState(ctx, st)
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ov, err := c.Overview(pctx)
	if err != nil {
		s.markFailure(st, now)
		_ = s.nodes.UpsertState(ctx, st)
		return
	}
	// 可达：重置失败计数，记录在线时刻（last_online_at 在此后失败时保留，FR-003）
	st.ConsecutiveFailures = 0
	st.LastOnlineAt = &now
	st.TraefikVersion = ov.Version

	// 加载实际态资源集合；任一拉取失败→加载错误（degraded 信号，宪章 XI actual 侧）
	loadErr := false
	if rs, err := c.HTTPRouters(pctx); err == nil {
		st.LoadedRouters = map[string]any{"names": keys(rs), "count": len(rs)}
	} else {
		loadErr = true
	}
	if svcs, err := c.HTTPServices(pctx); err == nil {
		st.LoadedServices = map[string]any{"names": boolKeys(svcs), "count": len(svcs)}
	} else {
		loadErr = true
	}
	if mws, err := c.HTTPMiddlewares(pctx); err == nil {
		st.LoadedMiddlewares = map[string]any{"names": boolKeys(mws), "count": len(mws)}
	} else {
		loadErr = true
	}

	// 启用 Target 失败→degraded（healthsvc 探测写回的 HealthStatus，FR-003/spec Assumption）
	targetDown := false
	if ts, err := s.targets.ListEnabledByNode(ctx, nodeID); err == nil {
		for _, t := range ts {
			if t.HealthStatus == "down" {
				targetDown = true
				break
			}
		}
	}

	if loadErr || targetDown {
		st.Status = "degraded"
	} else {
		st.Status = "online"
	}
	_ = s.nodes.UpsertState(ctx, st)
	// 仅在线时 actual 数据新鲜 → 比对漂移（T072；失败/禁用路径保留既有 drift 不触碰）
	if s.drift != nil {
		s.drift.Check(ctx, nodeID)
	}
}

// markFailure 记录一次探测失败：连续达阈值→offline，否则 unknown。
// last_online_at 不在此触碰，故跨失败保留最近一次在线时刻（FR-003）。
func (s *Service) markFailure(st *domain.NodeState, _ time.Time) {
	st.ConsecutiveFailures++
	if st.ConsecutiveFailures >= s.threshold() {
		st.Status = "offline"
	} else {
		st.Status = "unknown"
	}
}

func (s *Service) threshold() int {
	if t := s.settings.FailureThreshold(); t > 0 {
		return t
	}
	return 3
}

func keys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func boolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
