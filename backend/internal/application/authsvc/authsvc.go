// Package authsvc 认证用例（T015，research R6）。
// bcrypt cost≥12；Access JWT 2h；Refresh 旋转（刷新即作废旧 token）；
// 每请求由 rbac 中间件复核用户角色/disabled 即时 401。
package authsvc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/tokensx"
	"golang.org/x/crypto/bcrypt"
)

const BcryptCost = 12

type Service struct {
	users      *pgstore.UserRepo
	tokens     *pgstore.TokenRepo
	tm         *tokensx.Manager
	audit      *auditrec.Recorder
	refreshTTL time.Duration
	limiter    *loginLimiter
}

func NewService(users *pgstore.UserRepo, tokens *pgstore.TokenRepo, tm *tokensx.Manager,
	audit *auditrec.Recorder, refreshTTL time.Duration) *Service {
	return &Service{users: users, tokens: tokens, tm: tm, audit: audit,
		refreshTTL: refreshTTL, limiter: newLoginLimiter()}
}

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// Me 当前用户视图（GET /me；前端角色态来源）。
func (s *Service) Me(ctx context.Context, userID string) (*domain.User, *httperr.APIError) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, httperr.Unauthenticated()
	}
	return u, nil
}

// Login 校验凭据；成功签发双 token 并审计（login 动作）。
func (s *Service) Login(ctx context.Context, username, password, ip, userAgent, reqID string, actorID string) (*Tokens, *domain.User, *httperr.APIError) {
	if !s.limiter.allow(ip, username) {
		return nil, nil, httperr.Newf(429, "RATE_LIMITED", "登录尝试过多，请稍后再试")
	}
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgstore.ErrNotFound) {
			// 恒定时间假校验，防用户名枚举（时序侧信道）
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			return nil, nil, httperr.Unauthenticated()
		}
		return nil, nil, httperr.Internal(err)
	}
	if u.Status != "active" {
		return nil, nil, httperr.Unauthenticated()
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, nil, httperr.Unauthenticated()
	}
	access, exp, err := s.tm.Sign(u.ID, u.Username, string(u.Role))
	if err != nil {
		return nil, nil, httperr.Internal(err)
	}
	refresh, hash, err := tokensx.NewRefreshToken()
	if err != nil {
		return nil, nil, httperr.Internal(err)
	}
	if err := s.tokens.Issue(ctx, u.ID, hash, s.refreshTTL); err != nil {
		return nil, nil, httperr.Internal(err)
	}
	_ = s.users.TouchLogin(ctx, u.ID)
	s.audit.Record(ctx, nil, auditrec.Event{
		ActorID: u.ID, ActorUsername: u.Username, Action: "login",
		ResourceType: "session", ResourceID: u.ID, IP: ip, UserAgent: userAgent, RequestID: reqID,
	})
	_ = exp
	return &Tokens{AccessToken: access, RefreshToken: refresh, ExpiresIn: int64(time.Until(exp).Seconds())}, u, nil
}

// Refresh 旋转：验证旧 refresh，作废并签发新的（R6）。
func (s *Service) Refresh(ctx context.Context, refreshPlain string) (*Tokens, *httperr.APIError) {
	hash := tokensx.HashToken(refreshPlain)
	t, err := s.tokens.Find(ctx, hash)
	if err != nil || t.Revoked || time.Now().UTC().After(t.ExpiresAt) {
		return nil, httperr.Unauthenticated()
	}
	u, err := s.users.Get(ctx, t.UserID)
	if err != nil || u.Status != "active" {
		_ = s.tokens.Revoke(ctx, t.ID)
		return nil, httperr.Unauthenticated()
	}
	_ = s.tokens.Revoke(ctx, t.ID) // 旧 token 作废（旋转）
	access, exp, err := s.tm.Sign(u.ID, u.Username, string(u.Role))
	if err != nil {
		return nil, httperr.Internal(err)
	}
	nr, nh, err := tokensx.NewRefreshToken()
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if err := s.tokens.Issue(ctx, u.ID, nh, s.refreshTTL); err != nil {
		return nil, httperr.Internal(err)
	}
	return &Tokens{AccessToken: access, RefreshToken: nr, ExpiresIn: int64(time.Until(exp).Seconds())}, nil
}

// Logout 吊销该 refresh token。
func (s *Service) Logout(ctx context.Context, refreshPlain string) *httperr.APIError {
	t, err := s.tokens.Find(ctx, tokensx.HashToken(refreshPlain))
	if err != nil {
		return httperr.Unauthenticated()
	}
	if err := s.tokens.Revoke(ctx, t.ID); err != nil {
		return httperr.Internal(err)
	}
	return nil
}

// HashPassword 供种子/用户管理使用。
func HashPassword(pw string) (string, error) {
	if len(pw) < 12 {
		return "", httperr.ValidationFailed("密码强度不足",
			httperr.Detail{Field: "password", Hint: "至少 12 字符（research R6，bcrypt cost≥12）"})
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	return string(b), err
}

// 用户名规则：3–32 [a-z0-9_.-]（data-model §1）
func ValidateUsername(u string) *httperr.APIError {
	if len(u) < 3 || len(u) > 32 {
		return httperr.ValidationFailed("用户名长度需 3–32", httperr.Detail{Field: "username", Hint: "3–32 个字符"})
	}
	for _, r := range strings.ToLower(u) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-'
		if !ok {
			return httperr.ValidationFailed("用户名仅允许小写字母/数字/_/./-", httperr.Detail{Field: "username", Hint: "小写字母开头，可含 a-z0-9_.-"})
		}
	}
	return nil
}

var dummyHash = func() []byte {
	b, _ := bcrypt.GenerateFromPassword([]byte("timing-equalizer-not-a-real-password"), BcryptCost)
	return b
}()

// ---- 登录限速（IP+用户名计数，R6）----

type loginLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	window time.Duration
	limit  int
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{hits: map[string][]time.Time{}, window: 5 * time.Minute, limit: 10}
}

func (l *loginLimiter) allow(ip, username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := ip + "|" + strings.ToLower(username)
	now := time.Now()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
