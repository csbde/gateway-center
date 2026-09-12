// seed.go：migrate-seed 子命令（T010）。
// 职责：1) 数据库结构就位（幂等：goose 已建表时仅登记账本；否则按序执行 migrations/*.sql）；
// 2) 创建初始 Super Admin（存在即跳过，密码强度门槛 ≥12 由 authsvc.HashPassword 强制）。
// 注：quickstart §2 亦支持先 `goose -dir migrations postgres "$GC_DATABASE_URL" up`，两者可共存。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"gateway-center/backend/internal/application/authsvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/config"
	"gateway-center/backend/internal/infrastructure/logging"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

func runMigrateSeed() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := logging.New(slog.LevelInfo)
	slog.SetDefault(logger)

	store, err := pgstore.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("连接数据库: %w", err)
	}
	defer closeDB(store)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := applyMigrations(ctx, store.DB, migrationsDir()); err != nil {
		return err
	}
	return seedAdmin(ctx, store.DB, cfg)
}

func migrationsDir() string {
	if d := os.Getenv("GC_MIGRATIONS_DIR"); d != "" {
		return d
	}
	return "migrations"
}

// applyMigrations 账本 gc_migrations：文件按名升序，未记账本且对象不存在则执行 Up 段。
// goose 先行场景：users 表已存在但未记账本 → 直接登记（不重复执行）。
func applyMigrations(ctx context.Context, db *gorm.DB, dir string) error {
	if err := db.WithContext(ctx).Exec(
		`CREATE TABLE IF NOT EXISTS gc_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`).Error; err != nil {
		return fmt.Errorf("迁移账本: %w", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("迁移目录 %s 无 .sql 文件", dir)
	}
	sort.Strings(files)
	for _, f := range files {
		name := filepath.Base(f)
		var cnt int64
		if err := db.WithContext(ctx).Table("gc_migrations").Where("name = ?", name).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			continue
		}
		sql, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var usersExists int64
		if err := db.WithContext(ctx).Raw(
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'users'`).Scan(&usersExists).Error; err != nil {
			return err
		}
		if usersExists > 0 {
			slog.Info("结构已由外部迁移工具建立，登记账本", "file", name)
		} else {
			up := gooseUpSection(string(sql))
			if up == "" {
				up = string(sql)
			}
			if err := db.WithContext(ctx).Exec(up).Error; err != nil {
				return fmt.Errorf("执行迁移 %s: %w", name, err)
			}
			slog.Info("迁移已应用", "file", name)
		}
		if err := db.WithContext(ctx).Table("gc_migrations").
			Create(map[string]any{"name": name}).Error; err != nil {
			return err
		}
	}
	return nil
}

// gooseUpSection 提取 "-- +goose Up" 至 "-- +goose Down" 之间的 SQL（忽略 goose 注释指令行，
// StatementBegin/End 随语句一并交给 simple protocol 整体执行）。
func gooseUpSection(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	inUp := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case t == "-- +goose Up":
			inUp = true
			continue
		case t == "-- +goose Down":
			return strings.Join(out, "\n")
		case strings.HasPrefix(t, "-- +goose"):
			continue
		}
		if inUp {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

// seedAdmin 初始 Super Admin（幂等；已存在则仅确保角色为 super_admin 且 active）。
func seedAdmin(ctx context.Context, db *gorm.DB, cfg *config.Config) error {
	if cfg.InitialAdminPassword == "" {
		return fmt.Errorf("GC_INITIAL_ADMIN_PASSWORD 未设置：migrate-seed 需要提供初始管理员密码（≥12 字符）")
	}
	repo := pgstore.NewUserRepo(db)
	hash, err := authsvc.HashPassword(cfg.InitialAdminPassword)
	if err != nil {
		return fmt.Errorf("初始管理员密码不合规: %w", err)
	}
	u, err := repo.GetByUsername(ctx, cfg.InitialAdminUser)
	switch {
	case err == nil:
		if u.Role != domain.RoleSuperAdmin || u.Status != "active" {
			if err := repo.Update(ctx, u, u.RowVersion, map[string]any{
				"role": domain.RoleSuperAdmin, "status": "active", "updated_by": u.ID,
			}); err != nil {
				return fmt.Errorf("修正初始管理员: %w", err)
			}
			slog.Info("初始管理员已校正为 super_admin/active", "username", u.Username)
		} else {
			slog.Info("初始管理员已存在，跳过", "username", u.Username)
		}
		return nil
	case !errors.Is(err, pgstore.ErrNotFound):
		return fmt.Errorf("查询初始管理员: %w", err)
	}
	nu := &domain.User{Username: cfg.InitialAdminUser, DisplayName: "Platform Admin",
		PasswordHash: hash, Role: domain.RoleSuperAdmin, Status: "active"}
	nu.CreatedBy, nu.UpdatedBy = nu.ID, nu.ID
	if err := repo.Create(ctx, nu); err != nil {
		return fmt.Errorf("创建初始管理员: %w", err)
	}
	slog.Info("初始 Super Admin 已创建", "username", nu.Username)
	return nil
}
