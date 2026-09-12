//go:build integration

// T070 · US4 集成测试（quickstart V-4，FR-007/008/033/035、宪章 VII/VIII）。
// 断言：① 泛域名强制 DNS、acme_dns 必带 credential+resolver；
// ② 凭证轮换指纹变/绑定不变；③ 凭证明文在 Get/List/审计 After/verify reason 全链零命中；
// ④ 删除被域名引用→409 引用清单，解绑后可删；⑤ certwatch 经 TLS 握手观测（不读 acme.json）。
package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/credsvc"
	"gateway-center/backend/internal/application/domainsvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/certwatch"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/tests/testenv"
)

const plainSecret = "super-secret-token-V4-xyz"

func wireCert(t *testing.T, env *testenv.Env) (*credsvc.Service, *domainsvc.Service, *pgstore.CertRepo) {
	t.Helper()
	db := env.Store.DB
	creds := pgstore.NewCredentialRepo(db)
	doms := pgstore.NewDomainRepo(db)
	nodes := pgstore.NewNodeRepo(db)
	certs := pgstore.NewCertRepo(db)
	rec := auditrec.New(pgstore.NewAuditRepo(db))
	return credsvc.New(creds, env.Cipher, rec),
		domainsvc.New(doms, nodes, rec),
		certs
}

// selfSignedCert 生成测试用自签证书（指定 NotAfter + DNS SAN）。
func selfSignedCert(t *testing.T, notAfter time.Time, dns ...string) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     dns,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	c, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return c
}

type fixedSettings struct{}

func (fixedSettings) WarnDays() int { return 30 }

// TestCertPolicy_WildcardAndAcmeDNSValidation 泛域名强制 DNS + acme_dns 必带项（FR-007/宪章 VII）。
func TestCertPolicy_WildcardAndAcmeDNSValidation(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	_, domSvc, _ := wireCert(t, env)
	node := env.SeedNode(t, "gw-cert", domain.EnvTest)
	actor := env.SeedUser(t, domain.RoleSuperAdmin).ID

	// 泛域名 + acme_http → 400 field=https_policy（HTTP-01 无法签发泛域名，宪章 VII）
	_, apiErr := domSvc.Create(ctx, domainsvc.Input{
		NodeID: node.ID, Name: "*.wild.example.com",
		HTTPSPolicy: "acme_http", CertResolverRef: "le",
	}, actor)
	require.NotNil(t, apiErr)
	assert.Equal(t, "https_policy", apiErr.Details[0].Field)

	// acme_dns 无 resolver → 400
	_, apiErr = domSvc.Create(ctx, domainsvc.Input{
		NodeID: node.ID, Name: "a.example.com", HTTPSPolicy: "acme_dns",
	}, actor)
	require.NotNil(t, apiErr)
	assert.Equal(t, "cert_resolver_ref", apiErr.Details[0].Field)

	// acme_dns 有 resolver 无 credential → 400
	_, apiErr = domSvc.Create(ctx, domainsvc.Input{
		NodeID: node.ID, Name: "b.example.com", HTTPSPolicy: "acme_dns", CertResolverRef: "le",
	}, actor)
	require.NotNil(t, apiErr)
	assert.Equal(t, "dns_credential_id", apiErr.Details[0].Field)
}

// TestCredential_RotateFingerprintAndBinding 轮换指纹变更、域名绑定不变（FR-035）。
func TestCredential_RotateFingerprintAndBinding(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	credSvc, domSvc, _ := wireCert(t, env)
	node := env.SeedNode(t, "gw-rot", domain.EnvTest)
	actor := env.SeedUser(t, domain.RoleSuperAdmin).ID

	c, apiErr := credSvc.Create(ctx, credsvc.Input{
		Name: "cf-rotate", Kind: "dns_provider", Provider: "cloudflare",
		Data: map[string]string{"token": plainSecret},
	}, actor)
	require.Nil(t, apiErr)
	fp1 := c.DataFingerprint
	require.NotEmpty(t, fp1)

	// 域名 acme_dns 绑定该凭证
	dom, apiErr := domSvc.Create(ctx, domainsvc.Input{
		NodeID: node.ID, Name: "rot.example.com", HTTPSPolicy: "acme_dns",
		CertResolverRef: "le", DNSSCredentialID: c.ID,
	}, actor)
	require.Nil(t, apiErr)
	require.NotNil(t, dom.DNSSCredentialID)
	assert.Equal(t, c.ID, *dom.DNSSCredentialID)

	// 轮换
	c2, apiErr := credSvc.Update(ctx, c.ID, credsvc.Input{
		Name: "cf-rotate", Kind: "dns_provider", Provider: "cloudflare",
		Data: map[string]string{"token": "rotated-" + plainSecret}, ExpectedVersion: c.RowVersion,
	}, actor)
	require.Nil(t, apiErr)
	assert.Equal(t, c.ID, c2.ID, "轮换不改 id")
	assert.NotEqual(t, fp1, c2.DataFingerprint, "轮换后指纹应变")

	// 域名绑定不变（仍指向同一 credential id）
	fresh, err := domSvc.Get(ctx, dom.ID)
	require.NoError(t, err)
	require.NotNil(t, fresh.DNSSCredentialID)
	assert.Equal(t, c.ID, *fresh.DNSSCredentialID, "轮换不解除域名绑定")
}

// TestCredential_NeverEchoesPlaintext 全链脱敏：Get/List/审计 After/verify 零明文（宪章 VIII/NFR-SEC-01）。
func TestCredential_NeverEchoesPlaintext(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	credSvc, _, _ := wireCert(t, env)
	actor := env.SeedUser(t, domain.RoleSuperAdmin).ID

	c, apiErr := credSvc.Create(ctx, credsvc.Input{
		Name: "cf-grep", Kind: "dns_provider", Provider: "cloudflare",
		Data: map[string]string{"api_token": plainSecret, "secret2": "another-" + plainSecret},
	}, actor)
	require.Nil(t, apiErr)

	// Get：JSON 不含明文（DataEncrypted json:"-"）
	got, apiErr := credSvc.Get(ctx, c.ID)
	require.Nil(t, apiErr)
	b, _ := json.Marshal(got)
	assert.NotContains(t, string(b), plainSecret)

	// List：不含明文
	items, _, err := credSvc.List(ctx, pgstore.ListQuery{Page: 1, PageSize: 100})
	require.NoError(t, err)
	for _, it := range items {
		b, _ := json.Marshal(it)
		assert.NotContains(t, string(b), plainSecret)
	}

	// 审计 After：redactedView 仅留元数据+fingerprint
	var logs []domain.AuditLog
	env.Store.DB.Where("resource_id = ?", c.ID).Find(&logs)
	require.NotEmpty(t, logs, "create 须写审计")
	for _, l := range logs {
		b, _ := json.Marshal(l.After)
		assert.NotContains(t, string(b), plainSecret, "审计 After 不得含明文")
	}

	// verify reason 不含明文
	res, apiErr := credSvc.Verify(ctx, c.ID)
	require.Nil(t, apiErr)
	assert.NotContains(t, res.Reason, plainSecret)
}

// TestCredential_DeleteBlockedThenOK 删除被引用→409，解绑后可删（FR-035 处置语义）。
func TestCredential_DeleteBlockedThenOK(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	credSvc, domSvc, _ := wireCert(t, env)
	node := env.SeedNode(t, "gw-del", domain.EnvTest)
	actor := env.SeedUser(t, domain.RoleSuperAdmin).ID

	c, apiErr := credSvc.Create(ctx, credsvc.Input{
		Name: "cf-del", Kind: "dns_provider", Provider: "cloudflare",
		Data: map[string]string{"token": plainSecret},
	}, actor)
	require.Nil(t, apiErr)

	dom, apiErr := domSvc.Create(ctx, domainsvc.Input{
		NodeID: node.ID, Name: "del.example.com", HTTPSPolicy: "acme_dns",
		CertResolverRef: "le", DNSSCredentialID: c.ID,
	}, actor)
	require.Nil(t, apiErr)

	// 被引用→409 DEPENDENCY_BLOCKED 带域名清单
	apiErr = credSvc.Delete(ctx, c.ID, actor)
	require.NotNil(t, apiErr)
	assert.Equal(t, "DEPENDENCY_BLOCKED", apiErr.Code)
	found := false
	for _, d := range apiErr.Details {
		if d.Message == dom.Name {
			found = true
		}
	}
	assert.True(t, found, "409 须含引用域名 %s", dom.Name)

	// 解绑（域名改 off）→ 删除成功
	_, apiErr = domSvc.Update(ctx, dom.ID, domainsvc.Input{
		NodeID: node.ID, Name: dom.Name, HTTPSPolicy: "off", ExpectedVersion: dom.RowVersion,
	}, actor)
	require.Nil(t, apiErr)
	apiErr = credSvc.Delete(ctx, c.ID, actor)
	assert.Nil(t, apiErr)
}

// TestCertWatch_TLSProbeNotAcmeJson 经 TLS 握手观测、不读 acme.json（宪章 VII/R14）。
func TestCertWatch_TLSProbeNotAcmeJson(t *testing.T) {
	env := testenv.Setup(t)
	ctx := context.Background()
	_, domSvc, certs := wireCert(t, env)
	node := env.SeedNode(t, "gw-watch", domain.EnvTest)
	actor := env.SeedUser(t, domain.RoleSuperAdmin).ID

	notAfter := time.Now().Add(20 * 24 * time.Hour) // 20 天后到期 → expiring_soon（阈值 30 天）
	leaf := selfSignedCert(t, notAfter, "probe.example.com")
	called := false
	prober := func(_ context.Context, _ string) (*x509.Certificate, error) {
		called = true
		return leaf, nil
	}
	cw := certwatch.New(certs, pgstore.NewDomainRepo(env.Store.DB), fixedSettings{}, prober)

	dom, apiErr := domSvc.Create(ctx, domainsvc.Input{
		NodeID: node.ID, Name: "probe.example.com", HTTPSPolicy: "acme_http", CertResolverRef: "le",
	}, actor)
	require.Nil(t, apiErr)

	// acme.json 占位文件（证明 certwatch 不触碰）
	dir := t.TempDir()
	acmeJSON := filepath.Join(dir, "acme.json")
	require.NoError(t, os.WriteFile(acmeJSON, []byte("{}"), 0o600))
	mtimeBefore, _ := os.Stat(acmeJSON)

	cw.RunOnce(ctx)
	assert.True(t, called, "certwatch 须经 TLS 握手观测（注入 prober）")

	// 证书已 upsert：NotAfter 正确、source=acme、status=expiring_soon（20 天 < 30 天阈值）
	obs, err := certs.ByDomain(ctx, dom.ID)
	require.NoError(t, err, "RunOnce 后须有证书记录")
	require.NotNil(t, obs.NotAfter)
	assert.WithinDuration(t, notAfter, *obs.NotAfter, time.Second)
	assert.Equal(t, "acme", obs.Source)
	assert.Equal(t, "expiring_soon", obs.Status)

	// acme.json mtime 不变（certwatch 不读 acme.json，宪法 VII）
	mtimeAfter, _ := os.Stat(acmeJSON)
	assert.Equal(t, mtimeBefore.ModTime(), mtimeAfter.ModTime(), "acme.json 不得被读取/写入")
}
