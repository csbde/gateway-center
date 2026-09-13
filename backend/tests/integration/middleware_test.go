//go:build integration

// T063 · US3 集成测试（quickstart V-3，AC-005/015、FR-015~022、宪法 II/X）。
// 断言：① 一份白名单策略复用于两路由，删除被 409 引用清单阻止、解绑后可删；
// ② 绑定顺序 == routers/*.yml middlewares 数组顺序（FR-018）；
// ③ 被引用策略停用 → 生成版本被 route_mw_disabled 阻断（无旁路）；
// ④ 高级模式保存要求 gateway_admin（403），每次使用写 advanced_edit 审计关联 route_id（FR-016/宪法 X）；
// ⑤ 参数非法的中间件在 validate 阶段阻断（mw_param_invalid）。
package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/mwsvc"
	"gateway-center/backend/internal/application/routesvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/tests/testenv"
)

func wireMw(t *testing.T, env *testenv.Env) (*mwsvc.Service, *routesvc.Service) {
	t.Helper()
	db := env.Store.DB
	nodes := pgstore.NewNodeRepo(db)
	doms := pgstore.NewDomainRepo(db)
	svcs := pgstore.NewServiceRepo(db)
	routes := pgstore.NewRouteRepo(db)
	mws := pgstore.NewMiddlewareRepo(db)
	rec := auditrec.New(pgstore.NewAuditRepo(db))
	return mwsvc.New(mws, nodes, rec),
		routesvc.New(routes, doms, svcs, mws, nodes, rec)
}

func TestMiddleware_ReusedByTwoRoutesDeleteBlockedThenOK(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	dev := routesvc.Principal{ID: env.SeedUser(t, domain.RoleDeveloper).ID, Role: "developer"}
	mwsvcS, rsvc := wireMw(t, env)

	node := env.SeedNode(t, "gw-mw", domain.EnvTest)
	dom := env.SeedDomain(t, node.ID, "a.example.com")
	svc1 := env.SeedService(t, node.ID, "svc-1", "http://10.1.1.1:80")
	svc2 := env.SeedService(t, node.ID, "svc-2", "http://10.1.1.2:80")

	// 一份 ip 白名单策略
	mw, apiErr := mwsvcS.Create(ctx, mwsvc.Input{NodeID: node.ID, Name: "office-only",
		Type: "ip_allowlist", Params: map[string]any{"cidrs": []any{"203.0.113.0/24"}}}, dev.ID)
	require.Nil(t, apiErr)

	// 复用到两条路由（FR-022：配置不冗余，仅引用）
	r1, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "route-a", Mode: "simple",
		DomainID: dom.ID, Path: "/a", MatchType: "prefix", ServiceID: svc1.ID, MiddlewareIDs: []string{mw.ID}}, dev)
	require.Nil(t, apiErr)
	_, apiErr = rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "route-b", Mode: "simple",
		DomainID: dom.ID, Path: "/b", MatchType: "prefix", ServiceID: svc2.ID, MiddlewareIDs: []string{mw.ID}}, dev)
	require.Nil(t, apiErr)

	// referenced_by 回显两条引用方（FR-020）
	view, apiErr := mwsvcS.Get(ctx, mw.ID)
	require.Nil(t, apiErr)
	assert.ElementsMatch(t, []string{"route-a", "route-b"}, view.ReferencedBy)

	// 删除 → 409 DEPENDENCY_BLOCKED + details 引用清单
	derr := mwsvcS.Delete(ctx, mw.ID, dev.ID)
	require.NotNil(t, derr)
	assert.Equal(t, 409, derr.Status)
	assert.Equal(t, "DEPENDENCY_BLOCKED", derr.Code)
	require.NotEmpty(t, derr.Details)
	names := map[string]bool{}
	for _, d := range derr.Details {
		names[d.Message] = true
	}
	assert.True(t, names["route-a"] && names["route-b"], "引用清单必须逐条列出（AC-015）")

	// 从 route-a 解绑后仍被 route-b 阻止
	_, apiErr = rsvc.Update(ctx, r1.Route.ID, routesvc.Input{Name: r1.Name, Mode: "simple",
		DomainID: dom.ID, Path: "/a", MatchType: "prefix", ServiceID: svc1.ID,
		MiddlewareIDs: []string{}}, dev)
	require.Nil(t, apiErr)
	require.NotNil(t, mwsvcS.Delete(ctx, mw.ID, dev.ID))

	// 全部解绑 → 可删
	routes, err := pgstore.NewRouteRepo(env.Store.DB).ListByNode(ctx, node.ID)
	require.NoError(t, err)
	for _, r := range routes {
		_, apiErr = rsvc.Update(ctx, r.ID, routesvc.Input{Name: r.Name, Mode: "simple",
			DomainID: dom.ID, Path: r.Path, MatchType: "prefix", ServiceID: r.ServiceID,
			MiddlewareIDs: []string{}}, dev)
		require.Nil(t, apiErr)
	}
	require.Nil(t, mwsvcS.Delete(ctx, mw.ID, dev.ID))
	_, apiErr = mwsvcS.Get(ctx, mw.ID)
	require.NotNil(t, apiErr)
	assert.Equal(t, 404, apiErr.Status)
}

func TestMiddleware_BindingOrderHitsArtifacts(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	dev := routesvc.Principal{ID: env.SeedUser(t, domain.RoleDeveloper).ID, Role: "developer"}
	mwsvcS, rsvc := wireMw(t, env)
	rtDir := t.TempDir()
	node := env.SeedNode(t, "gw-order", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "o.example.com")
	svc := env.SeedService(t, node.ID, "svc", "http://10.9.9.9:80")

	sec, apiErr := mwsvcS.Create(ctx, mwsvc.Input{NodeID: node.ID, Name: "sec-h", Type: "security_headers",
		Params: map[string]any{"frame_options": "deny"}}, dev.ID)
	require.Nil(t, apiErr)
	rate, apiErr := mwsvcS.Create(ctx, mwsvc.Input{NodeID: node.ID, Name: "rate-h", Type: "rate_limit",
		Params: map[string]any{"average": 50, "burst": 100}}, dev.ID)
	require.Nil(t, apiErr)
	strip, apiErr := mwsvcS.Create(ctx, mwsvc.Input{NodeID: node.ID, Name: "strip-h", Type: "strip_prefix",
		Params: map[string]any{"prefixes": []any{"/api"}}}, dev.ID)
	require.Nil(t, apiErr)

	// 绑定顺序：rate → strip → sec（AC-005 保序）
	rRoute, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "ordered", Mode: "simple",
		DomainID: dom.ID, Path: "/", MatchType: "prefix", ServiceID: svc.ID,
		MiddlewareIDs: []string{rate.ID, strip.ID, sec.ID}}, dev)
	require.Nil(t, apiErr)
	// 路由须 enabled 才进快照并生成产物（draft 路由不进快照）
	_, apiErr = rsvc.SetStatus(ctx, rRoute.Route.ID, "enabled", rRoute.Route.RowVersion, dev.ID)
	require.Nil(t, apiErr)

	vsvc, _, _, _ := wirePipeline(t, env, rtDir)
	ver, _, apiErr := vsvc.CreateVersion(ctx, node.ID, dev.ID)
	require.Nil(t, apiErr)
	require.Equal(t, "ready", ver.Status)

	// routers/*.yml 的 middlewares 数组顺序 == position 顺序（产物存于版本 ArtifactFiles，CreateVersion 不落盘）
	yml := ver.ArtifactFiles["routers/ordered.yml"]
	require.NotEmpty(t, yml, "routers/ordered.yml 产物须存在")
	iR, iS, iX := strings.Index(yml, "rate-h"), strings.Index(yml, "strip-h"), strings.Index(yml, "sec-h")
	require.Positive(t, iR)
	assert.Less(t, iR, iS, "rate 必须先于 strip")
	assert.Less(t, iS, iX, "strip 必须先于 sec")
	// 每个被绑定的策略都有独立产物文件
	assert.Contains(t, ver.ArtifactFiles["middlewares/rate-h.yml"], "rateLimit")
}

func TestMiddleware_DisabledOrInvalidBlocksGeneration(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	dev := routesvc.Principal{ID: env.SeedUser(t, domain.RoleDeveloper).ID, Role: "developer"}
	mwsvcS, rsvc := wireMw(t, env)
	rtDir := t.TempDir()
	node := env.SeedNode(t, "gw-block", domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, env.Store.DB.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, "b.example.com")
	svc := env.SeedService(t, node.ID, "svc", "http://10.8.8.8:80")

	mw, apiErr := mwsvcS.Create(ctx, mwsvc.Input{NodeID: node.ID, Name: "lim", Type: "rate_limit",
		Params: map[string]any{"average": 10, "burst": 20}}, dev.ID)
	require.Nil(t, apiErr)
	rRoute, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "needs-mw", Mode: "simple",
		DomainID: dom.ID, Path: "/", MatchType: "prefix", ServiceID: svc.ID,
		MiddlewareIDs: []string{mw.ID}}, dev)
	require.Nil(t, apiErr)
	// 路由须 enabled 才进快照并被 validate 检查中间件引用（draft 路由不进快照）
	_, apiErr = rsvc.SetStatus(ctx, rRoute.Route.ID, "enabled", rRoute.Route.RowVersion, dev.ID)
	require.Nil(t, apiErr)
	vsvc, _, _, _ := wirePipeline(t, env, rtDir)

	// 停用被启用路由引用的策略 → 生成版本被阻断（route_mw_disabled，T058 接线）
	_, apiErr = mwsvcS.SetEnabled(ctx, mw.ID, false, mw.RowVersion, dev.ID)
	require.Nil(t, apiErr)
	_, vres, apiErr := vsvc.CreateVersion(ctx, node.ID, dev.ID)
	require.NotNil(t, apiErr)
	assert.Equal(t, 422, apiErr.Status)
	assert.Equal(t, "PIPELINE_BLOCKED", apiErr.Code)
	require.NotNil(t, vres)
	var found bool
	for _, is := range vres.Issues {
		if is.Code == "route_mw_disabled" {
			found = true
			assert.True(t, is.Blocking)
		}
	}
	assert.True(t, found, "必须报出 route_mw_disabled 阻断项")

	// 重新启用后放行（同版本可再次生成）
	mwFresh, apiErr := mwsvcS.Get(ctx, mw.ID)
	require.Nil(t, apiErr)
	_, apiErr = mwsvcS.SetEnabled(ctx, mw.ID, true, mwFresh.RowVersion, dev.ID)
	require.Nil(t, apiErr)
	_, _, apiErr = vsvc.CreateVersion(ctx, node.ID, dev.ID)
	require.Nil(t, apiErr)

	// 参数被绕过服务层直接改脏（DB 直写 average=0）→ validate 阻断 mw_param_invalid
	require.NoError(t, env.Store.DB.Exec(
		`UPDATE middlewares SET params = '{"average": 0, "burst": 20}'::jsonb WHERE id = $1`, mw.ID).Error)
	_, vres, apiErr = vsvc.CreateVersion(ctx, node.ID, dev.ID)
	require.NotNil(t, apiErr, "非法参数不得生成版本")
	found = false
	for _, is := range vres.Issues {
		if is.Code == "mw_param_invalid" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestAdvancedRoute_GateAuditAndValidate(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	devUser := env.SeedUser(t, domain.RoleDeveloper)
	dev := routesvc.Principal{ID: devUser.ID, Role: "developer"}
	adminUser := env.SeedUser(t, domain.RoleGatewayAdmin)
	admin := routesvc.Principal{ID: adminUser.ID, Role: "gateway_admin"}
	_, rsvc := wireMw(t, env)
	node := env.SeedNode(t, "gw-adv", domain.EnvTest)
	dom := env.SeedDomain(t, node.ID, "adv.example.com")
	svc := env.SeedService(t, node.ID, "svc", "http://10.7.7.7:80")

	// developer 直接拒绝（FR-015），且错误码可定位
	in := routesvc.Input{NodeID: node.ID, Name: "adv", Mode: "advanced",
		AdvancedRule: "Host(`adv.example.com`) && PathPrefix(`/api`)", ServiceID: svc.ID}
	_, apiErr := rsvc.Create(ctx, in, dev)
	require.NotNil(t, apiErr)
	assert.Equal(t, 403, apiErr.Status)
	assert.Equal(t, "FORBIDDEN", apiErr.Code)

	// 非法表达式即使 admin 也 400（field 定位 advanced_rule）
	bad := in
	bad.AdvancedRule = "Foo(`x`)"
	_, apiErr = rsvc.Create(ctx, bad, admin)
	require.NotNil(t, apiErr)
	assert.Equal(t, 400, apiErr.Status)
	require.NotEmpty(t, apiErr.Details)
	assert.Equal(t, "advanced_rule", apiErr.Details[0].Field)

	// admin 合法保存：回显 rule 归一化，且 advanced_edit 审计关联 route_id（FR-016/宪法 X）
	v, apiErr := rsvc.Create(ctx, in, admin)
	require.Nil(t, apiErr)
	assert.Equal(t, in.AdvancedRule, v.AdvancedRule)
	assert.Contains(t, v.GeneratedRulePreview, "Host(`adv.example.com`)")

	audits, total, err := pgstore.NewAuditRepo(env.Store.DB).Search(ctx,
		pgstore.AuditFilter{Action: "advanced_edit", ResourceID: v.ID}, pgstore.ListQuery{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "创建即一条 advanced_edit")
	assert.Equal(t, adminUser.ID, audits[0].ActorID)
	assert.Equal(t, "route", audits[0].ResourceType)
	assert.Equal(t, in.AdvancedRule, audits[0].After["advanced_rule"], "审计必须记录表达式本体")
	assert.Equal(t, true, audits[0].After["risk_notice_ack"], "风险提示确认随审计落库")

	// 更新一次 → 第二条 advanced_edit（每次使用都审计）
	upd := in
	upd.Name = "adv2"
	upd.AdvancedRule = "Host(`adv.example.com`) || Host(`alt.example.com`)"
	_, apiErr = rsvc.Update(ctx, v.ID, upd, admin)
	require.Nil(t, apiErr)
	_, total2, err := pgstore.NewAuditRepo(env.Store.DB).Search(ctx,
		pgstore.AuditFilter{Action: "advanced_edit", ResourceID: v.ID}, pgstore.ListQuery{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total2)

	// validate-advanced 服务：admin 通过、归一化预览非空；developer 403
	res, apiErr := rsvc.ValidateAdvanced(ctx, " Host(`a.com`)\t&&  Path(`/x`) ", admin)
	require.Nil(t, apiErr)
	assert.True(t, res.Valid)
	assert.Equal(t, "Host(`a.com`) && Path(`/x`)", res.NormalizedPreview)
	assert.NotEmpty(t, res.RiskNotice)
	_, apiErr = rsvc.ValidateAdvanced(ctx, "Host(`a.com`)", dev)
	require.NotNil(t, apiErr)
	assert.Equal(t, 403, apiErr.Status)

	// 双模式隔离（宪法 II）：简单模式保存后 advanced_rule 必为空
	simple, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: "plain", Mode: "simple",
		DomainID: dom.ID, Path: "/p", MatchType: "prefix", ServiceID: svc.ID,
		AdvancedRule: "Host(`sneaky.com`)"}, dev)
	require.Nil(t, apiErr, "developer 用简单模式不受高级门禁影响")
	assert.Empty(t, simple.AdvancedRule, "简单模式携带的高级字段必须被剥离")
}
