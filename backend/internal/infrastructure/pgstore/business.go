// 业务实体仓储：Domain / Service / Target / Route / Middleware（T028–T030/T057 文件路径）。
package pgstore

import (
	"context"
	"errors"

	"gateway-center/backend/internal/domain"
	"gorm.io/gorm"
)

var errNoRows = gorm.ErrRecordNotFound

// ---- Domain ----

type DomainRepo struct{ db *gorm.DB }

func NewDomainRepo(db *gorm.DB) *DomainRepo { return &DomainRepo{db: db} }

func (r *DomainRepo) List(ctx context.Context, q ListQuery) ([]domain.Domain, int64, error) {
	return listScanned[domain.Domain](ctx, r.db.Model(&domain.Domain{}), q, "name ASC")
}

func (r *DomainRepo) Get(ctx context.Context, id string) (*domain.Domain, error) {
	var m domain.Domain
	if err := r.db.WithContext(ctx).First(&m, "id = ?", id).Error; err != nil {
		if errors.Is(err, errNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

func (r *DomainRepo) Create(ctx context.Context, m *domain.Domain) error {
	m.RowVersion = 1
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *DomainRepo) Update(ctx context.Context, m *domain.Domain, expected int64, fields map[string]any) error {
	return UpdateOptimistic(ctx, r.db, m, expected, fields)
}

func (r *DomainRepo) SoftDelete(ctx context.Context, id string) error {
	var m domain.Domain
	m.ID = id
	return SoftDelete(ctx, r.db, &m)
}

func (r *DomainRepo) NameTaken(ctx context.Context, nodeID, name string) (bool, error) {
	var c int64
	err := r.db.WithContext(ctx).Model(&domain.Domain{}).
		Where("node_id = ? AND name = ? AND deleted_at IS NULL", nodeID, name).Count(&c).Error
	return c > 0, err
}

// ReferencingRoutes：依赖检查用——引用该域名的未归档路由（US7/V-7）。
func (r *DomainRepo) ReferencingRoutes(ctx context.Context, domainID string) ([]domain.Route, error) {
	var rs []domain.Route
	err := r.db.WithContext(ctx).
		Joins("JOIN domains d ON d.id = routes.domain_id").
		Where("routes.domain_id = ? AND routes.status <> 'archived' AND routes.deleted_at IS NULL AND d.deleted_at IS NULL", domainID).
		Find(&rs).Error
	return rs, err
}

// ---- Service / Target ----

type ServiceRepo struct{ db *gorm.DB }

func NewServiceRepo(db *gorm.DB) *ServiceRepo { return &ServiceRepo{db: db} }

func (r *ServiceRepo) List(ctx context.Context, q ListQuery) ([]domain.Service, int64, error) {
	return listScanned[domain.Service](ctx, r.db.Model(&domain.Service{}), q, "name ASC")
}

func (r *ServiceRepo) Get(ctx context.Context, id string) (*domain.Service, error) {
	var s domain.Service
	if err := r.db.WithContext(ctx).First(&s, "id = ?", id).Error; err != nil {
		if errors.Is(err, errNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *ServiceRepo) Create(ctx context.Context, s *domain.Service) error {
	s.RowVersion = 1
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *ServiceRepo) Update(ctx context.Context, s *domain.Service, expected int64, fields map[string]any) error {
	return UpdateOptimistic(ctx, r.db, s, expected, fields)
}

// SoftDelete 级联软删 Targets（data-model §4：Target 随 Service 处置）。
func (r *ServiceRepo) SoftDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s domain.Service
		s.ID = id
		if err := SoftDelete(ctx, tx, &s); err != nil {
			return err
		}
		return tx.Model(&domain.Target{}).Where("service_id = ?", id).
			Update("deleted_at", gorm.Expr("now()")).Error
	})
}

func (r *ServiceRepo) NameTaken(ctx context.Context, nodeID, name string) (bool, error) {
	var c int64
	err := r.db.WithContext(ctx).Model(&domain.Service{}).
		Where("node_id = ? AND name = ? AND deleted_at IS NULL", nodeID, name).Count(&c).Error
	return c > 0, err
}

func (r *ServiceRepo) ReferencingRoutes(ctx context.Context, serviceID string) ([]domain.Route, error) {
	var rs []domain.Route
	err := r.db.WithContext(ctx).
		Where("service_id = ? AND status <> 'archived' AND deleted_at IS NULL", serviceID).
		Find(&rs).Error
	return rs, err
}

type TargetRepo struct{ db *gorm.DB }

func NewTargetRepo(db *gorm.DB) *TargetRepo { return &TargetRepo{db: db} }

func (r *TargetRepo) ListByService(ctx context.Context, serviceID string) ([]domain.Target, error) {
	var ts []domain.Target
	err := r.db.WithContext(ctx).Where("service_id = ? AND deleted_at IS NULL", serviceID).
		Order("created_at ASC").Find(&ts).Error
	return ts, err
}

func (r *TargetRepo) Get(ctx context.Context, id string) (*domain.Target, error) {
	var t domain.Target
	if err := r.db.WithContext(ctx).First(&t, "id = ?", id).Error; err != nil {
		if errors.Is(err, errNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *TargetRepo) Create(ctx context.Context, t *domain.Target) error {
	t.RowVersion = 1
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *TargetRepo) Update(ctx context.Context, t *domain.Target, expected int64, fields map[string]any) error {
	return UpdateOptimistic(ctx, r.db, t, expected, fields)
}

func (r *TargetRepo) SoftDelete(ctx context.Context, id string) error {
	var t domain.Target
	t.ID = id
	return SoftDelete(ctx, r.db, &t)
}

// URLTaken 同 Service 内归一化唯一检查（FR-010）。
func (r *TargetRepo) URLTaken(ctx context.Context, serviceID, normURL string) (bool, error) {
	var c int64
	err := r.db.WithContext(ctx).Model(&domain.Target{}).
		Where("service_id = ? AND url = ? AND deleted_at IS NULL", serviceID, normURL).Count(&c).Error
	return c > 0, err
}

func (r *TargetRepo) ListEnabledWithService(ctx context.Context) ([]domain.Target, error) {
	var ts []domain.Target
	err := r.db.WithContext(ctx).
		Joins("JOIN services s ON s.id = targets.service_id").
		Where("targets.enabled = true AND s.enabled = true AND targets.deleted_at IS NULL AND s.deleted_at IS NULL").
		Find(&ts).Error
	return ts, err
}

// ListEnabledByNode 节点作用域的启用 Target（probesvc degraded 判定：是否有 down 的目标，T071/FR-003）。
func (r *TargetRepo) ListEnabledByNode(ctx context.Context, nodeID string) ([]domain.Target, error) {
	var ts []domain.Target
	err := r.db.WithContext(ctx).
		Joins("JOIN services s ON s.id = targets.service_id").
		Where("targets.enabled = true AND s.enabled = true AND s.node_id = ? AND targets.deleted_at IS NULL AND s.deleted_at IS NULL", nodeID).
		Find(&ts).Error
	return ts, err
}

// ---- Route ----

type RouteRepo struct{ db *gorm.DB }

func NewRouteRepo(db *gorm.DB) *RouteRepo { return &RouteRepo{db: db} }

func (r *RouteRepo) List(ctx context.Context, q ListQuery) ([]domain.Route, int64, error) {
	return listScanned[domain.Route](ctx, r.db.Model(&domain.Route{}), q, "name ASC")
}

func (r *RouteRepo) Get(ctx context.Context, id string) (*domain.Route, error) {
	var rt domain.Route
	err := r.db.WithContext(ctx).
		Preload("Middlewares", func(db *gorm.DB) *gorm.DB { return db.Order("position ASC") }).
		First(&rt, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, errNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &rt, nil
}

func (r *RouteRepo) Create(ctx context.Context, rt *domain.Route) error {
	rt.RowVersion = 1
	return r.db.WithContext(ctx).Create(rt).Error
}

func (r *RouteRepo) Update(ctx context.Context, rt *domain.Route, expected int64, fields map[string]any) error {
	return UpdateOptimistic(ctx, r.db, rt, expected, fields)
}

func (r *RouteRepo) SoftDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rt domain.Route
		rt.ID = id
		if err := SoftDelete(ctx, tx, &rt); err != nil {
			return err
		}
		return tx.Where("route_id = ?", id).Delete(&domain.RouteMiddleware{}).Error
	})
}

func (r *RouteRepo) NameTaken(ctx context.Context, nodeID, name string) (bool, error) {
	var c int64
	err := r.db.WithContext(ctx).Model(&domain.Route{}).
		Where("node_id = ? AND name = ? AND deleted_at IS NULL", nodeID, name).Count(&c).Error
	return c > 0, err
}

// ListByNode 生成快照/验证用全量（非分页；节点作用域）。
func (r *RouteRepo) ListByNode(ctx context.Context, nodeID string) ([]domain.Route, error) {
	var rs []domain.Route
	err := r.db.WithContext(ctx).
		Preload("Middlewares", func(db *gorm.DB) *gorm.DB { return db.Order("position ASC") }).
		Where("node_id = ? AND deleted_at IS NULL", nodeID).Order("created_at ASC").Find(&rs).Error
	return rs, err
}

// MaxPriority 自动分配优先级（data-model §6）。
// 聚合单行：Pluck 到 *int 空表时触发 "Scan without Next"，改 Raw+COALESCE。
func (r *RouteRepo) MaxPriority(ctx context.Context, nodeID string) (int, error) {
	var maxp int
	err := r.db.WithContext(ctx).Raw(
		"SELECT COALESCE(MAX(priority), 0) FROM routes WHERE node_id = ? AND deleted_at IS NULL",
		nodeID).Scan(&maxp).Error
	return maxp, err
}

// ReplaceMiddlewares 有序绑定整组替换（FR-018）。
func (r *RouteRepo) ReplaceMiddlewares(ctx context.Context, routeID string, mwIDs []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("route_id = ?", routeID).Delete(&domain.RouteMiddleware{}).Error; err != nil {
			return err
		}
		for i, mw := range mwIDs {
			if err := tx.Create(&domain.RouteMiddleware{RouteID: routeID, MiddlewareID: mw, Position: i}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- Middleware ----

type MiddlewareRepo struct{ db *gorm.DB }

func NewMiddlewareRepo(db *gorm.DB) *MiddlewareRepo { return &MiddlewareRepo{db: db} }

func (r *MiddlewareRepo) List(ctx context.Context, q ListQuery) ([]domain.Middleware, int64, error) {
	return listScanned[domain.Middleware](ctx, r.db.Model(&domain.Middleware{}), q, "name ASC")
}

func (r *MiddlewareRepo) Get(ctx context.Context, id string) (*domain.Middleware, error) {
	var m domain.Middleware
	if err := r.db.WithContext(ctx).First(&m, "id = ?", id).Error; err != nil {
		if errors.Is(err, errNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

func (r *MiddlewareRepo) Create(ctx context.Context, m *domain.Middleware) error {
	m.RowVersion = 1
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *MiddlewareRepo) Update(ctx context.Context, m *domain.Middleware, expected int64, fields map[string]any) error {
	return UpdateOptimistic(ctx, r.db, m, expected, fields)
}

func (r *MiddlewareRepo) SoftDelete(ctx context.Context, id string) error {
	var m domain.Middleware
	m.ID = id
	return SoftDelete(ctx, r.db, &m)
}

func (r *MiddlewareRepo) NameTaken(ctx context.Context, nodeID, name string) (bool, error) {
	var c int64
	err := r.db.WithContext(ctx).Model(&domain.Middleware{}).
		Where("node_id = ? AND name = ? AND deleted_at IS NULL", nodeID, name).Count(&c).Error
	return c > 0, err
}

// ReferencingRoutes：删除阻止时逐条列出的引用路由（AC-015/V-3⑥）。
func (r *MiddlewareRepo) ReferencingRoutes(ctx context.Context, middlewareID string) ([]domain.Route, error) {
	var rs []domain.Route
	err := r.db.WithContext(ctx).
		Joins("JOIN route_middlewares rm ON rm.route_id = routes.id").
		Where("rm.middleware_id = ? AND routes.status <> 'archived' AND routes.deleted_at IS NULL", middlewareID).
		Order("routes.name ASC").Find(&rs).Error
	return rs, err
}
