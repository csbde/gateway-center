// Package domainsvc 域名用例（T033，AC-002；策略一致性 FR-007/034）。
// 泛域名 + acme_http 拒绝（宪章 VII）；策略必填项齐备才可保存（Edge Case：策略与实际不符→后续发布阻断）。
package domainsvc

import (
	"context"
	"errors"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/depcheck"
	"gateway-center/backend/internal/domain/validate"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	domains *pgstore.DomainRepo
	nodes   *pgstore.NodeRepo
	audit   *auditrec.Recorder
}

func New(domains *pgstore.DomainRepo, nodes *pgstore.NodeRepo, audit *auditrec.Recorder) *Service {
	return &Service{domains: domains, nodes: nodes, audit: audit}
}

type Input struct {
	NodeID           string `json:"node_id"`
	Name             string `json:"name"`
	HTTPSPolicy      string `json:"https_policy"` // off/acme_http/acme_dns/imported
	CertResolverRef  string `json:"cert_resolver_ref,omitempty"`
	DNSSCredentialID string `json:"dns_credential_id,omitempty"`
	ImportedCertID   string `json:"imported_cert_id,omitempty"`
	ExpiryWarnDays   int    `json:"expiry_warn_days"`
	Enabled          *bool  `json:"enabled,omitempty"`
	ExpectedVersion  int64  `json:"expected_version,omitempty"`
}

func (s *Service) List(ctx context.Context, q pgstore.ListQuery) ([]domain.Domain, int64, error) {
	return s.domains.List(ctx, q)
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Domain, error) {
	return s.domains.Get(ctx, id)
}

func policyErr(msg, field, hint string) *httperr.APIError {
	return httperr.ValidationFailed(msg, httperr.Detail{Field: field, Hint: hint})
}

// validateInput 保存时的策略一致性（FR-007/034；泛域名强制 DNS = 宪章 VII）。
func validateInput(in Input) *httperr.APIError {
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if e := validate.ValidateDomainName(name); e != "" {
		return policyErr("域名格式非法: "+e, "name", "如 crm.example.com；泛域名 *.example.com")
	}
	wildcard := validate.IsWildcard(name)
	switch domain.HTTPSPolicy(in.HTTPSPolicy) {
	case domain.PolicyOff:
	case domain.PolicyAcmeHTTP:
		if wildcard {
			return policyErr("泛域名不能使用 HTTP Challenge", "https_policy",
				"HTTP-01 无法签发泛域名证书，请使用 acme_dns（宪章：泛域名必须 DNS Challenge）")
		}
		if strings.TrimSpace(in.CertResolverRef) == "" {
			return policyErr("acme_http 需要 resolver 名称", "cert_resolver_ref", "Traefik 静态配置中 certificatesResolvers 的 key")
		}
	case domain.PolicyAcmeDNS:
		if strings.TrimSpace(in.CertResolverRef) == "" {
			return policyErr("acme_dns 需要 resolver 名称", "cert_resolver_ref", "对应已预配 DNS challenge 的 resolver")
		}
		if strings.TrimSpace(in.DNSSCredentialID) == "" {
			return policyErr("acme_dns 需要 DNS Provider 凭证", "dns_credential_id", "在 设置→凭证 创建后选择")
		}
	case domain.PolicyImported:
		// 创建时空 cert 允许（随后 POST /certificates/import 挂接）；更新时由 import 流程保证
	default:
		return policyErr("HTTPS 策略非法", "https_policy", "off/acme_http/acme_dns/imported")
	}
	if in.ExpiryWarnDays < 0 || in.ExpiryWarnDays > 365 {
		return policyErr("到期预警天数 0–365", "expiry_warn_days", "默认 30")
	}
	return nil
}

func (s *Service) checkNode(ctx context.Context, nodeID string) *httperr.APIError {
	if nodeID == "" {
		return policyErr("node_id 必填", "node_id", "域名按节点作用域")
	}
	if _, err := s.nodes.Get(ctx, nodeID); err != nil {
		return mapNotFound(err, "节点")
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in Input, actorID string) (*domain.Domain, *httperr.APIError) {
	if apiErr := validateInput(in); apiErr != nil {
		return nil, apiErr
	}
	if apiErr := s.checkNode(ctx, in.NodeID); apiErr != nil {
		return nil, apiErr
	}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	taken, err := s.domains.NameTaken(ctx, in.NodeID, name)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if taken {
		return nil, policyErr("该节点下域名已存在", "name", "同一节点内域名唯一")
	}
	warn := in.ExpiryWarnDays
	if warn == 0 {
		warn = 30
	}
	d := &domain.Domain{
		NodeID: in.NodeID, Name: name,
		HTTPSPolicy:     domain.HTTPSPolicy(in.HTTPSPolicy),
		CertResolverRef: in.CertResolverRef,
		ExpiryWarnDays:  warn, Enabled: true,
	}
	d.DNSSCredentialID = nilIfEmpty(in.DNSSCredentialID)
	d.SetActor(actorID)
	if err := s.domains.Create(ctx, d); err != nil {
		if isUnique(err) {
			return nil, policyErr("该节点下域名已存在", "name", "同一节点内域名唯一")
		}
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "create", d.ID, d.Name, nil, d))
	return d, nil
}

func (s *Service) Update(ctx context.Context, id string, in Input, actorID string) (*domain.Domain, *httperr.APIError) {
	d, err := s.domains.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "域名")
	}
	in.NodeID = d.NodeID // 节点归属不可变
	if apiErr := validateInput(in); apiErr != nil {
		return nil, apiErr
	}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if name != d.Name {
		taken, err := s.domains.NameTaken(ctx, d.NodeID, name)
		if err != nil {
			return nil, httperr.Internal(err)
		}
		if taken {
			return nil, policyErr("该节点下域名已存在", "name", "同一节点内域名唯一")
		}
	}
	before := *d
	fields := map[string]any{
		"name": name, "https_policy": in.HTTPSPolicy,
		"cert_resolver_ref": in.CertResolverRef, "dns_credential_id": nilIfEmpty(in.DNSSCredentialID),
		"expiry_warn_days": max(in.ExpiryWarnDays, 1), "updated_by": actorID,
	}
	if in.Enabled != nil {
		fields["enabled"] = *in.Enabled
	}
	if in.ExpectedVersion == 0 {
		in.ExpectedVersion = d.RowVersion
	}
	if err := s.domains.Update(ctx, d, in.ExpectedVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(d.RowVersion)
		}
		if isUnique(err) {
			return nil, policyErr("该节点下域名已存在", "name", "")
		}
		return nil, httperr.Internal(err)
	}
	// 策略变更审计（FR-034 cert_policy_change；域名详情 diff 用）
	if before.HTTPSPolicy != domain.HTTPSPolicy(in.HTTPSPolicy) {
		s.audit.Record(ctx, nil, ev(actorID, "cert_policy_change", id, name,
			map[string]any{"policy": before.HTTPSPolicy}, map[string]any{"policy": in.HTTPSPolicy}))
	}
	fresh, _ := s.domains.Get(ctx, id)
	s.audit.Record(ctx, nil, ev(actorID, "update", id, name, before, fresh))
	return fresh, nil
}

func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool, expected int64, actorID string) (*domain.Domain, *httperr.APIError) {
	d, err := s.domains.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "域名")
	}
	if err := s.domains.Update(ctx, d, expected, map[string]any{"enabled": enabled, "updated_by": actorID}); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(d.RowVersion)
		}
		return nil, httperr.Internal(err)
	}
	fresh, _ := s.domains.Get(ctx, id)
	s.audit.Record(ctx, nil, ev(actorID, "update", id, d.Name, map[string]any{"enabled": d.Enabled}, map[string]any{"enabled": enabled}))
	return fresh, nil
}

// Delete 依赖守卫（US7 T083 完整接线前的基础版）。
func (s *Service) Delete(ctx context.Context, id, actorID string) *httperr.APIError {
	d, err := s.domains.Get(ctx, id)
	if err != nil {
		return mapNotFound(err, "域名")
	}
	refs, err := s.domains.ReferencingRoutes(ctx, id)
	if err != nil {
		return httperr.Internal(err)
	}
	rr := depcheck.RouteRefs(refs)
	if depcheck.AnyRefs(rr) {
		return httperr.DependencyBlocked("域名 "+d.Name, routeDetails(rr))
	}
	if err := s.domains.SoftDelete(ctx, id); err != nil {
		return httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "delete", id, d.Name, d, nil))
	return nil
}

func routeDetails(refs []depcheck.RouteRef) []httperr.Detail {
	out := make([]httperr.Detail, 0, len(refs))
	for _, r := range refs {
		out = append(out, httperr.Detail{Field: "routes", Message: r.Name, ID: r.ID})
	}
	return out
}

func ev(actorID, action, rid, name string, before, after any) auditrec.Event {
	return auditrec.Event{ActorID: actorID, Action: action, ResourceType: "domain",
		ResourceID: rid, ResourceName: name, Before: before, After: after}
}

func nilIfEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func mapNotFound(err error, what string) *httperr.APIError {
	if errors.Is(err, pgstore.ErrNotFound) {
		return httperr.NotFound(what)
	}
	return httperr.Internal(err)
}

func isUnique(err error) bool {
	s := err.Error()
	return strings.Contains(s, "SQLSTATE 23505") || strings.Contains(s, "duplicate key")
}
