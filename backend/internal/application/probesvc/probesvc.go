// Package probesvc 节点健康探测（T040，FR-002/003；完整 offline/degraded 判定 US5 T071）。
// 每周期 GET /api/overview + 资源集合摘要回写 node_states（desired/actual 分列，宪章 XI 的 actual 侧数据源）。
package probesvc

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/traefikapi"
)

type ClientFactory func(n *domain.GatewayNode) (*traefikapi.Client, error)

type Service struct {
	nodes    *pgstore.NodeRepo
	settings Settings
	factory  ClientFactory
	mu       sync.Mutex // 同节点探测不重叠
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

func New(nodes *pgstore.NodeRepo, settings Settings, factory ClientFactory) *Service {
	return &Service{nodes: nodes, settings: settings, factory: factory, running: map[string]bool{}}
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
		st.Status, st.ConsecutiveFailures = "unknown", st.ConsecutiveFailures+1
		_ = s.nodes.UpsertState(ctx, st)
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ov, err := c.Overview(pctx)
	if err != nil {
		st.ConsecutiveFailures++
		// 连续 3 次失败→offline（T071/FR-003；阈值可经 settings 调整）
		if st.ConsecutiveFailures >= s.threshold() {
			st.Status = "offline"
		} else if st.Status != "degraded" {
			st.Status = "unknown"
		}
		_ = s.nodes.UpsertState(ctx, st)
		return
	}
	st.ConsecutiveFailures = 0
	st.Status = "online"
	st.TraefikVersion = ov.Version
	st.LastOnlineAt = &now
	// 加载集合摘要（drift 判定 actual 侧，US5 完整化）
	if rs, err := c.HTTPRouters(pctx); err == nil {
		st.LoadedRouters = map[string]any{"names": keys(rs), "count": len(rs)}
	}
	if svcs, err := c.HTTPServices(pctx); err == nil {
		st.LoadedServices = map[string]any{"names": boolKeys(svcs), "count": len(svcs)}
	}
	if mws, err := c.HTTPMiddlewares(pctx); err == nil {
		st.LoadedMiddlewares = map[string]any{"names": boolKeys(mws), "count": len(mws)}
	}
	_ = s.nodes.UpsertState(ctx, st)
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
