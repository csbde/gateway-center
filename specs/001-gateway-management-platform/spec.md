# Feature Specification: Gateway Center 统一网关管理中心（一期）

**Feature Branch**: `001-gateway-management-platform`

**Created**: 2026-09-05

**Status**: Draft

**Input**: User description: "《Gateway Center —— 产品功能规格定义》—— 开发企业级 Traefik 可视化网关管理平台（控制平面）。Gateway Center 管理期望状态（域名、路由、上游服务、中间件、HTTPS 策略、配置发布），Traefik 作为数据平面负责实际流量转发。管理员通过 Web UI 完成'创建域名 → 创建服务 → 配置路由 → 绑定安全策略 → 预览配置 → 发布 → Traefik 自动生效'全流程，不登录服务器、不执行 SSH、不编辑 YAML、不修改 Docker Labels。"（完整输入含 28 节：一期范围、RBAC、审计、回滚、Drift 检测、AC-001~015 验收标准等）

## 项目定位与边界

> **Gateway Center 是网关控制平面（Control Plane），Traefik 是流量数据平面（Data Plane）。**

系统状态流转遵循：

```text
Gateway Center（期望配置 Desired State）
        ↓
Configuration Engine（校验 / 生成 / 版本化）
        ↓
Traefik Dynamic Configuration（动态配置）
        ↓
Traefik（实际运行状态 Actual State）
```

**一期明确不包含**（保留为未来扩展，本规格 MUST NOT 为其引入实现承诺）：
Kubernetes、Service Mesh、WAF、CDN、全局负载均衡、多区域容灾切换、流量分析、
计费、Docker 自动服务发现、TCP/UDP 网关、Gateway Agent（多节点代理下发）。

一期实际可以只部署一个 Gateway Node，但产品模型 MUST 支持未来多节点。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 通过 Web UI 端到端发布一条反向代理（Priority: P1）

网关管理员在不接触任何底层配置文件的前提下，为内部系统发布一个 Web 入口：
创建一个运行 Traefik 的网关节点，登记上游服务 CRM API 及其三个实例地址，
登记域名 crm.example.com 并启用 HTTPS，创建路由把「该域名 + / 路径」绑定到
CRM API 服务，经过去重/格式/依赖校验、配置变化预览、确认后发布，
系统自动产出网关可加载的动态配置并验证其已被实际加载，流量随即生效。

**Why this priority**: 这是平台的核心价值主张（成功标准"四不一自动"全部落在这条链路上）。
没有它，其余故事都没有对象可管理；只有它，就已经替代了手工 YAML 工作流，构成可交付的 MVP。

**Independent Test**: 在一个已运行 Traefik 的节点上，从空平台开始走完整流程，
随后用浏览器访问 crm.example.com，请求被转发到目标实例——全程不登录网关节点、
不手写任何配置，即可独立验证。

**Acceptance Scenarios**:

1. **Given** 平台无任何数据，**When** 管理员创建网关节点（名称、地址、环境类型），**Then** 节点出现在 Gateway Nodes 列表并进入状态探测（AC-001）
2. **Given** 一个启用的 Service，**When** 管理员为其添加多个 Target（地址、权重）并启用，**Then** Service 健康状态按其启用 Target 聚合展示（AC-003）
3. **Given** 域名 crm.example.com 已创建且启用，**When** 管理员用简单模式创建路由（域名 + 路径 / + 前缀匹配 + 目标 Service + HTTPS），**Then** 系统自动生成底层路由规则，界面不要求用户书写任何规则语法（AC-004）
4. **Given** 路由存在域名重复、目标服务被禁用等缺陷，**When** 管理员尝试发起发布，**Then** 验证阶段逐条列出问题并阻断发布（AC-007）
5. **Given** 配置已通过验证，**When** 管理员查看本次将发布的变化（新增/修改/删除的路由、服务、中间件），**Then** 未确认预览时发布按钮不可用（AC-008）
6. **Given** 发布成功，**When** 系统对照网关实际加载的路由/服务集合，**Then** 该次部署标记为「生效已验证」；若网关未加载预期配置，部署标记为「失败」并展示差异（AC-010）
7. **Given** 网关正在按版本 N 服务流量，**When** 新版本发布，**Then** 目标节点上不存在任何时刻可读到"半成品"配置（发布原子完成）

---

### User Story 2 - 配置版本历史与一键回滚（Priority: P2）

管理员在某次发布后发现新路由规则导致部分流量异常，进入发布历史，
选择上一个成功版本，对比两版差异确认无误，直接回滚；系统重新发布该版本的
配置快照并验证生效，全程不需要手工恢复任何配置。

**Why this priority**: 可回滚性是"敢于通过平台变更生产配置"的前提（宪法 V），
但它在 US1 的发布产物之上才有意义，故列为 P2。

**Independent Test**: 连续完成两次成功发布后回滚到第一版，验证网关恢复第一版行为，
且回滚产生了新的版本与部署记录——不依赖任何中间件/证书功能即可测试。

**Acceptance Scenarios**:

1. **Given** 任意一次发布，**When** 版本生成，**Then** 版本记录包含版本号、节点、创建人、时间、变更内容、配置快照与发布状态（AC-006）
2. **Given** 存在多个历史版本，**When** 用户选择两个版本对比，**Then** 差异以路由/服务/中间件为单位呈现新增、修改、删除
3. **Given** 一个历史成功版本，**When** 用户发起回滚，**Then** 回滚按与正向发布相同的管线执行（验证、部署、运行时校验），并生成新部署记录（AC-012）
4. **Given** 回滚也失败，**When** 系统检测到网关仍运行回滚前配置，**Then** 明确提示"网关运行版本 101 ≠ 期望版本 102"并给出最近一次已知良好版本（见 Edge Cases）

---

### User Story 3 - 可复用 Middleware 安全策略（Priority: P2）

管理员创建「办公网 IP 白名单」「限流 100 r/s」「统一安全响应头」三个中间件，
将其中若干个按指定执行顺序绑定到多条路由上复用；删除被引用中间件时系统阻止并列出引用方。

**Why this priority**: 复用与执行顺序是安全策略治理的关键，但 MVP 路由（纯转发）
可先不含中间件，故 P2。

**Independent Test**: 创建 IP Allow List 并绑定两条路由，非白名单来源访问被拒绝、
白名单来源放行；解绑后可删除，未解绑删除被阻止并显示引用清单（AC-005、AC-015）。

**Acceptance Scenarios**:

1. **Given** 五种一期类型（Security Headers / IP Allow List / Rate Limit / Redirect / Strip Prefix），**When** 管理员按类型表单填写参数，**Then** 每类参数经过独立校验（如 CIDR 列表合法性、速率阈值 > 0）
2. **Given** 一条路由绑定多个中间件，**When** 管理员调整执行顺序（IP Allow List → Rate Limit → Security Headers），**Then** 发布产物中的处理顺序与该顺序一致
3. **Given** 中间件被 N 条路由引用，**When** 管理员删除它，**Then** 操作被阻止并逐条列出引用路由（AC-015）
4. **Given** 高级模式路由，**When** 具备高级权限的用户编辑完整规则表达式，**Then** 系统显示风险提示、执行语法验证、发布前预览，并记录审计日志

---

### User Story 4 - HTTPS 与证书策略自动化（Priority: P2）

管理员为域名选择证书方式：自动签发、泛域名（DNS Challenge）、或导入自有证书；
平台展示每张证书的有效期与状态，到期前主动提示；DNS 服务商凭证加密保存、
任何界面与导出中都不出现明文。

**Why this priority**: HTTPS 是现代入口的默认要求，但平台可先以 HTTP 与
手工导入证书支撑内部场景，自动签发体系随后接入，故 P2。

**Independent Test**: 配置一个泛域名走 DNS Challenge 策略、一个域名导入自有证书，
验证域名详情正确反映策略与到期时间；检查 API 响应与审计记录中凭证均被脱敏。

**Acceptance Scenarios**:

1. **Given** 域名 `*.dev.example.com`，**When** 管理员为其配置自动证书，**Then** 系统强制要求 DNS Challenge 方式（HTTP Challenge 选项对该域名不可用）
2. **Given** 证书即将到期（阈值内），**When** 用户查看 Dashboard 或域名详情，**Then** 到期预警醒目展示
3. **Given** 任何读取配置的接口，**When** 返回含 DNS Token / 私钥的材料，**Then** 值被脱敏；日志与版本 Diff 中同样不可见

---

### User Story 5 - 运行状态可见性与配置漂移检测（Priority: P2）

运维人员打开 Dashboard，看到每个网关的在线状态、Traefik 版本、路由/服务/中间件
实际加载数量，以及"期望配置"与"网关实际配置"的一致性标志；有人在网关侧手工改动
导致实际版本落后于平台版本时，节点立刻显示 Configuration Drift 并展示差异项。

**Why this priority**: "发布成功 ≠ 实际生效"是宪法 XI 的硬约束，是平台可信度的
基石；但其消费场景在 US1/US2 的产物之上，故 P2。

**Independent Test**: 人为制造网关侧配置与平台期望不一致（如让节点上报旧版本），
验证漂移在下一轮状态同步内可见且差异可解释。

**Acceptance Scenarios**:

1. **Given** 期望版本 103、节点实际版本 101，**When** 状态同步完成，**Then** 该节点标记 Configuration Drift，Dashboard 汇总可见
2. **Given** 节点连续多次探测无响应，**When** 超过离线判定阈值，**Then** 状态由 online 变为 offline，最后在线时间保留
3. **Given** 期望态与实际态，**When** 用户查看任一路由，**Then** 平台意图与网关加载确认分列展示，互不混同

---

### User Story 6 - RBAC、生产发布审批与全量审计（Priority: P3）

开发者修改了路由草稿并提交发布申请；网关管理员在生产节点上审阅差异、批准后发布；
只读用户无法做任何修改；所有增删改、启停、发布、回滚、权限变更均留下可检索的
操作前/操作后审计记录。

**Why this priority**: 企业治理与安全合规必需（宪法 IX/X），但单管理员的小规模
一期部署可先自证流程，故 P3；仍为一期验收范围。

**Independent Test**: 以四种角色分别登录执行同一组操作，验证权限矩阵逐项生效；
抽查审计日志能还原一次"修改→提交→审批→发布→回滚"的完整链条。

**Acceptance Scenarios**:

1. **Given** Viewer 登录，**When** 尝试任何写操作入口，**Then** 入口不可用且直接调用被拒绝
2. **Given** Developer 对 production 环境节点，**When** 尝试直接发布，**Then** 被拒绝，仅可提交发布申请；**Given** test 节点，**Then** 可保存并发布（仍不可跳过验证）
3. **Given** 一条待审批申请，**When** 由他人（非提交者）的 Gateway Admin 批准并发布，**Then** 审计记录包含提交人、批准人两个身份
4. **Given** 任意 Create/Update/Delete/Enable/Disable/Deploy/Rollback/证书策略/权限变更操作（AC-014），**Then** 审计含操作人、动作、资源、前值、后值、时间、IP，敏感字段脱敏

---

### User Story 7 - 资源依赖保护与安全删除（Priority: P3）

管理员删除仍被两条路由使用的 CRM API，系统阻止并列出全部引用；管理员先禁用
该域名再归档相关路由，一周后资源才允许软删除；任何历史版本快照不受删除影响。

**Why this priority**: 防止误删导致不可解释的流量中断（宪法 VI），依赖关系在
US1 资源模型建立后即需完善，但删除是低频操作，故 P3。

**Independent Test**: 构造完整引用链（Domain←Route→Service→Target），逐层尝试删除，
验证依赖检查、Disable/Archive/软删除三种处置方式与历史快照完整性（AC-015）。

**Acceptance Scenarios**:

1. **Given** Service 被启用中的 Route 引用，**When** 删除，**Then** 阻止并提示"该资源正在被以下 Route 使用：crm.example.com、api.example.com，请先解除依赖关系"
2. **Given** 资源被软删除，**When** 生成新配置，**Then** 该资源从生成结果中排除，但历史版本快照与审计完整保留
3. **Given** Config Version / Deployment / 审计日志，**When** 任何角色尝试删除，**Then** 一律拒绝（不可删除类资源）

---

### User Flows

**UF-1 Create Gateway**：
进入 Gateway Nodes → 新建（名称、地址、环境类型 development/test/staging/production、备注）
→ 保存 → 系统开始周期状态探测 → 列表显示状态（online/offline/degraded/unknown）、
Traefik 版本、最后在线时间 → 可编辑/启用/禁用/软删除（无关联部署时）。

**UF-2 Create Service**：
进入 Services → 新建 Service（名称、描述、健康检查策略）→ 添加 Target（地址、权重、
协议 http/https）→ 校验地址格式与重复 → 启用/禁用单个 Target → 服务健康状态
按启用 Target 聚合展示。

**UF-3 Create Route（简单模式）**：
进入 Routes → 新建 → 选择 Gateway Node → 名称 → 选择 Domain → 路径 + 匹配方式
（Exact/Prefix）→ 选择目标 Service → HTTPS 开关（联动域名证书策略）→ 多选 Middleware
并排序 → 系统展示自动生成的规则预览 → 保存为草稿 →（高级模式分支：仅 Gateway Admin
及以上可切换，输入完整规则表达式，风险提示 + 语法验证 + 审计）。

**UF-4 Deploy Configuration**：
选择 Gateway Node → 触发验证（六类：Domain/Route/Service/Middleware/依赖/生成产物，
失败→逐项报告并终止）→ 生成配置 → 创建 Config Version（pending→validating→ready）
→ 预览 Diff → 用户确认 → 生产节点：需要审批通过 → Deploy（deploying，原子替换）
→ Runtime Verification（success/failed）→ 展示加载确认。

**UF-5 Rollback Configuration**：
Deployments / Config Versions → 选择历史成功版本 → 与当前版本对比 → 确认回滚
→ 复用 UF-4 的部署与验证管线（快照重发布）→ 生成新部署记录 → 验证生效。

### Edge Cases

- **网关离线时发布**：验证与配置生成可完成，部署环节被阻断并提示；节点恢复在线后须由用户重新确认发布（MUST NOT 在无人确认的情况下自动补发）。
- **部署中途失败**：目标节点保持并保持上一个完整有效配置（原子性）；部署标记 failed，错误可查，不产生"半生效"版本。
- **运行时校验失败**：文件写入成功但网关未加载预期配置（语法被拒、端口冲突、证书缺失）→ 部署标记 failed/漂移告警，展示期望与实际差异，给出最近已知良好版本供回滚。
- **回滚失败**：明确报告网关当前实际运行版本，允许重试或改选其他历史版本；MUST NOT 出现无记录的回滚尝试（回滚尝试本身进审计）。
- **Configuration Drift**：状态同步发现期望≠实际时持续标红并计入 Dashboard，直至一次成功发布或人工处置消除；漂移期间的发布要求先重新验证。
- **域名重复**：同一节点内精确域名/泛域名重复被拒；泛域名与精确域名并存时（`*.example.com` 与 `a.example.com`）允许共存，路由冲突检查按精确优先提示匹配歧义。
- **无效路由规则**：高级模式语法错误、引用不存在资源、路径格式非法均在保存草稿与发布前两次验证中拦截。
- **两条路由条件完全重叠**（同节点同匹配条件）：发布前冲突告警，要求处理后才可发布。
- **服务无启用 Target 仍被路由引用**：发布验证阻断，提示先添加/启用 Target 或解除绑定。
- **删除被引用资源**：一律阻止 + 引用清单（Service/Domain/Middleware 三类均检查）。
- **中间件参数非法**：CIDR 格式、速率下限、重定向目标等按类型独立校验，错误定位到字段。
- **敏感凭证**：DNS Token 校验失败（provider 拒绝）时仅报告失败原因，不回显凭证；凭证轮换无需修改域名绑定关系。
- **并发编辑**：两名管理员修改同一路由时，后保存者收到冲突提示，MUST NOT 静默覆盖。
- **节点被禁用/删除后**：其未完成部署冻结，相关路由在生成时排除，Dashboard 计入离线。
- **证书策略与实际不符**（域名启用 HTTPS 但无可用证书）：验证告警且发布前确认，运行时校验把证书加载纳入检查。

## Requirements *(mandatory)*

### Functional Requirements

#### A. 网关节点（Gateway Node）

- **FR-001**: 系统 MUST 允许创建、编辑、启用、禁用、软删除网关节点，属性含名称、地址、环境类型（development/test/staging/production）、备注；环境类型决定发布管控强度。
- **FR-002**: 系统 MUST 为每个节点维护并展示运行状态：online / offline / degraded / unknown，以及 Traefik 版本与最后在线时间。
- **FR-003**: 系统 MUST 周期性探测节点状态；连续超过容忍窗口无响应时 MUST 判定 offline 并保留最后成功探测信息。
- **FR-004**: 产品模型 MUST 支持同时管理多个节点且配置、版本、部署均以「节点」为作用域；一期运行实例允许仅部署一个节点。

#### B. 域名（Domain）

- **FR-005**: 系统 MUST 允许创建、编辑、启用、禁用、软删除域名资源，支持普通域名与泛域名（`*.dev.example.com`）两种形态；域名 MUST 归属某个网关节点并在节点内唯一。
- **FR-006**: 域名 MUST 为独立资源且 MUST NOT 直接决定流量去向；Host 匹配关系 MUST 仅通过 Route 建立。
- **FR-007**: 域名 MUST 支持 HTTPS 策略配置：不启用 / 自动签发 / 泛域名（仅 DNS Challenge）/ 自有证书导入。
- **FR-008**: 系统 MUST 在启用 HTTPS 的域名缺少可用证书（或证书处于到期阈值内）时，在验证与界面中给出明确告警。

#### C. 服务与实例（Service / Target）

- **FR-009**: 系统 MUST 允许创建、编辑、启用、禁用、软删除 Service；Service MUST NOT 等同于单台服务器或单容器，用户 MUST NOT 被要求区分 Docker/主机/远程服务类型。
- **FR-010**: 一个 Service MUST 支持多个 Target：添加、编辑、启用、禁用、软删除；Target 含地址、权重，一期协议范围 HTTP/HTTPS。
- **FR-011**: 系统 MUST 展示每个 Target 的健康状态，并按启用 Target 聚合出 Service 健康状态。
- **FR-012**: 发布验证 MUST 阻断「被路由引用但无任何启用 Target」的 Service。

#### D. 路由（Route）

- **FR-013**: Route MUST 关联一个 Gateway Node 与一个 Service；简单模式 MUST 提供：名称、Domain、路径、匹配方式（Exact/Prefix）、目标 Service、HTTPS、Middleware 集合。
- **FR-014**: 系统 MUST 依据简单模式字段自动生成底层路由规则；普通用户 MUST NOT 需要理解或输入规则语法、中间件语法或配置结构。
- **FR-015**: 系统 MUST 提供高级模式：由具备高级权限（Gateway Admin 及以上）的用户书写完整匹配表达式（支持 Host、Path、PathPrefix、HostRegexp、Headers、Method 及其与/或组合）；高级模式 MUST NOT 改变或破坏简单模式产物。
- **FR-016**: 高级模式 MUST 满足：风险提示、语法验证、发布前预览、强制审计记录。
- **FR-017**: Route MUST 具备 Draft / Enabled / Disabled / Archived 状态并按状态机约束流转；禁用与归档 MUST NOT 影响历史版本快照。
- **FR-018**: 一条 Route MUST 支持绑定多个 Middleware 并指定其执行顺序，执行顺序 MUST 在生成产物中得到保持。
- **FR-019**: 发布前验证 MUST 检出：引用资源缺失或禁用、路径格式非法、规则语法非法、同节点匹配条件完全重叠的冲突路由（告警并需人工处置）。

#### E. 中间件（Middleware）

- **FR-020**: Middleware MUST 作为独立、可复用资源管理：创建、编辑、启用、禁用、软删除、被多个 Route 复用。
- **FR-021**: 一期 MUST 提供五类中间件：Security Headers、IP Allow List、Rate Limit、Redirect（含 HTTP→HTTPS）、Strip Prefix；每类 MUST 有独立参数表单与类型校验（CIDR 合法性、速率正数等）。
- **FR-022**: 路由 MUST NOT 各自重复保存完整中间件配置；绑定关系仅引用中间件资源本身。

#### F. 配置管线、版本与发布（Desired State → Deploy）

- **FR-023**: 业务修改 MUST 进入期望状态并经过强制管线：Draft → Validation → 配置生成 → Diff Preview → Deploy → Runtime Verification；MUST NOT 存在「保存即生效」路径，MUST NOT 存在绕过验证或预览的发布入口。
- **FR-024**: 验证 MUST 覆盖六类：Domain、Route、Service、Middleware、依赖关系、生成产物可被网关接受；任一失败 MUST 阻断发布并逐条报告。
- **FR-025**: 系统 MUST 从业务模型单向生成网关动态配置（路由、服务、中间件、TLS）；人工编辑的底层配置 MUST NOT 被当作业务事实来源。
- **FR-026**: 配置下发 MUST 原子化：目标节点任一时刻读取到的都是完整配置；MUST NOT 以直接覆盖在用文件的方式发布。
- **FR-027**: 任何发布 MUST 针对明确的 Gateway Node，且每次生产变更 MUST 生成不可变 Config Version，记录：版本号、节点、创建人、时间、变更内容、配置快照、生成配置、发布状态。
- **FR-028**: Deployment MUST 具备并流转以下状态：pending → validating → ready → deploying → success / failed（回滚产生的部署同样如此）。
- **FR-029**: 发布前 MUST 提供版本间 Diff，至少以资源为单位展示路由/服务/中间件的新增、修改、删除；用户 MUST 确认后才可正式发布。
- **FR-030**: 发布成功后系统 MUST 独立执行运行时校验（对照网关实际加载的路由/服务集合与版本），MUST NOT 以「写入成功」推断「已生效」。
- **FR-031**: 用户 MUST 可选择任一历史成功版本回滚：以该版本快照重新发布，复用同一管线（验证、部署、运行时校验）并生成新部署记录；MUST NOT 要求手工编辑配置恢复。
- **FR-032**: 系统 MUST 支持版本历史浏览与任意两版本对比；Config Version、Deployment、部署日志、审计日志 MUST NOT 可删除。

#### G. HTTPS 与证书

- **FR-033**: 证书的申请、续期、存储 MUST 由 Traefik/证书提供方完成；Gateway Center MUST 仅管理证书策略、状态展示、到期监控与域名绑定关系，MUST NOT 修改底层证书存储文件。
- **FR-034**: 泛域名证书策略 MUST 限定 DNS Challenge；系统 MUST NOT 允许以 HTTP Challenge 为泛域名签发。
- **FR-035**: DNS Provider 凭证 MUST 加密存储、接口不回显、日志/Diff/导出中不可见明文；凭证轮换 MUST NOT 要求重配域名绑定。

#### H. 权限、审批与审计

- **FR-036**: 系统 MUST 内置 RBAC 四角色：Super Admin（全部权限）、Gateway Admin（节点/域名/服务/路由/中间件管理、发布、回滚、审批）、Developer（查看授权资源、创建与编辑、保存草稿、提交发布申请；默认不可直接发布 production 节点）、Viewer（只读）。
- **FR-037**: production 环境发布 MUST 支持审批链：提交 → 审阅 Diff → 批准 → 部署；批准人 MUST NOT 为提交人本人；非生产环境 MAY 由 Developer 直接保存并发布，但验证与运行时校验 MUST NOT 可跳过。
- **FR-038**: 系统 MUST 对 Create/Update/Delete/Enable/Disable/Deploy/Rollback/证书策略变更/权限变更 记录审计：操作人、动作、资源、操作前数据、操作后数据、时间、来源 IP；敏感字段 MUST 脱敏；审计记录 MUST NOT 可修改或删除。

#### I. 资源保护与状态分离

- **FR-039**: 删除任何资源前系统 MUST 执行依赖检查；被引用资源 MUST 被阻止删除并完整展示引用方清单；处置路径为 Disable → Archive → 软删除。
- **FR-040**: 期望状态与实际状态 MUST 分离存储与展示；当节点实际配置与期望不一致（版本或资源集合）时，系统 MUST 在节点与 Dashboard 显示 Configuration Drift 并可查看差异。

#### J. 界面与平台属性

- **FR-041**: 系统 MUST 提供页面：Dashboard、Gateway Nodes、Domains、Services、Routes、Middlewares、Deployments、Config Versions、Audit Logs、Settings；Dashboard MUST 汇总在线/离线网关、漂移节点、路由/服务/中间件数量、最近发布。
- **FR-042**: 普通界面 MUST 以「域名 → 路径 → 目标服务 → 安全策略」语言表达；Traefik 专业术语 MUST 仅出现在高级模式、技术详情与 Debug 视图。
- **FR-043**: 一期全部核心能力 MUST 通过可编程 API 提供，Web UI MUST 与 API 走同一业务能力（API First，禁止仅服务于单页面的私有逻辑）。

### Acceptance Criteria 追溯（AC-001~015 → FR）

| AC | 内容摘要 | 覆盖 FR |
|----|----------|---------|
| AC-001 | 创建 Gateway Node | FR-001 |
| AC-002 | 创建 Domain | FR-005 |
| AC-003 | Service 多 Target | FR-009、FR-010 |
| AC-004 | Route 绑定 Domain+Service | FR-013、FR-014 |
| AC-005 | Route 绑定多 Middleware | FR-018、FR-020 |
| AC-006 | 生成配置版本 | FR-027 |
| AC-007 | 发布前验证 | FR-023、FR-024 |
| AC-008 | 预览配置变化 | FR-029 |
| AC-009 | 发布到 Gateway | FR-026、FR-028 |
| AC-010 | 验证配置生效 | FR-030 |
| AC-011 | 查看历史版本 | FR-032 |
| AC-012 | 回滚历史成功版本 | FR-031 |
| AC-013 | Drift 检测 | FR-040 |
| AC-014 | 关键操作审计 | FR-038 |
| AC-015 | 依赖资源不可直删 | FR-039 |

### Key Entities *(include if feature involves data)*

- **Gateway Node**：运行 Traefik 的网关节点，配置发布的目标。属性：名称、地址、环境类型、运行状态（online/offline/degraded/unknown）、Traefik 版本、最后在线时间、启停标记。
- **Domain**：某节点下的域名资源（普通/泛域名）；属性：名称、HTTPS 策略、证书方式、状态。只通过 Route 建立流量关系。
- **Service**：逻辑上游服务（如 CRM API）；属性：名称、健康状态（由 Target 聚合）、状态机。
- **Target**：Service 的实际运行实例；属性：地址、协议（http/https）、权重、健康状态、启停。
- **Route**：核心流量规则实体：进入条件 → 匹配 → Middleware 链 → Service；属性：模式（简单/高级）、域名/路径/匹配方式或高级表达式、Middleware 顺序集合、状态（Draft/Enabled/Disabled/Archived）。
- **Middleware**：可复用处理能力（一期五类）；属性：类型、类型化参数、状态；被 Route 按序引用。
- **Certificate Policy**：域名侧证书策略（方式、DNS Challenge 凭证引用、到期监控阈值）；只读观测证书实际状态。
- **Config Version**：某节点某次发布的不可变快照：版本号、变更内容、创建人、时间、配置快照、生成配置、状态。
- **Deployment**：一次发布/回滚执行记录：目标节点、版本引用、状态机（pending…success/failed）、运行时校验结果、日志。
- **Audit Log**：追加写的操作记录：操作人、动作、资源、前值、后值、时间、IP（脱敏后）。
- **User / Role**：平台用户与 RBAC 角色（Super Admin/Gateway Admin/Developer/Viewer）。

### Non-Functional Requirements

- **NFR-SEC-01（安全）**: 全部写操作 MUST 经权限校验；Token/密码/私钥/证书私钥材料 MUST 加密存储且在任何响应、日志、Diff、导出、错误信息中脱敏；平台到网关的状态通道 MUST 认证。
- **NFR-AUD-01（可审计）**: 100% 关键操作可还原"谁、何时、对什么、从什么改成什么、来自哪里"；审计记录在线保留 ≥ 365 天，可检索、不可篡改。
- **NFR-REL-01（可靠性）**: 任一发布失败 MUST 使网关停留在最近一次成功配置；每个成功版本 MUST 可回滚；平台自身重启 MUST NOT 丢失已确认的期望状态或进行中的部署记录。
- **NFR-USE-01（易用性）**: 首次使用的管理员 ≤ 15 分钟完成「服务+域名+路由+发布」全流程；所有校验错误信息 MUST 指出资源定位与可执行的修复建议；普通流程零 Traefik 术语暴露。
- **NFR-MNT-01（可维护性）**: 新增一类中间件 MUST 不影响核心发布管线；生成规则 MUST 与业务模型分层隔离，网关产品版本升级的影响 MUST 收敛在生成层内。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 管理员在 15 分钟内，仅通过 Web UI 完成「节点+服务+域名+路由」的创建并发布成功，期间 0 次服务器登录、0 行手工配置编辑（对应成功标准"四不一自动"）。
- **SC-002**: 100% 到达网关的配置变更具备版本记录（快照、操作人、时间、节点）与对应审计日志，抽样审计违规数为 0。
- **SC-003**: 任一历史成功版本可在 3 分钟内完成回滚并通过运行时校验；回滚操作 100% 生成新部署记录。
- **SC-004**: 配置漂移在产生后 ≤ 2 分钟内于节点详情与 Dashboard 可见；验证失败或"发布成功但未生效"场景 100% 被显式标记而非静默通过。
- **SC-005**: 首次接触平台的非资深管理员在简单模式下一次性成功完成标准路由配置的比例 ≥ 90%（以内部用户测试度量）。
- **SC-006**: 一期目标规模 ≥ 10 个网关节点、≥ 200 个域名、≥ 1000 条路由、≥ 100 个中间件时，列表与详情页操作感知响应 ≤ 3 秒（95 分位）。

## Assumptions

- **登录方式**：一期使用平台自建账号（Super Admin 创建用户并分配角色）；企业 SSO/LDAP/OIDC 集成不在一期范围（延续 §21 防范围失控原则）。
- **审批边界**：仅 production 环境节点强制审批；非生产环境 Developer 可保存并直接发布（验证环节仍不可跳过）。非生产环境的简化链路依据宪法 IX。
- **一期下发方式**：单节点直连部署（Gateway Center 与目标 Traefik 可同宿主机/受控文件系统）；具体安全下发通道属技术方案（/speckit-plan）决定，但 MUST NOT 采用「平台直接 SSH 编辑配置」方式（宪法 XII/IV），MUST NOT 提前实现 Gateway Agent（§21）。
- **Traefik 前提**：目标节点上的 Traefik 实例及其 Static 配置（入口点、Provider、证书 Resolver）由运维预先部署维护，平台只读且不修改（宪法 III）。
- **状态采集**：节点状态与"实际配置"来自网关侧可观测信息（API/状态查询与文件对账）；默认探测周期 30 秒、连续 3 次无响应判 offline，degraded 定义为可达但存在失败的启用 Target 或加载错误。
- **规模与性能默认值**：SC-006 的规模目标为合理默认，如有企业既有规模数据应在 /speckit-clarify 或评审中修订。
- **证书到期预警阈值**：默认 30 天（可配置）。
- **时区/语言**：界面简体中文；时间按平台配置的时区展示，审计存储使用带时区标注的统一时间。
- **并发编辑冲突**：采用"后提交者提示冲突并刷新重试"策略，不提供实时协同编辑。
