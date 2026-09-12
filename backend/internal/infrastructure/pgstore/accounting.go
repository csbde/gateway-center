// User / RefreshToken / SecretCredential / Certificate / ReleaseRequest / PlatformSettings / AuditLog 仓储
// （T015/T064/T076/T079/T025 文件路径）。
package pgstore

import (
	"context"
	"errors"
	"time"

	"gateway-center/backend/internal/domain"
	"gorm.io/gorm"
)

// ---- User ----

type UserRepo struct{ db *gorm.DB }

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Get(ctx context.Context, id string) (*domain.User, error) {
	var u domain.User
	if err := r.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	var u domain.User
	if err := r.db.WithContext(ctx).First(&u, "username = ?", username).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	u.RowVersion = 1
	return r.db.WithContext(ctx).Create(u).Error
}

func (r *UserRepo) Update(ctx context.Context, u *domain.User, expected int64, fields map[string]any) error {
	return UpdateOptimistic(ctx, r.db, u, expected, fields)
}

func (r *UserRepo) TouchLogin(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&domain.User{}).Where("id = ?", id).
		Update("last_login_at", time.Now().UTC()).Error
}

func (r *UserRepo) List(ctx context.Context, q ListQuery) ([]domain.User, int64, error) {
	return listScanned[domain.User](ctx, r.db.Model(&domain.User{}).Select("id", "username", "display_name", "role", "status", "last_login_at", "created_at", "updated_at", "row_version"), q, "username ASC", "username")
}

func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	var c int64
	err := r.db.WithContext(ctx).Model(&domain.User{}).Count(&c).Error
	return c, err
}

// SoftDelete 用户 + 吊销全部会话（data-model §1）。
func (r *UserRepo) SoftDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		u := domain.User{}
		u.ID = id
		if err := SoftDelete(ctx, tx, &u); err != nil {
			return err
		}
		return tx.Model(&domain.RefreshToken{}).Where("user_id = ?", id).
			Update("revoked", true).Error
	})
}

// ---- RefreshToken ----

type TokenRepo struct{ db *gorm.DB }

func NewTokenRepo(db *gorm.DB) *TokenRepo { return &TokenRepo{db: db} }

func (r *TokenRepo) Issue(ctx context.Context, userID, tokenHash string, ttl time.Duration) error {
	return r.db.WithContext(ctx).Create(&domain.RefreshToken{
		UserID: userID, TokenHash: tokenHash, ExpiresAt: time.Now().UTC().Add(ttl),
	}).Error
}

func (r *TokenRepo) Find(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := r.db.WithContext(ctx).First(&t, "token_hash = ?", tokenHash).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (r *TokenRepo) Revoke(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&domain.RefreshToken{}).Where("id = ?", id).
		Update("revoked", true).Error
}

// RevokeAllForUser：disabled 即时吊销（FR-036/R6）。
func (r *TokenRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	return r.db.WithContext(ctx).Model(&domain.RefreshToken{}).Where("user_id = ?", userID).
		Update("revoked", true).Error
}

// ---- SecretCredential ----

type CredentialRepo struct{ db *gorm.DB }

func NewCredentialRepo(db *gorm.DB) *CredentialRepo { return &CredentialRepo{db: db} }

func (r *CredentialRepo) List(ctx context.Context, q ListQuery) ([]domain.SecretCredential, int64, error) {
	return listScanned[domain.SecretCredential](ctx, r.db.Model(&domain.SecretCredential{}), q, "name ASC")
}

func (r *CredentialRepo) Get(ctx context.Context, id string) (*domain.SecretCredential, error) {
	var c domain.SecretCredential
	if err := r.db.WithContext(ctx).First(&c, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *CredentialRepo) Create(ctx context.Context, c *domain.SecretCredential) error {
	c.RowVersion = 1
	return r.db.WithContext(ctx).Create(c).Error
}

// Rotate 更新密文与指纹（FR-035：轮换不改域名绑定——只动本行）。
func (r *CredentialRepo) Rotate(ctx context.Context, c *domain.SecretCredential, expected int64, enc []byte, fp string) error {
	return UpdateOptimistic(ctx, r.db, c, expected, map[string]any{
		"data_encrypted": enc, "data_fingerprint": fp,
	})
}

func (r *CredentialRepo) SoftDelete(ctx context.Context, id string) error {
	var c domain.SecretCredential
	c.ID = id
	return SoftDelete(ctx, r.db, &c)
}

// ReferencingDomains：被引用则不可删（V-4/FR-035 处置语义）。
func (r *CredentialRepo) ReferencingDomains(ctx context.Context, id string) ([]domain.Domain, error) {
	var ds []domain.Domain
	err := r.db.WithContext(ctx).
		Where("dns_credential_id = ? AND deleted_at IS NULL", id).Find(&ds).Error
	return ds, err
}

// ---- Certificate ----

type CertRepo struct{ db *gorm.DB }

func NewCertRepo(db *gorm.DB) *CertRepo { return &CertRepo{db: db} }

func (r *CertRepo) Get(ctx context.Context, id string) (*domain.Certificate, error) {
	var c domain.Certificate
	if err := r.db.WithContext(ctx).First(&c, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *CertRepo) Create(ctx context.Context, c *domain.Certificate) error {
	return r.db.WithContext(ctx).Create(c).Error
}

func (r *CertRepo) ByDomain(ctx context.Context, domainID string) (*domain.Certificate, error) {
	var c domain.Certificate
	err := r.db.WithContext(ctx).First(&c, "domain_id = ?", domainID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &c, err
}

// UpsertObserved：探测任务写入观测结果（用户不可直接编辑，data-model §12）。
// 已有私钥/公钥材料（imported）保持不被 acme 观测覆盖。
func (r *CertRepo) UpsertObserved(ctx context.Context, c *domain.Certificate) error {
	existing, err := r.ByDomain(ctx, c.DomainID)
	if errors.Is(err, ErrNotFound) {
		return r.db.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}
	updates := map[string]any{
		"not_before": c.NotBefore, "not_after": c.NotAfter, "issuer": c.Issuer,
		"sans": c.Sans, "status": c.Status, "observed_at": c.ObservedAt,
		"source": c.Source,
	}
	if c.PrivateKeyEncrypted != nil {
		updates["private_key_encrypted"] = c.PrivateKeyEncrypted
		updates["cert_pem"] = c.CertPEM
	}
	return r.db.WithContext(ctx).Model(&domain.Certificate{}).Where("id = ?", existing.ID).Updates(updates).Error
}

func (r *CertRepo) ListAll(ctx context.Context) ([]domain.Certificate, error) {
	var cs []domain.Certificate
	err := r.db.WithContext(ctx).Find(&cs).Error
	return cs, err
}

// ---- ReleaseRequest ----

type ApprovalRepo struct{ db *gorm.DB }

func NewApprovalRepo(db *gorm.DB) *ApprovalRepo { return &ApprovalRepo{db: db} }

func (r *ApprovalRepo) Create(ctx context.Context, rr *domain.ReleaseRequest) error {
	rr.RowVersion = 1
	return r.db.WithContext(ctx).Create(rr).Error
}

func (r *ApprovalRepo) Get(ctx context.Context, id string) (*domain.ReleaseRequest, error) {
	var rr domain.ReleaseRequest
	if err := r.db.WithContext(ctx).First(&rr, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &rr, nil
}

// Review 批准/驳回（自批由应用层+DB CHECK 双层守卫，FR-037/US6-AC2）。
func (r *ApprovalRepo) Review(ctx context.Context, id, from, to string, reviewerID, comment string) error {
	res := r.db.WithContext(ctx).Model(&domain.ReleaseRequest{}).
		Where("id = ? AND status = ?", id, from).
		Updates(map[string]any{
			"status": to, "reviewed_by": reviewerID, "reviewed_at": time.Now().UTC(),
			"comment": comment, "row_version": gorm.Expr("row_version + 1"),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("审批单状态迁移非法或并发已处理")
	}
	return nil
}

func (r *ApprovalRepo) Cancel(ctx context.Context, id, submitterID string) error {
	res := r.db.WithContext(ctx).Model(&domain.ReleaseRequest{}).
		Where("id = ? AND status = 'pending' AND submitted_by = ?", id, submitterID).
		Updates(map[string]any{"status": "cancelled", "row_version": gorm.Expr("row_version + 1")})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("仅提交人可撤回待审的申请")
	}
	return nil
}

func (r *ApprovalRepo) List(ctx context.Context, q ListQuery) ([]domain.ReleaseRequest, int64, error) {
	return listScanned[domain.ReleaseRequest](ctx, r.db.Model(&domain.ReleaseRequest{}), q, "created_at DESC")
}

// ApprovedForVersion：production 部署门禁查询（R15 ApproveGate）。
func (r *ApprovalRepo) ApprovedForVersion(ctx context.Context, versionID string) (*domain.ReleaseRequest, error) {
	var rr domain.ReleaseRequest
	err := r.db.WithContext(ctx).
		Where("config_version_id = ? AND status = 'approved'", versionID).
		Order("reviewed_at DESC").First(&rr).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &rr, err
}

// ---- PlatformSettings ----

type SettingsRepo struct{ db *gorm.DB }

func NewSettingsRepo(db *gorm.DB) *SettingsRepo { return &SettingsRepo{db: db} }

func (r *SettingsRepo) Get(ctx context.Context) (*domain.PlatformSettings, error) {
	var s domain.PlatformSettings
	err := r.db.WithContext(ctx).First(&s, "id = 1").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &domain.PlatformSettings{ID: 1, ProbeIntervalSec: 30, OfflineThreshold: 3, ExpiryWarnDays: 30, Timezone: "Asia/Shanghai"}, nil
	}
	return &s, err
}

func (r *SettingsRepo) Save(ctx context.Context, s *domain.PlatformSettings) error {
	s.ID = 1
	return r.db.WithContext(ctx).Model(&domain.PlatformSettings{}).Where("id = 1").Updates(map[string]any{
		"probe_interval_sec": s.ProbeIntervalSec, "offline_threshold": s.OfflineThreshold,
		"expiry_warn_days": s.ExpiryWarnDays, "timezone": s.Timezone,
	}).Error
}

// ---- AuditLog（仅 INSERT；分区表，revoke 改删见 0002）----

type AuditRepo struct{ db *gorm.DB }

func NewAuditRepo(db *gorm.DB) *AuditRepo { return &AuditRepo{db: db} }

// Append 在事务内写入（宪章 X：有变更必有审计，R12）。
func (r *AuditRepo) Append(ctx context.Context, tx *gorm.DB, a *domain.AuditLog) error {
	if tx == nil {
		tx = r.db
	}
	return tx.WithContext(ctx).Create(a).Error
}

func (r *AuditRepo) Get(ctx context.Context, id string) (*domain.AuditLog, error) {
	var a domain.AuditLog
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &a, err
}

type AuditFilter struct {
	ActorID, Action, ResourceType, ResourceID string
	From, To                                  time.Time
}

func (r *AuditRepo) Search(ctx context.Context, f AuditFilter, q ListQuery) ([]domain.AuditLog, int64, error) {
	db := r.db.WithContext(ctx).Model(&domain.AuditLog{})
	if f.ActorID != "" {
		db = db.Where("actor_id = ?", f.ActorID)
	}
	if f.Action != "" {
		db = db.Where("action = ?", f.Action)
	}
	if f.ResourceType != "" {
		db = db.Where("resource_type = ?", f.ResourceType)
	}
	if f.ResourceID != "" {
		db = db.Where("resource_id = ?", f.ResourceID)
	}
	if !f.From.IsZero() {
		db = db.Where("occurred_at >= ?", f.From)
	}
	if !f.To.IsZero() {
		db = db.Where("occurred_at < ?", f.To)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []domain.AuditLog
	limit, offset := 50, 0
	if q.PageSize > 0 {
		limit, offset = q.PageSize, (q.Page-1)*q.PageSize
	}
	err := db.Order("occurred_at DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, total, err
}
