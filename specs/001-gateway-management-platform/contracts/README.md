# API Contracts — Gateway Center 一期

**依据**: [spec.md FR-043（API First）](../spec.md)、[data-model.md](../data-model.md)、[research.md R6/R10/R15](../research.md)

主契约文件：[`openapi.yaml`](./openapi.yaml)（OpenAPI 3.1，REST JSON，前缀 `/api/v1`）。Web UI 是本合同的唯一消费方；前端 client 由本合同代码生成（`backend/` 的 handler 契约测试与之同源校验）。

## 通用约定

### 认证与会话（R6）

- `POST /auth/login` → `{access_token(JWT HS256, 2h), refresh_token(7d, 旋转)}`；其余端点 `Authorization: Bearer`。
- 每请求服务端复核用户当前角色（降权即时生效）；`status=disabled` 用户即 401。

### 错误模型（统一）

```json
{ "error": { "code": "ROUTE_CONFLICT", "message": "…", "request_id": "…",
             "details": [ { "field": "path", "hint": "路径须以 / 开头" } ] } }
```

| HTTP | code 示例 | 场景 |
|---|---|---|
| 400 | `VALIDATION_FAILED` | 字段/类型参数校验失败（定位到字段，NFR-USE-01） |
| 401 / 403 | `UNAUTHENTICATED` / `FORBIDDEN` | 未登录 / RBAC 拒绝（FR-036） |
| 404 | `NOT_FOUND` | 资源不存在或已软删 |
| 409 | `CONCURRENT_EDIT` | 乐观锁冲突（R10），details 含当前 row_version |
| 409 | `DEPENDENCY_BLOCKED` | 删除被引用资源，details 列出**引用方清单**（FR-039/AC-015） |
| 409 | `DEPLOY_IN_PROGRESS` | 同节点已有非终态部署 |
| 422 | `PIPELINE_BLOCKED` | 验证失败/未确认预览/审批未过/节点离线（管线无旁路，宪法 IV） |
| 502 | `GATEWAY_UNREACHABLE` | Traefik API 采集失败 |

### 列表与分页

所有集合端点：`?page=1&page_size=20(≤100)&q=&sort=-created_at&node_id=`（节点作用域资源必填 `node_id` 或 header `X-Gateway-Node`）；响应 `{items, page, page_size, total}`。**大字段不回传**：snapshot/artifact_files/私钥密文仅 `GET …/{id}` 详情（脱敏后）可得（SC-006）。

### 乐观锁（R10）

写端点 PUT/PATCH 必须携带 `expected_version`（body）；不匹配 → 409 `CONCURRENT_EDIT`，MUST NOT 静默覆盖（Edge Case）。

### 启停 / 归档 / 软删语义（FR-039）

统一子资源动作而非散放动词：`POST …/{id}/enable|disable|archive`；`DELETE …/{id}` = 软删（触发依赖检查，被引用则 409）。

### Secret 与脱敏（宪法 VIII/FR-035）

- 任何响应、错误、日志、Diff 预览中，DNS 凭证、私钥、密码字段以 `fingerprint + "****"` 呈现。
- 凭证轮换 = `PUT /credentials/{id}` 新值；域名绑定（`dns_credential_id`）不变（FR-035）。

### 权限矩阵（FR-036/037，逐端点标注于 openapi `x-rbac`）

| 能力 | viewer | developer | gateway_admin | super_admin |
|---|---|---|---|---|
| 读全部资源 | ✓ | ✓ | ✓ | ✓ |
| 业务实体增删改（节点/域名/服务/路由/中间件） | ✗ | 建/改草稿、test 类节点可发布 | ✓ 全量 | ✓ |
| production 直接发布 | ✗ | ✗（仅可 Submit） | ✓（Submit→他人 Approve→Deploy） | ✓ |
| 高级模式路由 | ✗ | ✗ | ✓ | ✓ |
| 回滚 | ✗ | ✗ | ✓ | ✓ |
| 审批 | ✗ | ✗ | ✓（非提交人） | ✓ |
| 用户与角色 | ✗ | ✗ | ✗ | ✓ |
| 审计读取 | ✓（只读页） | ✓ | ✓ | ✓ |

## 管线端点 ↔ 强制管线映射（FR-023/UF-4）

```text
Validate     POST /nodes/{id}/validate          → ValidationReport（六类逐条）
Generate     POST /nodes/{id}/versions          → ConfigVersion(pending→validating→ready)
Diff Preview GET  /versions/{id}/diff?against=  → 资源级 added/modified/removed
Deploy       POST /deployments {version_id, confirmed:true, approval_id}
             ├─ 未 confirmed → 422 PIPELINE_BLOCKED（AC-008）
             ├─ production 无已批 approval → 422
             └─ 节点离线 → 422（Edge Case：恢复后须重新确认）
Verify       内置于 Deploy 尾步；GET /deployments/{id} 的 verification_result
Rollback     POST /deployments/rollback {target_version_id, confirmed:true} → 同管线新部署
Approval     POST /release-requests → /{id}/approve|reject|cancel
```

`Deployment.status` 轮询流：`pending→validating→ready→deploying→success|failed`；终态后 `verification_result` 给出期望 vs 实际集合（FR-030，"写入成功≠生效"显式化）。

## 契约测试与 UI 对齐

- `backend/tests/contract/`：对 openapi 逐端点表驱动校验（状态码、错误结构、脱敏断言）。
- Drift 呈现：`GET /nodes/{id}/state` 返回 `drift + drift_detail + desired/actual_version`，Dashboard 与节点详情同源（FR-040/US5）。
- 错误信息含资源定位与修复建议（NFR-USE-01），如 `"target_service_disabled"` + `"路由 crm-web 绑定的服务 CRM API 已禁用，请启用或改绑"`.
