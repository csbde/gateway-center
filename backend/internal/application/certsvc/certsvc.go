// Package certsvc 证书管理服务（向 NPM 学习完善证书资产库；宪章 VII/VIII）。
// 支持独立证书、自有证书导入、实时 PEM/私钥解析与匹配校验、证书探测、到期倒计时与依赖安全删除保护。
// 私钥 AES-256-GCM 加密入库；任何视图仅回指纹/掩码，永不回显材料本身。
package certsvc

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
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
	certBlock, _ := pem.Decode([]byte(in.CertPEM))
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, httperr.ValidationFailed("证书 PEM 无法解析", httperr.Detail{Field: "cert_pem", Hint: "需 -----BEGIN CERTIFICATE----- 且为首段（中间链随后）"})
	}
	x5c, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, httperr.ValidationFailed("证书内容非法: "+err.Error(), httperr.Detail{Field: "cert_pem", Hint: "检查 PEM 编码与 X.509 格式"})
	}
	keyBlock, _ := pem.Decode([]byte(in.KeyPEM))
	if keyBlock == nil {
		return nil, httperr.ValidationFailed("私钥 PEM 无法解析", httperr.Detail{Field: "key_pem", Hint: "需 -----BEGIN PRIVATE KEY----- 等合法 PEM 头"})
	}
	key, err := parseAnyKey(keyBlock.Bytes)
	if err != nil {
		return nil, httperr.ValidationFailed("私钥不支持或损坏", httperr.Detail{Field: "key_pem", Hint: "支持 PKCS8 / PKCS1(RSA) / EC 私钥"})
	}
	if !publicKeysMatch(x5c.PublicKey, key) {
		return nil, httperr.ValidationFailed("私钥与证书公钥不匹配", httperr.Detail{Field: "key_pem", Hint: "确认私钥属于该证书对应的密钥对"})
	}
	if !coversDomain(x5c, strings.TrimPrefix(d.Name, "*.")) {
		return nil, httperr.ValidationFailed("证书 CN/SAN 不覆盖域名 "+d.Name, httperr.Detail{Field: "cert_pem", Hint: "泛域名需 *. 或对应子域 SAN"})
	}
	enc, err := s.cipher.Encrypt([]byte(in.KeyPEM))
	if err != nil {
		return nil, httperr.Internal(err)
	}
	c := &domain.Certificate{
		NodeID:              &d.NodeID,
		DomainID:            &d.ID,
		Name:                d.Name,
		Source:              "imported",
		NotBefore:           &x5c.NotBefore,
		NotAfter:            &x5c.NotAfter,
		Issuer:              x5c.Issuer.CommonName,
		Sans:                append([]string{x5c.Subject.CommonName}, x5c.DNSNames...),
		Status:              certStatus(x5c.NotAfter, d.ExpiryWarnDays),
		PrivateKeyEncrypted: enc,
		CertPEM:             in.CertPEM,
	}
	if err := s.certs.Create(ctx, c); err != nil {
		return nil, httperr.Internal(err)
	}
	// 域名切至 imported 策略并挂接证书
	if d.HTTPSPolicy != domain.PolicyImported || d.ImportedCertID == nil || *d.ImportedCertID != c.ID {
		apiErr := s.switchPolicy(ctx, d, domain.PolicyImported, &c.ID, actorID)
		if apiErr != nil {
			return nil, apiErr
		}
	}
	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, Action: "import",
		ResourceType: "certificate", ResourceID: c.ID, ResourceName: d.Name,
		After: map[string]any{"domain_id": d.ID, "not_after": c.NotAfter, "fingerprint": cryptox.Fingerprint(certBlock.Bytes), "key": "****"},
	})
	return c, nil
}

// ParseInput 前端即时解析与校验入参。
type ParseInput struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem,omitempty"`
}

// ParseResult 证书解析结果。
type ParseResult struct {
	Valid              bool       `json:"valid"`
	Subject            string     `json:"subject"`
	Issuer             string     `json:"issuer"`
	Sans               []string   `json:"sans"`
	NotBefore          *time.Time `json:"not_before"`
	NotAfter           *time.Time `json:"not_after"`
	DaysRemaining      int        `json:"days_remaining"`
	SignatureAlgorithm string     `json:"signature_algorithm"`
	KeyMatches         bool       `json:"key_matches"`
	ErrorMessage       string     `json:"error_message,omitempty"`
}

// Parse 对用户提交的 PEM 文本进行纯内存即时解析与校验（向 NPM 即时反馈学习）。
func (s *Service) Parse(in ParseInput) (*ParseResult, *httperr.APIError) {
	if strings.TrimSpace(in.CertPEM) == "" {
		return nil, httperr.ValidationFailed("证书 PEM 不能为空", httperr.Detail{Field: "cert_pem"})
	}
	certBlock, _ := pem.Decode([]byte(in.CertPEM))
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return &ParseResult{Valid: false, ErrorMessage: "证书 PEM 格式无效，必须以 -----BEGIN CERTIFICATE----- 开头"}, nil
	}
	x5c, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return &ParseResult{Valid: false, ErrorMessage: "证书解析失败: " + err.Error()}, nil
	}

	res := &ParseResult{
		Valid:              true,
		Subject:            x5c.Subject.CommonName,
		Issuer:             x5c.Issuer.CommonName,
		Sans:               append([]string{x5c.Subject.CommonName}, x5c.DNSNames...),
		NotBefore:          &x5c.NotBefore,
		NotAfter:           &x5c.NotAfter,
		DaysRemaining:      int(time.Until(x5c.NotAfter).Hours() / 24),
		SignatureAlgorithm: x5c.SignatureAlgorithm.String(),
	}
	if res.Issuer == "" {
		res.Issuer = x5c.Issuer.String()
	}

	if strings.TrimSpace(in.KeyPEM) != "" {
		keyBlock, _ := pem.Decode([]byte(in.KeyPEM))
		if keyBlock != nil {
			key, err := parseAnyKey(keyBlock.Bytes)
			if err == nil && publicKeysMatch(x5c.PublicKey, key) {
				res.KeyMatches = true
			} else {
				res.KeyMatches = false
				res.ErrorMessage = "私钥与证书公钥不匹配或私钥格式不支持"
			}
		} else {
			res.KeyMatches = false
			res.ErrorMessage = "私钥 PEM 格式无效"
		}
	}
	return res, nil
}

type CreateCustomInput struct {
	Name     string  `json:"name"`
	NodeID   *string `json:"node_id,omitempty"`
	DomainID *string `json:"domain_id,omitempty"`
	CertPEM  string  `json:"cert_pem"`
	KeyPEM   string  `json:"key_pem"`
}

// CreateCustom 支持从独立证书中心导入自有证书资产。
func (s *Service) CreateCustom(ctx context.Context, in CreateCustomInput, actorID string) (*domain.Certificate, *httperr.APIError) {
	if strings.TrimSpace(in.CertPEM) == "" {
		return nil, httperr.ValidationFailed("证书 PEM 必填", httperr.Detail{Field: "cert_pem"})
	}
	if strings.TrimSpace(in.KeyPEM) == "" {
		return nil, httperr.ValidationFailed("私钥 PEM 必填", httperr.Detail{Field: "key_pem"})
	}
	certBlock, _ := pem.Decode([]byte(in.CertPEM))
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, httperr.ValidationFailed("证书 PEM 无法解析", httperr.Detail{Field: "cert_pem", Hint: "需以 -----BEGIN CERTIFICATE----- 开头"})
	}
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
		return nil, httperr.ValidationFailed("私钥不支持或损坏: "+err.Error(), httperr.Detail{Field: "key_pem"})
	}
	if !publicKeysMatch(x5c.PublicKey, key) {
		return nil, httperr.ValidationFailed("私钥与证书公钥不匹配", httperr.Detail{Field: "key_pem"})
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		if x5c.Subject.CommonName != "" {
			name = x5c.Subject.CommonName
		} else if len(x5c.DNSNames) > 0 {
			name = x5c.DNSNames[0]
		} else {
			name = "Custom Certificate"
		}
	}

	enc, err := s.cipher.Encrypt([]byte(in.KeyPEM))
	if err != nil {
		return nil, httperr.Internal(err)
	}

	c := &domain.Certificate{
		NodeID:              in.NodeID,
		DomainID:            in.DomainID,
		Name:                name,
		Source:              "imported",
		NotBefore:           &x5c.NotBefore,
		NotAfter:            &x5c.NotAfter,
		Issuer:              x5c.Issuer.CommonName,
		Sans:                append([]string{x5c.Subject.CommonName}, x5c.DNSNames...),
		Status:              certStatus(x5c.NotAfter, 30),
		PrivateKeyEncrypted: enc,
		CertPEM:             in.CertPEM,
	}
	if c.Issuer == "" {
		c.Issuer = x5c.Issuer.String()
	}

	if err := s.certs.Create(ctx, c); err != nil {
		return nil, httperr.Internal(err)
	}

	// 若指定了关联域名，同时挂接
	if in.DomainID != nil && *in.DomainID != "" {
		if d, err := s.domains.Get(ctx, *in.DomainID); err == nil {
			_ = s.switchPolicy(ctx, d, domain.PolicyImported, &c.ID, actorID)
		}
	}

	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, Action: "create_certificate",
		ResourceType: "certificate", ResourceID: c.ID, ResourceName: name,
		After: map[string]any{"name": name, "not_after": c.NotAfter, "fingerprint": cryptox.Fingerprint(certBlock.Bytes)},
	})
	return c, nil
}

type DomainRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CertificateDetail struct {
	ID                string      `json:"id"`
	NodeID            *string     `json:"node_id,omitempty"`
	DomainID          *string     `json:"domain_id,omitempty"`
	Name              string      `json:"name"`
	Source            string      `json:"source"`
	Status            string      `json:"status"`
	NotBefore         *time.Time  `json:"not_before,omitempty"`
	NotAfter          *time.Time  `json:"not_after,omitempty"`
	DaysRemaining     int         `json:"days_remaining"`
	Issuer            string      `json:"issuer,omitempty"`
	Sans              []string    `json:"sans,omitempty"`
	CertPEM           string      `json:"cert_pem,omitempty"`
	ObservedAt        *time.Time  `json:"observed_at,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
	AssociatedDomains []DomainRef `json:"associated_domains,omitempty"`
}

// List 分页查询证书资产列表，附带计算剩余天数与绑定域名情况。
func (s *Service) List(ctx context.Context, q pgstore.ListQuery, status string) ([]CertificateDetail, int64, error) {
	certs, total, err := s.certs.List(ctx, q, status)
	if err != nil {
		return nil, 0, err
	}
	out := make([]CertificateDetail, 0, len(certs))
	for _, c := range certs {
		doms, _ := s.certs.FindReferencingDomains(ctx, c.ID)
		refs := make([]DomainRef, 0, len(doms))
		for _, d := range doms {
			refs = append(refs, DomainRef{ID: d.ID, Name: d.Name})
		}
		daysLeft := 0
		st := c.Status
		if c.NotAfter != nil {
			daysLeft = int(time.Until(*c.NotAfter).Hours() / 24)
			st = certStatus(*c.NotAfter, 30)
		}
		out = append(out, CertificateDetail{
			ID:                c.ID,
			NodeID:            c.NodeID,
			DomainID:          c.DomainID,
			Name:              c.Name,
			Source:            c.Source,
			Status:            st,
			NotBefore:         c.NotBefore,
			NotAfter:          c.NotAfter,
			DaysRemaining:     daysLeft,
			Issuer:            c.Issuer,
			Sans:              c.Sans,
			CertPEM:           c.CertPEM,
			ObservedAt:        c.ObservedAt,
			CreatedAt:         c.CreatedAt,
			UpdatedAt:         c.UpdatedAt,
			AssociatedDomains: refs,
		})
	}
	return out, total, nil
}

// Get 获取证书详情。
func (s *Service) Get(ctx context.Context, id string) (*CertificateDetail, *httperr.APIError) {
	c, err := s.certs.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, httperr.NotFound("证书")
		}
		return nil, httperr.Internal(err)
	}
	doms, _ := s.certs.FindReferencingDomains(ctx, c.ID)
	refs := make([]DomainRef, 0, len(doms))
	for _, d := range doms {
		refs = append(refs, DomainRef{ID: d.ID, Name: d.Name})
	}
	daysLeft := 0
	st := c.Status
	if c.NotAfter != nil {
		daysLeft = int(time.Until(*c.NotAfter).Hours() / 24)
		st = certStatus(*c.NotAfter, 30)
	}
	return &CertificateDetail{
		ID:                c.ID,
		NodeID:            c.NodeID,
		DomainID:          c.DomainID,
		Name:              c.Name,
		Source:            c.Source,
		Status:            st,
		NotBefore:         c.NotBefore,
		NotAfter:          c.NotAfter,
		DaysRemaining:     daysLeft,
		Issuer:            c.Issuer,
		Sans:              c.Sans,
		CertPEM:           c.CertPEM,
		ObservedAt:        c.ObservedAt,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
		AssociatedDomains: refs,
	}, nil
}

// Delete 安全删除证书：执行 Dependency Check，被引用则阻断（宪章 VI/X）。
func (s *Service) Delete(ctx context.Context, id string, actorID string) *httperr.APIError {
	c, err := s.certs.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return httperr.NotFound("证书")
		}
		return httperr.Internal(err)
	}
	doms, err := s.certs.FindReferencingDomains(ctx, id)
	if err != nil {
		return httperr.Internal(err)
	}
	if len(doms) > 0 {
		details := make([]httperr.Detail, 0, len(doms))
		for _, d := range doms {
			details = append(details, httperr.Detail{Field: "domain", Hint: d.Name})
		}
		return httperr.DependencyBlocked("证书「"+c.Name+"」", details)
	}

	if err := s.certs.Delete(ctx, id); err != nil {
		return httperr.Internal(err)
	}

	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, Action: "delete_certificate",
		ResourceType: "certificate", ResourceID: c.ID, ResourceName: c.Name,
		Before: map[string]any{"id": c.ID, "name": c.Name, "issuer": c.Issuer},
	})
	return nil
}

// Probe 对证书关联的主机进行即时 TLS 探测与观测更新。
func (s *Service) Probe(ctx context.Context, id string, actorID string) (*CertificateDetail, *httperr.APIError) {
	c, err := s.certs.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, httperr.NotFound("证书")
		}
		return nil, httperr.Internal(err)
	}

	// 优先以自定义证书内置 PEM 更新
	if c.Source == "imported" && c.CertPEM != "" {
		if block, _ := pem.Decode([]byte(c.CertPEM)); block != nil {
			if x5c, err := x509.ParseCertificate(block.Bytes); err == nil {
				now := time.Now().UTC()
				c.NotBefore = &x5c.NotBefore
				c.NotAfter = &x5c.NotAfter
				c.Issuer = x5c.Issuer.CommonName
				c.Sans = append([]string{x5c.Subject.CommonName}, x5c.DNSNames...)
				c.Status = certStatus(x5c.NotAfter, 30)
				c.ObservedAt = &now
				_ = s.certs.UpsertObserved(ctx, c)
			}
		}
	} else if len(c.Sans) > 0 {
		// 对首个公网 SAN 进行 TLS 握手探测
		host := c.Sans[0]
		if strings.HasPrefix(host, "*.") {
			host = "www." + strings.TrimPrefix(host, "*.")
		}
		leaf, err := probeTLS(ctx, host)
		if err == nil && leaf != nil {
			now := time.Now().UTC()
			c.NotBefore = &leaf.NotBefore
			c.NotAfter = &leaf.NotAfter
			c.Issuer = leaf.Issuer.CommonName
			c.Sans = append([]string{leaf.Subject.CommonName}, leaf.DNSNames...)
			c.Status = certStatus(leaf.NotAfter, 30)
			c.ObservedAt = &now
			_ = s.certs.UpsertObserved(ctx, c)
		}
	}
	return s.Get(ctx, id)
}

func probeTLS(ctx context.Context, host string) (*x509.Certificate, error) {
	addr := net.JoinHostPort(host, "443")
	d := &net.Dialer{Timeout: 3 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, &tls.Config{ServerName: host, InsecureSkipVerify: true})
	defer conn.Close()
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(pctx); err != nil {
		return nil, err
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("no certs found")
	}
	return certs[0], nil
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

// CertView 证书只读观测（FR-033；私钥/PEM 永不出，宪章 VIII）。
type CertView struct {
	Source     string     `json:"source"` // acme/imported/none
	Status     string     `json:"status"` // valid/expiring_soon/expired/missing/unknown
	NotBefore  *time.Time `json:"not_before,omitempty"`
	NotAfter   *time.Time `json:"not_after,omitempty"`
	Issuer     string     `json:"issuer,omitempty"`
	Sans       []string   `json:"sans,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

// View 供 GET /domains/{id}/certificate（T068）：域名无证书→source=none/status=missing；
// 有证书则按域名 expiry_warn_days 现算 status（不读 DB 陈旧值）。
func (s *Service) View(ctx context.Context, domainID string) (*CertView, *httperr.APIError) {
	d, err := s.domains.Get(ctx, domainID)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, httperr.NotFound("域名")
		}
		return nil, httperr.Internal(err)
	}
	warnDays := d.ExpiryWarnDays
	if warnDays <= 0 {
		warnDays = 30
	}
	c, err := s.certs.ByDomain(ctx, domainID)
	if errors.Is(err, pgstore.ErrNotFound) {
		return &CertView{Source: "none", Status: "missing"}, nil
	}
	if err != nil {
		return nil, httperr.Internal(err)
	}
	return &CertView{
		Source: c.Source, Status: StatusOf(c, warnDays),
		NotBefore: c.NotBefore, NotAfter: c.NotAfter,
		Issuer: c.Issuer, Sans: c.Sans, ObservedAt: c.ObservedAt,
	}, nil
}
