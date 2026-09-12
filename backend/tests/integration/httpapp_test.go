//go:build integration

// HTTP 测试 harness（T082/T085 共用）：完整复刻 serve.go 的 Handler 装配 +
// chi 路由 + Bearer 认证，四角色令牌预登录。走真实 HTTP，不触内部函数——
// 用于 RBAC 矩阵（RequireRoles × 四角色）与不可变记录 DELETE→403 复查。
package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gateway-center/backend/internal/api"
	"gateway-center/backend/internal/api/handlers"
	"gateway-center/backend/internal/api/middleware"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/application/approvalsvc"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/authsvc"
	"gateway-center/backend/internal/application/certsvc"
	"gateway-center/backend/internal/application/credsvc"
	"gateway-center/backend/internal/application/dashboardsvc"
	"gateway-center/backend/internal/application/domainsvc"
	"gateway-center/backend/internal/application/mwsvc"
	"gateway-center/backend/internal/application/nodesvc"
	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/application/routesvc"
	"gateway-center/backend/internal/application/servicesvc"
	"gateway-center/backend/internal/application/settingsvc"
	"gateway-center/backend/internal/application/usersvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/deployer"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/tokensx"
	"gateway-center/backend/internal/infrastructure/traefikapi"
	"gateway-center/backend/tests/testenv"
)

const httpTestPW = "Integration-Test-1" // ≥12，满足 R6 强度门槛

// httpApp 完整 HTTP 应用（fake Traefik 永不可达 → 管线发布在离线闸停住）。
type httpApp struct {
	srv    *httptest.Server
	env    *testenv.Env
	tokens map[string]string // role → access token
}

// newHTTPApp 复刻 serve.go 装配（含 Users/Approvals/Audits，四角色预登录）。
func newHTTPApp(t *testing.T) *httpApp {
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
	creds := pgstore.NewCredentialRepo(db)
	users := pgstore.NewUserRepo(db)
	toks := pgstore.NewTokenRepo(db)
	approvals := pgstore.NewApprovalRepo(db)
	setRepo := pgstore.NewSettingsRepo(db)
	auditRepo := pgstore.NewAuditRepo(db)
	rec := auditrec.New(auditRepo)

	tm := tokensx.NewManager(testenv.MustHexKey(t, testenv.TestKey), time.Hour)
	traefikFactory := pipeline.TraefikFactory(func(*domain.GatewayNode) (*traefikapi.Client, error) {
		return traefikapi.NewClient("http://127.0.0.1:1", "", ""), nil
	})
	h := &handlers.Handler{
		Auth:        authsvc.NewService(users, toks, tm, rec, 7*24*time.Hour),
		Nodes:       nodesvc.New(nodes, rec, env.Cipher),
		Domains:     domainsvc.New(doms, nodes, rec),
		Certs:       certsvc.New(certs, doms, env.Cipher, rec),
		Credentials: credsvc.New(creds, env.Cipher, rec),
		Dash:        dashboardsvc.New(nodes, doms, svcs, routes, mws, certs, deploys, setRepo),
		Services:    servicesvc.New(svcs, targets, nodes, rec),
		Routes:      routesvc.New(routes, doms, svcs, mws, nodes, rec),
		Middlewares: mwsvc.New(mws, nodes, rec),
		Versions: pipeline.NewVersionService(
			pipeline.NewGraphLoader(nodes, doms, svcs, targets, routes, mws),
			vers, deploys, certs, env.Cipher, rec),
		Deploys: pipeline.NewDeployService(vers, deploys, nodes, certs, env.Cipher,
			deployer.NewFileDeployer(), traefikFactory, rec, approvalsvc.NewProductionGate(approvals)),
		Settings:  settingsvc.New(setRepo, rec),
		Users:     usersvc.New(users, toks, rec),
		Approvals: approvalsvc.New(approvals, vers, rec),
		Vers:      vers,
		Deps:      deploys,
		Audits:    auditRepo,
	}
	srv := httptest.NewServer(api.NewRouter(h, middleware.Authenticate(tm, users)))
	t.Cleanup(srv.Close)
	a := &httpApp{srv: srv, env: env, tokens: map[string]string{}}
	for _, r := range []domain.Role{domain.RoleSuperAdmin, domain.RoleGatewayAdmin, domain.RoleDeveloper, domain.RoleViewer} {
		u := env.SeedUserWithPassword(t, r, httpTestPW)
		a.tokens[string(r)] = a.login(t, u.Username, httpTestPW)
	}
	return a
}

type httpResp struct {
	status int
	body   map[string]any
	raw    []byte
}

func (a *httpApp) do(t *testing.T, tok, method, path string, body any) httpResp {
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
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(hr.Body)
	raw := buf.Bytes()
	var b map[string]any
	_ = json.Unmarshal(raw, &b)
	return httpResp{status: hr.StatusCode, body: b, raw: raw}
}

func (a *httpApp) login(t *testing.T, user, pass string) string {
	t.Helper()
	r := a.do(t, "", "POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pass})
	require.Equalf(t, 200, r.status, "登录 %s: %s", user, r.raw)
	tok, _ := r.body["access_token"].(string)
	require.NotEmpty(t, tok, "access_token 非空")
	return tok
}

// requireErrorBody 断言 contracts 错误模型 {error:{code,message,request_id,details?}}。
func requireErrorBody(t *testing.T, r httpResp, status int, code string) map[string]any {
	t.Helper()
	require.Equalf(t, status, r.status, "响应体: %s", r.raw)
	require.NotNilf(t, r.body, "错误必须是 JSON: %s", r.raw)
	e, ok := r.body["error"].(map[string]any)
	require.Truef(t, ok, "缺少 error 包裹: %s", r.raw)
	assert.Equal(t, code, e["code"])
	assert.NotEmpty(t, e["message"], "错误须含人读 message（NFR-USE-01）")
	assert.NotEmpty(t, e["request_id"], "错误须携带 request_id（宪法 X 追溯）")
	return e
}

func tok(a *httpApp, role string) string { return a.tokens[role] }
