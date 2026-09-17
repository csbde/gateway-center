// Package proxyhostsvc 网站代理（Proxy Hosts）编排服务。
// 向 Nginx Proxy Manager (NPM) 学习，以向导式统一模型聚合 Domain、Service、Target、Route、Middleware 与 Certificate。
// 完全符合宪法 I（底层持久化业务实体）与宪法 II（声明式分层：用户表达域名→目标，系统转换为底层资源）。
package proxyhostsvc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/certsvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gorm.io/gorm"
)

type Service struct {
	db       *gorm.DB
	domains  *pgstore.DomainRepo
	services *pgstore.ServiceRepo
	targets  *pgstore.TargetRepo
	routes   *pgstore.RouteRepo
	mws      *pgstore.MiddlewareRepo
	certs    *pgstore.CertRepo
	certSvc  *certsvc.Service
	audit    *auditrec.Recorder
}

func New(
	db *gorm.DB,
	domains *pgstore.DomainRepo,
	services *pgstore.ServiceRepo,
	targets *pgstore.TargetRepo,
	routes *pgstore.RouteRepo,
	mws *pgstore.MiddlewareRepo,
	certs *pgstore.CertRepo,
	certSvc *certsvc.Service,
	audit *auditrec.Recorder,
) *Service {
	return &Service{
		db:       db,
		domains:  domains,
		services: services,
		targets:  targets,
		routes:   routes,
		mws:      mws,
		certs:    certs,
		certSvc:  certSvc,
		audit:    audit,
	}
}

// ProxyHostView 网站代理聚合视图（与 NPM 界面对齐）。
type ProxyHostView struct {
	ID                  string    `json:"id"` // 对应主 Route ID
	NodeID              string    `json:"node_id"`
	Name                string    `json:"name"`
	DomainID            string    `json:"domain_id"`
	DomainNames         []string  `json:"domain_names"`
	ForwardScheme       string    `json:"forward_scheme"`
	ForwardHost         string    `json:"forward_host"`
	ForwardPort         int       `json:"forward_port"`
	ServiceID           string    `json:"service_id"`
	ServiceName         string    `json:"service_name"`
	TargetURL           string    `json:"target_url"`
	Websockets          bool      `json:"websockets"`
	BlockCommonExploits bool      `json:"block_common_exploits"`
	HSTS                bool      `json:"hsts"`
	ForceSSL            bool      `json:"force_ssl"`
	SSLPolicy           string    `json:"ssl_policy"` // off, acme_http, acme_dns, imported
	CertificateID       *string   `json:"certificate_id,omitempty"`
	CertificateName     string    `json:"certificate_name,omitempty"`
	CertStatus          string    `json:"cert_status,omitempty"`
	CertDaysLeft        int       `json:"cert_days_left,omitempty"`
	Status              string    `json:"status"` // draft, enabled, disabled, archived
	Enabled             bool      `json:"enabled"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type CreateProxyHostInput struct {
	NodeID              string   `json:"node_id"`
	DomainNames         []string `json:"domain_names"` // 域名清单
	ForwardScheme       string   `json:"forward_scheme"` // http / https
	ForwardHost         string   `json:"forward_host"`   // 目标 IP 或域名
	ForwardPort         int      `json:"forward_port"`   // 目标端口
	ServiceID           string   `json:"service_id,omitempty"` // 或选用现有服务
	Websockets          bool     `json:"websockets"`
	BlockCommonExploits bool     `json:"block_common_exploits"`
	HSTS                bool     `json:"hsts"`
	ForceSSL            bool     `json:"force_ssl"`
	SSLMode             string   `json:"ssl_mode"` // none, letsencrypt_http, letsencrypt_dns, custom, upload
	CertificateID       string   `json:"certificate_id,omitempty"`
	CertResolver        string   `json:"cert_resolver,omitempty"`
	DNSCredentialID     string   `json:"dns_credential_id,omitempty"`
	CertPEM             string   `json:"cert_pem,omitempty"`
	KeyPEM              string   `json:"key_pem,omitempty"`
	AdvancedRule        string   `json:"advanced_rule,omitempty"`
}

type UpdateProxyHostInput struct {
	DomainNames         []string `json:"domain_names"`
	ForwardScheme       string   `json:"forward_scheme"`
	ForwardHost         string   `json:"forward_host"`
	ForwardPort         int      `json:"forward_port"`
	ServiceID           string   `json:"service_id,omitempty"`
	Websockets          bool     `json:"websockets"`
	BlockCommonExploits bool     `json:"block_common_exploits"`
	HSTS                bool     `json:"hsts"`
	ForceSSL            bool     `json:"force_ssl"`
	SSLMode             string   `json:"ssl_mode"`
	CertificateID       string   `json:"certificate_id,omitempty"`
	CertResolver        string   `json:"cert_resolver,omitempty"`
	DNSCredentialID     string   `json:"dns_credential_id,omitempty"`
	CertPEM             string   `json:"cert_pem,omitempty"`
	KeyPEM              string   `json:"key_pem,omitempty"`
}

var validDomainRegex = regexp.MustCompile(`^(\*\.)?([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

func (s *Service) List(ctx context.Context, q pgstore.ListQuery) ([]ProxyHostView, int64, error) {
	// 查询带有 Domain 关联的简单路由
	routes, total, err := s.routes.List(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	var out []ProxyHostView
	for _, rt := range routes {
		if rt.DomainID == nil || rt.Mode != "simple" {
			continue
		}
		view, err := s.assembleView(ctx, &rt)
		if err == nil && view != nil {
			out = append(out, *view)
		}
	}
	return out, total, nil
}

func (s *Service) Get(ctx context.Context, id string) (*ProxyHostView, *httperr.APIError) {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, httperr.NotFound("网站代理")
		}
		return nil, httperr.Internal(err)
	}
	view, err := s.assembleView(ctx, rt)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	return view, nil
}

func (s *Service) Create(ctx context.Context, in CreateProxyHostInput, actorID string) (*ProxyHostView, *httperr.APIError) {
	if strings.TrimSpace(in.NodeID) == "" {
		return nil, httperr.ValidationFailed("网关节点必填", httperr.Detail{Field: "node_id"})
	}
	if len(in.DomainNames) == 0 {
		return nil, httperr.ValidationFailed("至少输入一个域名", httperr.Detail{Field: "domain_names"})
	}
	for i, d := range in.DomainNames {
		d = strings.TrimSpace(d)
		if !validDomainRegex.MatchString(d) {
			return nil, httperr.ValidationFailed(fmt.Sprintf("域名「%s」格式非法", d), httperr.Detail{Field: "domain_names", Hint: "格式如 blog.example.com 或 *.example.com"})
		}
		in.DomainNames[i] = d
	}
	primaryDomain := in.DomainNames[0]

	if in.ForwardScheme == "" {
		in.ForwardScheme = "http"
	}
	if in.ServiceID == "" {
		if strings.TrimSpace(in.ForwardHost) == "" {
			return nil, httperr.ValidationFailed("转发目标主机必填", httperr.Detail{Field: "forward_host"})
		}
		if in.ForwardPort <= 0 || in.ForwardPort > 65535 {
			return nil, httperr.ValidationFailed("转发端口必须在 1-65535 之间", httperr.Detail{Field: "forward_port"})
		}
	}

	// 1. 若现场上传自有证书，先落库证书
	if in.SSLMode == "upload" {
		if in.CertPEM == "" || in.KeyPEM == "" {
			return nil, httperr.ValidationFailed("证书公钥与私钥均不能为空", httperr.Detail{Field: "cert_pem"})
		}
		c, apiErr := s.certSvc.CreateCustom(ctx, certsvc.CreateCustomInput{
			Name:    primaryDomain + " SSL",
			NodeID:  &in.NodeID,
			CertPEM: in.CertPEM,
			KeyPEM:  in.KeyPEM,
		}, actorID)
		if apiErr != nil {
			return nil, apiErr
		}
		in.CertificateID = c.ID
		in.SSLMode = "custom"
	}

	// 2. 检查或创建域名
	domainPolicy := domain.PolicyOff
	var importedCertID *string
	var dnsCredID *string
	resolver := in.CertResolver

	switch in.SSLMode {
	case "letsencrypt_http":
		domainPolicy = domain.PolicyAcmeHTTP
	case "letsencrypt_dns":
		domainPolicy = domain.PolicyAcmeDNS
		if in.DNSCredentialID != "" {
			dnsCredID = &in.DNSCredentialID
		}
	case "custom":
		domainPolicy = domain.PolicyImported
		if in.CertificateID != "" {
			importedCertID = &in.CertificateID
		}
	}

	dom, err := s.findOrCreateDomain(ctx, in.NodeID, primaryDomain, domainPolicy, resolver, dnsCredID, importedCertID, actorID)
	if err != nil {
		return nil, httperr.Internal(err)
	}

	// 3. 检查或创建后端 Service 及 Target
	svcID := in.ServiceID
	targetURL := ""
	if svcID == "" {
		targetURL = fmt.Sprintf("%s://%s:%d", in.ForwardScheme, in.ForwardHost, in.ForwardPort)
		svcName := makeSlug("svc-" + primaryDomain)
		svc := &domain.Service{
			NodeID:  in.NodeID,
			Name:    svcName,
			Enabled: true,
		}
		svc.SetActor(actorID)
		if err := s.services.Create(ctx, svc); err != nil {
			// 若重名则加时间戳
			svc.Name = fmt.Sprintf("%s-%d", svcName, time.Now().Unix()%10000)
			if err := s.services.Create(ctx, svc); err != nil {
				return nil, httperr.Internal(err)
			}
		}
		svcID = svc.ID

		tgt := &domain.Target{
			ServiceID: svc.ID,
			URL:       targetURL,
			Weight:    1,
			Enabled:   true,
		}
		tgt.SetActor(actorID)
		if err := s.targets.Create(ctx, tgt); err != nil {
			return nil, httperr.Internal(err)
		}
	}

	// 4. 处理安全防护中间件（Block Common Exploits / HSTS）
	var mwIDs []string
	if in.BlockCommonExploits || in.HSTS {
		mwID, err := s.findOrCreateSecHeaders(ctx, in.NodeID, primaryDomain, in.HSTS, actorID)
		if err == nil && mwID != "" {
			mwIDs = append(mwIDs, mwID)
		}
	}

	// 5. 创建主路由
	routeName := makeSlug(primaryDomain)
	if taken, _ := s.routes.NameTaken(ctx, in.NodeID, routeName); taken {
		routeName = fmt.Sprintf("%s-%d", routeName, time.Now().Unix()%10000)
	}
	rt := &domain.Route{
		NodeID:    in.NodeID,
		Name:      routeName,
		Mode:      "simple",
		DomainID:  &dom.ID,
		Path:      "/",
		MatchType: "prefix",
		ServiceID: svcID,
		HTTPS:     in.SSLMode != "none" && in.SSLMode != "",
		Status:    "enabled",
	}
	if in.AdvancedRule != "" {
		rt.Mode = "advanced"
		rt.AdvancedRule = in.AdvancedRule
	}
	rt.SetActor(actorID)
	if err := s.routes.Create(ctx, rt); err != nil {
		return nil, httperr.Internal(err)
	}
	if len(mwIDs) > 0 {
		_ = s.routes.ReplaceMiddlewares(ctx, rt.ID, mwIDs)
	}

	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, Action: "create_proxy_host",
		ResourceType: "proxy_host", ResourceID: rt.ID, ResourceName: primaryDomain,
		After: map[string]any{
			"domain": primaryDomain, "target": targetURL, "ssl_mode": in.SSLMode,
			"force_ssl": in.ForceSSL, "websockets": in.Websockets,
		},
	})

	view, err := s.assembleView(ctx, rt)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	return view, nil
}

func (s *Service) Update(ctx context.Context, id string, in UpdateProxyHostInput, actorID string) (*ProxyHostView, *httperr.APIError) {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, httperr.NotFound("网站代理")
		}
		return nil, httperr.Internal(err)
	}

	if len(in.DomainNames) > 0 {
		primaryDomain := strings.TrimSpace(in.DomainNames[0])
		if rt.DomainID != nil {
			dom, err := s.domains.Get(ctx, *rt.DomainID)
			if err == nil && dom != nil {
				fields := map[string]any{}
				switch in.SSLMode {
				case "none":
					fields["https_policy"] = domain.PolicyOff
				case "letsencrypt_http":
					fields["https_policy"] = domain.PolicyAcmeHTTP
				case "letsencrypt_dns":
					fields["https_policy"] = domain.PolicyAcmeDNS
					fields["cert_resolver_ref"] = in.CertResolver
					if in.DNSCredentialID != "" {
						fields["dns_credential_id"] = in.DNSCredentialID
					}
				case "custom":
					fields["https_policy"] = domain.PolicyImported
					if in.CertificateID != "" {
						fields["imported_cert_id"] = in.CertificateID
					}
				case "upload":
					if in.CertPEM != "" && in.KeyPEM != "" {
						c, apiErr := s.certSvc.CreateCustom(ctx, certsvc.CreateCustomInput{
							Name:    primaryDomain + " SSL",
							NodeID:  &rt.NodeID,
							CertPEM: in.CertPEM,
							KeyPEM:  in.KeyPEM,
						}, actorID)
						if apiErr == nil {
							fields["https_policy"] = domain.PolicyImported
							fields["imported_cert_id"] = c.ID
						}
					}
				}
				if len(fields) > 0 {
					_ = s.domains.Update(ctx, dom, dom.RowVersion, fields)
				}
			}
		}
	}

	// 更新 Target URL
	if in.ForwardHost != "" && in.ForwardPort > 0 {
		scheme := in.ForwardScheme
		if scheme == "" {
			scheme = "http"
		}
		targetURL := fmt.Sprintf("%s://%s:%d", scheme, in.ForwardHost, in.ForwardPort)
		tgts, _ := s.targets.ListByService(ctx, rt.ServiceID)
		if len(tgts) > 0 {
			_ = s.targets.Update(ctx, &tgts[0], tgts[0].RowVersion, map[string]any{
				"url": targetURL, "updated_by": actorID,
			})
		}
	}

	// 更新 Route
	routeFields := map[string]any{
		"https":      in.SSLMode != "none" && in.SSLMode != "",
		"updated_by": actorID,
	}
	var mwIDs []string
	if in.BlockCommonExploits || in.HSTS {
		domName := "site"
		if len(in.DomainNames) > 0 {
			domName = in.DomainNames[0]
		}
		mwID, err := s.findOrCreateSecHeaders(ctx, rt.NodeID, domName, in.HSTS, actorID)
		if err == nil && mwID != "" {
			mwIDs = append(mwIDs, mwID)
		}
	}
	if err := s.routes.Update(ctx, rt, rt.RowVersion, routeFields); err != nil {
		return nil, httperr.Internal(err)
	}
	if len(mwIDs) > 0 {
		_ = s.routes.ReplaceMiddlewares(ctx, rt.ID, mwIDs)
	}

	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, Action: "update_proxy_host",
		ResourceType: "proxy_host", ResourceID: rt.ID, ResourceName: rt.Name,
		After: map[string]any{"ssl_mode": in.SSLMode, "force_ssl": in.ForceSSL},
	})

	view, err := s.assembleView(ctx, rt)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	return view, nil
}

func (s *Service) Delete(ctx context.Context, id string, actorID string) *httperr.APIError {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return httperr.NotFound("网站代理")
		}
		return httperr.Internal(err)
	}
	if err := s.routes.SoftDelete(ctx, id); err != nil {
		return httperr.Internal(err)
	}

	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, Action: "delete_proxy_host",
		ResourceType: "proxy_host", ResourceID: rt.ID, ResourceName: rt.Name,
	})
	return nil
}

func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool, actorID string) (*ProxyHostView, *httperr.APIError) {
	rt, err := s.routes.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			return nil, httperr.NotFound("网站代理")
		}
		return nil, httperr.Internal(err)
	}
	st := "enabled"
	if !enabled {
		st = "disabled"
	}
	if err := s.routes.Update(ctx, rt, rt.RowVersion, map[string]any{"status": st, "updated_by": actorID}); err != nil {
		return nil, httperr.Internal(err)
	}
	rt.Status = st
	view, err := s.assembleView(ctx, rt)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	return view, nil
}

func (s *Service) assembleView(ctx context.Context, rt *domain.Route) (*ProxyHostView, error) {
	view := &ProxyHostView{
		ID:        rt.ID,
		NodeID:    rt.NodeID,
		Name:      rt.Name,
		Status:    rt.Status,
		Enabled:   rt.Status == "enabled",
		ForceSSL:  rt.HTTPS,
		CreatedAt: rt.CreatedAt,
		UpdatedAt: rt.UpdatedAt,
	}

	// 组装域名
	if rt.DomainID != nil {
		view.DomainID = *rt.DomainID
		d, err := s.domains.Get(ctx, *rt.DomainID)
		if err == nil && d != nil {
			view.DomainNames = []string{d.Name}
			view.SSLPolicy = string(d.HTTPSPolicy)
			if d.ImportedCertID != nil {
				view.CertificateID = d.ImportedCertID
				if c, err := s.certs.Get(ctx, *d.ImportedCertID); err == nil && c != nil {
					view.CertificateName = c.Name
					view.CertStatus = c.Status
					if c.NotAfter != nil {
						view.CertDaysLeft = int(time.Until(*c.NotAfter).Hours() / 24)
					}
				}
			}
		}
	}

	// 组装上游服务与目标
	if rt.ServiceID != "" {
		view.ServiceID = rt.ServiceID
		svc, err := s.services.Get(ctx, rt.ServiceID)
		if err == nil && svc != nil {
			view.ServiceName = svc.Name
			tgts, _ := s.targets.ListByService(ctx, svc.ID)
			if len(tgts) > 0 {
				view.TargetURL = tgts[0].URL
				u, err := url.Parse(tgts[0].URL)
				if err == nil {
					view.ForwardScheme = u.Scheme
					view.ForwardHost = u.Hostname()
					if p, err := strconv.Atoi(u.Port()); err == nil {
						view.ForwardPort = p
					} else if u.Scheme == "https" {
						view.ForwardPort = 443
					} else {
						view.ForwardPort = 80
					}
				}
			}
		}
	}

	// 组装中间件特性
	for _, m := range rt.Middlewares {
		mw, err := s.mws.Get(ctx, m.MiddlewareID)
		if err == nil && mw != nil {
			if mw.Type == "security_headers" {
				view.BlockCommonExploits = true
				if sts, ok := mw.Params["sts_seconds"].(float64); ok && sts > 0 {
					view.HSTS = true
				}
			}
		}
	}

	// Traefik 默认原生透明支持 Websockets
	view.Websockets = true

	return view, nil
}

func (s *Service) findOrCreateDomain(
	ctx context.Context,
	nodeID, name string,
	policy domain.HTTPSPolicy,
	resolver string,
	dnsCredID, importedCertID *string,
	actorID string,
) (*domain.Domain, error) {
	var dom domain.Domain
	err := s.db.WithContext(ctx).Where("node_id = ? AND name = ? AND deleted_at IS NULL", nodeID, name).First(&dom).Error
	if err == nil {
		// 存在则更新策略
		fields := map[string]any{
			"https_policy":      policy,
			"cert_resolver_ref": resolver,
			"dns_credential_id": dnsCredID,
			"imported_cert_id":  importedCertID,
			"updated_by":        actorID,
		}
		_ = s.domains.Update(ctx, &dom, dom.RowVersion, fields)
		return &dom, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	dom = domain.Domain{
		NodeID:           nodeID,
		Name:             name,
		HTTPSPolicy:      policy,
		CertResolverRef:  resolver,
		DNSSCredentialID: dnsCredID,
		ImportedCertID:   importedCertID,
		ExpiryWarnDays:   30,
		Enabled:          true,
	}
	dom.SetActor(actorID)
	if err := s.domains.Create(ctx, &dom); err != nil {
		return nil, err
	}
	return &dom, nil
}

func (s *Service) findOrCreateSecHeaders(
	ctx context.Context,
	nodeID, domainName string,
	hsts bool,
	actorID string,
) (string, error) {
	params := map[string]any{
		"content_type_nosniff": true,
		"browser_xss_filter":   true,
		"frame_options":        "deny",
	}
	if hsts {
		params["sts_seconds"] = 31536000
		params["sts_include_subdomains"] = true
	}
	mwName := makeSlug("sec-" + domainName)
	mw := &domain.Middleware{
		NodeID:  nodeID,
		Name:    mwName,
		Type:    "security_headers",
		Params:  params,
		Enabled: true,
	}
	mw.SetActor(actorID)
	if err := s.mws.Create(ctx, mw); err != nil {
		// 若已存在，获取已有的
		var existing domain.Middleware
		if err := s.db.WithContext(ctx).Where("node_id = ? AND name = ? AND deleted_at IS NULL", nodeID, mwName).First(&existing).Error; err == nil {
			return existing.ID, nil
		}
		return "", err
	}
	return mw.ID, nil
}

func makeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "*.", "wildcard-")
	s = strings.ReplaceAll(s, ".", "-")
	reg := regexp.MustCompile(`[^a-z0-9-]+`)
	s = reg.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
