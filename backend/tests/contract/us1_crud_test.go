//go:build contract

// T049 · US1 契约测试（contracts/openapi.yaml + README 错误模型/权限矩阵）：
// /nodes /domains /services /routes /versions /deployments 状态码、错误体结构、
// 409 乐观锁、api_auth 与私钥全链脱敏、Viewer 零写入口、不可变记录 DELETE→403。
// 走真实 HTTP（api.NewRouter + Bearer），不触任何内部函数——路由与合同一一对应。
package contract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/api"
	"gateway-center/backend/internal/api/handlers"
	"gateway-center/backend/internal/api/middleware"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/authsvc"
	"gateway-center/backend/internal/application/certsvc"
	"gateway-center/backend/internal/application/domainsvc"
	"gateway-center/backend/internal/application/nodesvc"
	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/application/routesvc"
	"gateway-center/backend/internal/application/servicesvc"
	"gateway-center/backend/internal/application/settingsvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/deployer"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/tokensx"
	"gateway-center/backend/internal/infrastructure/traefikapi"
	"gateway-center/backend/tests/testenv"
)

const testPW = "Contract-Test-Pass-1" // ≥12，满足 R6 强度门槛

type app struct {
	srv    *httptest.Server
	env    *testenv.Env
	tokens map[string]string // role → access token
}

// newApp 完整复刻 serve.go 装配（fake Traefik 永不视为在线）。
func newApp(t *testing.T) *app {
	t.Helper()
	env := testenv.Setup(t)
	db := env.Store.DB
	nodes := pgstore.NewNodeRepo(db)
	doms := pgstore.NewDomainRepo(db)
	svcs := pgstore.NewServiceRepo(db)
	targets := pgstore.NewTargetRepo(db)
	routes := pgstore.NewRouteRepo(db)
	mws := pgstore.NewMiddlewareRepo(db)
	vers := pgstore.NewVersionRepo(db)
	deploys := pgstore.NewDeploymentRepo(db)
	certs := pgstore.NewCertRepo(db)
	users := pgstore.NewUserRepo(db)
	toks := pgstore.NewTokenRepo(db)
	rec := auditrec.New(pgstore.NewAuditRepo(db))

	tm := tokensx.NewManager(testenv.MustHexKey(t, testenv.TestKey), time.Hour)
	nodeSvc := nodesvc.New(nodes, rec, env.Cipher)
	h := &handlers.Handler{
		Auth:     authsvc.NewService(users, toks, tm, rec, 7*24*time.Hour),
		Nodes:    nodeSvc,
		Domains:  domainsvc.New(doms, nodes, rec),
		Certs:    certsvc.New(certs, doms, env.Cipher, rec),
		Services: servicesvc.New(svcs, targets, nodes, rec),
		Routes:   routesvc.New(routes, doms, svcs, mws, nodes, rec),
		Versions: pipeline.NewVersionService(
			pipeline.NewGraphLoader(nodes, doms, svcs, targets, routes, mws),
			vers, deploys, certs, env.Cipher, rec),
		// 不可达 factory：管线发布必在离线闸停住（同步 422/502），不产生后台任务
		Deploys: pipeline.NewDeployService(vers, deploys, nodes, certs, env.Cipher,
			deployer.NewFileDeployer(),
			func(*domain.GatewayNode) (*traefikapi.Client, error) {
				return traefikapi.NewClient("http://127.0.0.1:1", "", ""), nil
			}, rec, nil),
		Settings: settingsvc.New(pgstore.NewSettingsRepo(db), rec),
		Vers:     vers,
		Deps:     deploys,
	}
	srv := httptest.NewServer(api.NewRouter(h, middleware.Authenticate(tm, users)))
	t.Cleanup(srv.Close)
	a := &app{srv: srv, env: env, tokens: map[string]string{}}
	for _, r := range []domain.Role{domain.RoleSuperAdmin, domain.RoleDeveloper, domain.RoleViewer} {
		u := env.SeedUserWithPassword(t, r, testPW)
		a.tokens[string(r)] = a.login(t, u.Username, testPW)
	}
	return a
}

type resp struct {
	status int
	body   map[string]any
	raw    []byte
}

func (a *app) do(t *testing.T, tok, method, path string, body any) resp {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, a.srv.URL+path, rd)
	require.NoError(t, err)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Content-Type", "application/json")
	hr, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer hr.Body.Close()
	var b map[string]any
	raw := toJSON(t, hr)
	_ = json.Unmarshal(raw, &b)
	return resp{status: hr.StatusCode, body: b, raw: raw}
}

func toJSON(t *testing.T, hr *http.Response) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(hr.Body)
	require.NoError(t, err)
	return buf.Bytes()
}

func (a *app) login(t *testing.T, user, pass string) string {
	t.Helper()
	r := a.do(t, "", "POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pass})
	require.Equal(t, 200, r.status, "登录 %s: %s", user, r.raw)
	tok, _ := r.body["access_token"].(string)
	require.NotEmpty(t, tok)
	return tok
}

// requireErrorBody 断言 contracts/README.md 错误模型：{error:{code,message,request_id,details?}}
func requireErrorBody(t *testing.T, r resp, status int, code string) map[string]any {
	t.Helper()
	require.Equal(t, status, r.status, "响应体: %s", r.raw)
	require.NotNil(t, r.body, "错误必须是 JSON: %s", r.raw)
	e, ok := r.body["error"].(map[string]any)
	require.True(t, ok, "缺少 error 包裹: %s", r.raw)
	assert.Equal(t, code, e["code"])
	assert.NotEmpty(t, e["message"], "错误须含人读 message（NFR-USE-01）")
	assert.NotEmpty(t, e["request_id"], "错误须携带 request_id（宪法 X 追溯）")
	return e
}

func dev(t *testing.T, a *app) string    { return a.tokens[string(domain.RoleDeveloper)] }
func viewer(t *testing.T, a *app) string { return a.tokens[string(domain.RoleViewer)] }
func sa(t *testing.T, a *app) string     { return a.tokens[string(domain.RoleSuperAdmin)] }

// ---- 认证 ----

func TestAuthContract(t *testing.T) {
	a := newApp(t)
	requireErrorBody(t, a.do(t, "", "GET", "/api/v1/nodes", nil), 401, "UNAUTHENTICATED")
	requireErrorBody(t, a.do(t, "garbage", "GET", "/api/v1/nodes", nil), 401, "UNAUTHENTICATED")
	u := a.env.SeedUserWithPassword(t, domain.RoleViewer, testPW)
	requireErrorBody(t, a.do(t, "", "POST", "/api/v1/auth/login",
		map[string]string{"username": u.Username, "password": "wrong-password-xx"}), 401, "UNAUTHENTICATED")

	r := a.do(t, dev(t, a), "GET", "/api/v1/me", nil)
	require.Equal(t, 200, r.status)
	assert.Equal(t, "developer", r.body["role"])
}

// ---- /nodes CRUD + 错误体 + 乐观锁 + 脱敏 ----

func nodeBody(name, root string) map[string]any {
	return map[string]any{"name": name, "base_url": "http://localhost:8081",
		"deploy_root": root, "env_type": "test"}
}

func TestNodesCRUD(t *testing.T) {
	a := newApp(t)
	root := t.TempDir()

	r := a.do(t, dev(t, a), "POST", "/api/v1/nodes", nodeBody("c-node", root))
	require.Equal(t, 201, r.status, "%s", r.raw)
	id, _ := r.body["id"].(string)
	require.NotEmpty(t, id)
	rv, _ := r.body["row_version"].(float64)
	assert.EqualValues(t, 1, rv)

	// 400：字段级 details（NFR-USE-01 修复建议）
	e := requireErrorBody(t, a.do(t, dev(t, a), "POST", "/api/v1/nodes",
		map[string]any{"name": "", "base_url": "ftp://x", "deploy_root": "rel", "env_type": "prod2"}),
		400, "VALIDATION_FAILED")
	details, _ := e["details"].([]any)
	assert.NotEmpty(t, details, "校验错误须逐字段定位")

	// 409 乐观锁：过期 expected_version
	requireErrorBody(t, a.do(t, dev(t, a), "PUT", "/api/v1/nodes/"+id,
		map[string]any{"name": "c-node", "base_url": "http://localhost:8081",
			"deploy_root": root, "env_type": "test", "expected_version": 99}),
		409, "CONCURRENT_EDIT")

	// 404
	requireErrorBody(t, a.do(t, dev(t, a), "GET", "/api/v1/nodes/00000000-0000-0000-0000-000000000000", nil),
		404, "NOT_FOUND")

	// 列表信封
	lr := a.do(t, viewer(t, a), "GET", "/api/v1/nodes?page=1&page_size=10", nil)
	require.Equal(t, 200, lr.status)
	assert.Contains(t, lr.body, "items")
	assert.Contains(t, lr.body, "total")

	// Viewer 一切写入口 403（US6-AC1 矩阵行，提前固化）
	requireErrorBody(t, a.do(t, viewer(t, a), "POST", "/api/v1/nodes", nodeBody("v-node", root)),
		403, "FORBIDDEN")

	// api_auth 脱敏（R7/宪章 VIII）：写入即不回显
	ar := a.do(t, dev(t, a), "POST", "/api/v1/nodes", func() map[string]any {
		m := nodeBody("c-auth-node", t.TempDir())
		m["api_auth"] = "admin:S3cr3t-Auth-Key!"
		return m
	}())
	require.Equal(t, 201, ar.status)
	assert.NotContains(t, string(ar.raw), "S3cr3t-Auth-Key!", "响应禁止含 api_auth 明文")
	assert.Equal(t, true, ar.body["has_api_auth"])
	gr := a.do(t, dev(t, a), "GET", "/api/v1/nodes/"+str(ar.body["id"]), nil)
	assert.NotContains(t, string(gr.raw), "S3cr3t-Auth-Key!")
}

// ---- /domains /services /targets /routes ----

func TestBusinessEntitiesCRUD(t *testing.T) {
	a := newApp(t)
	root := t.TempDir()
	nid := str(a.do(t, dev(t, a), "POST", "/api/v1/nodes", nodeBody("ent-node", root)).body["id"])

	// domains
	dr := a.do(t, dev(t, a), "POST", "/api/v1/domains",
		map[string]any{"node_id": nid, "name": "shop.example.com", "https_policy": "off"})
	require.Equal(t, 201, dr.status, "%s", dr.raw)
	did := str(dr.body["id"])
	requireErrorBody(t, a.do(t, dev(t, a), "POST", "/api/v1/domains",
		map[string]any{"node_id": nid, "name": "Not A Domain", "https_policy": "off"}),
		400, "VALIDATION_FAILED")

	// services + targets
	svr := a.do(t, dev(t, a), "POST", "/api/v1/services",
		map[string]any{"node_id": nid, "name": "shop-svc", "interval_sec": 30,
			"timeout_sec": 2, "expected_codes": "2xx-3xx"})
	require.Equal(t, 201, svr.status, "%s", svr.raw)
	sid := str(svr.body["id"])
	tr := a.do(t, dev(t, a), "POST", "/api/v1/services/"+sid+"/targets",
		map[string]any{"url": "http://10.0.0.7:8080", "weight": 10})
	require.Equal(t, 201, tr.status, "%s", tr.raw)
	requireErrorBody(t, a.do(t, dev(t, a), "POST", "/api/v1/services/"+sid+"/targets",
		map[string]any{"url": "ftp://bad", "weight": 0}), 400, "VALIDATION_FAILED")

	// routes：创建即回显 generated_rule_preview（FR-014 零 Traefik 语法）
	rr := a.do(t, dev(t, a), "POST", "/api/v1/routes",
		map[string]any{"node_id": nid, "name": "shop-route", "mode": "simple",
			"domain_id": did, "service_id": sid, "path": "/shop", "match_type": "prefix"})
	require.Equal(t, 201, rr.status, "%s", rr.raw)
	assert.Contains(t, str(rr.body["generated_rule_preview"]), "PathPrefix", "预览=合成规则回显")

	// 证书导入：非法 PEM → 400 字段定位；响应零私钥
	requireErrorBody(t, a.do(t, dev(t, a), "POST", "/api/v1/certificates/import",
		map[string]any{"domain_id": did, "cert_pem": "not-a-pem", "key_pem": "still-not"}),
		400, "VALIDATION_FAILED")

	// 写组之外：settings PUT 仅 super_admin
	st := a.do(t, sa(t, a), "GET", "/api/v1/settings", nil)
	require.Equal(t, 200, st.status)
	requireErrorBody(t, a.do(t, dev(t, a), "PUT", "/api/v1/settings",
		map[string]any{"probe_interval_sec": 30, "offline_threshold": 3,
			"expiry_warn_days": 30, "timezone": "Asia/Shanghai", "expected_version": 1}),
		403, "FORBIDDEN")
}

// ---- 管线端点合同：状态码语义 + DELETE 不可变 ----

func TestPipelineEndpointsContract(t *testing.T) {
	a := newApp(t)
	root := t.TempDir()
	nid := str(a.do(t, dev(t, a), "POST", "/api/v1/nodes", nodeBody("pl-node", root)).body["id"])
	did := str(a.do(t, dev(t, a), "POST", "/api/v1/domains",
		map[string]any{"node_id": nid, "name": "a.example.com", "https_policy": "off"}).body["id"])
	sid := str(a.do(t, dev(t, a), "POST", "/api/v1/services",
		map[string]any{"node_id": nid, "name": "a-svc", "interval_sec": 30,
			"timeout_sec": 2, "expected_codes": "2xx-3xx"}).body["id"])
	a.do(t, dev(t, a), "POST", "/api/v1/services/"+sid+"/targets",
		map[string]any{"url": "http://10.0.0.8:80", "weight": 1})
	a.do(t, dev(t, a), "POST", "/api/v1/routes",
		map[string]any{"node_id": nid, "name": "a-route", "mode": "simple",
			"domain_id": did, "service_id": sid, "path": "/", "match_type": "prefix"})

	// validate：200 + 报告结构
	vr := a.do(t, dev(t, a), "POST", "/api/v1/nodes/"+nid+"/validate", nil)
	require.Equal(t, 200, vr.status, "%s", vr.raw)
	assert.Contains(t, vr.body, "valid")
	assert.Contains(t, vr.body, "issues")

	// 生成版本：201 ready
	cr := a.do(t, dev(t, a), "POST", "/api/v1/nodes/"+nid+"/versions", nil)
	require.Equal(t, 201, cr.status, "%s", cr.raw)
	vid := str(cr.body["id"])
	assert.Equal(t, "ready", cr.body["status"])
	// 列表剔除大字段（SC-006）
	lv := a.do(t, dev(t, a), "GET", "/api/v1/nodes/"+nid+"/versions", nil)
	require.Equal(t, 200, lv.status)
	items, _ := lv.body["items"].([]any)
	require.NotEmpty(t, items)
	first, _ := items[0].(map[string]any)
	assert.NotContains(t, first, "snapshot", "列表禁止回传快照大字段")

	// 发布：未确认 → 422；确认但网关不可达 → 502；两者均零后台任务（同步拒绝在闸口）
	requireErrorBody(t, a.do(t, dev(t, a), "POST", "/api/v1/deployments",
		map[string]any{"node_id": nid, "version_id": vid, "confirmed": false}),
		422, "DEPLOY_NOT_CONFIRMED")
	off := requireErrorBody(t, a.do(t, dev(t, a), "POST", "/api/v1/deployments",
		map[string]any{"node_id": nid, "version_id": vid, "confirmed": true}),
		502, "GATEWAY_UNREACHABLE")
	assert.NotEmpty(t, off["request_id"])

	// 不可变记录：任何角色 DELETE → 403（FR-032）
	requireErrorBody(t, a.do(t, sa(t, a), "DELETE", "/api/v1/versions/"+vid, nil), 403, "FORBIDDEN")
	requireErrorBody(t, a.do(t, dev(t, a), "DELETE", "/api/v1/deployments/00000000-0000-0000-0000-000000000000", nil),
		403, "FORBIDDEN")

	// 回滚属 gateway_admin+：developer → 403（矩阵行）
	requireErrorBody(t, a.do(t, dev(t, a), "POST", "/api/v1/deployments/rollback",
		map[string]any{"node_id": nid, "version_id": vid, "confirmed": true}),
		403, "FORBIDDEN")
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
