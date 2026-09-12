/**
 * 由 `npm run gen:api`（openapi-typescript）从 contracts/openapi.yaml 生成。
 * 当前为手工最小占位（仅覆盖已引用类型）；执行 gen:api 后将被完整替换。
 */
export interface paths {
  readonly [path: string]: unknown;
}

export interface components {
  readonly schemas: {
    Timestamps: {
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    Error: {
      error: {
        code: string;
        message: string;
        request_id?: string;
        details?: Array<{
          field?: string;
          message?: string;
          id?: string;
          hint?: string;
        }>;
      };
    };
    User: {
      id?: string;
      username?: string;
      display_name?: string;
      role: "super_admin" | "gateway_admin" | "developer" | "viewer";
      status?: "active" | "disabled";
      last_login_at?: string;
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    TokenPair: {
      access_token: string;
      refresh_token: string;
      expires_in?: number;
      user?: components["schemas"]["User"];
    };
    GatewayNode: {
      id?: string;
      name?: string;
      base_url?: string;
      deploy_root?: string;
      env_type?: "development" | "test" | "staging" | "production";
      remark?: string;
      enabled?: boolean;
      has_api_auth?: boolean;
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    Domain: {
      id?: string;
      node_id?: string;
      name?: string;
      https_policy?: "imported" | "acme_http" | "acme_dns" | "off";
      enabled?: boolean;
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    Service: {
      id?: string;
      node_id?: string;
      name?: string;
      description?: string;
      healthcheck_path?: string;
      interval_sec?: number;
      timeout_sec?: number;
      expected_codes?: string;
      health_status?: "unknown" | "healthy" | "degraded" | "down";
      enabled?: boolean;
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    Target: {
      id?: string;
      service_id?: string;
      url?: string;
      weight?: number;
      enabled?: boolean;
      health_status?: "unknown" | "up" | "down";
      checked_at?: string;
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    Route: {
      id?: string;
      node_id?: string;
      name?: string;
      mode?: "simple" | "advanced";
      domain_id?: string;
      service_id?: string;
      path?: string;
      match_type?: "exact" | "prefix";
      rule?: string;
      priority?: number;
      status?: "draft" | "enabled" | "disabled" | "archived";
      https?: boolean;
      middleware_ids?: string[];
      generated_rule_preview?: string;
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    ConfigVersion: {
      id?: string;
      node_id?: string;
      version?: number;
      status?: "pending" | "validating" | "ready" | "failed";
      origin?: "generate" | "rollback";
      parent_version_id?: string;
      source_version_id?: string;
      changes_summary?: Record<string, unknown>;
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    Deployment: {
      id?: string;
      node_id?: string;
      config_version_id?: string;
      trigger?: "deploy" | "rollback";
      status?: "pending" | "validating" | "ready" | "deploying" | "success" | "failed";
      verification_result?: Record<string, unknown>;
      approval_id?: string;
      confirmed_at?: string;
      confirmed_by?: string;
      created_at?: string;
      updated_at?: string;
      row_version?: number;
    };
    ValidationReport: {
      node_id?: string;
      version_id?: string;
      version?: number;
      valid?: boolean;
      issues?: Array<{
        category?: string;
        severity?: string;
        message?: string;
        resource?: string;
        resource_id?: string;
        hint?: string;
      }>;
      route_count?: number;
    };
    NodeState: {
      node_id?: string;
      status?: "online" | "offline" | "degraded" | "unknown";
      traefik_version?: string;
      last_online_at?: string;
      failures?: number;
      desired_version?: number;
      actual_version?: number;
      drift?: boolean;
      drift_detail?: Record<string, unknown>;
    };
    Dashboard: {
      nodes_online?: number;
      nodes_total?: number;
      drift_nodes?: number;
      services_total?: number;
      routes_total?: number;
      domains_total?: number;
      expiring_certificates?: number;
      recent_deployments?: components["schemas"]["Deployment"][];
    };
    PlatformSettings: {
      probe_interval_sec?: number;
      offline_threshold?: number;
      expiry_warn_days?: number;
      timezone?: string;
      updated_at?: string;
      row_version?: number;
    };
  };
}

export interface operations {
  readonly [name: string]: unknown;
}
