// 列表查询助手（T023 queryutil 的存储侧搭档；SC-006 分页）。
package pgstore

import (
	"context"

	"gateway-center/backend/internal/api/queryutil"
	"gorm.io/gorm"
)

type ListQuery = queryutil.ListQuery

// listScanned 应用 q/sort/分页并扫描为目标切片。
// sort 已由 queryutil.Parse 白名单校验（^-?[a-z_]{1,32}$），此处直接拼接安全。
// searchCol 为 q= 模糊匹配列（默认 name）。
func listScanned[T any](ctx context.Context, base *gorm.DB, q ListQuery, defaultSort string, searchCol ...string) ([]T, int64, error) {
	col := "name"
	if len(searchCol) > 0 && searchCol[0] != "" {
		col = searchCol[0]
	}
	db := base.Where("deleted_at IS NULL").Session(&gorm.Session{})
	if q.Q != "" {
		db = db.Where(col+" ILIKE ?", "%"+q.Q+"%")
	}
	if q.NodeID != "" {
		db = db.Where("node_id = ?", q.NodeID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := q.Sort
	if order == "" {
		order = defaultSort
	}
	db = db.Order(order)
	if q.PageSize > 0 {
		db = db.Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize)
	}
	var items []T
	if err := db.Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
