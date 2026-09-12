// 手写视图类型：与 backend handlers 的 JSON 视图一一对应
// （generated/schema.d.ts 为 openapi 占位；npm run gen:api 后可替换）。
export interface Node {
  id: string;
  name: string;
  base_url: string;
  deploy_root: string;
  env_type: "development" | "test" | "staging" | "production";
  remark: string;
  enabled: boolean;
  has_api_auth: boolean;
  row_version: number;
  created_at: string;
  updated_at: string;
}

export interface NodeState {
  node_id: string;
  status: "unknown" | "online" | "offline" | "degraded";
  traefik_version: string;
  consecutive_failures: number;
  last_online_at: string | null;
  desired_version: number;
  actual_version: number;
  drift: boolean;
  drift_detail?: unknown[];
  updated_at: string;
}

/** drift_detail 条目（driftsvc.compareDrift 产出；宪章 XI 期望/实际差异）。 */
export interface DriftEntry {
  type: "missing" | "unexpected";
  resource: string; // router / service / middleware
  name: string;
}

export interface Domain {
  id: string;
  node_id: string;
  name: string;
  is_wildcard: boolean;
  https_policy: "off" | "acme_http" | "acme_dns" | "imported";
  cert_resolver_ref?: string;
  dns_credential_id?: string | null;
  imported_cert_id?: string | null;
  expiry_warn_days: number;
  enabled: boolean;
  row_version: number;
}

export interface Service {
  id: string;
  node_id: string;
  name: string;
  description?: string;
  healthcheck_path?: string;
  interval_sec: number;
  timeout_sec: number;
  expected_codes: string;
  enabled: boolean;
  row_version: number;
}

export interface Target {
  id: string;
  service_id: string;
  url: string;
  weight: number;
  enabled: boolean;
  health_status: "up" | "down" | "unknown";
  row_version: number;
}

export interface Route {
  id: string;
  node_id: string;
  name: string;
  mode: "simple" | "advanced";
  domain_id?: string | null;
  path?: string;
  match_type?: string;
  service_id: string;
  https: boolean;
  advanced_rule?: string;
  priority: number;
  status: "draft" | "enabled" | "disabled" | "archived";
  row_version: number;
  /** 后端 RouteMiddleware 无 json tag → PascalCase 字段；position 即绑定顺序（FR-018）。 */
  middlewares?: { MiddlewareID: string; Position: number }[];
}

export interface RouteView extends Route {
  generated_rule_preview: string;
}

export interface ValidationIssue {
  category: string;
  blocking: boolean;
  message: string;
  hint?: string;
  resource?: string;
  resource_id?: string;
}

export interface ValidateResult {
  node_id: string;
  version_id?: string;
  version?: number;
  valid: boolean;
  issues: ValidationIssue[];
  route_count: number;
}

export type MiddlewareType =
  | "security_headers"
  | "ip_allowlist"
  | "rate_limit"
  | "redirect"
  | "strip_prefix";

export interface Middleware {
  id: string;
  node_id: string;
  name: string;
  type: MiddlewareType;
  params: Record<string, unknown>;
  enabled: boolean;
  row_version: number;
  created_at?: string;
}

export interface MiddlewareView extends Middleware {
  referenced_by: string[];
}

export interface AdvancedValidateResult {
  valid: boolean;
  issues: { offset?: number; message: string }[];
  risk_notice: string;
  normalized_preview: string;
}

export interface ConfigVersion {
  id: string;
  node_id: string;
  version: number;
  status: "pending" | "validating" | "ready" | "failed";
  origin: string;
  changes_summary?: unknown[];
  created_by?: string | null;
  created_at: string;
  row_version: number;
}

export interface Deployment {
  deployment_id?: string; // POST 受理响应
  id?: string; // GET 详情
  node_id: string;
  config_version_id: string;
  trigger: string;
  status: "pending" | "validating" | "ready" | "deploying" | "success" | "failed";
  error_code?: string;
  error_message?: string;
  verification_result?: Record<string, unknown>;
  created_at: string;
}

export interface DiffResult {
  from_version: number;
  to_version: number;
  summary: Record<string, number>;
  items: {
    kind: string;
    name: string;
    change: "added" | "modified" | "removed";
    before?: unknown;
    after?: unknown;
  }[];
}

export interface PlatformSettings {
  probe_interval_sec: number;
  offline_threshold: number;
  expiry_warn_days: number;
  timezone: string;
  row_version: number;
}

export interface User {
  id: string;
  username: string;
  display_name: string;
  role: string;
  status?: string;
  last_login_at?: string | null;
  row_version?: number;
  created_at?: string;
  updated_at?: string;
}

// ---- US6：审计检索 / 发布审批 / 用户管理 ----

/** 审计日志（append-only，FR-038；GET /audit-logs 检索 + /audit-logs/{id}）。 */
export interface AuditLog {
  id: string;
  occurred_at: string;
  actor_id: string;
  actor_username: string;
  action: string;
  resource_type: string;
  resource_id: string;
  resource_name: string;
  before?: Record<string, unknown> | null;
  after?: Record<string, unknown> | null;
  ip: string;
  user_agent?: string;
  request_id: string;
}

/** 发布申请（FR-037 生产审批链；status: pending/approved/rejected/cancelled）。 */
export interface ReleaseRequest {
  id: string;
  node_id: string;
  config_version_id: string;
  submitted_by: string;
  status: "pending" | "approved" | "rejected" | "cancelled";
  reviewed_by?: string | null;
  reviewed_at?: string | null;
  comment?: string;
  row_version: number;
  created_at: string;
  updated_at: string;
}

// ---- US4：凭据 / 证书观测 / Dashboard 聚合 ----

/** DNS/API 凭证（FR-035；值永不出 API，仅 8 位 fingerprint）。 */
export interface SecretCredential {
  id: string;
  name: string;
  kind: "dns_provider" | "traefik_api" | "other";
  provider: string;
  fingerprint: string;
  row_version: number;
  created_at?: string;
}

export interface CredentialVerifyResult {
  ok: boolean;
  reason?: string;
}

/** 证书只读观测（FR-033；source=none 表示域名尚无证书）。 */
export interface CertificateView {
  source: "acme" | "imported" | "none";
  status: "valid" | "expiring_soon" | "expired" | "missing" | "unknown";
  not_before?: string | null;
  not_after?: string | null;
  issuer?: string;
  sans?: string[];
  observed_at?: string | null;
}

export interface DashboardSummary {
  nodes_online: number;
  nodes_offline: number;
  nodes_degraded: number;
  nodes_drift: number;
  counts: { nodes: number; domains: number; services: number; routes: number; middlewares: number };
  expiring_certificates: {
    domain_id: string;
    domain_name: string;
    status: string;
    not_after?: string | null;
    issuer?: string;
  }[];
  recent_deployments: {
    id: string;
    node_id: string;
    node_name: string;
    status: string;
    trigger: string;
    created_at: string;
  }[];
}

export type List<T> = { items: T[]; page: number; page_size: number; total: number };
