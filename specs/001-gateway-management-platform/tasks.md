# Tasks: Gateway Center 统一网关管理中心（一期）

**Input**: Design documents from `/specs/001-gateway-management-platform/`

**Prerequisites**: plan.md、spec.md、research.md、data-model.md、contracts/openapi.yaml、quickstart.md

**Tests**: 包含（research.md R17；宪法 IV/XI 的安全属性以测试固化）

**Organization**: 按用户故事分组，各故事可独立实现与验证

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[USn]**: 所属用户故事（spec.md 编号），仅用户故事阶段任务携带
- 每条任务含确切文件路径

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 项目初始化与基础结构

- [X] T001 初始化 Go module 与 CLI 骨架：go.mod（Go 1.24，chi/GORM/goose/golang-jwt/robfig-cron/testify）、backend/cmd/gateway-center/main.go（serve / migrate-seed 子命令）
- [X] T002 [P] 环境配置加载 backend/internal/infrastructure/config/config.go：GC_DATABASE_URL、GC_MASTER_KEY（32B，R7）、GC_INITIAL_ADMIN_*、GC_ADDR，envconfig + 默认值
- [X] T003 [P] slog 结构化日志与 SensitiveAttrHandler（token/password/private_key/authorization 键强制打码）backend/internal/infrastructure/logging/
- [X] T004 [P] 前端脚手架 frontend/：Vite + React 18 + TS + TailwindCSS 4 + shadcn/ui（含主题令牌、浅深色）
- [X] T005 [P] 前端路由与数据层：TanStack Router 10 页面占位路由树 frontend/src/routes/、TanStack Query client、openapi 类型化 client 生成脚本 frontend/src/api/generated/（契约源 specs/001-gateway-management-platform/contracts/openapi.yaml）
- [X] T006 [P] 参考部署拓扑：deploy/compose/docker-compose.yml（postgres:16 + traefik v3 + sample-crm 上游，共享 dynamic 卷）、deploy/traefik/traefik.yml（Static 预配样本：entrypoints web/websecure、file provider、api.dashboard、le resolver，平台只读）
- [X] T007 [P] 后端 Makefile（build / test / test-unit / test-contract / test-integration / test-pipeline / vet / lint）与 backend/.golangci.yml
- [X] T008 [P] 前端工具链：frontend/eslint + prettier + vitest 配置、frontend/playwright.config.ts
- [X] T009 [P] testcontainers-go 集成测试助手 backend/tests/integration/helper_test.go：PG 容器 + goose up + fixture 工厂
- [X] T010 初始 Super Admin 种子命令 backend/cmd/gateway-center/seed.go（migrate-seed；密码强度门槛 ≥12）

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 任何用户故事开始前 MUST 完成的核心基础设施

**⚠️ CRITICAL**: 本阶段完成前不得开始任何用户故事

- [X] T011 backend/migrations/0001_init.sql：data-model.md 全部表与部分唯一索引（nodes、node_states、domains、services、targets、routes、route_middlewares、middlewares、config_versions、deployments、release_requests、secret_credentials、certificates、users、platform_settings、audit_logs）+ `CHECK (reviewed_by <> submitted_by)` + 外键
- [X] T012 [P] backend/internal/infrastructure/cryptox：AES-256-GCM Encrypt/Decrypt + Fingerprint(8hex)（R7）
- [X] T013 [P] backend/internal/infrastructure/pgstore：GORM 会话、软删除、row_version 乐观锁（ErrConflict）、WithTx 事务助手
- [X] T014 [P] backend/internal/api/httperr：统一错误体 `{error:{code,message,request_id,details[]}}` 与 400/401/403/404/409/422/502 构造器（contracts/README.md 错误模型）
- [X] T015 backend/internal/api/handlers/auth.go + backend/internal/application/authsvc/ + backend/internal/infrastructure/tokensx/：login（bcrypt≥12、IP+用户名限速）、refresh 旋转、logout、/me；JWT HS256 2h/7d（R6）
- [X] T016 backend/internal/api/middleware/rbac.go：bearer 认证中间件（每请求复核角色/disabled 即时 401）+ RequireRoles(...) 路由组守卫（与 openapi x-rbac 对齐）
- [X] T017 backend/internal/application/auditrec/：事务内 AuditRecorder（actor/action/resource/before/after 经 redactor 脱敏/ip/request_id）+ backend/internal/api/middleware/requestid.go（宪法 X）
- [X] T018 [P] backend/internal/domain/state/：Route/Deployment/ConfigVersion/ReleaseRequest/Node 五状态机与合法迁移表（data-model.md §6/§9/§10；非法迁移即错误，宪法 XII）
- [X] T019 [P] backend/internal/infrastructure/traefikapi/：只读 client（/api/overview、/api/http/routers|services|middlewares，Basic 认证），超时与错误分类（R5）
- [X] T020 [P] backend/internal/generate/：自包含 Snapshot 结构体、Traefik v3 YAML 类型模型、`Generate(snapshot) ([]ArtifactFile, error)`（routers/services/middlewares/tls 分文件）+ 产物零明文 secret 断言（R8）
- [X] T021 [P] backend/internal/infrastructure/deployer/：Deployer 接口 {Deploy/ProbeWritable/Cleanup} + FileDeployer 临时文件→fsync→原子 rename（`<deploy_root>/dynamic/…`，按节点隔离，R4）
- [X] T022 [P] backend/internal/domain/diff/：SnapshotDiff(old,new)→DiffItem[]（资源单位 added/modified/removed，R11）
- [X] T023 backend/internal/api/queryutil/：分页/sort/q/node_id 参数与 `{items,page,page_size,total}` 列表响应（SC-006：大字段列表不回传）
- [X] T024 backend/internal/application/scheduler/：robfig/cron 注册表、探测并发上限 32、启动恢复钩子（中断部署→failed，R16/NFR-REL-01）
- [X] T025 backend/internal/application/settingsvc/ + backend/internal/api/handlers/settings.go：platform_settings 读写（探测周期/离线阈值/到期天数/时区）+ 缓存
- [X] T026 backend/internal/api/router.go：装配 /api/v1 全部路由组 + SPA embed 占位（合法调用链 Controller→Application→Domain→Generate→Deployer，宪法 XII）

**Checkpoint**: 无 UI 即可 curl 走通认证/错误模型/审计骨架；Deployer 与 Generate 可被后续故事复用

---

## Phase 3: User Story 1 - Web UI 端到端发布反向代理 (Priority: P1) 🎯 MVP

**Goal**: 空平台 → 节点+Service+Domain+Route（简单模式，纯转发）→ 六类验证 → Diff 确认 → 发布 → 运行时校验生效（AC-001~004、007~010，SC-001）

**Independent Test**: quickstart.md §4 V-1 九步 + §3 `make test-pipeline`

### Implementation for User Story 1

- [X] T027 [P] [US1] GatewayNode 实体与校验（name/base_url/deploy_root/env_type）+ repo backend/internal/domain/node.go、backend/internal/infrastructure/pgstore/nodes.go
- [X] T028 [P] [US1] Domain 实体：RFC1123/泛域名格式、节点内唯一、https_policy 四态 + repo backend/internal/domain/domain.go、pgstore/domains.go
- [X] T029 [P] [US1] Service/Target 实体：URL 格式与归一化重复、weight 1–65535、协议限 http/https + repo backend/internal/domain/service.go、target.go、pgstore/services.go
- [X] T030 [P] [US1] Route（simple）实体与保存校验（domain/service 同节点且 enabled；path 规则；route_middlewares 关系模型）+ repo backend/internal/domain/route.go、pgstore/routes.go
- [X] T031 [US1] backend/internal/application/nodesvc/service.go：节点 CRUD、启停、软删守卫（有部署拒删 UF-1）、创建即注册首轮探测
- [X] T032 [US1] backend/internal/api/handlers/nodes.go：/nodes 全端点 + enable/disable（AC-001；节点禁用→冻结部署）；全部写操作接 auditrec
- [X] T033 [US1] backend/internal/application/domainsvc/ + handlers/domains.go：域名 CRUD/启停（AC-002）；最小证书导入 internal/application/certsvc/ + POST /certificates/import（私钥 cryptox 加密，支撑 US1 场景 HTTPS）
- [X] T034 [US1] backend/internal/application/servicesvc/ + handlers/services.go、handlers/targets.go：Service+Target 管理与地址/重复校验（AC-003/UF-2）
- [X] T035 [US1] backend/internal/application/routesvc/ + handlers/routes.go：simple 草稿 CRUD、generated_rule_preview 回显（FR-014 用户零语法）、乐观锁 409（AC-004）
- [X] T036 [US1] backend/internal/domain/validate/：六类校验器→ValidationReport（引用缺失/禁用、路径非法、依赖、生成产物；blocker/warning + hint 修复建议；FR-024/AC-007/NFR-USE-01）
- [X] T037 [US1] backend/internal/application/pipeline/version.go：VersionService——当前期望态→自包含 Snapshot→Generate→traefik validate 自检→ConfigVersion pending→validating→ready（FR-025/027）
- [X] T038 [US1] backend/internal/application/pipeline/deploy.go：DeployService——confirmed=false→422（AC-008）；离线阻断（Edge Case）；同节点并发 409；FileDeployer 原子替换；部署后运行时校验（1s/2s/4s 退避比对集合→success/failed + verification_result，AC-009/010，FR-026/028/030）
- [X] T039 [US1] backend/internal/api/handlers/pipeline.go：POST /nodes/{id}/validate、POST+GET /nodes/{id}/versions、GET /versions/{id}、GET /versions/{id}/diff、POST+GET /deployments、GET /deployments/{id}（版本/部署记录 DELETE→403，FR-032）
- [X] T040 [US1] backend/internal/application/probesvc/service.go：节点探测 job（30s；online + Traefik 版本 + 加载集合摘要回写 node_states，FR-002）
- [X] T041 [US1] backend/internal/application/healthsvc/service.go：Target HTTP 探测 + up/down + Service 聚合 healthy/degraded/down（FR-011/AC-003，R13）
- [X] T042 [US1] 前端登录与会话 frontend/src/routes/login.tsx + frontend/src/api/session.ts（access/refresh 拦截、路由守卫）
- [X] T043 [P] [US1] 前端 Gateway Nodes 页 frontend/src/features/nodes/：列表/新建/编辑表单、状态徽章、启用禁用
- [X] T044 [P] [US1] 前端 Services 页 frontend/src/features/services/：Service+Target 表格（zod 地址/权重校验、错误定位字段）
- [X] T045 [P] [US1] 前端 Domains 页 frontend/src/features/domains/：域名 CRUD + HTTPS 策略（imported 证书上传）表单
- [X] T046 [P] [US1] 前端 Routes 简单模式向导 frontend/src/features/routes/：节点→域名→路径+匹配→服务→HTTPS，展示自动合成规则预览，全页零 Traefik 术语（FR-042）
- [X] T047 [US1] 前端发布向导 frontend/src/features/deployments/：验证报告逐条→生成版本→Diff 预览→勾选确认才启用发布按钮→轮询部署状态与校验结果（AC-007/008/010 的 UI 闭环）
- [X] T048 [P] [US1] 前端 Dashboard 占位 frontend/src/routes/dashboard.tsx（资源计数卡片；漂移/预警由 US5 补全）

### Tests for User Story 1 ⚠️（T049–T051 应先于对应实现编写并失败；编号按文件分组保持连续）

- [X] T049 [P] [US1] 契约测试 backend/tests/contract/us1_crud_test.go：/nodes /domains /services /routes /versions /deployments 状态码、错误体、409 乐观锁、私钥与 api_auth 脱敏断言
- [X] T050 [US1] 集成测试 backend/tests/integration/pipeline_e2e_test.go：空平台到 success 全链 + 无旁路（未 validate/未确认/离线三种 422）+ 原子性采样（make test-pipeline）
- [ ] T051 [US1] E2E frontend/tests/e2e/uf1-publish.spec.ts：quickstart V-1 步骤 1–9，含 15 分钟计时断言（SC-001）

**Checkpoint**: MVP 可独立交付——HTTP/自有证书 + 纯转发路由端到端发布生效并验证

---

## Phase 4: User Story 2 - 配置版本历史与一键回滚 (Priority: P2)

**Goal**: 任意两版对比 + 历史成功版本回滚（复用同一管线、新部署记录）（AC-006/011/012，SC-003）

**Independent Test**: quickstart.md V-2——两次发布后回滚，网关恢复 v1 行为

- [X] T052 [US2] backend/internal/application/pipeline/rollback.go：POST /deployments/rollback——以目标版本快照（不回读当前库）创建 origin=rollback 新版本 + 复用 T038 管线；失败时 verification_result 附 last_known_good_version（FR-031/Edge Case）
- [X] T053 [US2] backend/internal/api/handlers/versions.go 扩展：GET /versions/{id}/diff?against= 任意两版对比 + 版本历史列表（snapshot/artifact 大字段仅详情返回）（FR-032）
- [X] T054 [P] [US2] 前端 Config Versions 页 frontend/src/features/versions/ + frontend/src/routes/config-versions.tsx：历史列表、双版本 diff 视图（资源单位三色）、回滚确认对话框与状态轮询
- [X] T055 [P] [US2] 测试：backend/tests/integration/rollback_test.go——回滚生成新版本+部署、DB 业务态清空仍可回滚（快照自包含，宪法 V）；frontend/tests/e2e/rollback.spec.ts（V-2，≤3 分钟）

**Checkpoint**: US1+US2 独立可用；生产变更具备可回滚前提

---

## Phase 5: User Story 3 - 可复用 Middleware 安全策略 (Priority: P2)

**Goal**: 五类中间件建/用/序/护 + 高级模式（风险提示/语法验证/预览/审计）（AC-005/015、FR-015~022）

**Independent Test**: quickstart.md V-3——白名单拦放置、绑定顺序保序、被引用删除阻止

- [X] T056 [P] [US3] backend/internal/domain/mwreg/：类型注册表 Registry[type]{Validate→FieldError[], ToTraefik→map}，五类参数结构（data-model.md §7：CIDR 合法性、average≥1、frame_options 枚举等字段级校验）（FR-021，NFR-MNT-01 扩展点）
- [X] T057 [US3] backend/internal/application/mwsvc/ + handlers/middlewares.go：中间件 CRUD/启停、referenced_by 回显
- [X] T058 [US3] 生成链：backend/internal/generate/ Snapshot 增 middleware 段；routers/*.yml middlewares 数组顺序 == route_middlewares.position（FR-018/AC-005）；backend/internal/domain/validate 接入「被引用中间件 disabled→blocker」
- [X] T059 [US3] backend/internal/domain/advrule/：高级模式表达式解析器+白名单验证（Host/HostRegexp/Path/PathPrefix/Headers/HeadersRegexp/Method + &&/||/括号）+ 归一化预览（FR-015）
- [X] T060 [US3] POST /routes/validate-advanced + backend/internal/application/routesvc advanced 写路径：仅 gateway_admin+（403 守卫）、risk_notice 固定文案、每次使用写 advanced_edit 审计关联 route_id（FR-016/宪法 II/X）
- [X] T061 [P] [US3] 前端 Middlewares 页 frontend/src/features/middlewares/：五类类型化表单（zod per-type，错误定位字段）；frontend/src/features/routes/ 扩展：middleware 多选拖拽排序 + 高级模式编辑器（风险提示横幅 + 实时语法验证 + 预览）
- [X] T062 [P] [US3] 单元测试：backend/internal/domain/mwreg/*_test.go（五类参数边界）、backend/internal/domain/advrule/*_test.go（文法边界）、backend/internal/generate/order_test.go（保序）
- [X] T063 [P] [US3] 集成：backend/tests/integration/middleware_test.go（V-3：复用两路由、删除 409 引用清单、advanced 审计、解绑后可删）+ E2E frontend/tests/e2e/middleware.spec.ts

**Checkpoint**: 安全策略治理可用；简单/高级双模式互不影响（宪法 II）

---

## Phase 6: User Story 4 - HTTPS 与证书策略自动化 (Priority: P2)

**Goal**: acme/DNS Challenge/自有证书三路径 + 到期预警 + 凭证加密脱敏（FR-007/008/033~035）

**Independent Test**: quickstart.md V-4——泛域名强制 DNS、到期预警、全链路脱敏 grep 零命中

- [X] T064 [P] [US4] SecretCredential：backend/internal/infrastructure/pgstore/credentials.go、application/credsvc/、api/handlers/credentials.go——AES-GCM 存储、仅 fingerprint 回显、轮换更新指纹不改绑定、verify 调 provider 仅回原因、被引用 409（FR-035）
- [X] T065 [US4] backend/internal/application/domainsvc 策略校验闭环：泛域名+acme_http→400、acme_dns 必带 credential+resolver、策略变更→cert_policy_change 审计（FR-007/034）
- [X] T066 [US4] backend/internal/generate/tls.go：TLS 段生成——resolver 引用 + Host/SAN 规则 + 泛域名约束；tls/ 子目录产物（T020 扩展）
- [X] T067 [US4] backend/internal/infrastructure/certwatch/：TLS 握手探测叶子证书 NotAfter（导入证书直接解析材料）→ certificates 表 upsert；纳入 scheduler；阈值判定 expiring_soon（不读 acme.json，宪法 VII/R14）
- [X] T068 [US4] GET /domains/{id}/certificate + backend/internal/application/dashboardsvc：expiring_certificates 数据源 + validate 器「HTTPS 域名无可用证书→阻断项、临期→warning」（FR-008/Edge Case）
- [X] T069 [P] [US4] 前端：域名详情证书状态卡 frontend/src/features/domains/CertCard.tsx、Settings DNS 凭证管理页 frontend/src/routes/settings.tsx + frontend/src/features/credentials/、Dashboard/域名临期红色预警
- [X] T070 [P] [US4] 测试：契约 backend/tests/contract/credentials_test.go（端点永不回显值）+ 集成 backend/tests/integration/cert_test.go（V-4 全项：泛域名拒绝、轮换指纹、API/日志/Diff/审计 grep 零明文、acme.json mtime 不变）

**Checkpoint**: HTTPS 治理闭环；平台侧零证书私钥明文

---

## Phase 7: User Story 5 - 运行状态可见性与配置漂移检测 (Priority: P2)

**Goal**: offline/degraded 判定、Drift 持续标红与差异展示、期望/实际分列（FR-002/003/040，AC-013，SC-004）

**Independent Test**: quickstart.md V-5——人为漂移 ≤2 分钟可见、发布后清除、断网判 offline

- [X] T071 [US5] backend/internal/application/probesvc 完整化：连续 3 次失败→offline + last_online_at 保留；degraded=可达但启用 Target 失败/加载错误（FR-003/spec Assumption）
- [X] T072 [US5] backend/internal/application/driftsvc/service.go：每轮探测比对 desired vs actual（版本 + 资源集合）→ node_states.drift/drift_detail；成功发布+校验清除；漂移期间 deploy 要求先重新 validate（Edge Case）
- [X] T073 [US5] backend/internal/api/handlers/dashboard.go：GET /dashboard 完整聚合（online/offline/drift 计数、资源数、最近发布）+ GET /nodes/{id}/state 全字段
- [X] T074 [P] [US5] 前端：Dashboard 真实数据与漂移汇总卡 frontend/src/routes/dashboard.tsx、节点详情「平台意图 vs 网关加载确认」双列 + drift 红标与 diff 面板 frontend/src/features/nodes/StatePanel.tsx
- [X] T075 [P] [US5] 测试：backend/tests/integration/drift_test.go（注入漂移→周期内可见→发布后清除；offline 转换；SC-004 计时）+ 路由详情分列展示契约断言

**Checkpoint**: 「发布成功≠生效」全程可视化（宪法 XI）

---

## Phase 8: User Story 6 - RBAC、生产发布审批与全量审计 (Priority: P3)

**Goal**: 四角色矩阵逐项生效 + production 审批链（提交≠批准）+ 审计检索与防篡改（FR-036~038，AC-014）

**Independent Test**: quickstart.md V-6——四角色同操作矩阵、审计还原完整链条、UPDATE audit 被拒

- [X] T076 [US6] backend/internal/application/approvalsvc/ + handlers/release_requests.go：submit（版本须 ready）、cancel、approve/reject 422 自批守卫（FR-037）
- [X] T077 [US6] 管线接线：backend/internal/application/pipeline/deploy.go Gate——production 节点必须携带已批准 approval_id，Developer 对 production 直发→403、test 类节点可直发但验证不可跳（RBAC 复核）
- [X] T078 [US6] RBAC 矩阵化：backend/internal/api/router.go 全端点 RequireRoles 与 openapi x-rbac 逐项对齐，Viewer 一切写入口 403（前端按钮态同步）
- [X] T079 [US6] 审计：backend/internal/api/handlers/audits.go——GET /audit-logs 检索（actor/action/resource_type/from/to 分页）+ /audit-logs/{id}；backend/migrations/0002_audit_partitions.sql——按月 RANGE 分区、UPDATE/DELETE revoke（FR-038/NFR-AUD-01/宪法 X）
- [ ] T080 [US6] 用户管理：backend/internal/api/handlers/users.go（仅 super_admin）、permission_change 审计、禁用即时吊销会话（FR-036）
- [ ] T081 [P] [US6] 前端：Audit Logs 检索页 frontend/src/routes/audits.tsx、Users 管理页 frontend/src/routes/users.tsx、审批收件箱（Diff 审阅 + approve/reject 合一）frontend/src/routes/deployments.tsx 扩展
- [ ] T082 [P] [US6] 测试：backend/tests/integration/rbac_approval_test.go——TestRBACMatrix（四角色×端点矩阵）、V-6（自批拒绝、双身份审计、审计篡改被 DB 拒）

**Checkpoint**: 企业治理闭环；权限变更本身可审计

---

## Phase 9: User Story 7 - 资源依赖保护与安全删除 (Priority: P3)

**Goal**: 引用链逐层删除阻止 + Disable→Archive→软删处置 + 历史快照完整（FR-039，AC-015）

**Independent Test**: quickstart.md V-7——Domain←Route→Service→Target 链式删除尝试全部按预期阻止

- [ ] T083 [US7] backend/internal/domain/depcheck/：引用图查询（Domain/Service/Middleware→未归档引用路由清单）；三资源 DELETE 端点统一 409 DEPENDENCY_BLOCKED + details 引用方（FR-039/US7-AC1）
- [ ] T084 [US7] 处置流守卫：archive 终态迁移（Route 状态机接线 backend/internal/domain/state）、disabled/archived 资源生成排除（backend/internal/application/pipeline/version.go）、节点禁用冻结未完成部署（Edge Case）
- [ ] T085 [P] [US7] 删除不变式测试化支撑：backend/tests/integration/softdelete_snapshot_test.go——软删后新版本排除该资源、历史 ConfigVersion 快照读取不受影响、版本/部署/审计 DELETE 一律 403 复查（US7-AC2/03；与 T087 不同文件并行）
- [ ] T086 [P] [US7] 前端：删除阻止对话框 + 引用清单 + 「先解除依赖」引导 frontend/src/features/common/dependency-block.tsx（nodes/domains/services/middlewares 页接入）；Route 归档操作
- [ ] T087 [P] [US7] 集成测试 backend/tests/integration/dependency_test.go：V-7 全场景（引用链、三种处置、快照完整性）

**Checkpoint**: 误删不致不可解释流量中断（宪法 VI）

---

## Phase 10: Polish & Cross-Cutting Concerns

- [ ] T088 [P] 规模工具：backend/cmd/tools/seed/main.go（10 节点/200 域名/1000 路由/100 中间件）+ k6 backend/tests/perf/list-latency.js（SC-006 列表 p95≤3s；生成 <5s 断言）
- [ ] T089 [P] 安全扫描脚本 scripts/secret-scan.sh：日志/响应样本/Diff 输出 grep `-----BEGIN|token=|authorization:` 零命中，接入 make test（NFR-SEC-01）
- [ ] T090 错误文案审计：逐条 VALIDATION_FAILED/PIPELINE_BLOCKED 确认含资源定位 + hint 修复建议（NFR-USE-01），修 backend/internal/api/httperr/ 与前端错误映射
- [ ] T091 [P] 覆盖率门槛：管线相关包 ≥80% 入 backend/Makefile test-all（unit+contract+integration+e2e 串行）（R17/宪法合规审查）
- [ ] T092 宪法符合性人工检查：执行 quickstart.md §5 表全部项（无反向 YAML 解析 grep、写路径仅 dynamic/、TestNoPipelineBypass、快照自包含用例、审计 revoke）并记录结果
- [ ] T093 quickstart.md V-1~V-9 端到端全量回归（含 §2 全新环境可复现性）
- [ ] T094 [P] 文档：README.md（启动、POSIX rename 共享卷前置、Static 预配要求）+ docs/runbook.md（备份/主密钥轮换/故障排查）
- [ ] T095 SC-001 首次使用计时走查：新账号冷启动 15 分钟内完成服务+域名+路由+发布，结果与步骤截图记录于 docs/verification/sc-001.md（验收证据）
- [ ] T096 [P] UI 术语复查：普通页面零 Traefik 术语、术语仅现于高级模式/技术详情/Debug（FR-042，frontend/src/features 扫描）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup（Phase 1）**：无依赖——立即可开始
- **Foundational（Phase 2）**：依赖 Phase 1；**阻塞全部用户故事**
- **US1（Phase 3）**：依赖 Phase 2 —— 🎯 MVP
- **US2（Phase 4）**：依赖 US1（回滚对象=US1 产物）
- **US3（Phase 5）**：依赖 US1（Route/pipeline）+ Phase 2 generate；与 US4/US5 可并行
- **US4（Phase 6）**：依赖 US1（Domain）+ Phase 2 cryptox；与 US3/US5 可并行
- **US5（Phase 7）**：依赖 US1（pipeline/deploy 产物）；与 US3/US4 可并行
- **US6（Phase 8）**：依赖 US1+US2（审批/回滚门禁点）；US3 的 advanced 审计已在故事内完成，本阶段仅矩阵化
- **US7（Phase 9）**：依赖 US1/US3/US4（三资源引用链齐备后统一接线 depcheck）
- **Polish（Phase 10）**：依赖全部目标故事

### Within Each User Story

模型/校验 → Application Service → Handlers → 前端 → 测试（测试任务 [P] 可与前端并行；集成/E2E 用例先于对应实现提交并失败）

### Parallel Opportunities

- Phase 1 内 [P] 任务全部可并行（T002–T009）
- Phase 2 内 T012–T022 多数 [P]（不同包零冲突）
- Foundational 完成后 US3/US4/US5 三线并行（不同域文件）
- 每故事内各 [P] 实体/表单/测试行并行

---

## Parallel Example: User Story 1

```bash
# 实体四件并行（不同文件）：
Task: "T027 backend/internal/domain/node.go"
Task: "T028 backend/internal/domain/domain.go"
Task: "T029 backend/internal/domain/service.go + target.go"
Task: "T030 backend/internal/domain/route.go"

# 前端五页并行：
Task: "T043 frontend/src/features/nodes/"
Task: "T044 frontend/src/features/services/"
Task: "T045 frontend/src/features/domains/"
Task: "T046 frontend/src/features/routes/"
Task: "T048 frontend/src/routes/dashboard.tsx"
```

---

## Implementation Strategy

### MVP First（仅 User Story 1）

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational（关键闸口——阻塞一切故事）
3. Complete Phase 3: User Story 1
4. **STOP and VALIDATE**: 跑 quickstart.md V-1 + `make test-pipeline`，独立验证
5. MVP 即可交付：替代手工 YAML 工作流（「四不一自动」全部落在 US1 链路）

### Incremental Delivery

1. Setup + Foundational → 基础就绪
2. US1（发布闭环）→ V-1 验收 → **MVP**
3. US2（可回滚：敢改生产）→ V-2 验收
4. US3/US4/US5 并行（策略治理 / HTTPS / 可信观测）→ V-3/4/5 验收
5. US6/US7（合规审批 / 删除护栏）→ V-6/7 验收
6. Polish → V-8/9 + SC 证据（每步独立验收，不破坏既有故事）

### Parallel Team Strategy

Foundational 完成后：

1. Dev-A：US2 → US6（管线/回滚/审批线）
2. Dev-B：US3 → US7（资源治理/依赖保护线）
3. Dev-C：US4 → US5（证书/观测/漂移线）

三线文件互不冲突，故事独立集成验收。

---

## Notes

- [P] = 不同文件、无依赖；[USn] 对应 spec.md 用户故事编号
- 每个 T 完成即 commit（仅当前 Git 用户 author）；检查点处停下独立验证
- 测试先写并确保失败，再实现（本清单含测试，遵循 R17）
- 避免：跨故事同文件任务未标依赖、含糊任务、绕过管线的"捷径"任务（宪法 IV 红线）
