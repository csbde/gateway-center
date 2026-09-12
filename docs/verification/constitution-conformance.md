# 宪法符合性人工检查记录（T092）

**Feature**: 001-gateway-management-platform
**检查日期**: 2026-09-13
**检查依据**: quickstart.md §5「宪法符合性人工检查（评审用）」
**检查方式**: 静态代码审查（grep + 源码核对 + 测试用例核对），未运行测试套件

> 本记录覆盖 §5 表全部 7 项原则。标记「运行时」的子项需在 T093 端到端全量回归中实证，此处仅确认代码与测试用例具备对应断言。

---

## 原则 I — 模型→YAML 单向，库内无被当作事实来源的 YAML 字段

| 检查动作 | 结果 |
|---|---|
| `grep -r "ParseTraefikYAML" backend/` 应为空 | ✓ 空 |
| 无从 Traefik 动态配置反向解析回模型的代码（`UnmarshalYAML` / `ParseDynamic` / `Decode.*Dynamic`） | ✓ 空 |

**结论**：生成方向严格单向。平台仅经 `generate.Generate(snapshot)` 产出 YAML，从不回读动态配置作为期望态来源。`traefikapi` 客户端仅读取 `/api/overview` 与 `/api/http/routers\|services\|middlewares` 做运行时校验比对（只读），不反序列化为领域模型。

---

## 原则 III — 平台只读 Traefik Static，写入路径恒为 `<deploy_root>/dynamic/`

| 检查动作 | 结果 |
|---|---|
| 写入路径前缀恒为 `dynamic/` 子树 | ✓ `deployer.go:40` `filepath.Join(deployRoot, "dynamic")` |
| 路径越界防护（`..`/绝对路径/符号链接） | ✓ `deployer.go:112-119` `safeRelPath` 拒绝并返回 `ErrUnsafePath` |
| 平台代码不触碰 `acme.json` / `traefik.yml` | ✓ `rg "acme\.json\|traefik\.yml" backend/internal/`（排除测试注释与 deploy/ 样本）为空 |
| acme.json / traefik.yml 权限 0444 且 mtime 不变 | ⏳ 运行时（V-4⑥ 断言 `md5sum acme.json` 前后一致；代码层无写入口已确认） |

**结论**：`FileDeployer` 仅写 `dynamic/` 子树，`safeRelPath` 兜底越界；`Cleanup` 也仅清空 `dynamic/`（`deployer.go:95-110`）。Static 预配（`traefik.yml`）与 ACME 存储（`acme.json`）在平台代码中无任何写路径。

---

## 原则 IV — 强制管线无旁路：不得跳过 Validate 或 confirmed 直达 Deployer

| 检查动作 | 结果 |
|---|---|
| 代码中不存在跳过 `Validate` 或 `confirmed` 直达 Deployer 的调用 | ✓ `Deployer.Deploy` 唯一调用方为 `DeployService.run`（异步执行体，`deploy.go:142`），handler 仅调应用层 `DeployService.Deploy` |
| 测试 `TestNoPipelineBypass` | ✓ 等价断言见 `pipeline_e2e_test.go:104-129`（`TestPipelineE2E_HappyPath` 三段无旁路断言） |

**DeployService.Deploy 同步门禁链**（`deploy.go:69-122`，受理前全判定）：
1. 节点禁用→冻结（L75-77）
2. 版本归属校验（L82-84）
3. **闸 1**：仅 `ready` 版本可部署（L85-88）——未经管线产物的直接引用即 422 `PIPELINE_BLOCKED`
4. **闸 2**：`Confirmed=false`→422 `DEPLOY_NOT_CONFIRMED`（L89-92，AC-008）
5. **闸 3**：离线阻断 `assertOnline`（L93-96，L147）→502 `GATEWAY_UNREACHABLE`
6. 并发闸：同节点单活动部署→409 `DEPLOY_IN_PROGRESS`（L97-104）
7. 生产审批 Gate（L105-113）：production 须携带已批准 `approval_id`，gate==nil 时 production 直发→403
8. 漂移闸（L115-122）：`drift=true` 时仅允许部署最新 ready 版本

**测试三断言**（`pipeline_e2e_test.go`）：
- L104-109：`Confirmed:false`→422 `DEPLOY_NOT_CONFIRMED`
- L111-119：离线（不可达 client）→502 `GATEWAY_UNREACHABLE`
- L121-129：版本非 ready→422 `PIPELINE_BLOCKED`

**结论**：所有闸在 `deploys.Create`（L130，受理）之前同步判定，通过才 202 受理。`Deployer.Deploy` 仅在异步 `run` 内被调用，无任何旁路路径。

---

## 原则 V — 快照自包含可回滚：清空当前业务表后仍可回滚成功

| 检查动作 | 结果 |
|---|---|
| 集成测试：清空业务表后仍可回滚 | ✓ `rollback_test.go:149` `TestRollback_WorksAfterBusinessStateWiped` |

**测试证据**（`rollback_test.go:169-175`）：
```go
// 清空当前业务态（硬删，模拟误操作灾难现场）
DELETE FROM routes / targets / services / domains
// 回滚依然成功：产物完全来自 ver1 快照（自包含，宪法 V）
```
回滚以目标版本快照为源（`rollback.go` 不回读当前库），DB 业务态清空后仍生成完整产物并部署。

**结论**：快照自包含性由测试固化，灾难现场（业务表清空）可恢复。

---

## 原则 VIII — 平台侧零明文 secret：脱敏 + 日志/响应扫描零命中

| 检查动作 | 结果 |
|---|---|
| 审计 before/after 经 redactor 脱敏 | ✓ `auditrec.go:48-49` `redactx.RedactMap`；敏感值替换为 `{"value": MaskValue}`（L68） |
| `scripts/secret-scan.sh` 接入 `make test` | ✓ `Makefile:12` `test: test-unit test-contract test-integration secret-scan`；独立目标 L42-43 |
| V-4④ 脱敏抽查：API 响应/日志/Diff grep `-----BEGIN\|token=\|authorization:` 零命中 | ⏳ 运行时（`secret-scan.sh` 执行；静态：`credsvc` 仅回显 fingerprint，`certsvc` 私钥经 `cryptox` 加密存储） |
| 审计 before/after 敏感字段值为 `****` | ⏳ 运行时（V-4④；静态：`redactx.RedactMap` 已接入审计写入路径） |

**结论**：脱敏机制（`redactx`）在审计写入路径强制接入；凭证/证书私钥经 `cryptox` AES-256-GCM 加密存储，API 仅回显 fingerprint。运行时 grep 零命中留待 T093 执行 `secret-scan.sh` 实证。

---

## 原则 XI — 「发布成功≠生效」：写入成功但校验失败必须显式产出 failed

| 检查动作 | 结果 |
|---|---|
| 「写入成功但校验失败」用例显式产出 failed，禁止静默通过 | ✓ `pipeline_e2e_test.go:170` `TestPipelineE2E_AtomicityOnFailure` |

**测试证据**（`pipeline_e2e_test.go:175-217`）：
- 落盘成功但 fake Traefik 回报空集合 → 校验 failed
- L207：`assert.Equal(t, "failed", drec.Status)`
- L209：`assert.Contains(t, vd, "last_known_good_version")`
- L210：`assert.Contains(t, vd, "diff")`
- L217：版本仍 `ready` 可再次发布（failed 记录不可变保留）

**结论**：部署终态由运行时校验结果决定，非落盘成功即 success。校验失败显式标 failed 并附 diff + last_known_good_version，不静默通过。

---

## 原则 XII — 分层调用链 + API 覆盖：无仅 UI 逻辑页面

| 检查动作 | 结果 |
|---|---|
| `x-view-pages` 逐页核对 API 覆盖 | ✓ `openapi.yaml:1811-1852` 10 页全覆盖 |
| 确认无仅 UI 逻辑页面 | ✓ 每页均列对应 API 路径 |
| 一期未实现清单外能力 | ✓ openapi 53 路径 / 82 操作均有一期实现承载（tasks.md T001-T096） |

**x-view-pages 覆盖**（10 页）：
Dashboard / GatewayNodes / Domains / Services / Routes / Middlewares / Deployments / ConfigVersions / AuditLogs / Settings——每页列明消费的 API 路径。

**x-rbac 覆盖**：80 个 `x-rbac` 标记覆盖全部 54 个写端点；`/auth/login`、`/auth/refresh`、`/auth/logout` 豁免合理（登录前无角色）。

**分层调用链**（宪法 XII）：Handler（Controller）→ Application Service → Domain → Generate → Deployer，`router.go` 装配确认无跨层直调。

**结论**：API First 落实，前端每页均有 API 支撑，无纯 UI 逻辑；RBAC 矩阵在 openapi 声明且 router.go 逐项对齐（T078）。

---

## 汇总

| 原则 | 静态检查 | 运行时实证（T093） |
|---|---|---|
| I | ✓ PASS | — |
| III | ✓ PASS（写路径） | ⏳ acme.json mtime 不变 |
| IV | ✓ PASS（门禁链 + 三断言） | ⏳ `make test-pipeline` |
| V | ✓ PASS（清空回滚测试） | ⏳ V-2 回滚场景 |
| VIII | ✓ PASS（脱敏机制 + 脚本接入） | ⏳ `secret-scan.sh` 零命中 |
| XI | ✓ PASS（failed 显式断言） | ⏳ V-8④ 场景 |
| XII | ✓ PASS（x-view-pages + x-rbac） | — |

**静态检查结论**：7 项原则在代码与测试用例层全部具备对应实现与断言，无违宪项。4 个运行时子项留待 T093 端到端全量回归实证。
