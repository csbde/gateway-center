-- +goose Up
-- 0001_init：data-model.md 全部表；唯一约束一律为部分唯一索引（WHERE deleted_at IS NULL，
-- 软删后名称可复用，data-model 通用约定）。

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- 1. users
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  username CITEXT NOT NULL,
  display_name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('super_admin','gateway_admin','developer','viewer')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  last_login_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX users_username_uniq ON users (username) WHERE deleted_at IS NULL;
CREATE TRIGGER users_upd BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE refresh_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id),
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  revoked BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);

-- 2. gateway_nodes + node_states（期望/实际态分离，宪法 XI）
CREATE TABLE gateway_nodes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  name CITEXT NOT NULL,
  base_url TEXT NOT NULL,
  deploy_root TEXT NOT NULL,
  env_type TEXT NOT NULL CHECK (env_type IN ('development','test','staging','production')),
  remark TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT true,
  api_auth_encrypted BYTEA
);
CREATE UNIQUE INDEX nodes_name_uniq ON gateway_nodes (name) WHERE deleted_at IS NULL;
CREATE TRIGGER nodes_upd BEFORE UPDATE ON gateway_nodes FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE node_states (
  node_id UUID PRIMARY KEY REFERENCES gateway_nodes(id),
  status TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('online','offline','degraded','unknown')),
  traefik_version TEXT NOT NULL DEFAULT '',
  last_probe_at TIMESTAMPTZ,
  last_online_at TIMESTAMPTZ,
  consecutive_failures INT NOT NULL DEFAULT 0,
  loaded_routers JSONB,
  loaded_services JSONB,
  loaded_middlewares JSONB,
  drift BOOLEAN NOT NULL DEFAULT false,
  drift_detail JSONB,
  desired_version BIGINT NOT NULL DEFAULT 0,
  actual_version BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 11. secret_credentials（先建，domains 引用）
CREATE TABLE secret_credentials (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  name CITEXT NOT NULL,
  provider TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'dns_provider' CHECK (kind IN ('dns_provider','traefik_api','other')),
  data_encrypted BYTEA NOT NULL,
  data_fingerprint CHAR(8) NOT NULL
);
CREATE UNIQUE INDEX credentials_name_uniq ON secret_credentials (name) WHERE deleted_at IS NULL;
CREATE TRIGGER credentials_upd BEFORE UPDATE ON secret_credentials FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 12. certificates
CREATE TABLE certificates (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  domain_id UUID NOT NULL,
  source TEXT NOT NULL CHECK (source IN ('acme','imported')),
  not_before TIMESTAMPTZ,
  not_after TIMESTAMPTZ,
  issuer TEXT NOT NULL DEFAULT '',
  sans TEXT[] NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('valid','expiring_soon','expired','missing','unknown')),
  private_key_encrypted BYTEA,
  cert_pem TEXT NOT NULL DEFAULT '',
  observed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX certificates_domain_uniq ON certificates (domain_id);
CREATE TRIGGER certificates_upd BEFORE UPDATE ON certificates FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 3. domains（is_wildcard 生成列；泛域名+acme_http 拒绝在应用层+CHECK 双保险）
CREATE TABLE domains (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  node_id UUID NOT NULL REFERENCES gateway_nodes(id),
  name CITEXT NOT NULL,
  is_wildcard BOOLEAN GENERATED ALWAYS AS (name LIKE '*.%') STORED,
  https_policy TEXT NOT NULL DEFAULT 'off' CHECK (https_policy IN ('off','acme_http','acme_dns','imported')),
  cert_resolver_ref TEXT NOT NULL DEFAULT '',
  dns_credential_id UUID REFERENCES secret_credentials(id),
  imported_cert_id UUID REFERENCES certificates(id),
  expiry_warn_days INT NOT NULL DEFAULT 30,
  enabled BOOLEAN NOT NULL DEFAULT true,
  CONSTRAINT domains_wildcard_dns CHECK (NOT (is_wildcard AND https_policy = 'acme_http')), -- FR-034
  CONSTRAINT domains_dns_needs_credential CHECK (https_policy <> 'acme_dns' OR dns_credential_id IS NOT NULL),
  CONSTRAINT domains_imported_needs_cert CHECK (https_policy <> 'imported' OR imported_cert_id IS NOT NULL)
);
CREATE UNIQUE INDEX domains_node_name_uniq ON domains (node_id, name) WHERE deleted_at IS NULL;
CREATE TRIGGER domains_upd BEFORE UPDATE ON domains FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 4/5. services / targets
CREATE TABLE services (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  node_id UUID NOT NULL REFERENCES gateway_nodes(id),
  name CITEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  healthcheck_path TEXT NOT NULL DEFAULT '',
  interval_sec INT NOT NULL DEFAULT 30,
  timeout_sec INT NOT NULL DEFAULT 2,
  expected_codes TEXT NOT NULL DEFAULT '2xx-3xx',
  enabled BOOLEAN NOT NULL DEFAULT true
);
CREATE UNIQUE INDEX services_node_name_uniq ON services (node_id, name) WHERE deleted_at IS NULL;
CREATE TRIGGER services_upd BEFORE UPDATE ON services FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE targets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  service_id UUID NOT NULL REFERENCES services(id),
  url TEXT NOT NULL,
  weight INT NOT NULL DEFAULT 1 CHECK (weight BETWEEN 1 AND 65535),
  enabled BOOLEAN NOT NULL DEFAULT true,
  health_status TEXT NOT NULL DEFAULT 'unknown' CHECK (health_status IN ('up','down','unknown')),
  health_checked_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX targets_service_url_uniq ON targets (service_id, url) WHERE deleted_at IS NULL;
CREATE TRIGGER targets_upd BEFORE UPDATE ON targets FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 7. middlewares（先于 routes：route_middlewares 引用）
CREATE TABLE middlewares (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  node_id UUID NOT NULL REFERENCES gateway_nodes(id),
  name CITEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('security_headers','ip_allowlist','rate_limit','redirect','strip_prefix')),
  params JSONB NOT NULL DEFAULT '{}',
  enabled BOOLEAN NOT NULL DEFAULT true
);
CREATE UNIQUE INDEX middlewares_node_name_uniq ON middlewares (node_id, name) WHERE deleted_at IS NULL;
CREATE TRIGGER middlewares_upd BEFORE UPDATE ON middlewares FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 6. routes / route_middlewares
CREATE TABLE routes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  node_id UUID NOT NULL REFERENCES gateway_nodes(id),
  name CITEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'simple' CHECK (mode IN ('simple','advanced')),
  domain_id UUID REFERENCES domains(id),
  path TEXT NOT NULL DEFAULT '',
  match_type TEXT NOT NULL DEFAULT 'prefix' CHECK (match_type IN ('exact','prefix')),
  service_id UUID NOT NULL REFERENCES services(id),
  https BOOLEAN NOT NULL DEFAULT false,
  priority INT NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 32767),
  advanced_rule TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','enabled','disabled','archived')),
  CONSTRAINT routes_mode_consistency CHECK (
    (mode = 'simple' AND advanced_rule = '' AND domain_id IS NOT NULL AND path <> '')
    OR (mode = 'advanced' AND advanced_rule <> '')
  )
);
CREATE UNIQUE INDEX routes_node_name_uniq ON routes (node_id, name) WHERE deleted_at IS NULL;
CREATE TRIGGER routes_upd BEFORE UPDATE ON routes FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE route_middlewares (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  route_id UUID NOT NULL REFERENCES routes(id),
  middleware_id UUID NOT NULL REFERENCES middlewares(id),
  position INT NOT NULL CHECK (position >= 0),
  UNIQUE (route_id, middleware_id),
  UNIQUE (route_id, position)
);

-- 8. config_versions（不可变：status 单向迁移；DELETE 在 API 与 DB 双层禁止）
CREATE TABLE config_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  node_id UUID NOT NULL REFERENCES gateway_nodes(id),
  version BIGINT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','validating','ready','failed')),
  snapshot JSONB NOT NULL,
  artifact_files JSONB,
  changes_summary JSONB,
  parent_version_id UUID REFERENCES config_versions(id),
  origin TEXT NOT NULL DEFAULT 'forward' CHECK (origin IN ('forward','rollback')),
  source_version_id UUID REFERENCES config_versions(id)
);
CREATE UNIQUE INDEX cv_node_version_uniq ON config_versions (node_id, version);
CREATE TRIGGER cv_upd BEFORE UPDATE ON config_versions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 9. deployments
CREATE TABLE deployments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  node_id UUID NOT NULL REFERENCES gateway_nodes(id),
  config_version_id UUID NOT NULL REFERENCES config_versions(id),
  trigger TEXT NOT NULL DEFAULT 'deploy' CHECK (trigger IN ('deploy','rollback','retry')),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','validating','ready','deploying','success','failed')),
  confirmed_at TIMESTAMPTZ,
  confirmed_by UUID,
  approval_id UUID,
  verification_result JSONB,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  logs JSONB NOT NULL DEFAULT '[]'
);
-- 同节点同时仅一个非终态 Deployment（并发发布 409 DEPLOY_IN_PROGRESS）
CREATE UNIQUE INDEX deployments_node_active_uniq ON deployments (node_id)
  WHERE status IN ('pending','validating','ready','deploying') AND deleted_at IS NULL;
CREATE TRIGGER dep_upd BEFORE UPDATE ON deployments FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 10. release_requests（提交人≠批准人：DB CHECK 兜底，FR-037/US6-AC2）
CREATE TABLE release_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by UUID, updated_by UUID,
  row_version BIGINT NOT NULL DEFAULT 1,
  deleted_at TIMESTAMPTZ,
  node_id UUID NOT NULL REFERENCES gateway_nodes(id),
  config_version_id UUID NOT NULL REFERENCES config_versions(id),
  submitted_by UUID NOT NULL REFERENCES users(id),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
  reviewed_by UUID REFERENCES users(id),
  reviewed_at TIMESTAMPTZ,
  comment TEXT NOT NULL DEFAULT '',
  CONSTRAINT release_requests_no_self_approve CHECK (reviewed_by IS NULL OR reviewed_by <> submitted_by)
);
CREATE INDEX release_requests_pending_idx ON release_requests (node_id, status) WHERE status = 'pending';

-- 13. audit_logs：按月 RANGE 分区（NFR-AUD-01；revoke UPDATE/DELETE 见 0002）
CREATE TABLE audit_logs (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  occurred_at TIMESTAMPTZ NOT NULL,
  actor_id UUID,
  actor_username TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL DEFAULT '',
  resource_id TEXT NOT NULL DEFAULT '',
  resource_name TEXT NOT NULL DEFAULT '',
  before JSONB,
  after JSONB,
  ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  request_id TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (occurred_at, id)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE audit_logs_default PARTITION OF audit_logs DEFAULT;
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_id, occurred_at DESC);
CREATE INDEX audit_logs_resource_idx ON audit_logs (resource_type, resource_id);
CREATE INDEX audit_logs_action_idx ON audit_logs (action, occurred_at DESC);

-- platform_settings（单行）
CREATE TABLE platform_settings (
  id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  probe_interval_sec INT NOT NULL DEFAULT 30,
  offline_threshold INT NOT NULL DEFAULT 3,
  expiry_warn_days INT NOT NULL DEFAULT 30,
  timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO platform_settings (id) VALUES (1);

-- +goose Down
DROP TABLE IF EXISTS platform_settings, audit_logs_default, audit_logs, release_requests,
  deployments, config_versions, route_middlewares, routes, middlewares, targets, services,
  domains, certificates, secret_credentials, node_states, gateway_nodes, refresh_tokens, users CASCADE;
DROP FUNCTION IF EXISTS set_updated_at();
DROP EXTENSION IF EXISTS citext;
