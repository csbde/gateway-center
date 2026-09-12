// Package pgstore 是 PostgreSQL/GORM 访问层（T013）。
// 通用约定（data-model 头部）：软删 deleted_at；乐观锁 row_version；
// 列表默认过滤软删；唯一索引为部分唯一（WHERE deleted_at IS NULL），在迁移中定义。
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ErrConflict = 乐观锁失败（R10 → HTTP 409 CONCURRENT_EDIT）。
var ErrConflict = errors.New("并发编辑冲突：row_version 不匹配")

// ErrNotFound 未找到（含被软删过滤）。
var ErrNotFound = errors.New("record not found")

type Store struct{ DB *gorm.DB }

func Open(databaseURL string) (*Store, error) {
	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		Logger:  logger.Default.LogMode(logger.Warn),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(time.Hour)
	return &Store{DB: db}, nil
}

// WithTx 事务助手（AuditRecorder 要求与业务变更同提交，R12）。
func (s *Store) WithTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	tx := s.DB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

// Versionable：携带 row_version 的模型（domain.Base 统一实现）。
type Versionable interface {
	TableName() string
	RowRef() *int64
	UID() string
}

// UpdateOptimistic 以 expected_version 条件更新（map 可表达零值清除）：
// UPDATE t SET ..., row_version=row_version+1 WHERE id=? AND row_version=? → 0 行即 ErrConflict。
func UpdateOptimistic(ctx context.Context, db *gorm.DB, m Versionable, expectedVersion int64, fields map[string]any) error {
	if expectedVersion <= 0 {
		return errors.New("expected_version 必填")
	}
	var cur int64
	err := db.WithContext(ctx).Table(m.TableName()).
		Where("id = ? AND deleted_at IS NULL", m.UID()).
		Pluck("row_version", &cur).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if cur == 0 {
		return ErrNotFound
	}
	if cur != expectedVersion {
		return fmt.Errorf("%w（当前 row_version=%d）", ErrConflict, cur)
	}
	fields["row_version"] = gorm.Expr("row_version + 1")
	res := db.WithContext(ctx).Table(m.TableName()).
		Where("id = ? AND row_version = ?", m.UID(), expectedVersion).
		Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w（当前 row_version=%d）", ErrConflict, cur) // 竞态：读取后被抢先
	}
	*m.RowRef() = cur + 1
	return nil
}

// SoftDelete 软删（部分唯一索引随之释放名称）。
func SoftDelete(ctx context.Context, db *gorm.DB, m Versionable) error {
	res := db.WithContext(ctx).Table(m.TableName()).
		Where("id = ? AND deleted_at IS NULL", m.UID()).
		Update("deleted_at", gorm.Expr("now()"))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
