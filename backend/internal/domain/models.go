// Package domain 是业务实体与规则（宪法 XII 调用链核心层）。
// 实体字段与 data-model.md §1–§13 逐一对应；校验方法在 service 层调用。
// gorm 标签仅描述持久化映射（保持一层模型，避免贫血 DTO 复制；访问一律经 pgstore repo）。
package domain

import (
	"time"

	"gorm.io/gorm"
)

// Base：id/created/updated/row_version/软删（data-model 通用约定）。
type Base struct {
	ID         string         `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	CreatedBy  *string        `gorm:"type:uuid" json:"created_by"`
	UpdatedBy  *string        `gorm:"type:uuid" json:"updated_by"`
	RowVersion int64          `gorm:"not null;default:1" json:"row_version"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

func (b *Base) UID() string    { return b.ID }
func (b *Base) RowRef() *int64 { return &b.RowVersion }

// SetActor 写入创建/变更者（uuid；审计列可空，故用指针）。
func (b *Base) SetActor(actorID string) {
	if actorID == "" {
		return
	}
	b.CreatedBy = &actorID
	b.UpdatedBy = &actorID
}

// ---- 1. User / Role（FR-036）----

type Role string

const (
	RoleSuperAdmin   Role = "super_admin"
	RoleGatewayAdmin Role = "gateway_admin"
	RoleDeveloper    Role = "developer"
	RoleViewer       Role = "viewer"
)

func ValidRole(r string) bool {
	switch Role(r) {
	case RoleSuperAdmin, RoleGatewayAdmin, RoleDeveloper, RoleViewer:
		return true
	}
	return false
}

// CanManagePlatform：高级模式/回滚/审批等治理动作的最低角色（gateway_admin+）。
func RoleCan(r Role, advanced, production bool) bool {
	switch r {
	case RoleSuperAdmin:
		return true
	case RoleGatewayAdmin:
		return true
	case RoleDeveloper:
		return !advanced && !production
	case RoleViewer:
		return false
	}
	return false
}

type User struct {
	Base
	Username     string     `gorm:"type:citext;not null" json:"username"`
	DisplayName  string     `gorm:"not null" json:"display_name"`
	PasswordHash string     `gorm:"column:password_hash;not null" json:"-"`
	Role         Role       `gorm:"not null" json:"role"`
	Status       string     `gorm:"not null;default:active" json:"status"` // active/disabled
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

func (u *User) TableName() string { return "users" }

// RefreshToken（旋转式会话，R6；disabled 即时吊销=删/失效行）。
type RefreshToken struct {
	ID        string    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    string    `gorm:"type:uuid;not null;index"`
	TokenHash string    `gorm:"not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null"`
	Revoked   bool      `gorm:"not null;default:false"`
	CreatedAt time.Time
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

// ---- 2. GatewayNode + NodeState（FR-001~003/040）----

type EnvType string

const (
	EnvDevelopment EnvType = "development"
	EnvTest        EnvType = "test"
	EnvStaging     EnvType = "staging"
	EnvProduction  EnvType = "production"
)

func ValidEnvType(s string) bool {
	switch EnvType(s) {
	case EnvDevelopment, EnvTest, EnvStaging, EnvProduction:
		return true
	}
	return false
}

type GatewayNode struct {
	Base
	Name             string  `gorm:"type:citext;not null" json:"name"`
	BaseURL          string  `gorm:"not null" json:"base_url"`
	DeployRoot       string  `gorm:"not null" json:"deploy_root"`
	EnvType          EnvType `gorm:"not null" json:"env_type"`
	Remark           string  `json:"remark,omitempty"`
	Enabled          bool    `gorm:"not null;default:true" json:"enabled"`
	APIAuthEncrypted []byte  `gorm:"column:api_auth_encrypted" json:"-"` // R7：永不出 API
}

func (n *GatewayNode) TableName() string { return "gateway_nodes" }

type NodeState struct {
	NodeID              string           `gorm:"type:uuid;primaryKey;column:node_id" json:"node_id"`
	Status              string           `gorm:"not null;default:unknown" json:"status"` // online/offline/degraded/unknown
	TraefikVersion      string           `json:"traefik_version,omitempty"`
	LastProbeAt         *time.Time       `json:"last_probe_at,omitempty"`
	LastOnlineAt        *time.Time       `json:"last_online_at,omitempty"`
	ConsecutiveFailures int              `gorm:"not null;default:0" json:"consecutive_failures"`
	LoadedRouters       map[string]any   `gorm:"type:jsonb;serializer:json" json:"loaded_routers,omitempty"`
	LoadedServices      map[string]any   `gorm:"type:jsonb;serializer:json" json:"loaded_services,omitempty"`
	LoadedMiddlewares   map[string]any   `gorm:"type:jsonb;serializer:json" json:"loaded_middlewares,omitempty"`
	Drift               bool             `gorm:"not null;default:false" json:"drift"`
	DriftDetail         []map[string]any `gorm:"type:jsonb;serializer:json" json:"drift_detail,omitempty"`
	DesiredVersion      int64            `gorm:"not null;default:0" json:"desired_version"`
	ActualVersion       int64            `gorm:"not null;default:0" json:"actual_version"`
	UpdatedAt           time.Time        `json:"updated_at"`
}

func (NodeState) TableName() string { return "node_states" }

// ---- 3. Domain（FR-005~008）----

type HTTPSPolicy string

const (
	PolicyOff      HTTPSPolicy = "off"
	PolicyAcmeHTTP HTTPSPolicy = "acme_http"
	PolicyAcmeDNS  HTTPSPolicy = "acme_dns"
	PolicyImported HTTPSPolicy = "imported"
)

func ValidHTTPSPolicy(s string) bool {
	switch HTTPSPolicy(s) {
	case PolicyOff, PolicyAcmeHTTP, PolicyAcmeDNS, PolicyImported:
		return true
	}
	return false
}

type Domain struct {
	Base
	NodeID           string      `gorm:"type:uuid;not null" json:"node_id"`
	Name             string      `gorm:"type:citext;not null" json:"name"`
	IsWildcard       bool        `gorm:"->;column:is_wildcard" json:"is_wildcard"` // 生成列（迁移中 STORED）
	HTTPSPolicy      HTTPSPolicy `gorm:"column:https_policy;not null;default:off" json:"https_policy"`
	CertResolverRef  string      `json:"cert_resolver_ref,omitempty"`
	DNSSCredentialID *string     `gorm:"column:dns_credential_id;type:uuid" json:"dns_credential_id,omitempty"`
	ImportedCertID   *string     `gorm:"type:uuid" json:"imported_cert_id,omitempty"`
	ExpiryWarnDays   int         `gorm:"not null;default:30" json:"expiry_warn_days"`
	Enabled          bool        `gorm:"not null;default:true" json:"enabled"`
}

func (d *Domain) TableName() string { return "domains" }

// ---- 4/5. Service / Target（FR-009~012）----

type Service struct {
	Base
	NodeID          string `gorm:"type:uuid;not null" json:"node_id"`
	Name            string `gorm:"type:citext;not null" json:"name"`
	Description     string `json:"description,omitempty"`
	HealthcheckPath string `json:"healthcheck_path,omitempty"`
	IntervalSec     int    `gorm:"not null;default:30" json:"interval_sec"`
	TimeoutSec      int    `gorm:"not null;default:2" json:"timeout_sec"`
	ExpectedCodes   string `gorm:"not null;default:2xx-3xx" json:"expected_codes"`
	Enabled         bool   `gorm:"not null;default:true" json:"enabled"`
}

func (s *Service) TableName() string { return "services" }

type Target struct {
	Base
	ServiceID       string     `gorm:"type:uuid;not null" json:"service_id"`
	URL             string     `gorm:"not null" json:"url"`
	Weight          int        `gorm:"not null;default:1" json:"weight"`
	Enabled         bool       `gorm:"not null;default:true" json:"enabled"`
	HealthStatus    string     `gorm:"not null;default:unknown" json:"health_status"` // up/down/unknown（平台探测，R13）
	HealthCheckedAt *time.Time `json:"health_checked_at,omitempty"`
}

func (t *Target) TableName() string { return "targets" }

// ServiceHealth 聚合派生（只读，非存储事实，data-model §4）。
func ServiceHealth(enabledTargets []Target) string {
	if len(enabledTargets) == 0 {
		return "down"
	}
	up := 0
	known := 0
	for _, t := range enabledTargets {
		switch t.HealthStatus {
		case "up":
			up++
			known++
		case "down":
			known++
		}
	}
	if known == 0 {
		return "unknown"
	}
	switch {
	case up == len(enabledTargets):
		return "healthy"
	case up == 0:
		return "down"
	default:
		return "degraded"
	}
}

// ---- 6. Route / RouteMiddleware（FR-013~019）----

type Route struct {
	Base
	NodeID       string            `gorm:"type:uuid;not null" json:"node_id"`
	Name         string            `gorm:"type:citext;not null" json:"name"`
	Mode         string            `gorm:"not null;default:simple" json:"mode"` // simple/advanced
	DomainID     *string           `gorm:"type:uuid" json:"domain_id,omitempty"`
	Path         string            `json:"path,omitempty"`
	MatchType    string            `gorm:"not null;default:prefix" json:"match_type"` // exact/prefix
	ServiceID    string            `gorm:"type:uuid;not null" json:"service_id"`
	HTTPS        bool              `gorm:"column:https;not null;default:false" json:"https"`
	Priority     int               `gorm:"not null;default:0" json:"priority"`
	AdvancedRule string            `json:"advanced_rule,omitempty"`
	Status       string            `gorm:"not null;default:draft" json:"status"` // draft/enabled/disabled/archived
	Middlewares  []RouteMiddleware `gorm:"foreignKey:RouteID" json:"middlewares,omitempty"`
}

func (r *Route) TableName() string { return "routes" }

// RouteMiddleware 有序绑定（FR-018；配置不冗余存储，仅引用，FR-022）。
type RouteMiddleware struct {
	ID           string `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	RouteID      string `gorm:"type:uuid;not null;uniqueIndex:rm_route_mw"`
	MiddlewareID string `gorm:"type:uuid;not null;uniqueIndex:rm_route_mw"`
	Position     int    `gorm:"not null"`
}

func (RouteMiddleware) TableName() string { return "route_middlewares" }

// ---- 7. Middleware（FR-020/021）----

type Middleware struct {
	Base
	NodeID  string         `gorm:"type:uuid;not null" json:"node_id"`
	Name    string         `gorm:"type:citext;not null" json:"name"`
	Type    string         `gorm:"not null" json:"type"`
	Params  map[string]any `gorm:"type:jsonb;serializer:json" json:"params"`
	Enabled bool           `gorm:"not null;default:true" json:"enabled"`
}

func (m *Middleware) TableName() string { return "middlewares" }

// ---- 8. ConfigVersion（不可变，FR-027/032）----

type ConfigVersion struct {
	Base
	NodeID          string            `gorm:"type:uuid;not null" json:"node_id"`
	Version         int64             `gorm:"not null" json:"version"`
	Status          string            `gorm:"not null;default:pending" json:"status"`
	Snapshot        map[string]any    `gorm:"type:jsonb;serializer:json" json:"snapshot,omitempty"` // 列表不回传（SC-006）
	ArtifactFiles   map[string]string `gorm:"type:jsonb;column:artifact_files;serializer:json" json:"artifact_files,omitempty"`
	ChangesSummary  map[string]any    `gorm:"type:jsonb;serializer:json" json:"changes_summary,omitempty"`
	ParentVersionID *string           `gorm:"type:uuid" json:"parent_version_id,omitempty"`
	Origin          string            `gorm:"not null;default:forward" json:"origin"` // forward/rollback
	SourceVersionID *string           `gorm:"type:uuid" json:"source_version_id,omitempty"`
}

func (v *ConfigVersion) TableName() string { return "config_versions" }

// ---- 9. Deployment（FR-028/030）----

type Deployment struct {
	Base
	NodeID             string           `gorm:"type:uuid;not null" json:"node_id"`
	ConfigVersionID    string           `gorm:"type:uuid;not null" json:"config_version_id"`
	Trigger            string           `gorm:"not null;default:deploy" json:"trigger"` // deploy/rollback/retry
	Status             string           `gorm:"not null;default:pending" json:"status"`
	ConfirmedAt        *time.Time       `json:"confirmed_at,omitempty"`
	ConfirmedBy        *string          `gorm:"type:uuid" json:"confirmed_by,omitempty"`
	ApprovalID         *string          `gorm:"type:uuid" json:"approval_id,omitempty"`
	VerificationResult map[string]any   `gorm:"type:jsonb;serializer:json" json:"verification_result,omitempty"`
	ErrorCode          string           `json:"error_code,omitempty"`
	ErrorMessage       string           `json:"error_message,omitempty"`
	Logs               []map[string]any `gorm:"type:jsonb;serializer:json" json:"logs,omitempty"`
}

func (d *Deployment) TableName() string { return "deployments" }

// ---- 10. ReleaseRequest（FR-037；reviewed_by<>submitted_by 由 DB CHECK 兜底）----

type ReleaseRequest struct {
	Base
	NodeID          string     `gorm:"type:uuid;not null" json:"node_id"`
	ConfigVersionID string     `gorm:"type:uuid;not null" json:"config_version_id"`
	SubmittedBy     string     `gorm:"type:uuid;not null" json:"submitted_by"`
	Status          string     `gorm:"not null;default:pending" json:"status"`
	ReviewedBy      *string    `gorm:"type:uuid" json:"reviewed_by,omitempty"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty"`
	Comment         string     `json:"comment,omitempty"`
}

func (r *ReleaseRequest) TableName() string { return "release_requests" }

// ---- 11. SecretCredential（FR-035；值永不出 API，仅 fingerprint）----

type SecretCredential struct {
	Base
	Name            string `gorm:"type:citext;not null" json:"name"`
	Provider        string `gorm:"not null" json:"provider"` // lego 白名单
	Kind            string `gorm:"not null;default:dns_provider" json:"kind"`
	DataEncrypted   []byte `gorm:"column:data_encrypted" json:"-"`
	DataFingerprint string `gorm:"column:data_fingerprint;size:8" json:"fingerprint"`
}

func (c *SecretCredential) TableName() string { return "secret_credentials" }

// ---- 12. Certificate（只读观测，FR-008/033；私钥加密列）----

type Certificate struct {
	ID                  string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	NodeID              *string    `gorm:"type:uuid" json:"node_id,omitempty"`
	DomainID            *string    `gorm:"type:uuid" json:"domain_id,omitempty"`
	Name                string     `json:"name"`
	Source              string     `gorm:"not null" json:"source"` // acme/imported
	NotBefore           *time.Time `json:"not_before,omitempty"`
	NotAfter            *time.Time `json:"not_after,omitempty"`
	Issuer              string     `json:"issuer,omitempty"`
	Sans                []string   `gorm:"type:text[];serializer:json" json:"sans,omitempty"`
	Status              string     `gorm:"not null;default:unknown" json:"status"` // valid/expiring_soon/expired/missing/unknown
	PrivateKeyEncrypted []byte     `gorm:"column:private_key_encrypted" json:"-"`
	CertPEM             string     `gorm:"column:cert_pem" json:"cert_pem,omitempty"` // 公钥链（下发材料；详情可选展示）
	ObservedAt          *time.Time `json:"observed_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (c *Certificate) GetDomainID() string {
	if c.DomainID != nil {
		return *c.DomainID
	}
	return ""
}

func (Certificate) TableName() string { return "certificates" }

// ---- 13. AuditLog（append-only，FR-038；分区表，仅 INSERT）----

type AuditLog struct {
	ID            string         `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	OccurredAt    time.Time      `gorm:"not null;primaryKey" json:"occurred_at"` // 分区键
	ActorID       string         `gorm:"type:uuid" json:"actor_id"`
	ActorUsername string         `json:"actor_username"` // 快照列（用户删除后不失语）
	Action        string         `gorm:"not null" json:"action"`
	ResourceType  string         `json:"resource_type"`
	ResourceID    string         `json:"resource_id"`
	ResourceName  string         `json:"resource_name"`
	Before        map[string]any `gorm:"type:jsonb;serializer:json" json:"before,omitempty"`
	After         map[string]any `gorm:"type:jsonb;serializer:json" json:"after,omitempty"`
	IP            string         `json:"ip"`
	UserAgent     string         `json:"user_agent,omitempty"`
	RequestID     string         `json:"request_id"`
}

func (AuditLog) TableName() string { return "audit_logs" }

// ---- PlatformSettings（spec Assumptions 默认值承载）----

type PlatformSettings struct {
	ID               int       `gorm:"primaryKey;default:1" json:"-"`
	ProbeIntervalSec int       `gorm:"not null;default:30" json:"probe_interval_sec"`
	OfflineThreshold int       `gorm:"not null;default:3" json:"offline_threshold"`
	ExpiryWarnDays   int       `gorm:"not null;default:30" json:"expiry_warn_days"`
	Timezone         string    `gorm:"not null;default:Asia/Shanghai" json:"timezone"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (PlatformSettings) TableName() string { return "platform_settings" }
