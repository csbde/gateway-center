// Package certwatch 证书到期观测（T067，FR-008/033）。
// 宪法 VII/R14：绝不读 acme.json；仅通过 TLS 握手观测网关实际下发的叶子证书 NotAfter，
// 或对导入证书直接解析存储 PEM。观测结果 upsert 至 certificates 表，供 Dashboard 预警与域名证书视图。
package certwatch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

// Settings 证书观测参数（platform_settings 快照）。
type Settings interface {
	WarnDays() int
}

// SettingsAdapter 把 settingsvc 快照适配为 certwatch.Settings。
type SettingsAdapter struct {
	Snap func() *domain.PlatformSettings
}

func (a SettingsAdapter) WarnDays() int {
	if d := a.Snap().ExpiryWarnDays; d > 0 {
		return d
	}
	return 30
}

// Prober 返回域名实际下发证书的叶子（TLS 握手观测；可注入替换）。
type Prober func(ctx context.Context, host string) (*x509.Certificate, error)

type Service struct {
	certs    *pgstore.CertRepo
	doms     *pgstore.DomainRepo
	settings Settings
	prober   Prober
	mu       sync.Mutex // 同域名观测不重叠
	running  map[string]bool
}

func New(certs *pgstore.CertRepo, doms *pgstore.DomainRepo, settings Settings, prober Prober) *Service {
	if prober == nil {
		prober = defaultProber
	}
	return &Service{certs: certs, doms: doms, settings: settings, prober: prober, running: map[string]bool{}}
}

// RunOnce 观测全部启用且 https_policy != off 的域名（并发上限由 scheduler sem 保证）。
func (s *Service) RunOnce(ctx context.Context) {
	// PageSize=0 即全量（listScanned 仅在 PageSize>0 时 Limit）
	domains, _, err := s.doms.List(ctx, pgstore.ListQuery{Page: 1, PageSize: 0})
	if err != nil {
		slog.Error("certwatch 枚举域名失败", "err", err)
		return
	}
	var wg sync.WaitGroup
	for i := range domains {
		d := &domains[i]
		if !d.Enabled || d.HTTPSPolicy == domain.PolicyOff {
			continue
		}
		s.mu.Lock()
		if s.running[d.ID] {
			s.mu.Unlock()
			continue
		}
		s.running[d.ID] = true
		s.mu.Unlock()
		wg.Add(1)
		go func(d *domain.Domain) {
			defer wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.running, d.ID)
				s.mu.Unlock()
			}()
			s.observe(ctx, d)
		}(d)
	}
	wg.Wait()
}

func (s *Service) observe(ctx context.Context, d *domain.Domain) {
	var leaf *x509.Certificate
	source := "acme"
	switch d.HTTPSPolicy {
	case domain.PolicyImported:
		existing, err := s.certs.ByDomain(ctx, d.ID)
		if err != nil || existing.Source != "imported" {
			return // 无导入证书或非导入来源，跳过（不覆盖）
		}
		leaf, err = parseLeaf(existing.CertPEM)
		if err != nil {
			slog.Warn("certwatch 解析导入证书 PEM 失败", "domain", d.Name, "err", err)
			return
		}
		source = "imported"
	case domain.PolicyAcmeHTTP, domain.PolicyAcmeDNS:
		host := strings.TrimPrefix(d.Name, "*.") // 泛域名取裸域 best-effort 观测
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		l, err := s.prober(c, host)
		if err != nil {
			// 域名不可达/无 TLS：不覆盖已有记录（ACME 证书缺失不构成发布阻断，仅影响预警）
			return
		}
		leaf = l
	default:
		return
	}
	if leaf == nil {
		return
	}
	now := time.Now().UTC()
	nb, na := leaf.NotBefore.UTC(), leaf.NotAfter.UTC()
	c := &domain.Certificate{
		NodeID:     &d.NodeID,
		DomainID:   &d.ID,
		Name:       d.Name,
		Source:     source,
		NotBefore:  &nb,
		NotAfter:   &na,
		Issuer:     issuerOf(leaf),
		Sans:       append([]string{}, leaf.DNSNames...),
		Status:     computeStatus(&na, s.settings.WarnDays(), now),
		ObservedAt: &now,
	}
	if err := s.certs.UpsertObserved(ctx, c); err != nil {
		slog.Error("certwatch upsert 失败", "domain", d.Name, "err", err)
	}
}

// defaultProber 生产实现：TLS 握手取叶子证书。
// InsecureSkipVerify=true：仅观测 NotAfter，不做信任决策（过期/自签也能观测到事实）。
func defaultProber(ctx context.Context, host string) (*x509.Certificate, error) {
	addr := net.JoinHostPort(host, "443")
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("拨号: %w", err)
	}
	conn := tls.Client(raw, &tls.Config{ServerName: host, InsecureSkipVerify: true})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("握手: %w", err)
	}
	defer conn.Close()
	cs := conn.ConnectionState()
	if len(cs.PeerCertificates) == 0 {
		return nil, fmt.Errorf("无证书")
	}
	return cs.PeerCertificates[0], nil
}

func parseLeaf(pemStr string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("PEM 无效或非 CERTIFICATE")
	}
	return x509.ParseCertificate(block.Bytes)
}

func issuerOf(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		return c.Issuer.CommonName
	}
	return c.Issuer.String()
}

// computeStatus 与 certsvc.certStatus 同逻辑（基础设施层不依赖 application，避免反向依赖）。
func computeStatus(notAfter *time.Time, warnDays int, now time.Time) string {
	if notAfter == nil {
		return "missing"
	}
	if warnDays < 1 {
		warnDays = 1
	}
	switch {
	case notAfter.Before(now):
		return "expired"
	case notAfter.Before(now.AddDate(0, 0, warnDays)):
		return "expiring_soon"
	default:
		return "valid"
	}
}
