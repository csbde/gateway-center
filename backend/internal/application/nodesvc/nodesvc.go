// Package nodesvc Gateway Node 用例（T031，AC-001/UF-1）。
// 全部写操作经 auditrec（宪章 X）；软删守卫：有部署记录拒删（UF-1）。
package nodesvc

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/cryptox"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	nodes  *pgstore.NodeRepo
	audit  *auditrec.Recorder
	cipher *cryptox.Cipher
}

func New(nodes *pgstore.NodeRepo, audit *auditrec.Recorder, cipher *cryptox.Cipher) *Service {
	return &Service{nodes: nodes, audit: audit, cipher: cipher}
}

type Input struct {
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	DeployRoot string `json:"deploy_root"`
	EnvType    string `json:"env_type"`
	Remark     string `json:"remark"`
	// APIAuth 可选 Traefik API Basic 凭据 "user:pass"；仅写入，永不回显（R7）
	APIAuth         string `json:"api_auth,omitempty"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
}

func (s *Service) List(ctx context.Context, q pgstore.ListQuery) ([]domain.GatewayNode, int64, error) {
	return s.nodes.List(ctx, q)
}

func (s *Service) Get(ctx context.Context, id string) (*domain.GatewayNode, error) {
	return s.nodes.Get(ctx, id)
}

func (s *Service) State(ctx context.Context, id string) (*domain.NodeState, error) {
	if _, err := s.nodes.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.nodes.State(ctx, id)
}

type Actor struct {
	ID, Username, IP, UserAgent, RequestID string
}

func (a Actor) ev(action, rt, rid, rname string, before, after any) auditrec.Event {
	return auditrec.Event{ActorID: a.ID, ActorUsername: a.Username, Action: action,
		ResourceType: rt, ResourceID: rid, ResourceName: rname,
		Before: before, After: after, IP: a.IP, UserAgent: a.UserAgent, RequestID: a.RequestID}
}

func (s *Service) Create(ctx context.Context, in Input, actor Actor) (*domain.GatewayNode, *httperr.APIError) {
	if apiErr := validateInput(in); apiErr != nil {
		return nil, apiErr
	}
	n := &domain.GatewayNode{
		Name: strings.TrimSpace(in.Name), BaseURL: strings.TrimRight(in.BaseURL, "/"),
		DeployRoot: in.DeployRoot, EnvType: domain.EnvType(in.EnvType),
		Remark: in.Remark, Enabled: true,
	}
	n.SetActor(actor.ID)
	if in.APIAuth != "" {
		enc, err := s.cipher.Encrypt([]byte(in.APIAuth))
		if err != nil {
			return nil, httperr.Internal(err)
		}
		n.APIAuthEncrypted = enc
	}
	if err := s.nodes.Create(ctx, n); err != nil {
		if isUniqueViolation(err) {
			return nil, httperr.ValidationFailed("节点名称已存在", httperr.Detail{Field: "name", Hint: "节点名称全局唯一"})
		}
		return nil, httperr.Internal(err)
	}
	// 创建即注册首轮探测（T031）：预建 unknown 状态行，调度器后续覆写
	st := &domain.NodeState{NodeID: n.ID, Status: "unknown", ConsecutiveFailures: 0}
	if err := s.nodes.UpsertState(ctx, st); err != nil {
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, actor.ev("create", "gateway_node", n.ID, n.Name, nil, publicView(n)))
	return n, nil
}

func (s *Service) Update(ctx context.Context, id string, in Input, actor Actor) (*domain.GatewayNode, *httperr.APIError) {
	n, err := s.nodes.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "节点")
	}
	if apiErr := validateInput(in); apiErr != nil {
		return nil, apiErr
	}
	before := publicView(n)
	fields := map[string]any{
		"name": strings.TrimSpace(in.Name), "base_url": strings.TrimRight(in.BaseURL, "/"),
		"deploy_root": in.DeployRoot, "env_type": in.EnvType, "remark": in.Remark,
		"updated_by": actor.ID,
	}
	if in.APIAuth != "" {
		enc, err := s.cipher.Encrypt([]byte(in.APIAuth))
		if err != nil {
			return nil, httperr.Internal(err)
		}
		fields["api_auth_encrypted"] = enc
	}
	if err := s.nodes.Update(ctx, n, in.ExpectedVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, concurrentErr(err)
		}
		if isUniqueViolation(err) {
			return nil, httperr.ValidationFailed("节点名称已存在", httperr.Detail{Field: "name"})
		}
		return nil, httperr.Internal(err)
	}
	fresh, _ := s.nodes.Get(ctx, id)
	s.audit.Record(ctx, nil, actor.ev("update", "gateway_node", id, in.Name, before, publicView(fresh)))
	return fresh, nil
}

func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool, expected int64, actor Actor) (*domain.GatewayNode, *httperr.APIError) {
	n, err := s.nodes.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "节点")
	}
	// 与 servicesvc/mwsvc 的 enable/disable 同语义：调用方未带 expected_version 时以当前版本为准；
	// 否则 UpdateOptimistic 返回裸 error 被映射成 500，把「少传字段」误报成服务端故障。
	if expected == 0 {
		expected = n.RowVersion
	}
	if err := s.nodes.Update(ctx, n, expected, map[string]any{"enabled": enabled, "updated_by": actor.ID}); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, concurrentErr(err)
		}
		return nil, httperr.Internal(err)
	}
	action := "enable"
	if !enabled {
		action = "disable"
	}
	fresh, _ := s.nodes.Get(ctx, id)
	s.audit.Record(ctx, nil, actor.ev(action, "gateway_node", id, n.Name, nil, publicView(fresh)))
	return fresh, nil
}

// Delete 软删守卫（UF-1：有部署记录拒删）；禁用的节点才可删。
func (s *Service) Delete(ctx context.Context, id string, actor Actor) *httperr.APIError {
	n, err := s.nodes.Get(ctx, id)
	if err != nil {
		return mapNotFound(err, "节点")
	}
	if n.Enabled {
		return httperr.DependencyBlocked("节点 "+n.Name, []httperr.Detail{{Field: "enabled", Hint: "请先禁用节点再删除"}})
	}
	has, err := s.nodes.HasDeployments(ctx, id)
	if err != nil {
		return httperr.Internal(err)
	}
	if has {
		return httperr.DependencyBlocked("节点 "+n.Name, []httperr.Detail{{Field: "deployments", Hint: "该节点存在部署记录（设计上不可删除）；如需回收请归档此节点配置"}})
	}
	if err := s.nodes.SoftDelete(ctx, id); err != nil {
		return httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, actor.ev("delete", "gateway_node", id, n.Name, publicView(n), nil))
	return nil
}

// APIAuth 供探测任务解密 Traefik API Basic 凭据。
func (s *Service) APIAuth(ctx context.Context, n *domain.GatewayNode) (user, pass string, err error) {
	if len(n.APIAuthEncrypted) == 0 {
		return "", "", nil
	}
	plain, err := s.cipher.Decrypt(n.APIAuthEncrypted)
	if err != nil {
		return "", "", err
	}
	u, p, found := strings.Cut(string(plain), ":")
	if !found {
		return "", "", errors.New("api_auth 格式应为 user:pass")
	}
	return u, p, nil
}

func validateInput(in Input) *httperr.APIError {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 64 {
		return httperr.ValidationFailed("节点名称必填且 ≤64 字符", httperr.Detail{Field: "name"})
	}
	u, err := url.Parse(in.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return httperr.ValidationFailed("API 地址必须为 http(s)://host:port", httperr.Detail{Field: "base_url", Hint: "平台在容器内（compose）填 http://traefik:8080；宿主进程直连才用 http://localhost:8081"})
	}
	if !strings.HasPrefix(in.DeployRoot, "/") {
		return httperr.ValidationFailed("落盘根路径必须为绝对路径", httperr.Detail{Field: "deploy_root", Hint: "compose 拓扑填 /shared；宿主直连填 /etc/traefik（平台只写其 dynamic/ 子树）"})
	}
	if !domain.ValidEnvType(in.EnvType) {
		return httperr.ValidationFailed("环境类型非法", httperr.Detail{Field: "env_type", Hint: "development/test/staging/production"})
	}
	return nil
}

func publicView(n *domain.GatewayNode) map[string]any {
	return map[string]any{
		"id": n.ID, "name": n.Name, "base_url": n.BaseURL, "deploy_root": n.DeployRoot,
		"env_type": n.EnvType, "remark": n.Remark, "enabled": n.Enabled,
		"has_api_auth": len(n.APIAuthEncrypted) > 0,
		"row_version":  n.RowVersion, "created_at": n.CreatedAt, "updated_at": n.UpdatedAt,
	}
}

func mapNotFound(err error, what string) *httperr.APIError {
	if errors.Is(err, pgstore.ErrNotFound) {
		return httperr.NotFound(what)
	}
	return httperr.Internal(err)
}

func concurrentErr(err error) *httperr.APIError {
	// pgstore 错误串携带当前 row_version（contracts 要求 details 含当前值）
	return httperr.Newf(409, "CONCURRENT_EDIT", err.Error())
}

func isUniqueViolation(err error) bool {
	s := err.Error()
	return strings.Contains(s, "SQLSTATE 23505") || strings.Contains(s, "duplicate key")
}
