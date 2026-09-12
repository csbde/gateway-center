//go:build integration

// T082 · US6 RBAC 矩阵 + 生产发布审批 + 审计防篡改集成测试（quickstart V-6，
// AC-014、FR-036/037/038、宪法 IX/X）。
// 断言：① 四角色 × 代表端点 RBAC 矩阵（RequireWritable/GatewayAdminOrAbove/SuperAdminOnly
//   边界 403；不可变记录 DELETE 一律 403）；② 自批守卫（reviewed_by≠submitted_by，应用层+DB CHECK
//   双层）→ 422 PIPELINE_BLOCKED；③ 双身份审计（submit 与 approve 各记一条，approve 同时记录
//   submitted_by 与 reviewed_by）；④ 审计篡改被 DB 触发器拒绝（UPDATE/DELETE RAISE，宪法 X）。
package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/application/approvalsvc"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/nodesvc"
	"gateway-center/backend/internal/application/routesvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/tests/testenv"
)

// nilUUID 合法 UUID 格式但不存在的资源 ID：RBAC 矩阵用缺失资源隔离角色守卫的 403 边界
// （缺失资源返回 404 而非 403，从而区分「角色不足 403」与「角色足够但资源缺失 404」）。
const nilUUID = "00000000-0000-0000-0000-000000000000"

// TestRBACMatrix —— 四角色 × 代表端点，断言各路由组 RequireRoles 边界（T078/T082，US6-AC1）。
// want403=true 期望 403（角色不足或不可变记录）；want403=false 期望非 403（角色足够，缺失资源→404/200）。
func TestRBACMatrix(t *testing.T) {
	app := newHTTPApp(t)
	settingsBody := map[string]any{
		"probe_interval_sec": 30, "offline_threshold": 3,
		"expiry_warn_days": 30, "timezone": "Asia/Shanghai",
	}
	cases := []struct {
		name    string
		role    string
		method  string
		path    string
		body    any
		want403 bool
	}{
		// 读：任何认证角色 → 200（viewer+）
		{"viewer 读列表", "viewer", "GET", "/api/v1/nodes", nil, false},
		{"developer 读列表", "developer", "GET", "/api/v1/nodes", nil, false},
		{"admin 读列表", "gateway_admin", "GET", "/api/v1/nodes", nil, false},
		{"super 读列表", "super_admin", "GET", "/api/v1/nodes", nil, false},

		// RequireWritable（developer+）：viewer 403；developer+ 缺失资源 404
		{"viewer 删节点", "viewer", "DELETE", "/api/v1/nodes/" + nilUUID, nil, true},
		{"developer 删节点", "developer", "DELETE", "/api/v1/nodes/" + nilUUID, nil, false},
		{"admin 删节点", "gateway_admin", "DELETE", "/api/v1/nodes/" + nilUUID, nil, false},
		{"super 删节点", "super_admin", "DELETE", "/api/v1/nodes/" + nilUUID, nil, false},

		// GatewayAdminOrAbove（审批批准/回滚/归档/高级模式）：viewer/developer 403；admin+ 缺失资源 404
		{"viewer 批准申请", "viewer", "POST", "/api/v1/release-requests/" + nilUUID + "/approve", map[string]string{"comment": "ok"}, true},
		{"developer 批准申请", "developer", "POST", "/api/v1/release-requests/" + nilUUID + "/approve", map[string]string{"comment": "ok"}, true},
		{"admin 批准申请", "gateway_admin", "POST", "/api/v1/release-requests/" + nilUUID + "/approve", map[string]string{"comment": "ok"}, false},
		{"super 批准申请", "super_admin", "POST", "/api/v1/release-requests/" + nilUUID + "/approve", map[string]string{"comment": "ok"}, false},

		// 不可变记录 DELETE → 任何角色 403（FR-032/宪法 V）
		{"viewer 删版本", "viewer", "DELETE", "/api/v1/versions/" + nilUUID, nil, true},
		{"developer 删版本", "developer", "DELETE", "/api/v1/versions/" + nilUUID, nil, true},
		{"admin 删版本", "gateway_admin", "DELETE", "/api/v1/versions/" + nilUUID, nil, true},
		{"super 删版本", "super_admin", "DELETE", "/api/v1/versions/" + nilUUID, nil, true},
		{"super 删部署", "super_admin", "DELETE", "/api/v1/deployments/" + nilUUID, nil, true},

		// SuperAdminOnly（用户管理/平台设置）：viewer/developer/admin 403；super 非 403
		{"viewer 删用户", "viewer", "DELETE", "/api/v1/users/" + nilUUID, nil, true},
		{"developer 删用户", "developer", "DELETE", "/api/v1/users/" + nilUUID, nil, true},
		{"admin 删用户", "gateway_admin", "DELETE", "/api/v1/users/" + nilUUID, nil, true},
		{"super 删用户", "super_admin", "DELETE", "/api/v1/users/" + nilUUID, nil, false},
		{"admin 改设置", "gateway_admin", "PUT", "/api/v1/settings", settingsBody, true},
		{"super 改设置", "super_admin", "PUT", "/api/v1/settings", settingsBody, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := app.do(t, tok(app, c.role), c.method, c.path, c.body)
			if c.want403 {
				requireErrorBody(t, r, 403, "FORBIDDEN")
			} else {
				assert.NotEqualf(t, 403, r.status, "%s 角色足够不应 403: %s", c.name, r.raw)
			}
		})
	}
}

// seedReadyVersion 构建一个 ready 状态配置版本（审批 Submit 前置：版本须 ready，FR-037）。
// 复用 dependency_test.go 同款装配：节点+域名+服务+启用路由 → wirePipeline → CreateVersion。
func seedReadyVersion(t *testing.T, env *testenv.Env, ctx context.Context, name string) (*domain.GatewayNode, *domain.ConfigVersion) {
	t.Helper()
	db := env.Store.DB
	rec := auditrec.New(pgstore.NewAuditRepo(db))
	rsvc := routesvc.New(pgstore.NewRouteRepo(db), pgstore.NewDomainRepo(db),
		pgstore.NewServiceRepo(db), pgstore.NewMiddlewareRepo(db), pgstore.NewNodeRepo(db), rec)
	admin := env.SeedUser(t, domain.RoleSuperAdmin)
	dev := routesvc.Principal{ID: env.SeedUser(t, domain.RoleDeveloper).ID, Role: "developer"}

	rtDir := t.TempDir()
	node := env.SeedNode(t, name, domain.EnvTest)
	node.DeployRoot = rtDir
	require.NoError(t, db.Save(node).Error)
	dom := env.SeedDomain(t, node.ID, name+".example.com")
	svc := env.SeedService(t, node.ID, name+"-svc", "http://10.9.9.9:80")
	rRoute, apiErr := rsvc.Create(ctx, routesvc.Input{NodeID: node.ID, Name: name + "-route", Mode: "simple",
		DomainID: dom.ID, Path: "/", MatchType: "prefix", ServiceID: svc.ID}, dev)
	require.Nil(t, apiErr)
	_, apiErr = rsvc.SetStatus(ctx, rRoute.Route.ID, "enabled", rRoute.Route.RowVersion, dev.ID)
	require.Nil(t, apiErr)

	vsvc, _, _, _ := wirePipeline(t, env, rtDir)
	ver, _, apiErr := vsvc.CreateVersion(ctx, node.ID, admin.ID)
	require.Nil(t, apiErr)
	require.Equal(t, "ready", ver.Status)
	return node, ver
}

// TestApproval_SelfApprovalRejectedAndDualIdentityAudit —— V-6 自批守卫 + 双身份审计（FR-037/US6-AC3）。
func TestApproval_SelfApprovalRejectedAndDualIdentityAudit(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	db := env.Store.DB
	node, ver := seedReadyVersion(t, env, ctx, "appr")

	appS := approvalsvc.New(pgstore.NewApprovalRepo(db), pgstore.NewVersionRepo(db), auditrec.New(pgstore.NewAuditRepo(db)))
	dev := env.SeedUser(t, domain.RoleDeveloper)
	admin := env.SeedUser(t, domain.RoleGatewayAdmin)

	// 提交发布申请（dev）
	rr, apiErr := appS.Submit(ctx, node.ID, ver.ID, dev.ID, "请审批")
	require.Nil(t, apiErr)
	assert.Equal(t, "pending", rr.Status)

	// ① 自批守卫：同一 dev 批准自己提交的申请 → 422 PIPELINE_BLOCKED（FR-037 应用层+DB CHECK 双层）
	_, apiErr = appS.Approve(ctx, rr.ID, dev.ID, "自批")
	require.NotNil(t, apiErr)
	assert.Equal(t, 422, apiErr.Status)
	assert.Equal(t, "PIPELINE_BLOCKED", apiErr.Code)
	assert.Contains(t, apiErr.Message, "自批")

	// ② 双身份审计：admin 批准 dev 提交的申请 → 成功，审计同时记录 submitted_by(dev) 与 reviewed_by(admin)
	approved, apiErr := appS.Approve(ctx, rr.ID, admin.ID, "批准发布")
	require.Nil(t, apiErr)
	assert.Equal(t, "approved", approved.Status)

	logs, _, err := pgstore.NewAuditRepo(db).Search(ctx,
		pgstore.AuditFilter{ResourceType: "release_request", ResourceID: rr.ID},
		pgstore.ListQuery{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Len(t, logs, 2, "submit + approve 各一条审计")

	var submitLog, approveLog *domain.AuditLog
	for i := range logs {
		switch logs[i].Action {
		case "release_request.submit":
			submitLog = &logs[i]
		case "release_request.approve":
			approveLog = &logs[i]
		}
	}
	require.NotNil(t, submitLog, "submit 审计记录存在")
	require.NotNil(t, approveLog, "approve 审计记录存在")
	assert.Equal(t, dev.ID, submitLog.ActorID, "submit 审计 actor=提交人 dev")
	assert.Equal(t, admin.ID, approveLog.ActorID, "approve 审计 actor=批准人 admin")
	// approve 审计同时记录双身份（US6-AC3：提交人 + 批准人同落一条）
	assert.Equal(t, dev.ID, approveLog.Before["submitted_by"], "approve before 保留 submitted_by=dev")
	assert.Equal(t, admin.ID, approveLog.After["reviewed_by"], "approve after 记录 reviewed_by=admin")
	assert.Equal(t, dev.ID, approveLog.After["submitted_by"], "approve after 保留 submitted_by=dev（双身份）")
}

// TestAudit_TamperingRejectedByDB —— V-6 审计篡改被 DB 触发器拒绝（宪法 X / FR-038）。
// 0002_audit_partitions.sql：BEFORE UPDATE OR DELETE RAISE（对 owner 也生效，owner 绕过 GRANT 不绕过触发器）。
func TestAudit_TamperingRejectedByDB(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	db := env.Store.DB
	nodeS := nodesvc.New(pgstore.NewNodeRepo(db), auditrec.New(pgstore.NewAuditRepo(db)), env.Cipher)
	admin := env.SeedUser(t, domain.RoleSuperAdmin)

	// 创建节点 → 产生 gateway_node/create 审计记录
	n, apiErr := nodeS.Create(ctx, nodesvc.Input{
		Name: "audit-tamper", BaseURL: "http://localhost:8081",
		DeployRoot: "/tmp/audit-tamper", EnvType: "test",
	}, nodesvc.Actor{ID: admin.ID, Username: admin.Username, IP: "127.0.0.1"})
	require.Nil(t, apiErr)

	auditRepo := pgstore.NewAuditRepo(db)
	logs, _, err := auditRepo.Search(ctx,
		pgstore.AuditFilter{ResourceType: "gateway_node", ResourceID: n.ID},
		pgstore.ListQuery{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.NotEmpty(t, logs, "创建节点必产生审计记录")
	logID := logs[0].ID

	// UPDATE → 触发器 RAISE（宪法 X 仅追加）
	res := db.Exec("UPDATE audit_logs SET action = ? WHERE id = ?", "tampered", logID)
	require.Error(t, res.Error, "审计 UPDATE 必须被 DB 触发器拒绝")

	// DELETE → 触发器 RAISE
	res = db.Exec("DELETE FROM audit_logs WHERE id = ?", logID)
	require.Error(t, res.Error, "审计 DELETE 必须被 DB 触发器拒绝")

	// 原记录未变（UPDATE 被回滚）
	fresh, err := auditRepo.Get(ctx, logID)
	require.NoError(t, err)
	assert.Equal(t, "create", fresh.Action, "篡改失败后原 action 不变")
}
