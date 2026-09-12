// Package settingsvc 平台设置读写（T025，spec Assumptions：探测周期/离线阈值/到期天数/时区）。
// 带进程内缓存（单实例部署，R16；保存即失效）。
package settingsvc

import (
	"context"
	"sync"
	"time"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

type Service struct {
	repo  *pgstore.SettingsRepo
	audit *auditrec.Recorder

	mu    sync.RWMutex
	cache *domain.PlatformSettings
}

func New(repo *pgstore.SettingsRepo, audit *auditrec.Recorder) *Service {
	return &Service{repo: repo, audit: audit}
}

// Current 返回缓存设置（调度器/探测任务热路径调用）。
func (s *Service) Current(ctx context.Context) *domain.PlatformSettings {
	s.mu.RLock()
	c := s.cache
	s.mu.RUnlock()
	if c != nil {
		return c
	}
	st, err := s.repo.Get(ctx)
	if err != nil {
		return &domain.PlatformSettings{ProbeIntervalSec: 30, OfflineThreshold: 3, ExpiryWarnDays: 30, Timezone: "Asia/Shanghai"}
	}
	s.mu.Lock()
	s.cache = st
	s.mu.Unlock()
	return st
}

type Input struct {
	ProbeIntervalSec int    `json:"probe_interval_sec"`
	OfflineThreshold int    `json:"offline_threshold"`
	ExpiryWarnDays   int    `json:"expiry_warn_days"`
	Timezone         string `json:"timezone"`
	ExpectedVersion  int64  `json:"expected_version"`
}

func (s *Service) Update(ctx context.Context, in Input, actorID, actorName, ip, reqID string) (*domain.PlatformSettings, *httperr.APIError) {
	if in.ProbeIntervalSec < 5 || in.ProbeIntervalSec > 3600 {
		return nil, httperr.ValidationFailed("探测周期范围 5–3600 秒", httperr.Detail{Field: "probe_interval_sec"})
	}
	if in.OfflineThreshold < 1 || in.OfflineThreshold > 10 {
		return nil, httperr.ValidationFailed("离线判定阈值范围 1–10 次", httperr.Detail{Field: "offline_threshold"})
	}
	if in.ExpiryWarnDays < 1 || in.ExpiryWarnDays > 365 {
		return nil, httperr.ValidationFailed("证书到期预警范围 1–365 天", httperr.Detail{Field: "expiry_warn_days"})
	}
	if in.Timezone == "" {
		return nil, httperr.ValidationFailed("时区必填", httperr.Detail{Field: "timezone", Hint: "如 Asia/Shanghai"})
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return nil, httperr.ValidationFailed("时区不合法", httperr.Detail{Field: "timezone", Hint: err.Error()})
	}
	cur, err := s.repo.Get(ctx)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	before := *cur
	cur.ProbeIntervalSec = in.ProbeIntervalSec
	cur.OfflineThreshold = in.OfflineThreshold
	cur.ExpiryWarnDays = in.ExpiryWarnDays
	cur.Timezone = in.Timezone
	if err := s.repo.Save(ctx, cur); err != nil {
		return nil, httperr.Internal(err)
	}
	s.mu.Lock()
	s.cache = cur
	s.mu.Unlock()
	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: actorID, ActorUsername: actorName, Action: "update",
		ResourceType: "platform_settings", ResourceID: "1", ResourceName: "platform_settings",
		Before: before, After: *cur, IP: ip, RequestID: reqID,
	})
	return cur, nil
}
