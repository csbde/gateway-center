// Package certsvc 自有证书导入（T033，支撑 US1 HTTPS 场景；宪章 VIII）。
// 私钥 AES-256-GCM 加密入库；任何视图仅回指纹/掩码，永不回显材料本身。
package certsvc

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"reflect"
	"strings"
	"time"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/cryptox"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	certs   *pgstore.CertRepo
	domains *pgstore.DomainRepo
	cipher  *cryptox.Cipher
	audit   *auditrec.Recorder
}

func New(certs *pgstore.CertRepo, domains *pgstore.DomainRepo, cipher *cryptox.Cipher, audit *auditrec.Recorder) *Service {
	return &Service{certs: certs, domains: domains, cipher: cipher, audit: audit}
}

type ImportInput struct {
	DomainID        string `json:"domain_id"`
	CertPEM         string `json:"cert_pem"`
	KeyPEM          string `json:"key_pem"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
}

// Import 验证 PEM→x509、私钥匹配、域名覆盖 → 加密入库 → 域名指向 imported 证书。
func (s *Service) Import(ctx context.Context, in ImportInput, actorID string) (*domain.Certificate, *httperr.APIError) {
	d, err := s.domains.Get(ctx, in.DomainID)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, httperr.NotFound("域名")
		}
		return nil, httperr.Internal(err)
	}
	certBlock, rest := pem.Decode([]byte(in.CertPEM))
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, httperr.ValidationFailed("证书 PEM 无法解析", httperr.Detail{Field: "cert_pem", Hint: "需 -----BEGIN CERTIFICATE----- 且为首段（中间链随后）"})
	}
	_ = rest
	x5c, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, httperr.ValidationFailed("证书内容非法: "+err.Error(), httperr.Detail{Field: "cert_pem"})
	}
	keyBlock, _ := pem.Decode([]byte(in.KeyPEM))
	if keyBlock == nil {
		return nil, httperr.ValidationFailed("私钥 PEM 无法解析", httperr.Detail{Field: "key_pem"})
	}
	key, err := parseAnyKey(keyBlock.Bytes)
	if err != nil {
		return nil, httperr.ValidationFailed("私钥不支持或损坏", httperr.Detail{Field: "key_pem", Hint: "支持 PKCS8 / PKCS1(RSA) / EC 私钥"})
	}
	if !publicKeysMatch(x5c.PublicKey, key) {
		return nil, httperr.ValidationFailed("私钥与证书公钥不匹配", httperr.Detail{Field: "key_pem"})
	}
	if !coversDomain(x5c, strings.TrimPrefix(d.Name, "*.")) {
		return nil, httperr.ValidationFailed("证书 CN/SAN 不覆盖域名 "+d.Name, httperr.Detail{Field: "cert_pem", Hint: "泛域名需 *. 或对应子域 SAN"})
	}
	enc, err := s.cipher.Encrypt([]byte(in.KeyPEM))
	if err != nil {
		return nil, httperr.Internal(err)
	}
	c := &domain.Certificate{
		DomainID: d.ID, Source: "imported",
		NotBefore: &x5c.NotBefore, NotAfter: &x5c.NotAfter, Issuer: x5c.Issuer.CommonName,
		Sans:                append([]string{x5c.Subject.CommonName}, x5c.DNSNames...),
		Status:              certStatus(x5c.NotAfter, d.ExpiryWarnDays),
		PrivateKeyEncrypted: enc, CertPEM: in.CertPEM,
	}
	if err := s.certs.Create(ctx, c); err != nil {
		return nil, httperr.Internal(err)
	}
	// 域名切至 imported 策略并挂接证书（不改策略来源即 cert_policy_change 场景，见 T065）
	if d.HTTPSPolicy != domain.PolicyImported || d.ImportedCertID == nil || *d.ImportedCertID != c.ID {
		apiErr := s.switchPolicy(ctx, d, domain.PolicyImported, &c.ID, actorID)
		if apiErr != nil {
			return nil, apiErr
		}
	}
	s.audit.Record(ctx, nil, auditrec.Event{ActorID: actorID, Action: "import",
		ResourceType: "certificate", ResourceID: c.ID, ResourceName: d.Name,
		After: map[string]any{"domain_id": d.ID, "not_after": c.NotAfter, "fingerprint": cryptox.Fingerprint(certBlock.Bytes), "key": "****"}})
	return c, nil
}

// switchPolicy 更新域名 HTTPS 策略（乐观锁）。
func (s *Service) switchPolicy(ctx context.Context, d *domain.Domain, policy domain.HTTPSPolicy, certID *string, actorID string) *httperr.APIError {
	fields := map[string]any{"https_policy": policy, "imported_cert_id": certID, "updated_by": actorID}
	if policy != domain.PolicyAcmeDNS {
		fields["dns_credential_id"] = nil
	}
	if err := s.domains.Update(ctx, d, d.RowVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return httperr.ConcurrentEdit(d.RowVersion)
		}
		return httperr.Internal(err)
	}
	return nil
}

// publicKeysMatch 私钥与证书公钥匹配判定（crypto.PrivateKey 均实现 Public()）。
func publicKeysMatch(pub any, priv any) bool {
	if p, ok := priv.(interface{ Public() crypto.PublicKey }); ok {
		return reflect.DeepEqual(pub, p.Public())
	}
	return false
}

func parseAnyKey(der []byte) (any, error) {
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(der); err == nil {
		return k, nil
	}
	return nil, errors.New("unknown key format")
}

// coversDomain 证书是否覆盖 host（含 *. 通配 SAN）。
func coversDomain(c *x509.Certificate, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	match := func(p string) bool {
		p = strings.ToLower(strings.TrimSuffix(p, "."))
		if p == host {
			return true
		}
		if strings.HasPrefix(p, "*.") {
			suffix := p[1:] // ".example.com"
			i := strings.Index(host, ".")
			return i >= 0 && host[i:] == suffix
		}
		return false
	}
	if match(c.Subject.CommonName) {
		return true
	}
	for _, s := range c.DNSNames {
		if match(s) {
			return true
		}
	}
	return false
}

// certStatus 有效期判定（certwatch/导入共用，阈值=域名 expiry_warn_days）。
func certStatus(notAfter time.Time, warnDays int) string {
	now := time.Now()
	switch {
	case notAfter.Before(now):
		return "expired"
	case notAfter.Before(now.AddDate(0, 0, max(warnDays, 1))):
		return "expiring_soon"
	default:
		return "valid"
	}
}

// StatusOf 导出供域名详情证书状态卡（T068）。
func StatusOf(c *domain.Certificate, warnDays int) string {
	if c == nil || c.NotAfter == nil {
		return "missing"
	}
	return certStatus(*c.NotAfter, warnDays)
}
