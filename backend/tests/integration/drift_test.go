//go:build integration

// T075 · US5 漂移检测集成测试（quickstart V-5，FR-040/AC-013、SC-004、宪章 XI）。
// 断言：① 注入漂移（手写 rogue 路由文件）→ 周期内 drift=true 可见（≤2min）；
// ② 发布最新 ready 版本 → deployer removeStale 清除 rogue → 校验通过 → drift 清除；
// ③ 漂移期间部署旧版本 → 422（仅允许最新 ready 覆盖漂移，避免死锁）；
// ④ 断网连续失败达阈值 → offline，last_online_at 保留；
// ⑤ NodeState 全字段（期望/实际/加载集合/漂移清单）= 路由详情分列展示契约。
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/driftsvc"
	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/application/probesvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/deployer"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/traefikapi"
	"gateway-center/backend/tests/testenv"
)

// driftSettings 固定探测参数（threshold=3 → 连续 3 次失败 offline）。
type driftSettings struct{}

func (driftSettings) ProbeInterval() time.Duration { return 30 * time.Second }
func (driftSettings) FailureThreshold() int         { return 3 }

// driftFake 构建按落盘目录回报的 Traefik API 模拟（overview + routers/services/middlewares）。
// 读 dynamic/{routers,services,middlewares}/*.yml 文件名 → 模拟 file provider watch。
func driftFake(t *testing.T, rtDir string) *traefikapi.Client {
	t.Helper()
	loaded := func(sub string) []string {
		entries, _ := os.ReadDir(filepath.Join(rtDir, "dynamic", sub))
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, strings.TrimSuffix(e.Name(), ".yml"))
		}
		return out
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "v3.3.0",
			"total": map[string]any{"http": map[string]any{
				"routers": map[string]int{"total": len(loaded("routers"))}}}})
	})
	itemsHandler := func(sub string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			items := make([]map[string]any, 0)
			for _, n := range loaded(sub) {
				items = append(items, map[string]any{"name": n + "@file"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		}
	}
	mux.HandleFunc("/api/http/routers", itemsHandler("routers"))
	mux.HandleFunc("/api/http/services", itemsHandler("services"))
	mux.HandleFunc("/api/http/middlewares", itemsHandler("middlewares"))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return traefikapi.NewClient(srv.URL, "", "")
}

// wireDrift 组装 version/deploy/probe 服务，共用同一 fake（按落盘回报）。
func wireDrift(t *testing.T, env *testenv.Env, rtDir string) (*pipeline.VersionService, *pipeline.DeployService,
	*pgstore.VersionRepo, *pgstore.DeploymentRepo, *pgstore.NodeRepo, *probesvc.Service) {
	t.Helper()
	fake := driftFake(t, rtDir)
	rec := auditrec.New(pgstore.NewAuditRepo(env.Store.DB))
	loader := pipeline.NewGraphLoader(
		pgstore.NewNodeRepo(env.Store.DB), pgstore.NewDomainRepo(env.Store.DB),
		pgstore.NewServiceRepo(env.Store.DB), pgstore.NewTargetRepo(env.Store.DB),
		pgstore.NewRouteRepo(env.Store.DB), pgstore.NewMiddlewareRepo(env.Store.DB))
	vers := pgstore.NewVersionRepo(env.Store.DB)
	deploys := pgstore.NewDeploymentRepo(env.Store.DB)
	nodesRepo := pgstore.NewNodeRepo(env.Store.DB)
	factory := pipeline.TraefikFactory(func(*domain.GatewayNode) (*traefikapi.Client, error) { return fake, nil })
	vsvc := pipeline.NewVersionService(loader, vers, deploys,
		pgstore.NewCertRepo(env.Store.DB), env.Cipher, rec)
	dsvc := pipeline.NewDeployService(vers, deploys, nodesRepo,
		pgstore.NewCertRepo(env.Store.DB), env.Cipher, deployer.NewFileDeployer(), factory, rec, nil)
	driftSvc := driftsvc.New(nodesRepo, vers)
	probe := probesvc.New(nodesRepo, pgstore.NewTargetRepo(env.Store.DB), driftSettings{},
		probesvc.ClientFactory(factory), driftSvc)
	return vsvc, dsvc, vers, deploys, nodesRepo, probe
}

// TestDrift_InjectVisibleAndClearAfterDeploy V-5 主链：注入漂移→可见→发布清除。
func TestDrift_InjectVisibleAndClearAfterDeploy(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)

	rtDir := t.TempDir()
	node := env.SeedNode(t, "gw-drift", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "drift.example.com")
	svc := env.SeedService(t, node.ID, "drift-svc", "http://10.0.0.1:8080")
	env.SeedSimpleRoute(t, node.ID, dom.ID, svc.ID, "/")

	vsvc, dsvc, vers, deploys, nodesRepo, probe := wireDrift(t, env, rtDir)

	// 1. 生成版本 + 部署成功（建立 success 快照基线；deploy 成功即清 drift）
	ver, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	res, apiErr := dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver.ID, Confirmed: true}, admin.ID, syncRunner())
	require.Nil(t, apiErr)
	drec := awaitDeployment(t, deploys, res.DeploymentID)
	require.Equal(t, "success", drec.Status)

	st, _ := nodesRepo.State(ctx, node.ID)
	assert.False(t, st.Drift, "部署成功即清漂移")
	assert.Equal(t, ver.Version, st.DesiredVersion)
	assert.Equal(t, ver.Version, st.ActualVersion)

	// 2. 探测：fake 按落盘回报（匹配）→ online, drift=false
	probe.RunOnce(ctx)
	st, _ = nodesRepo.State(ctx, node.ID)
	require.Equal(t, "online", st.Status)
	assert.False(t, st.Drift, "匹配集合不应漂移")
	require.NotNil(t, st.LastOnlineAt)

	// 3. 注入漂移：手写 rogue 路由文件到 dynamic/routers/（模拟人为改 Traefik 动态配置）
	rogueDir := filepath.Join(rtDir, "dynamic", "routers")
	require.NoError(t, os.MkdirAll(rogueDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rogueDir, "rogue.yml"),
		[]byte("http:\n  routers:\n    rogue:\n      rule: Host(`rogue.local`)\n      service: noop\n"), 0o644))

	// 4. 探测：fake 读到 rogue → drift=true（SC-004: ≤2 分钟可见；此处单次探测即见）
	start := time.Now()
	probe.RunOnce(ctx)
	st, _ = nodesRepo.State(ctx, node.ID)
	assert.True(t, st.Drift, "注入漂移后应标红")
	assert.Less(t, time.Since(start), 2*time.Minute, "SC-004: 漂移须 ≤2 分钟可见")

	// 5. 路由详情分列展示契约：NodeState 全字段（期望/实际/加载集合/漂移清单）
	require.NotEmpty(t, st.LoadedRouters, "loaded_routers 须有实际加载集合")
	assert.NotEmpty(t, st.DriftDetail, "drift_detail 须有差异清单")
	rogueDetail := false
	for _, d := range st.DriftDetail {
		if d["type"] == "unexpected" && d["name"] == "rogue" {
			rogueDetail = true
		}
	}
	assert.True(t, rogueDetail, "drift_detail 须含 unexpected rogue")
	assert.GreaterOrEqual(t, st.DesiredVersion, st.ActualVersion, "desired ≥ actual")

	// 6. 重新 validate + 生成新版本（漂移期间要求先重新 validate）
	ver2, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	assert.Greater(t, ver2.Version, ver.Version)

	// 7. 漂移期间部署旧版本（非最新 ready）→ 422（仅允许最新 ready 覆盖漂移）
	_, apiErr = dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver.ID, Confirmed: true}, admin.ID, syncRunner())
	require.NotNil(t, apiErr)
	assert.Equal(t, 422, apiErr.Status)
	assert.Contains(t, apiErr.Message, "漂移")

	// 8. 部署最新 ready → deployer removeStale 删 rogue → 校验通过 → success → drift 清除
	res2, apiErr := dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver2.ID, Confirmed: true}, admin.ID, syncRunner())
	require.Nil(t, apiErr, "发布最新 ready 应被允许（覆盖漂移）")
	drec2 := awaitDeployment(t, deploys, res2.DeploymentID)
	require.Equal(t, "success", drec2.Status)

	st, _ = nodesRepo.State(ctx, node.ID)
	assert.False(t, st.Drift, "发布后漂移应清除")
	assert.Equal(t, ver2.Version, st.ActualVersion)

	// 9. rogue 文件已被 removeStale 删除
	_, err := os.Stat(filepath.Join(rogueDir, "rogue.yml"))
	assert.True(t, os.IsNotExist(err), "rogue 文件须被 deployer 清理")

	_ = vers // 保持引用链可读
}

// TestDrift_OfflineTransition 断网连续失败达阈值→offline；last_online_at 保留。
func TestDrift_OfflineTransition(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()

	node := env.SeedNode(t, "gw-offline", domain.EnvTest)
	nodesRepo := pgstore.NewNodeRepo(env.Store.DB)
	vers := pgstore.NewVersionRepo(env.Store.DB)
	driftSvc := driftsvc.New(nodesRepo, vers)

	rtDir := t.TempDir()
	onlineFake := driftFake(t, rtDir) // 空目录 → overview 可达、资源空集
	reachable := true
	factory := probesvc.ClientFactory(func(*domain.GatewayNode) (*traefikapi.Client, error) {
		if !reachable {
			return traefikapi.NewClient("http://127.0.0.1:1", "", ""), nil
		}
		return onlineFake, nil
	})
	probe := probesvc.New(nodesRepo, pgstore.NewTargetRepo(env.Store.DB), driftSettings{}, factory, driftSvc)

	// 在线一次 → online + last_online_at 已设
	probe.RunOnce(ctx)
	st, _ := nodesRepo.State(ctx, node.ID)
	require.Equal(t, "online", st.Status)
	require.NotNil(t, st.LastOnlineAt)
	onlineAt := *st.LastOnlineAt

	// 断网 → 连续失败达阈值（3）→ offline；last_online_at 保留不变
	reachable = false
	for i := 0; i < 3; i++ {
		probe.RunOnce(ctx)
	}
	st, _ = nodesRepo.State(ctx, node.ID)
	assert.Equal(t, "offline", st.Status)
	assert.Equal(t, 3, st.ConsecutiveFailures)
	require.NotNil(t, st.LastOnlineAt, "断网后 last_online_at 须保留")
	assert.Equal(t, onlineAt, *st.LastOnlineAt, "last_online_at 跨失败不变")

	// 恢复 → 重新 online，失败计数归零
	reachable = true
	probe.RunOnce(ctx)
	st, _ = nodesRepo.State(ctx, node.ID)
	assert.Equal(t, "online", st.Status)
	assert.Equal(t, 0, st.ConsecutiveFailures)
}
