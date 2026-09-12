<!--
Sync Impact Report
==================
Version change: (unversioned scaffold) → 1.0.0
Bump rationale: MAJOR-equivalent first ratification. The file previously held an
  unfilled placeholder scaffold ([PROJECT_NAME] / [PRINCIPLE_N_NAME]); no project-
  specific values existed to preserve, so this is treated as ratification of a new
  constitution rather than an amendment of a governing one.

Modified principles (source doc → constitution):
  - §2 管理模型分离        → I. 管理模型与 Traefik 配置分离
  - §3 声明式配置 + §5 双模式 + §22 UI 不暴露复杂性 → II. 声明式配置与双模式分层
  - §6 Static/Dynamic 分离  → III. Static 与 Dynamic 配置职责分离
  - §7 禁止直接改配置 + §10 原子发布 + §11/§12 Agent/禁 SSH + §21 发布前验证
                            → IV. 发布必须经过强制管线
  - §8 版本化 + §9 回滚     → V. 配置版本化与可回滚
  - §13 统一抽象 + §20 安全删除 → VI. 统一服务抽象与依赖保护
  - §14 HTTPS 自动化 + §15 泛域名 DNS Challenge → VII. 证书自动化与泛域名约束
  - §16 Secret 管理         → VIII. Secret 与敏感信息保护
  - §17 RBAC + §18 生产审批 → IX. RBAC 与发布审批
  - §19 操作审计            → X. 全量操作审计
  - §23 状态分离 + §24 Drift → XI. 期望态与实际态分离及 Drift 检测
  - §26 API First + §29 分层 + §31.7/§31.8 可扩展与不过度设计 → XII. API First、分层架构与范围纪律

Added sections:
  - Project Mission (lead section, from §1)
  - 领域模型与核心状态机 (from §4, §25)
  - 一期范围边界与目标架构 (from §27, §28, §30)
  - Governance, including 冲突裁决规则 (from Constitution Rule)

Removed sections: none. Template HTML comments replaced by project content.

Deferred TODOs:
  - RATIFICATION_DATE 取本次批准日 2026-09-05；若项目另有正式批准日期，需人工更正。
-->

# Gateway Center Constitution（统一网关管理中心）

**项目定位**：企业级 Traefik 可视化网关管理平台
**版本**: 1.0.0 | **批准日期**: 2026-09-05 | **最后修订**: 2026-09-05

## Project Mission

Gateway Center 面向企业内部基础设施，集中管理网关节点、域名、反向代理、上游服务、
路由规则、Middleware、HTTPS/SSL、配置发布、配置版本、网关运行状态与操作审计。

Gateway Center MUST NOT 替代 Traefik。系统职责严格分层：

```text
Gateway Center   →  管理意图（Management Intent）
Traefik          →  运行执行（Runtime Execution）
Application Services
```

所有设计决策服务于四条终极属性：**配置安全性、系统边界清晰性、长期可维护性、范围可控性**。

## Core Principles（核心原则）

以下原则均为不可协商（NON-NEGOTIABLE）的强制约束。偏离任一原则 MUST 在
architecture record 中记录理由、影响范围与补偿措施，并经 Super Admin 批准。

### I. 管理模型与 Traefik 配置分离

- 数据库 MUST NOT 以 Traefik YAML 作为核心业务数据载体。
- 数据库持久化的是业务实体：`Domain` `Route` `Service` `Target` `Middleware` `Certificate Policy`。
- 数据库 MUST NOT 存储 `routers.yml` / `services.yml` / `middlewares.yml` 等 Traefik 原生配置文件内容作为事实来源。
- 所有 Traefik 配置 MUST 由系统从业务模型单向生成（generate），MUST NOT 反向解析人工编辑的 YAML 作为事实来源。

**Rationale**：一旦 YAML 成为业务事实来源，校验、版本化、依赖检查、审计与回滚全部失效，
平台退化为一个带 Web 界面的文本编辑器。生成方向必须单向。

### II. 声明式配置与双模式分层

- 系统 MUST 采用声明式配置架构：用户表达目标（`crm.example.com` → `192.168.1.10:8080`），
  系统负责转换为 `Domain → Route → Service → Target → Traefik Configuration`。
- 系统 MUST NOT 要求普通用户理解 Router Rule、Service Definition、Middleware Syntax 或 YAML 结构。
- 系统 MUST 提供 Simple Mode（默认）与 Advanced Mode 两种配置模式，二者 MUST 互不影响：
  高级模式的使用 MUST NOT 改变或破坏普通模式生成的配置。
- Advanced Mode MUST 满足四项：显示风险提示、执行语法验证、记录审计日志、支持发布前预览。
  Advanced Mode MUST 仅对具备高级权限（Gateway Admin 及以上）的用户开放。
- 普通用户界面 MUST 以「域名 → 路径 → 目标服务 → 安全策略」呈现；
  Traefik 专业术语 MUST 仅出现在高级模式、技术详情与 Debug 页面。

**Rationale**：平台的价值在于抽象。抽象一旦可被绕过，就会在第一个紧急变更时被绕过，
之后不可恢复。风险提示与语法验证是高级模式的准入门槛，而非可选装饰。

### III. Static 与 Dynamic 配置职责分离

- Traefik Static Configuration（`EntryPoints` `Providers` `Certificate Resolver` `API` `Logging` `Metrics`）
  MUST 仅由管理员通过服务器配置维护；Gateway Center MUST NOT 修改核心 Static Configuration。
- Gateway Center MUST 只生成 Dynamic Configuration（`Routers` `Services` `Middlewares` `TLS Configuration`）。
- 动态配置目录结构 MUST 与静态配置物理隔离，建议布局：

```text
/etc/traefik/
├── traefik.yml          # Static，平台只读
├── dynamic/             # Dynamic，平台独占写入
│   ├── routers/
│   ├── services/
│   ├── middlewares/
│   └── tls/
└── acme.json            # 平台只读
```

**Rationale**：静态配置改动需要 Traefik 重启并影响全部流量入口。平台若可写静态配置，
一次路由笔误即演变为网关级事故。目录边界即故障域边界。

### IV. 发布必须经过强制管线

- 任何配置到达 Traefik MUST 经过完整管线，缺一环即不得发布：

```text
Draft → Validation → Configuration Generation → Diff Preview → Deploy → Verification
```

- 系统 MUST NOT 提供「保存即生效」路径。MUST NOT 存在绕过 Validation 或 Diff Preview 的发布入口。
- Validation MUST 至少覆盖：Domain、Route、Service、Middleware、Dependency、Traefik Config 六类。
  任一验证失败 MUST 阻断发布（禁止发布），MUST NOT 降级为警告。
- 发布 MUST 原子化：先写临时文件 → 校验 → 原子 rename 替换。
  MUST NOT 直接覆盖正在使用的配置文件，MUST NOT 让 Traefik 读取到写入中的半成品。
- 多节点架构 MUST 经由 Gateway Agent 下发配置。Gateway Center MUST NOT 直接 SSH 登录网关节点修改配置。
  Agent 职责限定为：接收配置、校验、原子部署、健康检查、状态上报、心跳；Agent MUST NOT 承载业务管理逻辑。
- 一期允许单节点直连部署，但该部署路径 MUST 复用同一管线（不得因单节点而省略任何环节）。

**Rationale**：这是全宪法中唯一直接防止生产事故的约束。原子 rename 与「禁止保存即生效」
共同保证任一时刻 Traefik 读到的都是完整、已验证、可追溯的配置。

### V. 配置版本化与可回滚

- 任何发布到 Gateway 的配置变更 MUST 生成不可变版本（Version），并记录：
  版本号、变更内容、操作人员、发布时间、发布节点、发布状态、配置快照、生成配置。
- 系统 MUST NOT 存在无法追踪历史的配置变更路径。
- 任何成功发布的版本 MUST 支持回滚。回滚 MUST 以「选择历史版本 → 重新部署该版本配置快照」
  实现，MUST NOT 要求用户手工重新编辑配置以恢复。
- 回滚 MUST 走与正向发布相同的管线（含 Validation 与 Verification），MUST NOT 是一条专用捷径通道。
- `Config Version` / `Deployment` / `Deployment Log` / `Audit Log` MUST NOT 可删除。

**Rationale**：可回滚性只有在快照是完整、不可变、自包含时才成立。
若快照依赖当前数据库状态，回滚将回滚出一半旧一半新的配置。

### VI. 统一服务抽象与依赖保护

- 用户 MUST NOT 面对 `Docker Service` / `Host Service` / `Remote Service` 的分类差异；
  对外统一抽象为一个 `Service`。Target 类型（Docker / Local / Remote）是实现细节。
- `Route` MUST 只绑定 `Service`。`Route` MUST NOT 直接绑定 IP 地址、端口或 Docker 容器。
- 生产资源（`Domain` `Route` `Service` `Middleware` 等）MUST NOT 直接物理删除；
  MUST 支持 Disable / Archive / Soft Delete。
- 删除被引用资源前 MUST 执行 Dependency Check 并展示完整引用清单：

```text
CRM API 被以下 Route 使用：
  crm.example.com
  api.example.com
→ 禁止直接删除，请先解除依赖关系
```

**Rationale**：Route 直连 IP 会在实例漂移的瞬间变成静默失效的死路由。
软删除与依赖检查共同保证「一次误删不会导致不可解释的流量丢失」。

### VII. 证书自动化与泛域名约束

- 系统 MUST 支持 HTTP、HTTPS、自动签发证书、泛域名证书、自定义证书。
- 证书生命周期（申请、续期、存储）MUST 由 Traefik 或指定 Certificate Provider 负责。
- Gateway Center 职责限定为：证书状态、到期监控、证书策略、域名绑定关系。
- Gateway Center MUST NOT 直接修改 `acme.json`。
- 泛域名（`*.example.com`）MUST 采用 DNS Challenge。MUST NOT 通过 HTTP Challenge 实现泛域名证书。
- DNS Provider Credentials MUST 加密存储、Secret 隔离、权限受限，MUST NOT 明文展示 API Token。

**Rationale**：HTTP Challenge 在协议层面无法为泛域名签发证书，这不是实现限制而是规范限制。
直接写 `acme.json` 会与 Traefik 的续期写入竞争，结果是不可预测的证书损坏。

### VIII. Secret 与敏感信息保护

- 禁止明文持久化：`API Token` `DNS Token` `Password` `Private Key` `Certificate Secret`。
  上述值 MUST 加密存储。
- API 响应 MUST NOT 返回完整 Secret 值（含回显、导出、预览、Diff 视图）。
- 日志 MUST NOT 出现：Token、Password、Authorization Header、Private Key。
  审计记录的 before/after 快照 MUST 对敏感字段脱敏。
- Secret MUST NOT 出现在配置生成产物的可读预览中（Diff Preview 同样受此约束）。

**Rationale**：Secret 一旦出现在任意一个日志文件、审计记录或浏览器网络面板中，
就已泄露且不可撤回；加密存储只保护了数据库这一层。

### IX. RBAC 与发布审批

- 系统 MUST 采用 RBAC，一期至少提供四级角色：

| 角色 | 权限 |
|---|---|
| Super Admin | 所有权限 |
| Gateway Admin | Gateway / Domain / Route / Service / Middleware / Deployment 全量管理、发布、回滚 |
| Developer | 查看授权资源、创建与编辑草稿、提交发布申请 |
| Viewer | 只读 |

- Developer MUST NOT 默认具备直接发布生产配置的能力。
- 生产环境发布 MUST 支持审批链路：`Developer → Create Draft → Submit → Gateway Admin → Approve → Deploy`。
- 非生产环境 MAY 采用简化链路（`Save → Deploy`），但 MUST NOT 省略 Validation 与 Verification。
- 权限变更本身 MUST 记录审计日志。

**Rationale**：审批是「变更由另一个人看过」的唯一保证。环境分级允许开发环境快、
生产环境稳，而不是让全局一起变慢或一起变险。

### X. 全量操作审计

- 以下操作 MUST 记录：Create、Update、Delete、Enable、Disable、Deploy、Rollback、
  Certificate Change、Permission Change、Gateway Node Change。
- 审计记录 MUST 包含：操作人（Operator）、动作（Action）、资源（Resource）、
  变更前（Before）、变更后（After）、时间（Time）、来源 IP。
- 审计日志 MUST 追加写入（append-only），MUST NOT 可被普通管理员修改或删除。
- Before/After 中的敏感字段 MUST 脱敏（见原则 VIII）。
- Advanced Mode 的每一次使用 MUST 产生可关联到具体 Route 的审计记录。

**Rationale**：审计是事故复盘与责任归属的唯一客观依据。可删除的审计等于没有审计。

### XI. 期望态与实际态分离及 Drift 检测

- 系统 MUST 区分并分别存储 Desired State 与 Actual State，MUST NOT 以其中一方推断另一方。
- 系统 MUST 持续检测配置漂移：当 `Desired Version != Actual Version`，
  或资源集合不一致时，MUST 显式展示 `Configuration Drift` 并告警。
- 系统 MUST NOT 假设「发布成功 = 实际生效」。每次 Deploy MUST 有独立的 Runtime Verification 结果。
- 运行状态监控 MUST 至少展示：Gateway Status、Traefik Version、Router Count、Service Count、
  Middleware Count，以及每个 Router/Service 的加载确认。

**Rationale**：Traefik 可以在配置文件写入成功的情况下拒绝加载（规则语法、证书缺失、端口冲突）。
缺少 Verification 的部署只是「我提交了」，不是「已生效」。

### XII. API First、分层架构与范围纪律

- 所有核心能力 MUST 通过 API 暴露；Web UI MUST 是 API 的消费方而非业务逻辑宿主。

```text
Web UI → Gateway Center API → { Web, CLI, Future Integration, Automation }
```

- MUST NOT 出现只服务于单个 Web 页面、且无法通过 API 复现的业务逻辑。
- 分层 MUST 保持：`Frontend → API → Application Layer → Domain Layer → Infrastructure Layer`。
- MUST NOT 出现 `Controller → 直接写 Traefik YAML` 的短路路径。
  合法调用链为 `Controller → Application Service → Domain Model → Config Generator → Deployment`。
- 状态机 MUST 明确且互不混用：

```text
Deployment: Draft | Validating | Ready | Deploying | Success | Failed | Rolled Back
Route:      Draft | Enabled | Disabled | Archived
Gateway:    Online | Offline | Degraded | Unknown
```

- MUST NOT 因短期便利引入一期不需要的能力（见「一期范围边界」）。可扩展性 MUST 体现为
  模型与接口的正确性，而非提前实现未来功能。

**Rationale**：API First 使自动化与未来集成成为可能而非重写。分层约束使 Traefik 版本升级
停留在 Config Generator 一层，而不下沉污染整个业务模型。

## 领域模型与核心状态机

一期核心领域实体固定为以下拓扑，MUST NOT 随意增删实体种类：

```text
Gateway Node
    ├── Domain
    ├── Route
    │     ├── Service
    │     │     └── Target
    │     └── Middleware
    └── Deployment
```

| 实体 | 职责 | 关键约束 |
|---|---|---|
| Gateway Node | 实际运行 Traefik 的节点，配置发布的目标 | 环境类型：development / test / staging / production |
| Domain | 域名资源（普通域名与泛域名） | 独立资源；MUST NOT 直接决定流量去向，只经 Route 建立关系 |
| Route | 最核心流量规则实体：进入条件 → 匹配 → Middleware → Service | MUST 关联 Gateway Node 与 Service |
| Service | 逻辑服务（CRM API / User Center） | MUST NOT 等同于单台服务器或单容器 |
| Target | 实际运行实例（`192.168.1.10:8080`） | 多 Target 组成负载均衡，支持权重与健康状态 |
| Middleware | 可复用能力（Security Headers / Rate Limit / IP Allow List / CORS / Basic Auth / Strip Prefix / Redirect） | MUST 独立管理，禁止每个 Route 重复保存完整配置；支持多绑定与执行顺序 |
| Deployment | 一次配置发布记录 | 不可删除；承载版本、快照与状态机 |

Middleware 执行顺序 MUST 可表达并生效：

```text
IP Allow List → Rate Limit → Security Headers → Service
```

## 一期范围边界与目标架构

### 一期必须完成

| 领域 | 范围 |
|---|---|
| Gateway | Gateway Node 管理 |
| Domain | 域名管理（普通 + 泛域名） |
| Service | Service 与 Target 管理 |
| Route | Host、Path、HTTPS、Service 绑定 |
| Middleware | Security Headers、IP Allow List、Rate Limit、Redirect、Strip Prefix |
| Deployment | Validation、Config Generation、Deploy、Version、Rollback |
| Monitoring | Gateway Status、Router Status、Service Status |
| Platform | RBAC、Audit Log、Drift Detection |

### 一期明确不做

MUST NOT 实现：Kubernetes、Service Mesh、WAF、Global Load Balancer、CDN、
Traffic Analytics、Billing、Multi-Region Failover、Docker Automatic Discovery、
TCP/UDP Gateway、Gateway Agent（多节点下发）。

上述能力保留为未来扩展，MUST 通过模型与接口预留，MUST NOT 提前实现。此清单是防范围失控的硬边界。

### 目标架构演进

```text
一期：Gateway Center → Traefik Dynamic Config → Traefik

二期：Gateway Center → Gateway Agent × N → Traefik × N
```

一期 MUST 在不实现 Agent 的前提下，保证发布抽象支持未来多节点（`Deployment` 以节点为维度）。

## Governance（治理）

### 权威性

本 Constitution 优先于其他一切开发实践、既有代码习惯与个人偏好。
冲突时以本文件为准；本文件与更安全的设计冲突时，以安全性为准。

### 冲突裁决规则

当新的功能设计与本 Constitution 冲突时：

> **优先保证配置安全性、系统边界清晰性和长期可维护性，而不是为了快速实现而直接修改 Traefik 底层配置。**

### 修订程序

1. 提出修订：说明动机、受影响原则、对既有实现的冲击。
2. 影响评估：列出需要同步更新的模板、spec、plan 与代码区域（Sync Impact）。
3. 批准：原则新增或语义变更 MUST 由 Super Admin 批准；措辞澄清 MAY 由 Gateway Admin 批准。
4. 落盘：更新本文件、版本号与 `Last Amended` 日期，并在文件顶部记录 Sync Impact Report。
5. 迁移：破坏性修订 MUST 附迁移说明（旧做法如何收敛到新约束）。

### 版本策略

采用语义化版本 `MAJOR.MINOR.PATCH`：

- **MAJOR**：删除或重定义治理原则 / 任何向后不兼容的治理变更（如移除 Validation 强制门禁）。
- **MINOR**：新增原则或章节，或实质性扩充既有指引（如新增一类 Middleware 约束）。
- **PATCH**：措辞澄清、错别字修正、非语义性精炼。

### 合规审查

- 每次 code review MUST 验证变更是否符合本 Constitution；不符合须给出修正方案或已批准的偏离记录。
- 引入额外复杂度 MUST 说明理由，否则按违反原则 XII（范围纪律）处理。
- `/speckit-analyze` 与 `/speckit-checklist` MUST 将 constitution 一致性纳入检查范围。
- 运行时开发指引以 `.specify/memory/constitution.md` 为唯一治理来源。

**Version**: 1.0.0 | **Ratified**: 2026-09-05 | **Last Amended**: 2026-09-05
