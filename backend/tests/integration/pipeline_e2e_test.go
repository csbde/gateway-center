//go:build integration

// T050 · 管线端到端（make test-pipeline，quickstart V-1/§3）：
// 空平台 → 节点/域/服务/路由 → validate → 生成版本 → confirmed 发布 → 运行时校验 success；
// 无旁路三断言：未确认 422 / 离线阻断 / 版本非 ready 422；原子性：产物落盘完整且密钥 0600。
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
	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/deployer"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/traefikapi"
	"gateway-center/backend/tests/testenv"
)

// syncRunner 同步执行异步体（测试确定性）。
func syncRunner() pipeline.AsyncRunner {
	return func(fn func()) { fn() }
}

// fakeTraefik 模拟 Traefik API：/api/overview 可达；/api/http/routers 按脚本返回。
func fakeTraefik(t *testing.T, routers func() []string) (*traefikapi.Client, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "v3.3.0",
			"total": map[string]any{"http": map[string]any{
				"routers": map[string]int{"total": len(routers())}}}})
	})
	mux.HandleFunc("/api/http/routers", func(w http.ResponseWriter, _ *http.Request) {
		items := make([]map[string]any, 0)
		for _, n := range routers() {
			items = append(items, map[string]any{"name": n})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return traefikapi.NewClient(srv.URL, "", ""), srv.Close
}

func TestPipelineE2E_HappyPath(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)

	// fake Traefik：按已落盘的 dynamic/routers/ 文件名回报“已加载”集合（模拟 file provider watch）
	rtDir := t.TempDir()
	fake, _ := fakeTraefik(t, func() []string {
		entries, _ := os.ReadDir(filepath.Join(rtDir, "dynamic", "routers"))
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, strings.TrimSuffix(e.Name(), ".yml"))
		}
		return out
	})

	node := env.SeedNode(t, "gw-e2e", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "crm.example.com")
	svc := env.SeedService(t, node.ID, "crm-svc", "http://10.0.0.1:8080")
	route := env.SeedSimpleRoute(t, node.ID, dom.ID, svc.ID, "/")

	rec := auditrec.New(pgstore.NewAuditRepo(env.Store.DB))
	loader := pipeline.NewGraphLoader(
		pgstore.NewNodeRepo(env.Store.DB), pgstore.NewDomainRepo(env.Store.DB),
		pgstore.NewServiceRepo(env.Store.DB), pgstore.NewTargetRepo(env.Store.DB),
		pgstore.NewRouteRepo(env.Store.DB), pgstore.NewMiddlewareRepo(env.Store.DB))
	vers := pgstore.NewVersionRepo(env.Store.DB)
	deploys := pgstore.NewDeploymentRepo(env.Store.DB)
	vsvc := pipeline.NewVersionService(loader, vers, deploys,
		pgstore.NewCertRepo(env.Store.DB), env.Cipher, rec)
	dsvc := pipeline.NewDeployService(vers, deploys, pgstore.NewNodeRepo(env.Store.DB),
		pgstore.NewCertRepo(env.Store.DB), env.Cipher, deployer.NewFileDeployer(),
		func(*domain.GatewayNode) (*traefikapi.Client, error) { return fake, nil }, rec, nil)

	// —— 验证 ——
	report, apiErr := vsvc.Validate(ctx, node.ID)
	require.Nil(t, apiErr)
	require.True(t, report.Valid, "issues: %+v", report.Issues)

	// —— 生成版本（pending→validating→ready）——
	ver, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	require.Equal(t, "ready", ver.Status)
	require.Equal(t, int64(1), ver.Version)

	// —— 无旁路断言 1：未确认 → 422 ——
	res, apiErr := dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver.ID, Confirmed: false}, admin.ID, syncRunner())
	require.NotNil(t, apiErr)
	assert.Equal(t, 422, apiErr.Status)
	assert.Equal(t, "DEPLOY_NOT_CONFIRMED", apiErr.Code)
	assert.Nil(t, res)

	// —— 无旁路断言 2：离线 → 阻断（不可达 client）——
	offlineSvc := pipeline.NewDeployService(vers, deploys, pgstore.NewNodeRepo(env.Store.DB),
		pgstore.NewCertRepo(env.Store.DB), env.Cipher, deployer.NewFileDeployer(),
		func(*domain.GatewayNode) (*traefikapi.Client, error) {
			return traefikapi.NewClient("http://127.0.0.1:1", "", ""), nil
		}, rec, nil)
	_, apiErr = offlineSvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver.ID, Confirmed: true}, admin.ID, syncRunner())
	require.NotNil(t, apiErr)
	assert.Equal(t, 502, apiErr.Status) // GATEWAY_UNREACHABLE

	// —— 无旁路断言 3：版本非 ready → 422 PIPELINE_BLOCKED ——
	pendingVer := &domain.ConfigVersion{NodeID: node.ID, Version: 99, Status: "pending",
		Snapshot: map[string]any{"node_id": node.ID}}
	pendingVer.SetActor(admin.ID)
	require.NoError(t, vers.Create(ctx, pendingVer))
	_, apiErr = dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: pendingVer.ID, Confirmed: true}, admin.ID, syncRunner())
	require.NotNil(t, apiErr)
	assert.Equal(t, 422, apiErr.Status)
	assert.Contains(t, apiErr.Message, "ready")

	// —— 确认发布（成功链）：202 受理 → 同步 runner 执行 → 终态 success ——
	res, apiErr = dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver.ID, Confirmed: true}, admin.ID, syncRunner())
	require.Nil(t, apiErr, "发布应成功")
	require.NotNil(t, res)
	assert.Equal(t, "pending", res.Status) // 受理即返回（合同 202）
	drec := awaitDeployment(t, deploys, res.DeploymentID)
	assert.Equal(t, "success", drec.Status)
	var vr map[string]any
	vraw, _ := json.Marshal(drec.VerificationResult)
	require.NoError(t, json.Unmarshal(vraw, &vr))
	assert.Equal(t, true, vr["passed"])

	// —— 原子性：dynamic/ 下产物完整（routers/<route>.yml 存在，无 .tmp 残留）——
	files := walk(t, filepath.Join(rtDir, "dynamic"))
	assert.Contains(t, files, "routers")
	assert.Contains(t, walkAll(t, filepath.Join(rtDir, "dynamic")), "routers/"+route.Name+".yml", "router 产物名=路由名")
	var hasRouter, hasService, hasTmp bool
	for _, f := range walkAll(t, filepath.Join(rtDir, "dynamic")) {
		if strings.HasPrefix(f, "routers/") {
			hasRouter = true
		}
		if strings.HasPrefix(f, "services/") {
			hasService = true
		}
		if strings.Contains(f, ".tmp") {
			hasTmp = true
		}
	}
	assert.True(t, hasRouter, "router 产物落盘")
	assert.True(t, hasService, "service 产物落盘")
	assert.False(t, hasTmp, "无临时文件残留（temp→fsync→rename）")

	// —— 节点态：actual==desired，无漂移 ——
	st, err := pgstore.NewNodeRepo(env.Store.DB).State(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), st.ActualVersion)
	assert.False(t, st.Drift)
}

func TestPipelineE2E_AtomicityOnFailure(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)

	// 落盘成功但 fake 回报空集合 → 校验 failed，verification_result 带 diff 与 last_known_good
	fake, _ := fakeTraefik(t, func() []string { return nil })
	rtDir := t.TempDir()
	node := env.SeedNode(t, "gw-atomic", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "x.example.com")
	svc := env.SeedService(t, node.ID, "x-svc", "http://10.0.0.2:80")
	env.SeedSimpleRoute(t, node.ID, dom.ID, svc.ID, "/api")

	rec := auditrec.New(pgstore.NewAuditRepo(env.Store.DB))
	loader := pipeline.NewGraphLoader(
		pgstore.NewNodeRepo(env.Store.DB), pgstore.NewDomainRepo(env.Store.DB),
		pgstore.NewServiceRepo(env.Store.DB), pgstore.NewTargetRepo(env.Store.DB),
		pgstore.NewRouteRepo(env.Store.DB), pgstore.NewMiddlewareRepo(env.Store.DB))
	vers := pgstore.NewVersionRepo(env.Store.DB)
	deploys := pgstore.NewDeploymentRepo(env.Store.DB)
	vsvc := pipeline.NewVersionService(loader, vers, deploys,
		pgstore.NewCertRepo(env.Store.DB), env.Cipher, rec)
	dsvc := pipeline.NewDeployService(vers, deploys, pgstore.NewNodeRepo(env.Store.DB),
		pgstore.NewCertRepo(env.Store.DB), env.Cipher, deployer.NewFileDeployer(),
		func(*domain.GatewayNode) (*traefikapi.Client, error) { return fake, nil }, rec, nil)

	ver, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)

	start := time.Now()
	res, apiErr := dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver.ID, Confirmed: true}, admin.ID, syncRunner())
	require.Nil(t, apiErr) // 受理 202；校验失败体现在部署终态
	require.NotNil(t, res)
	drec := awaitDeployment(t, deploys, res.DeploymentID)
	assert.Less(t, time.Since(start), 30*time.Second, "1s/2s/4s 退避 ≤15s + 余量")
	assert.Equal(t, "failed", drec.Status)
	vd, _ := json.Marshal(drec.VerificationResult)
	assert.Contains(t, string(vd), "last_known_good_version")
	assert.Contains(t, string(vd), "diff")

	// failed 记录不可变保留；版本仍 ready 可再次发布
	d := drec
	assert.Equal(t, "failed", d.Status)
	v2, err := vers.Get(ctx, ver.ID)
	require.NoError(t, err)
	assert.Equal(t, "ready", v2.Status)
}

// awaitDeployment 轮询至终态（syncRunner 下通常首轮即返回）。
func awaitDeployment(t *testing.T, repo *pgstore.DeploymentRepo, id string) *domain.Deployment {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		d, err := repo.Get(ctx, id)
		require.NoError(t, err)
		if d.Status == "success" || d.Status == "failed" {
			return d
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("部署未在预期时间内到达终态")
	return nil
}

func walk(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func walkAll(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}
