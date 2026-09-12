// Package driftsvc 配置漂移检测（T072，FR-040/AC-013、宪章 XI 期望/实际）。
// 每轮探测后比对 desired（最近成功版本快照资源集合）vs actual（Traefik 加载集合）→
// node_states.drift/drift_detail。仅在线时 actual 数据新鲜，故由 probesvc 在探测成功后
// 同 goroutine 调用 Check（避免与 probesvc 全行 UpsertState 的并发写竞争）。
// 列级 UpdateDriftState 只写漂移字段，probesvc 只写探测字段，二者互不覆写。
package driftsvc

import (
	"context"
	"log/slog"
	"sort"

	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/generate"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	nodes *pgstore.NodeRepo
	vers  *pgstore.VersionRepo
}

func New(nodes *pgstore.NodeRepo, vers *pgstore.VersionRepo) *Service {
	return &Service{nodes: nodes, vers: vers}
}

// Check 评估单节点漂移并写回 drift 字段（probesvc 探测成功后调用）。
// desired = 最新 ready 版本号（有更新 ready 未部署时 desired > actual）；
// actual 基线 = 最近成功版本快照的资源名集合 vs Traefik 实际加载集合。
// 无成功部署 → 无漂移参照（不标红）；快照损坏 → 跳过（不误报）。
func (s *Service) Check(ctx context.Context, nodeID string) {
	st, err := s.nodes.State(ctx, nodeID)
	if err != nil {
		slog.Warn("driftsvc: 读取节点状态失败", "node_id", nodeID, "err", err)
		return
	}
	success, err := s.vers.LatestSuccessByNode(ctx, nodeID)
	if err != nil {
		// 无成功版本：desired/actual 归零、清除漂移（无基线不误报）
		_ = s.nodes.UpdateDriftState(ctx, nodeID, false, nil, 0, 0)
		return
	}
	desiredVer := success.Version
	if ready, err := s.vers.LatestReady(ctx, nodeID); err == nil && ready.Version > desiredVer {
		desiredVer = ready.Version
	}
	snap, ok := pipeline.SnapshotFrom(success.Snapshot)
	if !ok {
		slog.Warn("driftsvc: 成功版本快照损坏，跳过漂移判定", "node_id", nodeID)
		return
	}
	detail := compareDrift(snap, st)
	_ = s.nodes.UpdateDriftState(ctx, nodeID, len(detail) > 0, detail, desiredVer, success.Version)
}

// compareDrift 比对期望快照资源名集合 vs 实际加载集合，返回差异清单。
// missing = 期望有但网关未加载（资源丢失）；unexpected = 网关加载但期望无（手动新增）。
func compareDrift(snap *generate.Snapshot, st *domain.NodeState) []map[string]any {
	var detail []map[string]any
	add := func(kind string, want, have map[string]bool) {
		for _, name := range sortedKeys(missing(want, have)) {
			detail = append(detail, map[string]any{"type": "missing", "resource": kind, "name": name})
		}
		for _, name := range sortedKeys(missing(have, want)) {
			detail = append(detail, map[string]any{"type": "unexpected", "resource": kind, "name": name})
		}
	}
	add("router", snapNames(snap.Routers, func(r generate.RouterSnap) string { return r.Name }),
		loadedNames(st.LoadedRouters))
	add("service", snapNames(snap.Services, func(s generate.ServiceSnap) string { return s.Name }),
		loadedNames(st.LoadedServices))
	add("middleware", snapNames(snap.Middlewares, func(m generate.MiddlewareSnap) string { return m.Name }),
		loadedNames(st.LoadedMiddlewares))
	return detail
}

// snapNames 从快照资源切片提取名集合。
func snapNames[T any](items []T, name func(T) string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, it := range items {
		out[name(it)] = true
	}
	return out
}

// loadedNames 从 node_states 的 LoadedXxx jsonb 提取名集合。
// probesvc 写入 {"names": []string, "count": N}；经 DB jsonb 往返后 names 变 []any。
func loadedNames(m map[string]any) map[string]bool {
	out := map[string]bool{}
	if m == nil {
		return out
	}
	switch raw := m["names"].(type) {
	case []any:
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out[s] = true
			}
		}
	case []string:
		for _, s := range raw {
			out[s] = true
		}
	}
	return out
}

func missing(want, have map[string]bool) []string {
	var out []string
	for k := range want {
		if !have[k] {
			out = append(out, k)
		}
	}
	return out
}

func sortedKeys(s []string) []string {
	sort.Strings(s)
	return s
}
