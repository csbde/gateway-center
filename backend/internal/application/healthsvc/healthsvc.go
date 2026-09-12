// Package healthsvc Target 健康探测（T041，FR-011/AC-003，research R13）。
// 平台侧 GET <target><healthcheck_path>（无路径则 GET /），期望码段 2xx-3xx 判定 up/down；
// Service 聚合：全 up=healthy / 部分=degraded / 全 down=down（derived，非存储事实）。
package healthsvc

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	targets *pgstore.TargetRepo
	svcs    *pgstore.ServiceRepo
	http    *http.Client
}

func New(targets *pgstore.TargetRepo, svcs *pgstore.ServiceRepo) *Service {
	return &Service{targets: targets, svcs: svcs,
		http: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			MaxIdleConns: 32, DisableKeepAlives: false}}}
}

// RunOnce 探测全部启用 Target（每 Service 独立周期由 IntervalSec 粗化为每轮全探——
// IntervalSec 缺省 30s，与 scheduler 注册周期一致）。
func (s *Service) RunOnce(ctx context.Context) {
	ts, err := s.targets.ListEnabledWithService(ctx)
	if err != nil {
		return
	}
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for i := range ts {
		t := ts[i]
		sv, err := s.svcs.Get(ctx, t.ServiceID)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			up := s.probeTarget(ctx, sv, t)
			t.HealthStatus = map[bool]string{true: "up", false: "down"}[up]
			now := time.Now().UTC()
			t.HealthCheckedAt = &now
			_ = s.targets.Update(ctx, &t, t.RowVersion, map[string]any{
				"health_status": t.HealthStatus, "health_checked_at": now})
		}()
	}
	wg.Wait()
}

func (s *Service) probeTarget(ctx context.Context, sv *domain.Service, t domain.Target) bool {
	url := strings.TrimRight(t.URL, "/")
	path := sv.HealthcheckPath
	if path == "" {
		path = "/"
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(orDef(sv.TimeoutSec, 2))*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url+path, nil)
	if err != nil {
		return false
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return codeMatches(sv.ExpectedCodes, resp.StatusCode)
}

// codeMatches "2xx-3xx"/"200"/"2xx,404" 形式期望码判定（导出供测试）。
func codeMatches(spec string, code int) bool {
	if strings.TrimSpace(spec) == "" {
		spec = "2xx-3xx"
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			if codeGE(lo, code) && codeLE(hi, code) {
				return true
			}
			continue
		}
		if codeGE(part, code) && codeLE(part, code) { // 单值
			return true
		}
	}
	return false
}

func codeGE(spec string, code int) bool {
	lo, ok := bound(spec, true)
	return !ok || code >= lo
}

func codeLE(spec string, code int) bool {
	hi, ok := bound(spec, false)
	return !ok || code <= hi
}

// bound "2xx" 取下界 200/上界 299；纯数字两者相等。
func bound(spec string, low bool) (int, bool) {
	spec = strings.TrimSpace(spec)
	if len(spec) == 3 && (spec[1] == 'x' || spec[1] == 'X') && (spec[2] == 'x' || spec[2] == 'X') {
		d, err := strconv.Atoi(spec[:1])
		if err != nil {
			return 0, false
		}
		if low {
			return d * 100, true
		}
		return d*100 + 99, true
	}
	n, err := strconv.Atoi(spec)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ServiceHealthView 供 services 列表回显聚合健康（derived）。
func (s *Service) ServiceHealthView(ctx context.Context, serviceID string) string {
	ts, err := s.targets.ListByService(ctx, serviceID)
	var en []domain.Target
	for _, t := range ts {
		if t.Enabled {
			en = append(en, t)
		}
	}
	if err != nil {
		return "unknown"
	}
	return domain.ServiceHealth(en)
}

func orDef(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
