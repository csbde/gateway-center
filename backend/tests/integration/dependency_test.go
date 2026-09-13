//go:build integration

// T087 · US7 依赖保护集成测试（quickstart V-7，AC-015、FR-039、宪法 VI）。
// 断言：① 引用链 Domain←Route→Service（并绑 Middleware）逐层删除均被 409 DEPENDENCY_BLOCKED +
//   details 逐条列出引用方路由名阻止；② 处置流 Archive（路由终态）解除引用后，Domain/Service/Middleware
//   方可软删（depcheck 经 ReferencingRoutes 过滤 status<>'archived'）；③ Disable 的资源不进新版本快照
//   （enabled 排除，宪章 VI 处置第一步）；④ 历史快照自包含且不可变（处置后 v1 快照仍含被删资源，宪法 V）。
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/domainsvc"
	"gateway-center/backend/internal/application/mwsvc"
	"gateway-center/backend/internal/application/routesvc"
	"gateway-center/backend/internal/application/servicesvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/tests/testenv"
)

// wireDep 组装四类资源服务（合法调用链 Controller→Application→Domain，宪法 XII）。
func wireDep(t *testing.T, env *testenv.Env) (*domainsvc.Service, *servicesvc.Service, *mwsvc.Service, *routesvc.Service) {
	t.Helper()
	db := env.Store.DB
	rec := auditrec.New(pgstore.NewAuditRepo(db))
	nodes := pgstore.NewNodeRepo(db)
	return domainsvc.New(pgstore.NewDomainRepo(db), nodes, rec),
		servicesvc.New(pgstore.NewServiceRepo(db), pgstore.NewTargetRepo(db), nodes, rec),
		mwsvc.New(pgstore.NewMiddlewareRepo(db), nodes, rec),
		routesvc.New(pgstore.NewRouteRepo(db), pgstore.NewDomainRepo(db),
			pgstore.NewServiceRepo(db), pgstore.NewMiddlewareRepo(db), nodes, rec)
}

// snapJSON 取版本快照序列化（断言资源名在/不在快照，不依赖结构细节）。
func snapJSON(t *testing.T, vers *pgstore.VersionRepo, id string) string {
	t.Helper()
	v, err := vers.Get(context.Background(), id)
	require.NoError(t, err)
	b, err := json.Marshal(v.Snapshot)
	require.NoError(t, err)
	return string(b)
}

// TestDependency_ChainBlockedThenArchivedUnblocks —— V-7 引用链逐层阻止 + Archive 处置解锁 + 软删。
func TestDependency_ChainBlockedThenArchivedUnblocks(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	dev := routesvc.Principal{ID: env.SeedUser(t, domain.RoleDeveloper).ID, Role: "developer"}
	domS, svcS, mwS, rsvc := wireDep(t, env)

	node := env.SeedNode(t, "gw-dep", domain.EnvTest)
	dom := env.SeedDomain(t, node.ID, "dep.example.com")
	svc := env.SeedService(t, node.ID, "dep-svc", "http://10.5.5.5:80")
	mw, apiErr := mwS.Create(ctx, mwsvc.Input{NodeID: node.ID, Name: "dep-mw",
		Type: "ip_allowlist", Params: map[string]any{"cidrs": []any{"203.0.113.0/24"}}}, dev.ID)
	require.Nil(t, apiErr)

	// 路由同时引用 域名 + 服务 + 中间件（链根：Domain←Route→Service，并绑 Middleware）
	route, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "dep-route", Mode: "simple",
		DomainID: dom.ID, Path: "/", MatchType: "prefix", ServiceID: svc.ID,
		MiddlewareIDs: []string{mw.ID}}, dev)
	require.Nil(t, apiErr)

	// ① 三资源删除均被 409 阻止，details 逐条列出引用方路由名（AC-015/FR-039）
	for _, c := range []struct {
		name string
		e    *httperr.APIError
	}{
		{"域名", domS.Delete(ctx, dom.ID, dev.ID)},
		{"服务", svcS.Delete(ctx, svc.ID, dev.ID)},
		{"中间件", mwS.Delete(ctx, mw.ID, dev.ID)},
	} {
		require.NotNil(t, c.e, "%s 删除应被依赖阻断", c.name)
		assert.Equal(t, 409, c.e.Status, "%s", c.name)
		assert.Equal(t, "DEPENDENCY_BLOCKED", c.e.Code, "%s", c.name)
		require.NotEmpty(t, c.e.Details, "%s 引用清单不得为空", c.name)
		got := map[string]bool{}
		for _, d := range c.e.Details {
			got[d.Message] = true
		}
		assert.True(t, got["dep-route"], "%s 引用清单必须列出路由 dep-route", c.name)
	}

	// ② 处置流：归档路由（draft→archived 合法迁移）→ archived 路由不再计入引用
	archived, apiErr := rsvc.SetStatus(ctx, route.Route.ID, "archived", route.Route.RowVersion, dev.ID)
	require.Nil(t, apiErr, "归档迁移应合法（draft→archived）")
	assert.Equal(t, "archived", archived.Route.Status)

	// 归档后三资源均可软删（引用方已处置，ReferencingRoutes 过滤 archived）
	require.Nil(t, domS.Delete(ctx, dom.ID, dev.ID), "域名在路由归档后应可删")
	require.Nil(t, svcS.Delete(ctx, svc.ID, dev.ID), "服务在路由归档后应可删")
	require.Nil(t, mwS.Delete(ctx, mw.ID, dev.ID), "中间件在路由归档后应可删")

	// 软删后域名 Get → ErrNotFound（GORM 软删过滤；domainsvc.Get 返回 error）
	_, err := domS.Get(ctx, dom.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, pgstore.ErrNotFound))
}

// TestDependency_DisableExcludesFromSnapshot —— disabled 资源不进新版本（宪章 VI/US7 处置第一步）。
func TestDependency_DisableExcludesFromSnapshot(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)
	dev := routesvc.Principal{ID: env.SeedUser(t, domain.RoleDeveloper).ID, Role: "developer"}
	_, svcS, _, rsvc := wireDep(t, env)
	rtDir := t.TempDir()
	node := env.SeedNode(t, "gw-dis", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "dis.example.com")
	svcA := env.SeedService(t, node.ID, "svc-a", "http://10.6.6.1:80")
	svcB := env.SeedService(t, node.ID, "svc-b", "http://10.6.6.2:80") // 独立未被引用

	// 启用路由引用 svc-a（使配置非空且真实）；svc-b 独立存在但同入快照（enabled services 全量纳入）
	rRoute, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "dis-route", Mode: "simple",
		DomainID: dom.ID, Path: "/", MatchType: "prefix", ServiceID: svcA.ID}, dev)
	require.Nil(t, apiErr)
	_, apiErr = rsvc.SetStatus(ctx, rRoute.Route.ID, "enabled", rRoute.Route.RowVersion, dev.ID)
	require.Nil(t, apiErr)

	vsvc, _, vers, _ := wirePipeline(t, env, rtDir)
	ver1, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	require.Equal(t, "ready", ver1.Status)
	j1 := snapJSON(t, vers, ver1.ID)
	assert.Contains(t, j1, "svc-a", "v1 快照含启用服务 svc-a")
	assert.Contains(t, j1, "svc-b", "v1 快照含独立启用服务 svc-b")

	// 处置第一步：Disable svc-b（独立未被引用 → SetServiceEnabled 不查依赖，直接放行）
	_, apiErr = svcS.SetServiceEnabled(ctx, svcB.ID, false, svcB.RowVersion, admin.ID)
	require.Nil(t, apiErr)

	// 新版本不再纳入 disabled 服务（buildSnapshot 仅取 enabled services，宪章 VI）
	ver2, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	require.Equal(t, "ready", ver2.Status)
	j2 := snapJSON(t, vers, ver2.ID)
	assert.Contains(t, j2, "svc-a", "v2 仍含启用的 svc-a")
	assert.NotContains(t, j2, "svc-b", "禁用服务不得进入新版本快照")
}

// TestDependency_HistoricalSnapshotIntact —— 处置后历史快照仍含被删资源（宪法 V 自包含/不可变）。
func TestDependency_HistoricalSnapshotIntact(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)
	dev := routesvc.Principal{ID: env.SeedUser(t, domain.RoleDeveloper).ID, Role: "developer"}
	domS, svcS, _, rsvc := wireDep(t, env)
	rtDir := t.TempDir()
	node := env.SeedNode(t, "gw-snap", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "snap.example.com")
	svc := env.SeedService(t, node.ID, "snap-svc", "http://10.7.7.7:80")
	rRoute, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "snap-route", Mode: "simple",
		DomainID: dom.ID, Path: "/", MatchType: "prefix", ServiceID: svc.ID}, dev)
	require.Nil(t, apiErr)
	enabledRoute, apiErr := rsvc.SetStatus(ctx, rRoute.Route.ID, "enabled", rRoute.Route.RowVersion, dev.ID)
	require.Nil(t, apiErr)

	vsvc, _, vers, _ := wirePipeline(t, env, rtDir)
	ver1, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	j1 := snapJSON(t, vers, ver1.ID)
	assert.Contains(t, j1, "snap.example.com")
	assert.Contains(t, j1, "snap-svc")
	assert.Contains(t, j1, "snap-route")

	// 处置：归档路由 → 软删域名与服务
	_, apiErr = rsvc.SetStatus(ctx, rRoute.Route.ID, "archived", enabledRoute.Route.RowVersion, dev.ID)
	require.Nil(t, apiErr)
	require.Nil(t, domS.Delete(ctx, dom.ID, admin.ID))
	require.Nil(t, svcS.Delete(ctx, svc.ID, admin.ID))

	// 历史 v1 快照不受当前库删除影响（自包含，宪法 V）
	j1After := snapJSON(t, vers, ver1.ID)
	assert.JSONEq(t, j1, j1After, "历史快照不可变")
	assert.Contains(t, j1After, "snap.example.com", "被删资源仍在历史快照")
	assert.Contains(t, j1After, "snap-route", "被归档路由仍在历史快照")
}
