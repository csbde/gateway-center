//go:build integration

// T085 · US7 删除不变式测试化支撑（quickstart US7-AC2/03、FR-039、宪法 V/VI）。
// 与 T087（V-7 全场景）不同文件并行：本文件聚焦 ① 软删后新版本快照排除该资源、
// ② 历史 ConfigVersion 快照读取不受影响（自包含，宪法 V）、③ 不可变记录 DELETE 一律 403
// （版本/部署 HTTP 403，任何角色；审计 DELETE 由 DB 触发器兜底，见 T082 TestAudit_TamperingRejectedByDB）。
package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/routesvc"
	"gateway-center/backend/internal/application/servicesvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/tests/testenv"
)

// TestSoftDelete_ExcludesFromNewVersionAndHistoryIntact —— 软删独立服务后：
// 新版本快照排除该资源（deleted_at IS NULL 过滤），历史 v1 快照仍含且不变（自包含，宪法 V）。
func TestSoftDelete_ExcludesFromNewVersionAndHistoryIntact(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	admin := env.SeedUser(t, domain.RoleSuperAdmin)
	dev := routesvc.Principal{ID: env.SeedUser(t, domain.RoleDeveloper).ID, Role: "developer"}
	db := env.Store.DB
	rec := auditrec.New(pgstore.NewAuditRepo(db))
	svcS := servicesvc.New(pgstore.NewServiceRepo(db), pgstore.NewTargetRepo(db), pgstore.NewNodeRepo(db), rec)
	rsvc := routesvc.New(pgstore.NewRouteRepo(db), pgstore.NewDomainRepo(db),
		pgstore.NewServiceRepo(db), pgstore.NewMiddlewareRepo(db), pgstore.NewNodeRepo(db), rec)

	rtDir := t.TempDir()
	node := env.SeedNode(t, "gw-soft", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, db.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "soft.example.com")
	svcA := env.SeedService(t, node.ID, "soft-a", "http://10.10.10.1:80") // 被路由引用
	svcB := env.SeedService(t, node.ID, "soft-b", "http://10.10.10.2:80") // 独立未引用

	// 启用路由引用 svc-a（使配置非空）；svc-b 独立存在但同入 v1 快照（enabled services 全量纳入）
	rRoute, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "soft-route", Mode: "simple",
		DomainID: dom.ID, Path: "/", MatchType: "prefix", ServiceID: svcA.ID}, dev)
	require.Nil(t, apiErr)
	_, apiErr = rsvc.SetStatus(ctx, rRoute.Route.ID, "enabled", rRoute.Route.RowVersion, dev.ID)
	require.Nil(t, apiErr)

	vsvc, _, vers, _ := wirePipeline(t, env, rtDir)
	ver1, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	require.Equal(t, "ready", ver1.Status)
	j1 := snapJSON(t, vers, ver1.ID)
	assert.Contains(t, j1, "soft-a", "v1 含启用服务 soft-a")
	assert.Contains(t, j1, "soft-b", "v1 含独立启用服务 soft-b")

	// 软删 svc-b（独立未引用 → 无依赖阻断，无需先禁用；servicesvc.Delete 仅查 ReferencingRoutes）
	require.Nil(t, svcS.Delete(ctx, svcB.ID, admin.ID), "独立服务软删应放行")

	// ① 新版本排除软删资源（buildSnapshot 查 deleted_at IS NULL）
	ver2, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	require.Equal(t, "ready", ver2.Status)
	j2 := snapJSON(t, vers, ver2.ID)
	assert.Contains(t, j2, "soft-a", "v2 仍含 soft-a")
	assert.NotContains(t, j2, "soft-b", "软删服务不得进入新版本快照")

	// ② 历史 v1 快照不受软删影响（自包含，宪法 V）
	j1After := snapJSON(t, vers, ver1.ID)
	assert.JSONEq(t, j1, j1After, "历史快照不可变")
	assert.Contains(t, j1After, "soft-b", "被软删资源仍在历史快照")
}

// TestImmutableRecord_DELETE_Always403 —— 版本/部署记录 DELETE 任何角色 403（FR-032/宪法 V）。
// 审计记录无 HTTP DELETE 端点（设计上仅追加），其 DB 层篡改防护见 T082 TestAudit_TamperingRejectedByDB。
func TestImmutableRecord_DELETE_Always403(t *testing.T) {
	app := newHTTPApp(t)
	roles := []string{"viewer", "developer", "gateway_admin", "super_admin"}
	for _, role := range roles {
		t.Run(role+"_删版本", func(t *testing.T) {
			r := app.do(t, tok(app, role), "DELETE", "/api/v1/versions/"+nilUUID, nil)
			requireErrorBody(t, r, 403, "FORBIDDEN")
		})
		t.Run(role+"_删部署", func(t *testing.T) {
			r := app.do(t, tok(app, role), "DELETE", "/api/v1/deployments/"+nilUUID, nil)
			requireErrorBody(t, r, 403, "FORBIDDEN")
		})
	}
}
