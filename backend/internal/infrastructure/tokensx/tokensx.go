// Package tokensx 签发/校验 JWT（T015，research R6）。
// Access Token HS256 2h；Refresh Token 随机 256bit（存库旋转，此处只生成）。
package tokensx

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func sha256sum(b []byte) []byte { s := sha256.Sum256(b); return s[:] }

type Claims struct {
	UserID   string `json:"sub"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

type Manager struct {
	key       []byte
	accessTTL time.Duration
}

func NewManager(key []byte, accessTTL time.Duration) *Manager {
	return &Manager{key: key, accessTTL: accessTTL}
}

func (m *Manager) Sign(userID, username, role string) (string, time.Time, error) {
	exp := time.Now().UTC().Add(m.accessTTL)
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		UserID: userID, Username: username, Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			Issuer:    "gateway-center",
		},
	})
	s, err := t.SignedString(m.key)
	return s, exp, err
}

var ErrInvalidToken = errors.New("token 无效或已过期")

func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(tp *jwt.Token) (any, error) {
		if _, ok := tp.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", tp.Header["alg"])
		}
		return m.key, nil
	})
	if err != nil {
		return nil, ErrInvalidToken
	}
	c, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	return c, nil
}

// NewRefreshToken 返回 (明文, sha256 hex)；只存哈希。
func NewRefreshToken() (plain, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, HashToken(plain), nil
}

func HashToken(plain string) string {
	sum := sha256sum([]byte(plain))
	return base64.RawURLEncoding.EncodeToString(sum)
}

func CompareHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
