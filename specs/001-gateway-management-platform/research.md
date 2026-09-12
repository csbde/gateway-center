# Phase 0 Research: Gateway Center 一期

**Feature**: 001-gateway-management-platform | **Date**: 2026-09-12
**输入**: `specs/001-gateway-management-platform/spec.md`、`.specify/memory/constitution.md`
**用户已确认选型**: 后端 Go；前端 React + TailwindCSS；主存储 PostgreSQL。

本文档解决 Technical Context 与 Assumptions 中移交 `/speckit-plan` 决定的全部未知项。

---

## R1. 后端框架与 ORM

- **Decision**: Go 1.24 + [chi](https://github.com/go-chi/chi) 路由 + GORM + `database/sql` 混合（事务用 GORM）；配置结构用 `envconfig`；日志 `slog`（标准库）。
- **Rationale**: chi 轻量、中间件模型清晰，不绑定框架风格，符合宪法 XII 的显式分层；GORM 在国内 Go 生态成熟、迁移工具（goose 管 SQL 迁移文件，GORM 只做运行时访问）可选搭配；slog 内置结构化日志便于统一脱敏 Handler（见 R7）。
- **Alternatives considered**: Gin（生态大但对 REST 小团队无额外收益）、Echo（与 chi 等价，选社区更活跃的 chi）、sqlc（生成类型安全 SQL，但动态查询与乐观锁场景手写成本高，一期不采用）。

## R2. 前端栈

- **Decision**: React 18 + TypeScript + Vite + TailwindCSS 4 + shadcn/ui（基础组件）+ TanStack Router（路由）+ TanStack Query（服务端状态）+ react-hook-form + zod（表单与类型化校验，与后端校验规则语义对齐）。
- **Rationale**: 用户指定 React + TailwindCSS。shadcn/ui 提供可复制源码的无样式绑定组件（表格、对话框、表单），避免 AntD 的样式体系与 Tailwind 冲突；zod schema 可集中定义域名/CIDR/速率等校验规则，与 Go 侧校验测试互相对照。
- **Alternatives considered**: Ant Design Pro（开箱 CRUD 多，但主题定制与 Tailwind 混用差）、Redux（TanStack Query 已覆盖服务端状态，一期无需全局 store）。

## R3. 存储

- **Decision**: PostgreSQL 16+。业务实体、Config Version 快照、审计日志全部存 PG；JSONB 列存放类型化 Middleware 参数与生成配置快照。
- **Rationale**: 用户确认。JSONB 满足 NFR-MNT-01「新增一类 Middleware 不改核心表」；事务隔离级别 read committed + 行级锁满足乐观并发；表分区（审计按月）支撑 NFR-AUD-01 的 ≥365 天在线保留。
- **Alternatives considered**: SQLite（并发写与审计增长上限低，Assumptions 规模目标下不选）、MySQL 8（JSON 能力弱于 PG，用户未选）。

## R4. 一期配置下发通道（spec Assumption 移交本阶段决定）

- **Decision**: **本地文件系统共享卷直连 + 接口化 Deployer**。Deployment Pipeline 通过 `Deployer` 接口下发；一期唯一实现为 `FileDeployer`：Gateway Center 与目标 Traefik 约定的 dynamic 目录挂载为同一受控路径（同宿主机 bind mount 或 NFS 类受控共享文件系统），写入流程为 `写临时文件 → 服务端语法自检 → fsync → 原子 rename 替换 → 触发/等待 Traefik 观测`。目录布局遵循宪法 III（`dynamic/{routers,services,middlewares,tls}`，按节点隔离：`deploy/gateway-center/<node_id>/dynamic/...`）。
- **Rationale**: 宪法 IV 明确「先写临时文件 → 校验 → 原子 rename」即为文件发布模型；Assumptions 允许单节点直连且禁止 SSH 直改、禁止提前实现 Agent。共享卷是无 Agent、无 SSH 前提下唯一满足原子替换语义的通道。`Deployer` 接口（`Deploy(ctx, node, artifact) / Cleanup / Capabilities`）是宪法 XII「可扩展性体现为接口正确性」的落点：二期 Gateway Agent 只是新增一个实现，管线不变。
- **Alternatives considered**: SSH+SFTP（违反宪法 IV 明文禁令）、Kubernetes Provider 式 API 下发（一期范围外）、Traefik 无远程写入 API，HTTP 下发通道不存在。
- **风险与缓解**: 共享文件系统故障 → 部署前探测目录可写性（校验阶段），失败即阻断并标记原因；NFS rename 非原子的老内核问题 → 部署要求本地文件系统或 POSIX 可靠挂载，文档化前置条件（见 quickstart）。

## R5. 运行时状态采集与 Runtime Verification（期望态 vs 实际态）

- **Decision**: 只读使用 Traefik 官方 Dashboard/API（static 配置中由运维预先启用 `api` + 基础认证）：`GET /api/overview`、`/api/http/routers`、`/api/http/services`、`/api/http/middlewares`、`/api/tcp/routers`（不启用）。节点探测周期 30s、连续 3 次失败判 offline（spec 默认值）。Runtime Verification 在部署后以指数退避（1s/2s/4s，总窗口 ≤15s，超时判 failed）轮询上述端点，将**期望资源集合**（本次快照中的 router/service/middleware 名称与关键字段摘要）与**网关实际加载集合**做双向 diff。
- **Rationale**: 宪法 XI 要求逐 Router/Service 加载确认，仅靠文件对账无法证明「被 Traefik 接受」；Traefik API 是唯一可观测的 actual state 来源（版本、加载对象、错误信息经 `/api/overview` 与 logs 端点旁证）。API 认证通道满足 NFR-SEC-01「状态通道 MUST 认证」。
- **Alternatives considered**: 读 `acme.json`/文件 hash 对账（不能证明加载成功，仅作为 Drift 差异展示的辅助信号）、解析 Traefik metrics（一期不引入 Prometheus 依赖，超出范围）。
- **Drift 判定**: 每轮探测中 `desiredVersion != lastVerifiedVersion` 或集合 diff 非空 → 节点标 `drift`，差异项持久化供 Dashboard/详情页展示；一次成功部署+校验后清除。

## R6. 认证与会话

- **Decision**: 平台自建账号（spec Assumption）：bcrypt（cost ≥12）密码散列；JWT HS256（Access Token 2h），Refresh Token 7 天存库（旋转式：每次刷新作废旧 token）；RBAC 四角色作为 JWT claim + 每请求 DB 复核角色（防止降权后旧 token 越权）。登录接口限速（IP+用户名计数）。
- **Rationale**: 一期单实例、内网场景，HS256 单密钥足够；角色复核成本低（单行查询）换取权限变更即时生效（FR-036/宪法 IX）。
- **Alternatives considered**: Session Cookie（需服务端会话存储，SPA+API First 下 JWT 更利于 CLI/自动化复用同一 API）、OIDC（spec 明确一期不做）。

## R7. Secret 存储与脱敏（宪法 VIII / FR-035）

- **Decision**: AES-256-GCM 应用层加密，主密钥来自环境变量 `GC_MASTER_KEY`（32B，部署文档要求放入 systemd EnvironmentFile / secrets 挂载，不落库不落仓库）。密文列统一 `*_encrypted BYTEA` + `*_fingerprint CHAR(8)`（明文 SHA-256 前 8 位）。加密对象：DNS Provider 凭证、导入证书私钥、Traefik API Basic 凭据、用户密码除外（bcrypt 单独列）。**脱敏策略在序列化层实现**：DTO 白名单输出（secret 字段仅暴露 fingerprint/掩码），slog 注册 `SensitiveAttrHandler` 对 `token/password/private_key/authorization` 键强制打码，审计 before/after 快照写入前经同一 redactor 处理。
- **Rationale**: 白名单序列化 + 集中 redactor 使「接口不回显、日志不可见、Diff/审计脱敏」由一处机制保证而非逐字段遗忘；fingerprint 支撑凭证轮换确认（FR-035 轮换不重配绑定）。
- **Alternatives considered**: PostgreSQL pgcrypto（密钥进 DB 层，应用轮换困难）、Vault（引入一期不需要的外部依赖，违反范围纪律）。

## R8. 配置生成层（业务模型 → Traefik Dynamic Config）

- **Decision**: 独立包 `internal/generate`，输入为**版本快照结构体**（自包含：路由/服务/中间件/TLS 全部内联，不查当前库——保证回滚快照语义，宪法 V Rationale），输出为 Traefik v3 file provider 的 YAML 文件集合（routers/services/middlewares/tls 分文件，文件名即资源 ID）。生成器为纯函数：`Generate(snapshot) ([]ArtifactFile, error)`；产物先经 `traefik validate --configFile` 容器/二进制自检（校验阶段第六类"生成产物可被网关接受"）再入库为快照。
- **Rationale**: NFR-MNT-01 要求 Traefik 版本升级影响收敛在本层；纯函数使生成可单测、可重放、Diff 可复现。快照自包含是「回滚不会回出一半新半旧」的前提。
- **Alternatives considered**: Go text/template 拼 YAML（错误处理分散，改用手写结构体 + `yaml.Marshal` 类型化模型）、CRD/Gateway API 中间层（一期范围外）。
- **简单/高级模式映射**: 简单模式字段 → 规则字符串在生成层合成（`Host(\`crm.example.com\`) && PathPrefix(\`/\`)`）；高级模式存原始表达式，生成层只做语法验证（词法+函数白名单：Host/HostRegexp/Path/PathPrefix/Headers/HeadersRegexp/Method + &&/||/括号），两模式产物互不影响（宪法 II）。

## R9. Middleware 类型化参数建模

- **Decision**: 单表 `middlewares`，`type` 枚举 + `params JSONB`；每类参数结构体在 Go 侧注册（`middleware.Registry[type]`），实现 `Validate(params) []FieldError` 与 `ToTraefik(params) map` 两方法。新增类型 = 新增一个注册项，管线与表结构不动。一期五类：`security_headers` / `ip_allowlist` / `rate_limit` / `redirect`（含 http→https）/ `strip_prefix`。
- **Rationale**: FR-021 的独立校验与 NFR-MNT-01 同时满足；JSONB + 类型注册器避免五类各建一表或 EAV 反模式。

## R10. 并发编辑冲突

- **Decision**: 乐观锁：所有可编辑业务实体带 `version INTEGER` 列；PUT 携带 If-Match 语义（body 中 `expected_version`），不匹配返回 409 + 当前值摘要。不提供实时协同（spec Assumption：后提交者提示冲突并刷新重试）。
- **Rationale**: 实现最小、语义清晰；409 冲突提示可在 UI 展示对方已改字段。
- **Alternatives considered**: CRDT/OT（过度设计）、悲观行锁（长事务风险）。

## R11. Diff 计算与版本对比

- **Decision**: Diff 以**逻辑资源为单位**（router/service/middleware/tlsconfig，键=资源 ID+类型），对新旧两份快照的结构体做语义比较（比较归一化后的生成产物 + 业务元数据），输出 `added/modified/removed` 三段列表；任意两 Config Version 可对比（FR-032），计算为纯函数，结果随 Deployment 缓存。
- **Rationale**: 用户语言展示（「哪些路由变了」）而非文本行 diff；FR-029/AC-008 要求以资源为单位。

## R12. 审计日志

- **Decision**: 独立 append-only 表 `audit_logs`（按月 RANGE 分区，在线保留 ≥365 天），数据库层 revoke `UPDATE/DELETE`；写入走 Application 层统一 `AuditRecorder`（在事务内与业务变更同提交），记录 actor/action/resource_type/resource_id/before/after/redacted 标记/ip/occurred_at。Advanced Mode 使用记录 `route.advanced_edit` 动作并关联路由 ID（宪法 X）。
- **Rationale**: 事务内同提交保证「有变更必有审计」；权限 revoke 在存储层兜底"不可篡改"。
- **Alternatives considered**: 触发器审计（before/after 拿到的是含 secret 明文的行，脱敏困难，放弃）、外部日志系统（一期不引入）。

## R13. 健康检查与 Target 状态

- **Decision**: Target 健康状态一期 = 平台侧主动 HTTP 探测（可配置 path/interval/timeout/期望码，默认 `/` 30s 2s `2xx-3xx`），探测由节点状态同一调度器执行；Service 状态 = 启用 Target 聚合（all up → healthy，部分 → degraded，全挂/无启用 → down）。Traefik 自身负载均衡不带对平台可见的健康回传，故以平台探测为 actual health 来源，UI 标注"平台视角健康"。
- **Rationale**: FR-011/AC-003 要求平台展示健康；探测模型与节点探测复用同一基础设施。
- **Alternatives considered**: 依赖 Traefik 运行时错误率推断（无按 Target 粒度）、WRR 层健康透传（Traefik OSS 无此 API）。

## R14. HTTPS/证书策略（职责边界落地）

- **Decision**: 域名 HTTPS 策略四态：`off / acme_http / acme_dns_wildcard / imported`。自动签发实际执行者 = 节点 Traefik 的 resolver（static 预配，宪法 III 平台只读）；平台仅：(a) 生成 tls 段引用 resolver + Host/SAN 规则；(b) 导入自有证书 → 生成 `services/tls` 动态证书文件（私钥经 R7 加密入库、下发写入文件权限 0600）；(c) 到期监控：读证书材料（导入证书解析 NotAfter；acme 经节点 Traefik API `/api/http/routers` 的 tls 关联 + 探测握手 `tls.Dial` 取叶子证书过期时间，不读 acme.json）。泛域名策略强制 `acme_dns_wildcard`（FR-034，校验层拦截 HTTP Challenge 选项）。
- **Rationale**: 探测握手法获取到期时间避免触碰 `acme.json`（宪法 VII 禁令），且对自有证书/acme 证书统一适用。阈值默认 30 天可配置（spec Assumption）。
- **Alternatives considered**: 读 acme.json（明文禁令，放弃）、Let's Encrypt 侧 API（无法覆盖自有证书）。

## R15. 发布管线与审批状态机

- **Decision**: 单一管线实现为 Application Service 编排：`Validate → Generate → CreateVersion(pending→validating→ready) → DiffPreview(用户确认=Deployment 创建入参 require confirmed flag) → ApproveGate(production 且提交人≠批准人) → Deploy(deploying) → Verify(success/failed)`。状态机集中定义（Go 类型 + 允许迁移表），非法迁移 panic 级错误。回滚 = 对历史 success 版本快照创建新 Deployment（trigger=rollback），复用同一编排（FR-031/宪法 V）。Deployment 状态机对齐宪法 XII：`pending|validating|ready|deploying|success|failed`（rolled_back 以"回滚产生的新 success 部署"表达，不引入终态别名）。
- **Rationale**: 一条代码路径同时服务正向发布/回滚/非生产直发，宪法 IV「无旁路」以测试强制（存在绕过 Validate/Preview 的调用即失败）。
- **Alternatives considered**: 工作流引擎（Temporal 等，超出范围纪律）、以状态列散落守卫（易漏，弃）。

## R16. 项目结构与部署形态

- **Decision**: 单仓库 monorepo：`backend/`（Go，分层 cmd/internal/{api,application,domain,infrastructure}）+ `frontend/`（React SPA）。API 与 SPA 同源部署（Go embed 或反代），单容器/二进制 + PG。后台任务（节点探测、Target 健康、证书到期、部署执行）在单进程内以任务调度器（robfig/cron + 工作队列）运行；一期明确单实例部署，NFR-REL-01 重启恢复 = 启动时加载未完成 Deployment 状态并标记中断为 failed（要求人工重新确认发布，符合 Edge Case「无人确认不自动补发」）。
- **Rationale**: 规模目标（10 节点/1000 路由）下单体充分；分层目录即宪法 XII 调用链的物理映射。
- **Alternatives considered**: 微服务拆分（过度设计）、多副本 leader 选举（一期单实例约束下不需要）。

## R17. 测试策略

- **Decision**: Go 标准 `testing` + testify；单元（domain 状态机、校验器、生成器纯函数、redactor）、集成（testcontainers-go 起 PG，管线端到端）、契约测试（HTTP handler 层表驱动）、前端 Playwright（UF-1/UF-4 关键路径 E2E + 组件测试 Vitest）。CI：`go vet`/`golangci-lint`/`race`/覆盖率门槛 80%（核心管线包）。
- **Rationale**: 宪法 IV/XI 的安全属性（无旁路、发布≠生效显式化）必须以测试固化。
- **Alternatives considered**: 仅手工验收（无法满足 SC-002 抽样审计违规为 0 的可信度）。

## R18. 可观测性与性能默认值

- **Decision**: slog 结构化日志（脱敏 Handler）、`/healthz`、`/metrics`（prometheus client，暴露 API 时延/探测周期/部署计数）。目标：API p95 ≤3s（SC-006 规模下列表查询分页默认 20、上限 100；快照大字段列表接口不回传）、探测并发上限 32 节点、1000 路由生成 <5s。
- **Rationale**: SC-006 为验收标准，需在设计与测试中显式预算。
- **Alternatives considered**: 一期引入告警系统（超出范围，Dashboard 内置告警位即可）。

---

## NEEDS CLARIFICATION 消解清单

| 未知项 | 来源 | 消解 |
|---|---|---|
| 后端/前端/存储选型 | Technical Context | 用户确认：Go / React+Tailwind / PostgreSQL（R1–R3） |
| 安全下发通道 | spec Assumption | R4：共享卷 FileDeployer + Deployer 接口 |
| 状态采集/验证端点 | spec Assumption | R5：Traefik API 只读 + 指数退避校验 |
| 认证方式 | spec Assumption | R6：自建账号 + JWT |
| Secret 加密方案 | FR-035/NFR-SEC-01 | R7：AES-256-GCM + 白名单序列化 |
| 证书到期观测方式 | FR-033/008 | R14：TLS 握手探测，不读 acme.json |
| 审批与状态机细节 | FR-037/028 | R15：单管线 + ApproveGate |
| 重启恢复语义 | NFR-REL-01 | R16：中断部署标 failed 待人工确认 |
