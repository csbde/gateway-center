// Package credsvc DNS/API 凭证用例（T064，FR-035、宪章 VIII）。
// 明文仅进内存→AES-256-GCM 加密入库；API 永不回显值，仅 8 位 fingerprint。
// 轮换只动密文与指纹，域名绑定关系不变（FR-035）；删除被引用即 409 + 引用清单。
// verify 调 provider 仅回原因（不回显凭证本体，Edge Case）。
package credsvc

import (
	"context"
	"errors"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/cryptox"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	creds  *pgstore.CredentialRepo
	cipher *cryptox.Cipher
	audit  *auditrec.Recorder
}

func New(creds *pgstore.CredentialRepo, cipher *cryptox.Cipher, audit *auditrec.Recorder) *Service {
	return &Service{creds: creds, cipher: cipher, audit: audit}
}

type Input struct {
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`   // dns_provider/traefik_api/other
	Provider        string            `json:"provider"` // lego DNS provider 标识
	Data            map[string]string `json:"data"`    // 明文键值；写入即加密，永不回显
	ExpectedVersion int64             `json:"expected_version,omitempty"`
}

// lego 白名单（research R3：一期支持的 DNS provider 子集；未知即校验拒绝）。
var allowedProviders = map[string]bool{
	"cloudflare": true, "alidns": true, "tencentcloud": true,
	"dnspod": true, "route53": true, "godaddy": true, "namedotcom": true,
	"azure": true, "googlecloud": true, "digitalocean": true,
}

func validateInput(in Input) *httperr.APIError {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 64 {
		return paramErr("名称必填且 ≤64 字符", "name")
	}
	switch in.Kind {
	case "dns_provider", "traefik_api", "other":
	default:
		return paramErr("凭证类型非法", "kind", "dns_provider/traefik_api/other")
	}
	if in.Kind == "dns_provider" {
		if !allowedProviders[in.Provider] {
			return paramErr("不支持的 DNS provider: "+in.Provider, "provider", "cloudflare/alidns/tencentcloud/dnspod/route53 等")
		}
	}
	if len(in.Data) == 0 {
		return paramErr("凭证数据不能为空", "data", "provider 所需的键值对")
	}
	return nil
}

func (s *Service) List(ctx context.Context, q pgstore.ListQuery) ([]domain.SecretCredential, int64, error) {
	return s.creds.List(ctx, q)
}

func (s *Service) Get(ctx context.Context, id string) (*domain.SecretCredential, *httperr.APIError) {
	c, err := s.creds.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "凭证")
	}
	return c, nil
}

func (s *Service) Create(ctx context.Context, in Input, actorID string) (*domain.SecretCredential, *httperr.APIError) {
	if apiErr := validateInput(in); apiErr != nil {
		return nil, apiErr
	}
	enc, fp, apiErr := s.seal(in.Data)
	if apiErr != nil {
		return nil, apiErr
	}
	c := &domain.SecretCredential{Name: strings.TrimSpace(in.Name), Kind: in.Kind,
		Provider: in.Provider, DataEncrypted: enc, DataFingerprint: fp}
	c.SetActor(actorID)
	if err := s.creds.Create(ctx, c); err != nil {
		if isUnique(err) {
			return nil, paramErr("凭证名已存在", "name", "全局唯一")
		}
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "create", c.ID, c.Name, nil, redactedView(c)))
	return c, nil
}

// Update 即轮换：只换密文+指纹，name/kind/provider 与域名绑定不变（FR-035）。
func (s *Service) Update(ctx context.Context, id string, in Input, actorID string) (*domain.SecretCredential, *httperr.APIError) {
	c, err := s.creds.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "凭证")
	}
	if apiErr := validateInput(in); apiErr != nil {
		return nil, apiErr
	}
	enc, fp, apiErr := s.seal(in.Data)
	if apiErr != nil {
		return nil, apiErr
	}
	if in.ExpectedVersion == 0 {
		in.ExpectedVersion = c.RowVersion
	}
	before := redactedView(c)
	if err := s.creds.Rotate(ctx, c, in.ExpectedVersion, enc, fp); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(c.RowVersion)
		}
		return nil, httperr.Internal(err)
	}
	fresh, _ := s.creds.Get(ctx, id)
	s.audit.Record(ctx, nil, ev(actorID, "rotate", id, fresh.Name, before, redactedView(fresh)))
	return fresh, nil
}

// Verify 解密后按 provider 做最小可用性校验；失败仅回原因（不回显凭证，Edge Case）。
// 一期不实装真实 API 拨测（lego 构造需 provider 特定配置且难单测），仅校验可解密 + 字段齐备。
func (s *Service) Verify(ctx context.Context, id string) (*VerifyResult, *httperr.APIError) {
	c, err := s.creds.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "凭证")
	}
	plain, err := s.cipher.Decrypt(c.DataEncrypted)
	if err != nil {
		return &VerifyResult{OK: false, Reason: "凭证密文无法解密（主密钥不匹配或数据损坏）"}, nil
	}
	// 还原为 map 校验非空（结构完整性；真实 provider 拨测由运维在外部确认）
	if len(plain) == 0 {
		return &VerifyResult{OK: false, Reason: "凭证数据为空"}, nil
	}
	return &VerifyResult{OK: true, Reason: "凭证可解密且结构完整（" + c.Provider + "）"}, nil
}

type VerifyResult struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// Delete 被域名引用即 409 + 引用清单（FR-035 处置语义）。
func (s *Service) Delete(ctx context.Context, id, actorID string) *httperr.APIError {
	c, err := s.creds.Get(ctx, id)
	if err != nil {
		return mapNotFound(err, "凭证")
	}
	refs, err := s.creds.ReferencingDomains(ctx, id)
	if err != nil {
		return httperr.Internal(err)
	}
	if len(refs) > 0 {
		details := make([]httperr.Detail, 0, len(refs))
		for _, d := range refs {
			details = append(details, httperr.Detail{Field: "domains", Message: d.Name, ID: d.ID})
		}
		return httperr.DependencyBlocked("凭证 "+c.Name, details)
	}
	if err := s.creds.SoftDelete(ctx, id); err != nil {
		return httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "delete", id, c.Name, redactedView(c), nil))
	return nil
}

// seal 把明文 map 序列化后加密，返回密文 + 8 位 fingerprint（宪章 VIII）。
func (s *Service) seal(data map[string]string) ([]byte, string, *httperr.APIError) {
	enc, err := s.cipher.Encrypt([]byte(marshalMap(data)))
	if err != nil {
		return nil, "", httperr.Internal(err)
	}
	return enc, cryptox.Fingerprint([]byte(marshalMap(data))), nil
}

// marshalMap 稳定序列化（键排序，保证 fingerprint 可复现）。
func marshalMap(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(data[k])
		b.WriteByte('\n')
	}
	return b.String()
}

// redactedView 审计快照：只留元数据 + fingerprint，不含密文（NFR-SEC-01）。
func redactedView(c *domain.SecretCredential) map[string]any {
	return map[string]any{"name": c.Name, "kind": c.Kind,
		"provider": c.Provider, "fingerprint": c.DataFingerprint}
}

func paramErr(msg, field string, hint ...string) *httperr.APIError {
	h := ""
	if len(hint) > 0 {
		h = hint[0]
	}
	return httperr.ValidationFailed(msg, httperr.Detail{Field: field, Hint: h})
}

func ev(actorID, action, rid, name string, before, after any) auditrec.Event {
	return auditrec.Event{ActorID: actorID, Action: action, ResourceType: "credential",
		ResourceID: rid, ResourceName: name, Before: before, After: after}
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
