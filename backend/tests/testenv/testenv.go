// Package testenv 测试环境助手（T009）：testcontainers PostgreSQL 16 + migrations + fixtures。
// 无 build tag：contract/integration 两套测试共用；实际起容器仅发生在被测试函数调用时。
package testenv

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/cryptox"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

// TestKey 64 hex = 32B 测试主密钥（仅测试进程内，非生产）。
const TestKey = "abababababababababababababababababababababababababababababababab"

// Env 一次容器 + 一份已迁移库。
type Env struct {
	Store  *pgstore.Store
	Cipher *cryptox.Cipher
	DSN    string
}

func Setup(t *testing.T) *Env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	pg, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("gc_test"),
		postgres.WithUsername("gc"),
		postgres.WithPassword("gc"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		t.Fatalf("启动 PostgreSQL 容器: %v", err)
	}
	t.Cleanup(func() {
		if err := pg.Terminate(context.Background()); err != nil {
			t.Logf("终止容器: %v", err)
		}
	})
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("获取连接串: %v", err)
	}
	store, err := pgstore.Open(dsn)
	if err != nil {
		t.Fatalf("连接: %v", err)
	}
	ApplyMigrations(t, store.DB)
	cipher, err := cryptox.NewCipher(MustHexKey(t, TestKey))
	if err != nil {
		t.Fatal(err)
	}
	return &Env{Store: store, Cipher: cipher, DSN: dsn}
}

func MustHexKey(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		t.Fatalf("hex key 非法：需 64 hex")
	}
	return b
}

// ApplyMigrations 按文件名序执行 migrations/*.sql 的 goose Up 段。
func ApplyMigrations(t *testing.T, db *gorm.DB) {
	t.Helper()
	dir := MigrationsDir()
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("迁移目录 %s 无 sql 文件", dir)
	}
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(ExtractUp(string(sql))).Error; err != nil {
			t.Fatalf("迁移 %s: %v", filepath.Base(f), err)
		}
	}
}

func MigrationsDir() string {
	if d := os.Getenv("GC_MIGRATIONS_DIR"); d != "" {
		return d
	}
	// 调用方测试工作目录在 tests/<pkg>，回退两级到 backend/migrations
	return filepath.Join("..", "..", "migrations")
}

var upSection = regexp.MustCompile(`(?s)-- \+goose Up(.*?)-- \+goose Down`)

func ExtractUp(content string) string {
	m := upSection.FindStringSubmatch(content)
	if m == nil {
		return content
	}
	var out []string
	for _, ln := range strings.Split(m[1], "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "-- +goose") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// ---- fixture 工厂 ----

// SeedUserWithPassword 真实 bcrypt 哈希用户（登录流/契约测试用）。
func (e *Env) SeedUserWithPassword(t *testing.T, role domain.Role, password string) *domain.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		t.Fatal(err)
	}
	u := &domain.User{Username: fmt.Sprintf("u%s", RandSuffix()), DisplayName: "Test",
		PasswordHash: string(hash), Role: role, Status: "active"}
	if err := pgstore.NewUserRepo(e.Store.DB).Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *Env) SeedUser(t *testing.T, role domain.Role) *domain.User {
	t.Helper()
	u := &domain.User{Username: fmt.Sprintf("u%s", RandSuffix()), DisplayName: "Test",
		PasswordHash: "$2a$12$xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		Role:         role, Status: "active"}
	if err := pgstore.NewUserRepo(e.Store.DB).Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *Env) SeedNode(t *testing.T, name string, envType domain.EnvType) *domain.GatewayNode {
	t.Helper()
	n := &domain.GatewayNode{Name: name, BaseURL: "http://localhost:1", DeployRoot: t.TempDir(),
		EnvType: envType, Enabled: true}
	if err := e.Store.DB.Create(n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *Env) SeedDomain(t *testing.T, nodeID, name string) *domain.Domain {
	t.Helper()
	d := &domain.Domain{NodeID: nodeID, Name: name, HTTPSPolicy: domain.PolicyOff, Enabled: true}
	if err := e.Store.DB.Create(d).Error; err != nil {
		t.Fatal(err)
	}
	return d
}

func (e *Env) SeedService(t *testing.T, nodeID, name string, targets ...string) *domain.Service {
	t.Helper()
	s := &domain.Service{NodeID: nodeID, Name: name, ExpectedCodes: "2xx-3xx",
		IntervalSec: 30, TimeoutSec: 2, Enabled: true}
	if err := e.Store.DB.Create(s).Error; err != nil {
		t.Fatal(err)
	}
	for _, u := range targets {
		tg := &domain.Target{ServiceID: s.ID, URL: u, Weight: 1, Enabled: true}
		if err := e.Store.DB.Create(tg).Error; err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (e *Env) SeedSimpleRoute(t *testing.T, nodeID, domainID, serviceID, path string) *domain.Route {
	t.Helper()
	did := domainID
	r := &domain.Route{NodeID: nodeID, Name: "r-" + RandSuffix(), Mode: "simple",
		DomainID: &did, ServiceID: serviceID, Path: path, MatchType: "prefix",
		Priority: 100, Status: "enabled", HTTPS: false}
	if err := e.Store.DB.Create(r).Error; err != nil {
		t.Fatal(err)
	}
	return r
}

var alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func RandSuffix() string {
	b := make([]byte, 8)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = alphabet[int(now>>uint(i*3))%len(alphabet)]
	}
	return string(b)
}
