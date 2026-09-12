//go:build integration

// T055 · US2 回滚集成测试（quickstart V-2，AC-011/012、SC-003、宪法 V）。
// 断言：① 回滚生成 origin=rollback 新版本并走同一管线至 success；
// ② 目标版本快照逐字节复用（不回读当前库）——DB 业务态清空后仍可回滚（快照自包含）；
// ③ 从未成功部署过的版本不可回滚；④ confirmed=false 同样被门禁拒绝。
package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/deployer"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/traefikapi"
	"gateway-center/backend/tests/testenv"
)

// routersLoaded 从落盘目录读出已加载路由名集合（fake 网关据此回报，模拟 file provider watch）。
func routersLoaded(rtDir string) []string {
	entries, _ := os.ReadDir(filepath.Join(rtDir, "dynamic", "routers"))
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".yml"))
	}
	return out
}

// readFile 读取落盘产物（路径相对 <deploy_root>/dynamic）。
func readFile(t *testing.T, rtDir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(rtDir, "dynamic", filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(b)
}

// wirePipeline 组装版本+部署服务（与 T050 相同的合法调用链），fake 网关按已落盘 routers 回报。
func wirePipeline(t *testing.T, env *testenv.Env, rtDir string) (*pipeline.VersionService, *pipeline.DeployService, *pgstore.VersionRepo, *pgstore.DeploymentRepo) {
	t.Helper()
	fake, _ := fakeTraefik(t, func() []string { return routersLoaded(rtDir) })
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
	return vsvc, dsvc, vers, deploys
}

func TestRollback_GeneratesNewVersionAndDeploys(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)
	rtDir := t.TempDir()

	node := env.SeedNode(t, "gw-rb", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "a.example.com")
	svc1 := env.SeedService(t, node.ID, "svc-v1", "http://10.1.1.1:80")
	env.SeedSimpleRoute(t, node.ID, dom.ID, svc1.ID, "/")

	vsvc, dsvc, vers, deploys := wirePipeline(t, env, rtDir)

	// —— v1：发布成功 ——
	ver1, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	res, apiErr := dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver1.ID, Confirmed: true}, admin.ID, syncRunner())
	require.Nil(t, apiErr)
	require.Equal(t, "success", awaitDeployment(t, deploys, res.DeploymentID).Status)

	// —— v2：改路由 path → /v2，再发布成功 ——
	svc2 := env.SeedService(t, node.ID, "svc-v2", "http://10.2.2.2:80")
	routes, err := pgstore.NewRouteRepo(env.Store.DB).ListByNode(ctx, node.ID)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	routes[0].ServiceID = svc2.ID
	routes[0].Path = "/v2"
	require.NoError(t, env.Store.DB.Save(routes[0]).Error)

	ver2, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	res, apiErr = dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver2.ID, Confirmed: true}, admin.ID, syncRunner())
	require.Nil(t, apiErr)
	require.Equal(t, "success", awaitDeployment(t, deploys, res.DeploymentID).Status)

	// —— 门禁：未确认 → 422 ——
	_, apiErr = dsvc.Rollback(ctx, node.ID, ver1.ID, false, admin.ID, syncRunner())
	require.NotNil(t, apiErr)
	assert.Equal(t, 422, apiErr.Status)
	assert.Equal(t, "DEPLOY_NOT_CONFIRMED", apiErr.Code)

	// —— 回滚到 v1 ——
	res, apiErr = dsvc.Rollback(ctx, node.ID, ver1.ID, true, admin.ID, syncRunner())
	require.Nil(t, apiErr, "回滚应被受理")
	drec := awaitDeployment(t, deploys, res.DeploymentID)
	require.Equal(t, "success", drec.Status, "回滚部署终态 success：%+v", drec.VerificationResult)
	assert.Equal(t, "rollback", drec.Trigger)

	// 新版本：v3、origin=rollback、指向 v1 为 source
	ver3, err := vers.Get(ctx, drec.ConfigVersionID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), ver3.Version)
	assert.Equal(t, "rollback", ver3.Origin)
	require.NotNil(t, ver3.SourceVersionID)
	assert.Equal(t, ver1.ID, *ver3.SourceVersionID)
	assert.Equal(t, "ready", ver3.Status)

	// 快照逐字节复用：v3.Snapshot 与 v1.Snapshot 序列化一致（宪法 V——不回读当前库）
	a, _ := json.Marshal(ver1.Snapshot)
	b, _ := json.Marshal(ver3.Snapshot)
	assert.JSONEq(t, string(a), string(b))

	// 落盘恢复 v1 行为：router 产物指向 svc-v1 且 rule 含 PathPrefix(`/`)（而非 /v2）
	content := readFile(t, rtDir, "routers/"+routes[0].Name+".yml")
	assert.Contains(t, content, "PathPrefix(`/`)")
	assert.NotContains(t, content, "/v2")

	// 节点态 actual 推进到 v3，无漂移
	st, err := pgstore.NewNodeRepo(env.Store.DB).State(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), st.ActualVersion)
	assert.False(t, st.Drift)

	// 历史不被删除（FR-032）：4 个版本齐在
	list, total, err := vers.ListByNode(ctx, node.ID, queryutil.ListQuery{Page: 1, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Len(t, list, 4)
}

func TestRollback_WorksAfterBusinessStateWiped(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)
	rtDir := t.TempDir()

	node := env.SeedNode(t, "gw-rb-wipe", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "wipe.example.com")
	svc := env.SeedService(t, node.ID, "wipe-svc", "http://10.3.3.3:80")
	route := env.SeedSimpleRoute(t, node.ID, dom.ID, svc.ID, "/keep")

	vsvc, dsvc, _, deploys := wirePipeline(t, env, rtDir)
	ver1, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	res, apiErr := dsvc.Deploy(ctx, pipeline.DeployInput{NodeID: node.ID, VersionID: ver1.ID, Confirmed: true}, admin.ID, syncRunner())
	require.Nil(t, apiErr)
	require.Equal(t, "success", awaitDeployment(t, deploys, res.DeploymentID).Status)

	// 清空当前业务态（硬删，模拟误操作灾难现场）——期望表已无任何可生成内容
	require.NoError(t, env.Store.DB.Exec("DELETE FROM routes").Error)
	require.NoError(t, env.Store.DB.Exec("DELETE FROM targets").Error)
	require.NoError(t, env.Store.DB.Exec("DELETE FROM services").Error)
	require.NoError(t, env.Store.DB.Exec("DELETE FROM domains").Error)

	// 回滚依然成功：产物完全来自 ver1 快照（自包含，宪法 V）
	res, apiErr = dsvc.Rollback(ctx, node.ID, ver1.ID, true, admin.ID, syncRunner())
	require.Nil(t, apiErr)
	drec := awaitDeployment(t, deploys, res.DeploymentID)
	assert.Equal(t, "success", drec.Status)
	content := readFile(t, rtDir, "routers/"+route.Name+".yml")
	assert.Contains(t, content, "PathPrefix(`/keep`)")
}

func TestRollback_RejectsNeverDeployedVersion(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)
	rtDir := t.TempDir()

	node := env.SeedNode(t, "gw-rb-never", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "n.example.com")
	svc := env.SeedService(t, node.ID, "n-svc", "http://10.4.4.4:80")
	env.SeedSimpleRoute(t, node.ID, dom.ID, svc.ID, "/")

	vsvc, dsvc, _, _ := wirePipeline(t, env, rtDir)
	ver, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID) // 仅生成，从未部署
	require.Nil(t, apiErr)

	_, apiErr = dsvc.Rollback(ctx, node.ID, ver.ID, true, admin.ID, syncRunner())
	require.NotNil(t, apiErr)
	assert.Equal(t, 422, apiErr.Status)
	assert.Contains(t, apiErr.Message, "成功")
}
