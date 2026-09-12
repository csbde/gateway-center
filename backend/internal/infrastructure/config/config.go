// Package config 加载环境变量配置（T002 / research R6-R7）。
// 密钥仅经环境变量注入（GC_MASTER_KEY 32 字节 hex），不落库不落仓库（宪法 VIII）。
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Addr                 string        `envconfig:"GC_ADDR" default:":8080"`
	DatabaseURL          string        `envconfig:"GC_DATABASE_URL" required:"true"`
	MasterKeyHex         string        `envconfig:"GC_MASTER_KEY" required:"true"` // 64 hex chars = 32 bytes (AES-256)
	JWTSecret            string        `envconfig:"GC_JWT_SECRET" default:""`      // 缺省则从 MasterKey 派生
	InitialAdminUser     string        `envconfig:"GC_INITIAL_ADMIN_USERNAME" default:"admin"`
	InitialAdminPassword string        `envconfig:"GC_INITIAL_ADMIN_PASSWORD" default:""`
	AccessTokenTTL       time.Duration `envconfig:"GC_ACCESS_TOKEN_TTL" default:"2h"`
	RefreshTokenTTL      time.Duration `envconfig:"GC_REFRESH_TOKEN_TTL" default:"168h"` // 7d
	ProbeInterval        time.Duration `envconfig:"GC_PROBE_INTERVAL" default:"30s"`
	SPAStaticDir         string        `envconfig:"GC_SPA_DIR" default:""` // 空=不服务前端
}

// MasterKey 返回解析后的 32 字节主密钥。
func (c *Config) MasterKey() ([]byte, error) {
	k, err := hex.DecodeString(c.MasterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("GC_MASTER_KEY 不是合法 hex: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("GC_MASTER_KEY 必须为 32 字节（64 hex），当前 %d 字节", len(k))
	}
	return k, nil
}

// JWTSigningKey 返回 HS256 签名密钥（未显式提供时由主密钥 HKDF 式派生，避免复用加密密钥本身）。
func (c *Config) JWTSigningKey() ([]byte, error) {
	if c.JWTSecret != "" {
		return []byte(c.JWTSecret), nil
	}
	k, err := c.MasterKey()
	if err != nil {
		return nil, err
	}
	// 域分隔派生：sha256(master || "jwt")
	sum := sha256.Sum256(append(k, []byte("|jwt")...))
	return sum[:], nil
}

func Load() (*Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return nil, fmt.Errorf("加载环境配置: %w", err)
	}
	if _, err := c.MasterKey(); err != nil {
		return nil, err
	}
	return &c, nil
}
