# Phase 1 Data Model: Gateway Center 一期

**Feature**: 001-gateway-management-platform | **Date**: 2026-09-12
**依据**: [spec.md](./spec.md) Key Entities/FR、[research.md](./research.md)（R7/R8/R9/R10/R12/R15）

通用约定：

- 所有聚合根表含 `id UUID PK`、`created_at/updated_at TIMESTAMPTZ`、`created_by/updated_by UUID FK users`、`row_version INT`（乐观锁，FR 并发编辑冲突→409，R10）。
- 「状态」列统一命名 `status`；启停用 `enabled BOOL`（与生命周期状态分列，避免混用）。
- 软删除：`deleted_at TIMESTAMPTZ NULL`（GORM 约定）；查询默认过滤。唯一约束均为**部分唯一索引**（`WHERE deleted_at IS NULL`），软删后名称可复用。
- 时间一律 UTC 存储、按平台时区展示（spec Assumption）。
- 敏感列命名 `*_encrypted BYTEA` + `*_fingerprint CHAR(8)`（AES-256-GCM，R7），API/日志/Diff/审计中只出现 fingerprint 与掩码。

### 实体追溯（宪法「实体种类固定」/ 原则 XII 范围纪律）

核心拓扑严格等于宪法与 spec Key Entities 所列 11 类（Gateway Node、Domain、Service、Target、Route、
Middleware、Certificate Policy、Config Version、Deployment、Audit Log、User/Role）。下表实体**不是新增能力**，
而是上述核心实体的必需承载体，逐个可追溯到一期 FR：

| 表 | 性质 | 来源 FR |
|---|---|---|
| NodeState | Gateway Node 的 Actual State 分离存储（不与期望态混用） | FR-002、FR-040（原则 XI） |
| RouteMiddleware | Route→Middleware 有序绑定关系（不冗余存配置） | FR-018、FR-022 |
| ReleaseRequest | production 审批链记录（提交人≠批准人） | FR-037（原则 IX） |
| SecretCredential | 加密凭证独立存储与轮换（绑定关系不随之改动） | FR-035（原则 VIII） |
| Certificate | 证书只读观测（生命周期仍归 Traefik/Provider） | FR-008、FR-033（原则 VII） |
| PlatformSettings | 探测周期、到期阈值、时区等默认值 | spec Assumptions |

未出现任何清单外能力（无 Agent、无 Docker 服务发现、无 TCP/UDP、无 K8s）。

实体拓扑（宪法「领域模型」，MUST NOT 增删实体种类）：

```text
User ─(RBAC)→ 全部资源
GatewayNode
 ├── Domain ──┐
 ├── Route ───┼→ Service → Target
 │     └──────┘     （Route ⇄ Middleware：有序多对多 RouteMiddleware）
 ├── Deployment → ConfigVersion → GatewayNode（快照自包含）
 ├── ReleaseRequest（production 审批）
 └── 节点侧只读观测：NodeState / Certificate（Actual State）
SecretCredential（DNS Provider 凭证等，独立加密存储）
AuditLog（append-only，全局）
```

---

## 1. User（平台用户）

| 字段 | 类型 | 约束/校验 |
|---|---|---|
| username | citext | 必填；3–32 `[a-z0-9_.-]`；全局唯一 |
| display_name | text | 必填 ≤64 |
| password_hash | text | bcrypt cost≥12；不出任何 API |
| role | enum | `super_admin / gateway_admin / developer / viewer`（FR-036） |
| status | enum | `active / disabled`；disabled 即时吊销会话（R6） |
| last_login_at | timestamptz | 只读 |

**规则**
- 初始 Super Admin 由部署期环境变量种子创建；其余用户仅 Super Admin 可建/改角色（权限变更进审计，FR-038）。
- 删除用户 = 软删 + 强制吊销 refresh token；历史审计中的 actor 以 `actor_username_snapshot` 冗余保留（审计不可解释性禁止）。

**关系**：`AuditLog.actor`、各实体 created_by/updated_by、Deployment.triggered_by、ReleaseRequest.submitted_by/approved_by → User。

---

## 2. GatewayNode（网关节点）

| 字段 | 类型 | 约束/校验 |
|---|---|---|
| name | citext | 必填 ≤64；全局唯一 |
| base_url | text | 必填；`http(s)://host:port`，Traefik API 地址 |
| api_auth_encrypted | bytea | 可选；Traefik API Basic 凭据（R7 加密） |
| deploy_root | text | 必填；dynamic 配置落盘根路径（R4，如 `/etc/traefik/`，平台写 `dynamic/` 子树） |
| env_type | enum | `development / test / staging / production`（FR-001） |
| remark | text | ≤256 |
| enabled | bool | 启停 |

**运行状态（Actual State，独立表 NodeState，1:1）**

| 字段 | 说明 |
|---|---|
| status | `online / offline / degraded / unknown`（FR-002；unknown=创建后未探测成功） |
| traefik_version | 探测回写（`/api/overview`） |
| last_probe_at / last_online_at | 最后成功探测/在线时间（FR-003） |
| consecutive_failures | ≥3 判 offline（spec 默认） |
| loaded_routers/services/middlewares | JSONB 摘要，实际加载集合（宪法 XI 展示与 diff 输入） |
| drift | bool + `drift_detail JSONB`（期望≠实际的差异项，R5） |
| desired_version / actual_version | 期望=最新 success ConfigVersion 号；实际=最近验证通过号 |

**状态机（生命周期）**：`active →(disable)→ inactive →(软删)→ deleted`；软删前置：无关联 Deployment（UF-1）。

**规则**
- 环境类型决定管控：`production` 发布强制审批链（FR-037）；其余 Developer 可直发但验证不可跳（R15）。
- 节点禁用/删除后：未完成部署冻结为 failed、生成排除其资源、Dashboard 计离线（Edge Case）。
- degraded 定义：API 可达但存在失败的启用 Target 或 Traefik 加载错误（spec Assumption）。

---

## 3. Domain（域名）

| 字段 | 类型 | 约束/校验 |
|---|---|---|
| node_id | FK GatewayNode | 必填；域名归属节点（FR-005） |
| name | citext | 必填；普通域名 `crm.example.com` 或泛域名 `*.dev.example.com`；RFC1123 label 规则，泛域名仅允许最左 `*.`；**节点内唯一** |
| is_wildcard | bool | 生成列（name 前缀派生），驱动证书约束 |
| https_policy | enum | `off / acme_http / acme_dns / imported`（FR-007） |
| cert_resolver_ref | text | acme_* 时必填：节点 static 预配 resolver 名（平台只引用不创建，宪法 III/VII） |
| dns_credential_id | FK SecretCredential | `acme_dns` 必填；泛域名 MUST 走 DNS Challenge（FR-034） |
| imported_cert_id | FK Certificate | `imported` 必填 |
| expiry_warn_days | int | 默认 30，可配置（spec Assumption） |
| enabled | bool | 启停 |

**不变式**
- `is_wildcard && https_policy == 'acme_http'` → 校验拒绝（FR-034/US4-AC1）。
- `https_policy != 'off'` 且无可用证书/私钥，或证书剩余有效期 < 预警阈值 → 验证告警项 + UI 醒目（FR-008/US4-AC2）；「缺可用证书」在发布验证中为**阻断项**（Edge Case：证书策略与实际不符 → 告警且发布前确认，运行时校验纳入证书加载检查）。
- `http→https` 重定向由 Redirect middleware 表达，域名侧不生成流量规则。
- 域名 MUST NOT 直接决定流量去向；仅 Route 引用建立 Host 关系（FR-006）。

**删除**：软删前依赖检查 → 被 N 条未归档 Route 引用则阻止并列清单（FR-039/US7）。

---

## 4. Service（逻辑上游服务）

| 字段 | 类型 | 约束/校验 |
|---|---|---|
| node_id | FK GatewayNode | 必填（配置以节点为作用域，FR-004） |
| name | citext | 必填 ≤63 `[a-z0-9-]`；节点内唯一 |
| description | text | ≤256 |
| healthcheck_path / interval_sec / timeout_sec / expected_codes | text/int | 可选探测参数（R13，默认 `/`、30、2、`2xx-3xx`） |
| enabled | bool | 启停 |

**聚合健康状态（只读派生，非存储事实）**：`healthy`（全部启用 Target up）/ `degraded`（部分 up）/ `down`（全 down 或无启用 Target）。UI 与 FR-011 使用；`unknown` = 尚无探测结果。

**不变式**：被启用 Route 引用且**无任何启用 Target** → 发布验证阻断（FR-012/Edge Case）。

**删除**：软删前依赖检查（被引用 Route 清单，FR-039）；Target 随 Service 级联软删。

---

## 5. Target（服务实例）

| 字段 | 类型 | 约束/校验 |
|---|---|---|
| service_id | FK Service | 必填 |
| url | text | 必填；`http(s)://host:port[/path]`，协议限 http/https（FR-010）；同 Service 内归一化唯一 |
| weight | int | 1–65535，默认 1（负载均衡权重） |
| enabled | bool | 启停（UF-2） |
| health_status / health_checked_at | enum/timestamptz | 只读：`up / down / unknown`（平台探测，R13） |

**规则**：Service→Traefik `servers` 映射为 WRR（weight 直传）；Target 类型（Docker/Host）MUST NOT 出现在模型与 UI（宪法 VI）。

---

## 6. Route（路由）

| 字段 | 类型 | 约束/校验 |
|---|---|---|
| node_id | FK GatewayNode | 必填 |
| name | citext | 必填 ≤63 `[a-z0-9-]`；节点内唯一 |
| mode | enum | `simple / advanced`（FR-015）；advanced 仅 gateway_admin+ 可保存（FR-016） |
| domain_id | FK Domain | simple 必填；必须同节点、enabled |
| path | text | simple 必填；以 `/` 开头，`[A-Za-z0-9/_:.\-~%]+`，≤256 |
| match_type | enum | `exact / prefix`（FR-013） |
| service_id | FK Service | 必填；同节点（FR-013/宪法 VI：只绑 Service） |
| https | bool | 依赖 domain.https_policy != off；关闭时生成 http 入口路由 |
| priority | int | 1–32767，默认按创建序自动分配；冲突检查用 |
| advanced_rule | text | advanced 必填：完整表达式白名单文法（R8）；simple MUST 为空 |
| entry_points | text[] | 派生：`web` / `websecure`（生成层约定名，节点 static 预配） |
| status | enum | `draft / enabled / disabled / archived`（FR-017） |

**状态机（Route，FR-017/宪法 XII）**：

```text
draft → enabled（要求校验通过且发布生效；草稿不可直接 deploy 目标态）
draft → archived          （任意草稿可废弃）
enabled ⇄ disabled
enabled|disabled → archived
archived → 终态（仅可查询/恢复为新草稿 MAY 不做）
```

禁用/归档 MUST NOT 影响历史快照（快照自包含，R8）。

**规则**
- 简单模式规则由生成层合成（FR-014）：`Host(\`d\`) && (Path(\`p\`) | PathPrefix(\`p\`))` + TLS 段；用户不书写语法。
- 高级模式：保存时语法验证（词法+函数白名单，FR-016/Edge Case）；风险提示 UI；每次使用审计 `route.advanced_edit`（宪法 X）；MUST NOT 破坏简单模式产物（分文件生成）。
- 同节点两条路由**匹配条件完全重叠** → 发布验证告警并阻断，需人工处置（FR-019/Edge Case）；精确域名与泛域名并存允许共存，按精确优先提示歧义（Edge Case）。
- 引用的 Domain/Service/Middleware 缺失、禁用、软删、跨节点 → 保存草稿与发布前两次验证拦截（FR-019/Edge Case）。
- 并发编辑：`row_version` 乐观锁（R10）。

**关系 RouteMiddleware（有序绑定）**

| 字段 | 约束 |
|---|---|
| route_id / middleware_id | 均 FK 且同节点；联合唯一 |
| position | int ≥0，连续编号；执行顺序即发布产物顺序（FR-018） |

中间件配置 MUST NOT 在 Route 内冗余存储，仅引用（FR-022）。

---

## 7. Middleware（中间件）

| 字段 | 类型 | 约束/校验 |
|---|---|---|
| node_id | FK GatewayNode | 必填（节点作用域） |
| name | citext | 必填 ≤63 `[a-z0-9-]`；节点内唯一 |
| type | enum | 一期五类（FR-021）：`security_headers / ip_allowlist / rate_limit / redirect / strip_prefix` |
| params | JSONB | 按类型结构体校验（R9 注册表）；字段级错误定位 |
| enabled | bool | 被启用 Route 引用且 disabled → 验证阻断 |

**params 按类型（校验规则即 AC/Edge Case 依据）**

| type | 字段 | 校验 |
|---|---|---|
| security_headers | `sts_seconds?`（0–63072000）、`sts_include_subdomains?`、`content_type_nosniff`（默认 true）、`frame_options`（`deny/sameorigin/allow-from`）、`referrer_policy?` | 枚举白名单；sts 非零时 frame_options 必填 |
| ip_allowlist | `cidrs[]` 1–100 项 | 每项合法 IPv4/IPv6 CIDR 或 IP；错误定位到项序号（FR-021） |
| rate_limit | `average` int ≥1、`burst` int ≥average、`period` ∈ {1s..1m}、`source` ∈ {ip, headers? 白名单} | 速率阈值 >0（US3-AC1） |
| redirect | 二选一：`scheme`（`https`/`http`）+`permanent?` 或 `host+path?` | http→https 即 scheme=https（FR-021）；禁自环（目标与来源同构时告警） |
| strip_prefix | `prefixes[]` 1–10，每项 `/` 开头 | 与 Route.path 前缀一致性告警 |

**删除**：被任意未归档 Route 引用 → 阻止 + 逐条列出引用路由（FR-039/AC-015/US3-AC3）；处置路径 Disable → Archive → 软删。

**扩展约束（NFR-MNT-01）**：新增 type = 注册 `Validate/ToTraefik` 两方法 + zod schema，管线/表不动。

---

## 8. ConfigVersion（配置版本，不可变）

| 字段 | 类型 | 说明 |
|---|---|---|
| node_id | FK | 版本以节点为作用域（FR-004/027） |
| version | bigint | 节点内单调递增（`max+1`，节点级 advisory lock 防并发跳号） |
| status | enum | `pending → validating → ready`（UF-4）；ready 后方可部署 |
| snapshot | JSONB | **自包含业务快照**（R8：路由/服务/中间件/TLS 全量内联，回滚不依赖当前库） |
| artifact_files | JSONB | 生成配置 `{path: content}`（不含明文 secret，R7） |
| changes_summary | JSONB | 相对上一版 diff（资源为单位 added/modified/removed，FR-029） |
| parent_version_id | FK NULL | 对比链 |
| created_by / created_at | | FR-006/027 全字段 |
| origin | enum | `forward / rollback`（rollback 时记录源版本引用 source_version_id） |

**规则**：**不可 UPDATE / DELETE**（仅 status 单向迁移与追加）；软删禁止（FR-032/US7-AC3）。历史版本任意两两对比（FR-032）。

---

## 9. Deployment（部署记录，不可删除）

| 字段 | 类型 | 说明 |
|---|---|---|
| node_id / config_version_id | FK | 目标与版本 |
| trigger | enum | `deploy / rollback / retry` |
| status | enum | 状态机见下（FR-028） |
| confirmed_at / confirmed_by | | Diff 预览确认（AC-008：未确认不可部署） |
| approval_id | FK ReleaseRequest NULL | production 必经 |
| verification_result | JSONB | 运行时校验：期望/实际集合、diff、耗时（FR-030） |
| error_code / error_message | text | 阻断原因（离线/校验失败/加载拒绝…） |
| logs | JSONB 数组 | 管线各阶段时间戳事件（部署日志，不可删） |

**状态机（FR-028 + 宪法 XII）**：

```text
pending → validating → ready → deploying → success
                         │          │
                         ├→ failed（验证/生成失败，版本回 pending 重建）
                         └→(冻结) → failed（节点离线、审批拒绝、重启恢复）
deploying → failed（原子替换失败/超时/运行时校验未加载）
```

**规则**
- 运行时校验失败 → failed + 展示期望/实际差异 + 给出最近已知良好版本（Edge Case/FR-030）。
- 网关离线：验证/生成可完成，`ready→deploying` 被阻断；恢复后须人工重新确认（不自动补发，Edge Case）。
- 回滚 = 新 Deployment（trigger=rollback）+ 新 ConfigVersion（origin=rollback，内容=历史快照重发布），复用同一管线（FR-031/US2-AC3/SC-003）。
- 重启恢复：`validating/ready/deploying` 中断记录标 failed，待人工确认（NFR-REL-01/R16）。
- 并发发布：同节点同时仅一个非终态 Deployment（局部唯一索引）。

---

## 10. ReleaseRequest（生产审批，FR-037）

| 字段 | 约束 |
|---|---|
| node_id / config_version_id | production 节点；提交时版本须 ready |
| submitted_by | FK User |
| status | `pending → approved / rejected / cancelled`（提交人可 cancel） |
| reviewed_by / reviewed_at / comment | approved/rejected 必填 reviewer |

**不变式**：`reviewed_by != submitted_by`（US6-AC2/FR-037，DB 层 CHECK）；approved 的申请是唯一可解锁 production `ready→deploying` 的凭据；审批人批准即视为 Diff 确认（合并 confirmed 步骤，避免双重确认）。审计记录提交人+批准人双身份（US6-AC3）。

---

## 11. SecretCredential（DNS Provider 凭证等）

| 字段 | 说明 |
|---|---|
| name / provider | 如 `aliyun-dns-prod`（lego 支持的 provider 白名单） |
| data_encrypted | AES-256-GCM（含 API Token/AccessKey，整体 JSON） |
| data_fingerprint | 8 hex；轮换后展示新指纹供确认（FR-035：轮换不换绑定） |
| kind | `dns_provider / traefik_api / other` |

**规则**：值仅创建/轮换时单向写入；任何读取端点返回 `{name, provider, fingerprint, updated_at}`；验证接口回传脱敏失败原因（provider 拒绝时不回显凭证，Edge Case）。

---

## 12. Certificate（证书只读观测）

| 字段 | 说明 |
|---|---|
| domain_id | FK |
| source | `acme / imported` |
| not_before / not_after / issuer / sans | 观测值（导入证书解析；acme 经 TLS 握手，R14） |
| status | `valid / expiring_soon / expired / missing / unknown`（阈值 = domain.expiry_warn_days） |
| observed_at | 最近观测时间 |

**私有材料**：导入证书私钥存 `private_key_encrypted BYTEA`（R7）；acme 证书私钥 NEVER 入库（归 Traefik 管理，宪法 VII）。Certificate 行由探测任务 upsert，用户不可直接编辑/删除（策略在 Domain 侧）。

---

## 13. AuditLog（审计，append-only）

| 字段 | 说明 |
|---|---|
| occurred_at | timestamptz，按月 RANGE 分区（R12） |
| actor_id / actor_username | 快照列防用户删除后失语 |
| action | `create/update/delete/enable/disable/deploy/rollback/approve/reject/cert_policy_change/permission_change/advanced_edit/login…` |
| resource_type / resource_id / resource_name | 资源定位（含审计本身的操作，FR-038） |
| before / after | JSONB，写入前经统一 redactor 脱敏（R7；宪法 VIII/X） |
| ip / user_agent | 来源（FR-038） |
| request_id | 链路关联 |

**规则**：事务内与业务变更同提交（R12）；仅 INSERT；DB revoke UPDATE/DELETE；在线保留 ≥365 天（NFR-AUD-01）；按 actor/resource/时间/动作可检索（FR-041 页面）。Config Version / Deployment / Deployment Log / Audit Log MUST NOT 可删除（FR-032/US7-AC3）。

---

## 14. 跨实体不变式汇总（发布验证六类，FR-024）

| 类别 | 检查项（→ 上文规则） |
|---|---|
| Domain | 格式与节点内唯一；泛域名+HTTP Challenge 拒绝；HTTPS 缺证书告警/阻断（§3） |
| Route | 路径/表达式语法、匹配重叠冲突、引用存在性、状态合法（§6） |
| Service | 被引用但无启用 Target 阻断（§4） |
| Middleware | 类型参数逐字段校验、启用性（§7） |
| Dependency | Route→Domain/Service/Middleware 同节点、未软删、enabled；删除引用清单（§2–7） |
| 生成产物 | `Generate(snapshot)` 成功 + `traefik validate` 自检通过 + secret 零明文扫描（R7/R8） |

任一失败 → 逐条 `ValidationIssue{category, resource, code, message, hint}`（message 指出资源定位与修复建议，NFR-USE-01）并阻断发布。

## 15. 软删除与处置矩阵（FR-039）

| 资源 | Disable | Archive | 软删 | 硬删 |
|---|---|---|---|---|
| Domain / Service / Target / Middleware / Route | ✓ | ✓（Route 状态机） | ✓（需无未归档引用者；历史快照保留） | ✗（一期不做） |
| GatewayNode | ✓ | — | ✓（无关联部署时，UF-1） | ✗ |
| ConfigVersion / Deployment / AuditLog | 不适用 | 不适用 | **一律拒绝（任何角色）** | ✗ |
