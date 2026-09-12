// Package servicesvc Service/Target 用例（T034，AC-003/UF-2、FR-009~012）。
// Target URL 归一化（scheme/host 小写、去尾斜杠、去默认端口）后同 Service 内唯一。
package servicesvc

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/depcheck"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	svcs    *pgstore.ServiceRepo
	targets *pgstore.TargetRepo
	nodes   *pgstore.NodeRepo
	audit   *auditrec.Recorder
}

func New(svcs *pgstore.ServiceRepo, targets *pgstore.TargetRepo, nodes *pgstore.NodeRepo, audit *auditrec.Recorder) *Service {
	return &Service{svcs: svcs, targets: targets, nodes: nodes, audit: audit}
}

type Input struct {
	NodeID          string `json:"node_id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	HealthcheckPath string `json:"healthcheck_path,omitempty"`
	IntervalSec     int    `json:"interval_sec"`
	TimeoutSec      int    `json:"timeout_sec"`
	ExpectedCodes   string `json:"expected_codes"`
	Enabled         *bool  `json:"enabled,omitempty"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
}

type TargetInput struct {
	URL             string `json:"url"`
	Weight          int    `json:"weight"`
	Enabled         *bool  `json:"enabled,omitempty"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
}

func (s *Service) List(ctx context.Context, q pgstore.ListQuery) ([]domain.Service, int64, error) {
	return s.svcs.List(ctx, q)
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Service, error) {
	return s.svcs.Get(ctx, id)
}

func (s *Service) Targets(ctx context.Context, serviceID string) ([]domain.Target, error) {
	if _, err := s.svcs.Get(ctx, serviceID); err != nil {
		return nil, err
	}
	return s.targets.ListByService(ctx, serviceID)
}

func validateSvc(in Input) *httperr.APIError {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 64 {
		return httperr.ValidationFailed("服务名称必填且 ≤64 字符", httperr.Detail{Field: "name"})
	}
	if in.IntervalSec < 0 || in.IntervalSec > 3600 {
		return httperr.ValidationFailed("健康检查周期 0–3600 秒", httperr.Detail{Field: "interval_sec"})
	}
	if in.TimeoutSec < 0 || in.TimeoutSec > 60 {
		return httperr.ValidationFailed("健康检查超时 0–60 秒", httperr.Detail{Field: "timeout_sec"})
	}
	if p := in.HealthcheckPath; p != "" && !strings.HasPrefix(p, "/") {
		return httperr.ValidationFailed("健康检查路径需以 / 开头", httperr.Detail{Field: "healthcheck_path"})
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in Input, actorID string) (*domain.Service, *httperr.APIError) {
	if apiErr := validateSvc(in); apiErr != nil {
		return nil, apiErr
	}
	if in.NodeID == "" {
		return nil, httperr.ValidationFailed("node_id 必填", httperr.Detail{Field: "node_id"})
	}
	if _, err := s.nodes.Get(ctx, in.NodeID); err != nil {
		return nil, mapNotFound(err, "节点")
	}
	taken, err := s.svcs.NameTaken(ctx, in.NodeID, in.Name)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if taken {
		return nil, httperr.ValidationFailed("该节点下服务名已存在", httperr.Detail{Field: "name"})
	}
	sv := &domain.Service{
		NodeID: in.NodeID, Name: strings.TrimSpace(in.Name), Description: in.Description,
		HealthcheckPath: in.HealthcheckPath,
		IntervalSec:     orDefault(in.IntervalSec, 30), TimeoutSec: orDefault(in.TimeoutSec, 2),
		ExpectedCodes: orDefaultStr(in.ExpectedCodes, "2xx-3xx"), Enabled: true,
	}
	sv.SetActor(actorID)
	if err := s.svcs.Create(ctx, sv); err != nil {
		if isUnique(err) {
			return nil, httperr.ValidationFailed("该节点下服务名已存在", httperr.Detail{Field: "name"})
		}
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "create", sv.ID, sv.Name, nil, sv))
	return sv, nil
}

func (s *Service) Update(ctx context.Context, id string, in Input, actorID string) (*domain.Service, *httperr.APIError) {
	sv, err := s.svcs.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "服务")
	}
	if apiErr := validateSvc(in); apiErr != nil {
		return nil, apiErr
	}
	in.NodeID = sv.NodeID
	if in.ExpectedVersion == 0 {
		in.ExpectedVersion = sv.RowVersion
	}
	before := *sv
	fields := map[string]any{
		"name": strings.TrimSpace(in.Name), "description": in.Description,
		"healthcheck_path": in.HealthcheckPath,
		"interval_sec":     orDefault(in.IntervalSec, 30), "timeout_sec": orDefault(in.TimeoutSec, 2),
		"expected_codes": orDefaultStr(in.ExpectedCodes, "2xx-3xx"), "updated_by": actorID,
	}
	if in.Enabled != nil {
		fields["enabled"] = *in.Enabled
	}
	if err := s.svcs.Update(ctx, sv, in.ExpectedVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(sv.RowVersion)
		}
		if isUnique(err) {
			return nil, httperr.ValidationFailed("该节点下服务名已存在", httperr.Detail{Field: "name"})
		}
		return nil, httperr.Internal(err)
	}
	fresh, _ := s.svcs.Get(ctx, id)
	s.audit.Record(ctx, nil, ev(actorID, "update", id, fresh.Name, before, fresh))
	return fresh, nil
}

// SetTargetEnabled 仅翻转启用态（enable/disable 端点；不动 URL/权重）。
func (s *Service) SetTargetEnabled(ctx context.Context, targetID string, enabled bool, expected int64, actorID string) (*domain.Target, *httperr.APIError) {
	t, err := s.targets.Get(ctx, targetID)
	if err != nil {
		return nil, mapNotFound(err, "Target")
	}
	if expected == 0 {
		expected = t.RowVersion
	}
	if err := s.targets.Update(ctx, t, expected, map[string]any{"enabled": enabled, "updated_by": actorID}); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(t.RowVersion)
		}
		return nil, httperr.Internal(err)
	}
	fresh, _ := s.targets.Get(ctx, targetID)
	s.audit.Record(ctx, nil, ev(actorID, "update", targetID, fresh.URL, map[string]any{"enabled": t.Enabled}, map[string]any{"enabled": enabled}))
	return fresh, nil
}

// SetServiceEnabled 启停服务（enable/disable 端点）。
func (s *Service) SetServiceEnabled(ctx context.Context, id string, enabled bool, expected int64, actorID string) (*domain.Service, *httperr.APIError) {
	sv, err := s.svcs.Get(ctx, id)
	if err != nil {
		return nil, mapNotFound(err, "服务")
	}
	if expected == 0 {
		expected = sv.RowVersion
	}
	if err := s.svcs.Update(ctx, sv, expected, map[string]any{"enabled": enabled, "updated_by": actorID}); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(sv.RowVersion)
		}
		return nil, httperr.Internal(err)
	}
	fresh, _ := s.svcs.Get(ctx, id)
	s.audit.Record(ctx, nil, ev(actorID, "update", id, fresh.Name, map[string]any{"enabled": sv.Enabled}, map[string]any{"enabled": enabled}))
	return fresh, nil
}

func (s *Service) Delete(ctx context.Context, id, actorID string) *httperr.APIError {
	sv, err := s.svcs.Get(ctx, id)
	if err != nil {
		return mapNotFound(err, "服务")
	}
	refs, err := s.svcs.ReferencingRoutes(ctx, id)
	if err != nil {
		return httperr.Internal(err)
	}
	rr := depcheck.RouteRefs(refs)
	if depcheck.AnyRefs(rr) {
		return httperr.DependencyBlocked("服务 "+sv.Name, routeDetails(rr))
	}
	if err := s.svcs.SoftDelete(ctx, id); err != nil {
		return httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "delete", id, sv.Name, sv, nil))
	return nil
}

// ---- Targets ----

func (s *Service) AddTarget(ctx context.Context, serviceID string, in TargetInput, actorID string) (*domain.Target, *httperr.APIError) {
	sv, err := s.svcs.Get(ctx, serviceID)
	if err != nil {
		return nil, mapNotFound(err, "服务")
	}
	norm, apiErr := NormalizeTargetURL(in.URL)
	if apiErr != nil {
		return nil, apiErr
	}
	if in.Weight < 0 || in.Weight > 65535 {
		return nil, httperr.ValidationFailed("权重范围 1–65535（0=默认1）", httperr.Detail{Field: "weight"})
	}
	taken, err := s.targets.URLTaken(ctx, serviceID, norm)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if taken {
		return nil, httperr.ValidationFailed("Target 地址已存在", httperr.Detail{Field: "url", Hint: "同一服务内地址（归一化后）不可重复，UF-2"})
	}
	w := in.Weight
	if w == 0 {
		w = 1
	}
	t := &domain.Target{ServiceID: sv.ID, URL: norm, Weight: w, Enabled: in.Enabled == nil || *in.Enabled}
	t.SetActor(actorID)
	if err := s.targets.Create(ctx, t); err != nil {
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "create", t.ID, norm, nil, t))
	return t, nil
}

func (s *Service) UpdateTarget(ctx context.Context, targetID string, in TargetInput, actorID string) (*domain.Target, *httperr.APIError) {
	t, err := s.targets.Get(ctx, targetID)
	if err != nil {
		return nil, mapNotFound(err, "Target")
	}
	norm, apiErr := NormalizeTargetURL(in.URL)
	if apiErr != nil {
		return nil, apiErr
	}
	if in.Weight < 0 || in.Weight > 65535 {
		return nil, httperr.ValidationFailed("权重范围 1–65535", httperr.Detail{Field: "weight"})
	}
	if in.ExpectedVersion == 0 {
		in.ExpectedVersion = t.RowVersion
	}
	fields := map[string]any{"url": norm, "updated_by": actorID}
	if in.Weight > 0 {
		fields["weight"] = in.Weight
	}
	if in.Enabled != nil {
		fields["enabled"] = *in.Enabled
	}
	if norm != t.URL {
		taken, err := s.targets.URLTaken(ctx, t.ServiceID, norm)
		if err != nil {
			return nil, httperr.Internal(err)
		}
		if taken {
			return nil, httperr.ValidationFailed("Target 地址已存在", httperr.Detail{Field: "url"})
		}
	}
	before := *t
	if err := s.targets.Update(ctx, t, in.ExpectedVersion, fields); err != nil {
		if errors.Is(err, pgstore.ErrConflict) {
			return nil, httperr.ConcurrentEdit(t.RowVersion)
		}
		return nil, httperr.Internal(err)
	}
	fresh, _ := s.targets.Get(ctx, targetID)
	s.audit.Record(ctx, nil, ev(actorID, "update", targetID, fresh.URL, before, fresh))
	return fresh, nil
}

func (s *Service) DeleteTarget(ctx context.Context, targetID, actorID string) *httperr.APIError {
	t, err := s.targets.Get(ctx, targetID)
	if err != nil {
		return mapNotFound(err, "Target")
	}
	if err := s.targets.SoftDelete(ctx, targetID); err != nil {
		return httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, ev(actorID, "delete", targetID, t.URL, t, nil))
	return nil
}

// NormalizeTargetURL 协议限 http(s)、host 必填；scheme/host 小写、去尾斜杠、去默认端口。
// 导出：validate 与前端契约测试共用同一归一化语义。
func NormalizeTargetURL(raw string) (string, *httperr.APIError) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	bad := func() *httperr.APIError {
		return httperr.ValidationFailed("Target 地址非法: "+raw, httperr.Detail{Field: "url", Hint: "使用 http(s)://host[:port]，协议仅 http/https"})
	}
	if err != nil {
		return "", bad()
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", bad()
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", bad()
	}
	port := u.Port()
	def := map[string]string{"http": "80", "https": "443"}[u.Scheme]
	if port == "" || port == def {
		port = ""
	}
	var b strings.Builder
	b.WriteString(u.Scheme)
	b.WriteString("://")
	b.WriteString(host)
	if port != "" {
		b.WriteString(":")
		b.WriteString(port)
	}
	p := strings.TrimRight(u.Path, "/")
	if p == "/" {
		p = ""
	}
	b.WriteString(p)
	return b.String(), nil
}

func routeDetails(refs []depcheck.RouteRef) []httperr.Detail {
	out := make([]httperr.Detail, 0, len(refs))
	for _, r := range refs {
		out = append(out, httperr.Detail{Field: "routes", Message: r.Name, ID: r.ID})
	}
	return out
}

func ev(actorID, action, rid, name string, before, after any) auditrec.Event {
	return auditrec.Event{ActorID: actorID, Action: action, ResourceType: "service",
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

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func orDefaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
