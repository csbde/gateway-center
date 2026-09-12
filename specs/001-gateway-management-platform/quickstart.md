# Quickstart & Validation Guide: Gateway Center 一期

**Feature**: 001-gateway-management-platform | **Date**: 2026-09-12

本文是**验证指南**：用可运行的场景证明平台按期望工作，不含实现代码。
实体与规则见 [data-model.md](./data-model.md)，接口见 [contracts/openapi.yaml](./contracts/openapi.yaml)，
技术决策见 [research.md](./research.md)。

---

## 1. 前置条件

| 依赖 | 版本/说明 |
|---|---|
| Go | 1.24+ |
| Node.js | 22 LTS + npm |
| Docker Engine + Compose v2 | 起 PostgreSQL、Traefik 参考实例 |
| PostgreSQL | 16+（Compose 提供） |
| Traefik | v3.x（Compose 提供，Static 配置为运维预配样本，见 `deploy/traefik/`） |
| 主机 | Linux/macOS；需一个本地目录可被 Gateway Center 写、被 Traefik 读（共享卷） |

Traefik 侧前提（宪法 III：平台只读，不修改）：

```yaml
# deploy/traefik/traefik.yml —— 运维预配示例（非平台产物）
entryPoints:
  web: { address: ":80" }
  websecure: { address: ":443" }
api:
  dashboard: true
providers:
  file:
    directory: /etc/traefik/dynamic   # 平台独占写入
    watch: true
certificatesResolvers:
  le:
    acme: { email: ops@example.com, storage: /etc/traefik/acme.json, caServer: … }
```

## 2. 启动

```bash
# 1) 依赖与数据面
docker compose -f deploy/compose/docker-compose.yml up -d postgres traefik sample-crm

# 2) 迁移与初始 Super Admin（密钥仅经环境变量注入，R7）
cd backend
export GC_DATABASE_URL=postgres://gc:gc@localhost:5432/gc?sslmode=disable
export GC_MASTER_KEY=$(openssl rand -hex 32)         # 32 字节主密钥
export GC_INITIAL_ADMIN_PASSWORD='ChangeMe-Strong-1'
goose -dir migrations postgres "$GC_DATABASE_URL" up
go run ./cmd/gateway-center migrate-seed

# 3) 后端 + 前端
go run ./cmd/gateway-center serve --addr :8080
cd ../frontend && npm ci && npm run dev              # http://localhost:5173

# 4) 注册第一个节点（Web UI: Gateway Nodes → 新建）
#    base_url=http://localhost:8081  deploy_root=<共享卷路径>  env_type=test
```

预期：`GET /api/v1/dashboard` 返回 `nodes_online ≥ 1`，节点详情显示 Traefik 版本号，
探测状态 `online`（30s 内完成首轮）。

## 3. 自动化验证入口

```bash
cd backend && make test            # unit + contract + integration（testcontainers 起 PG）
cd backend && make test-pipeline   # 仅管线端到端：无旁路、原子性、回滚、校验失败标记
cd frontend && npm run test        # Vitest 单测
cd frontend && npm run e2e         # Playwright：UF-1 → UF-4 主链路
go vet ./... && golangci-lint run  # 静态检查（CI 门槛：管线包覆盖率 ≥80%）
```

预期：全部通过；`test-pipeline` 额外断言「不存在绕过 Validation 或 Diff 的调用路径」（宪法 IV）。

## 4. 端到端场景（对应验收标准）

### V-1 · 一条反向代理全流程（US1，P1）— 验证 AC-001~004、007~010、SC-001

1. 创建 Service `crm-api`，添加 3 个 Target（含权重）并启用 → 详情页健康状态按启用 Target 聚合（AC-003）。
2. 创建 Domain `crm.example.com`，`https_policy=imported`（先导入自有证书）（AC-002）。
3. Routes → 新建，简单模式：域名 + 路径 `/` + Prefix + `crm-api` + HTTPS 开启
   → 界面展示**自动生成的规则预览**（`Host(...) && PathPrefix(...)`），全程无语法输入（AC-004/FR-014）。
4. 反向验证：临时禁用 `crm-api` → 触发 `POST /nodes/{id}/validate`
   → 报告逐条列出 blocker（服务禁用 + 无启用 Target）且发布按钮不可用（AC-007/FR-012）。
5. 重新启用 → `POST /nodes/{id}/versions` 生成版本（pending→validating→ready）。
6. `GET /versions/{id}/diff` 预览新增资源 → **未勾选确认时 Deploy 请求返回 422**（AC-008）。
7. 确认并发布 → 轮询 `GET /deployments/{id}` 至 `success`，`verification_result.passed=true`（AC-010）。
8. 落盘检查（原子性，AC-009/宪法 IV）：

```bash
ls -l <共享卷>/dynamic/routers/                 # 每路由一文件，权限 0644
# 部署期间持续采样，确认不存在读到半成品配置的瞬间：
while :; do cat <共享卷>/dynamic/routers/*.yml | md5sum; sleep 0.02; done > samples.log &
# 结束比对：samples.log 中出现的每个哈希状态都必须是一个完整版本
```

9. 真实流量：`curl -sk --resolve crm.example.com:443:127.0.0.1 https://crm.example.com/`
   → 命中 3 个实例之一；全程未登录网关节点、未编辑 YAML（SC-001「四不一自动」）。

### V-2 · 版本历史与一键回滚（US2）— AC-006、011、012、SC-003

```text
① 追加第二条路由并发布 → version N+1（含版本号/节点/创建人/时间/变更/快照/状态，AC-006）
② Config Versions → 选 N 与 N+1 对比 → 差异以 router 为单位显示 added（AC-011）
③ Deployments → 对版本 N 发起 rollback → 走同一管线（日志可见 validating→deploying→success）
④ 断言：网关只剩版本 N 的路由；新增了一条 trigger=rollback 的部署与 origin=rollback 的版本（AC-012）
⑤ 计时 ≤ 3 分钟（SC-003）
⑥ 对回滚后流量验证：curl 第二条路由 → 404
```

### V-3 · 中间件复用与执行顺序（US3）— AC-005、015

```text
① 创建 3 个 Middleware：ip_allowlist（CIDR 列表）、rate_limit（100 r/s）、security_headers
② 参数反向用例：CIDR 写错 / average=0 → 400 且错误定位到具体字段
③ 两条路由各绑定三者，顺序 IP AllowList → RateLimit → SecurityHeaders
④ 生成的 routers/*.yml 中 middlewares 数组顺序 == 界面顺序（AC-005/FR-018）
⑤ 非白名单来源 curl → 404/403；白名单来源 → 200
⑥ DELETE /middlewares/{id} → 409 DEPENDENCY_BLOCKED，details 逐条列出 2 条引用路由（AC-015）
⑦ 解绑两条路由后可正常软删
⑧ 高级模式（Gateway Admin）：写非法表达式 → 语法验证失败；合法 → 风险提示 + 预览 +
   审计出现 action=advanced_edit 且 resource 关联该 route id
```

### V-4 · HTTPS 与证书策略（US4）— FR-007、008、033~035

```text
① 创建泛域名 *.dev.example.com，https_policy=acme_http → 400（泛域名强制 DNS Challenge，FR-034）
② 改 acme_dns + 指向 SecretCredential → 域名详情显示策略与 resolver 引用
③ 导入一张即将过期（<30 天）的自有证书到另一域名 → Dashboard expiring_certificates 与域名详情红色预警（FR-008）
④ 脱敏抽查（宪法 VIII / US4-AC3）：
   - grep -R "-----BEGIN" 全部 API 响应 / 平台日志 / Diff 输出 → 零命中
   - GET /credentials/{id} → 仅 name/provider/fingerprint
   - 审计 before/after 中该字段值为 "****"
⑤ 轮换凭证（PUT /credentials/{id}）→ fingerprint 变化、域名 dns_credential_id 不变（FR-035）
⑥ 断言平台从未写 acme.json：md5sum acme.json 在全流程前后一致
```

### V-5 · 状态可见性与 Drift（US5）— AC-013、SC-004

```text
① Dashboard：节点 online、Traefik 版本、加载 router/service/middleware 计数与平台期望一致
② 人为制造漂移（在共享卷手工塞入一个 extra-router.yml，或用脚本让节点只上报旧版本集合）
③ ≤2 分钟内：节点 status 出现 drift=true、drift_detail 列出该差异项；Dashboard nodes_drift=1（SC-004）
④ 漂移期间发起发布 → 要求先 validate 通过（422 PIPELINE_BLOCKED 或提示重新验证，Edge Case）
⑤ 一次成功发布+校验后 drift 清除
⑥ 关闭 Traefik → 连续 3 次探测失败（约 90s）→ status=offline，last_online_at 保留（FR-003）
⑦ 路由详情页：平台意图 与 网关加载确认 两列分列展示（US5-AC3）
```

### V-6 · RBAC、生产审批与审计（US6）— AC-014、FR-036/037

```bash
# 以四个角色分别登录，脚本化验证权限矩阵（contracts/README.md 表格）
go test ./tests/integration -run TestRBACMatrix -v
```

```text
① Viewer：所有写入口 UI 置灰；直接 POST /routes → 403（US6-AC1）
② Developer：production 节点 POST /deployments → 403/422；POST /release-requests → 201；
   test 节点可保存并发布，但确认前仍 422（不可跳过验证，US6-AC2）
③ Gateway Admin B 批准 A 提交的申请 → 200；A 自批 → 422（reviewer≠submitter，US6-AC3）
④ 审计链还原：按 request_id/时间范围检索，能完整串起
   create → update → submit → approve → deploy → rollback，
   每条含 actor/action/resource/before/after/occurred_at/ip（AC-014）
⑤ 篡改防护：以业务账号执行 UPDATE/DELETE audit_logs → 权限拒绝（DB revoke）
```

### V-7 · 依赖保护与安全删除（US7）— AC-015、FR-039

```text
① 构造引用链 Domain ← Route → Service → Target
② DELETE /services/crm-api → 409，details 列出 crm.example.com、api.example.com 两条引用路由（US7-AC1）
③ 处置路径：Disable →（归档相关 Route）→ Archive → 一周后软删成功
④ 软删后 POST /nodes/{id}/versions：产物不含该资源，但历史版本快照仍可读到它（US7-AC2）
⑤ ConfigVersion / Deployment / AuditLog 删除请求 → 一律 403（US7-AC3）
```

### V-8 · 并发、失败与恢复（Edge Cases / NFR-REL-01）

```text
① 两名管理员并发改同一路由：后保存者 expected_version 不匹配 → 409 CONCURRENT_EDIT，无静默覆盖
② 节点离线时发布：validate 与生成版本可完成；deploy 被 422 阻断并提示；
   恢复在线后仍须人工重新确认（无自动补发）
③ 部署中失败（写盘前 chmod 阻断 / 制造非法产物）：网关保持上一版完整配置，部署标 failed，error_message 可查
④ 运行时校验失败（产物语法被 Traefik 拒绝）：文件写入成功但 verification_result.passed=false
   → 部署标 failed + 期望/实际 diff + last_known_good_version 提示（宪法 XI）
⑤ 平台重启：中断中的部署记录状态转为 failed 并要求人工确认；已确认的期望状态与部署记录不丢失
   （重启前后对比 SELECT count(*) FROM config_versions/deployments/audit_logs）
⑥ 同节点并发两个发布 → 后者 409 DEPLOY_IN_PROGRESS
```

### V-9 · 性能与规模（SC-006）

```bash
cd backend && go run ./cmd/tools/seed --nodes 10 --domains 200 --routes 1000 --middlewares 100
# k6 或 vegeta：列表/详情 5 分钟稳态
k6 run tests/perf/list-latency.js
```

预期：列表与详情 p95 ≤ 3s；`POST /nodes/{id}/versions` 生成 1000 路由产物 < 5s；
生成产物文件数与资源数线性、无重复解析开销。

## 5. 宪法符合性人工检查（评审用）

| 原则 | 检查动作 |
|---|---|
| I | 库内不存在被当作事实来源的 YAML 字段；生成方向单向（`grep -r "ParseTraefikYAML" backend/` 应为空） |
| III | 平台写入路径前缀恒为 `<deploy_root>/dynamic/`；acme.json / traefik.yml 权限 0444 且 mtime 不变 |
| IV | 代码中不存在跳过 `pipeline.Validate` 或 `confirmed` 直达 Deployer 的调用（测试 `TestNoPipelineBypass`） |
| V | 快照自包含：清空当前业务表后仍可回滚成功（集成测试用例） |
| VIII | V-4④ 脱敏抽查 + 日志扫描零命中 |
| XI | 「写入成功但校验失败」用例必须显式产出 failed（V-8④），禁止静默通过 |
| XII | `x-view-pages` 逐页核对 API 覆盖，确认无仅 UI 逻辑；一期未实现清单外能力 |

## 6. 清理

```bash
docker compose -f deploy/compose/docker-compose.yml down -v
rm -rf <共享卷>/dynamic/*
# 说明：审计与版本记录设计上不可删；测试环境清理需直接操作 PG（运维专用，无 API）
```
