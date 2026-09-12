# Gateway Center

企业级 Traefik 可视化网关控制平面：以「向导式配置 → 验证 → Diff 预览 → 发布 → 校验生效」的强制管线替代手写 YAML，让网关变更可追溯、可回滚、可审计。

- **后端**：Go 1.25 · chi · GORM · PostgreSQL 16 · robfig/cron
- **前端**：React 18 · TypeScript · TailwindCSS · TanStack Router/Query · Playwright
- **数据面**：Traefik v3（平台只读其 API，只写 `dynamic/` 子树）

> 设计与规格见 [`specs/001-gateway-management-platform/`](./specs/001-gateway-management-platform/)；宪法（12 条不可协商约束）见 [`.specify/memory/constitution.md`](./.specify/memory/constitution.md)。

---

## 架构

```
┌─────────────┐   ┌──────────────────────────────┐   ┌──────────────┐
│  浏览器 UI  │──▶│  Gateway Center 后端 (:8080) │──▶│  PostgreSQL  │
│  Vite :5173 │   │  Controller→App→Domain→      │   │  (业务态/审计) │
└─────────────┘   │  Generate→Deployer           │   └──────────────┘
                  └──────────┬───────────────────┘
                             │ 只读 API + 只写 dynamic/
                             ▼
                  ┌──────────────────┐
                  │  Traefik v3      │──▶ 上游服务（sample-crm …）
                  │  :80/:443/:8081  │
                  └──────────────────┘
```

分层纪律（宪法 XII）：`Controller → Application → Domain → Generate → Deployer`，单向依赖；Domain 层不感知 HTTP/数据库；Generate 只消费自包含 Snapshot。

---

## 前置条件

| 依赖 | 版本/说明 |
|---|---|
| Go | 1.25+ |
| Node.js | 22 LTS + npm |
| Docker Engine + Compose v2 | 起 PostgreSQL、Traefik 参考实例 |
| PostgreSQL | 16+（Compose 提供） |
| Traefik | v3.x（Compose 提供，Static 配置为运维预配样本） |
| 主机 | Linux/macOS；需一个本地目录可被 Gateway Center 写、被 Traefik 读（**共享卷**，见下） |

---

## ⚠️ 共享卷与 POSIX rename 前置（宪法 IV）

平台的发布以「临时文件 → fsync → 原子 rename」替换 Traefik 的 dynamic 配置。**原子 rename 仅在 POSIX 同一文件系统内成立**——跨主机 NFS / virtiofs / 远程卷不保证原子性。

因此：

- `deploy_root` 指向的目录 **必须与 Traefik 容器同主机 bind mount**（见 `deploy/compose/docker-compose.yml` 的 `./dynamic:/etc/traefik/dynamic`）。
- 平台 **只写** `<deploy_root>/dynamic/` 子树，**只读** Traefik API；绝不修改 Traefik Static 配置、绝不直接 SSH 节点、绝不覆盖正在使用的配置文件（宪法 III/IV）。
- 多节点生产架构须经 Gateway Agent 下发；一期单节点直连部署路径**必须复用同一管线**，不得省略任一环节。

---

## Static 预配要求（宪法 III）

Traefik Static 配置由运维预配，平台不触碰。参考样本见 [`deploy/traefik/traefik.yml`](./deploy/traefik/traefik.yml)：

```yaml
entryPoints:
  web: { address: ":80" }
  websecure: { address: ":443" }
providers:
  file:
    directory: /etc/traefik/dynamic   # 平台独占写入
    watch: true
certificatesResolvers:
  le:
    acme: { email: ops@example.com, storage: /etc/traefik/acme.json, caServer: ... }
```

- `providers.file.directory` 必须指向平台 `deploy_root/dynamic`（共享卷）。
- `certificatesResolvers` 中的 resolver 名称由运维定义；平台在域名 HTTPS 策略中引用该名称（acme_dns / acme_http）。
- 平台 **不读 `acme.json`**（宪法 VII）；证书到期监控经 TLS 握手探测叶子证书 NotAfter。

---

## 启动

```bash
# 1) 数据面（PostgreSQL + Traefik + 示例上游）
docker compose -f deploy/compose/docker-compose.yml up -d postgres traefik sample-crm

# 2) 迁移与初始 Super Admin（密钥仅经环境变量注入，宪法 VIII）
cd backend
export GC_DATABASE_URL=postgres://gc:gc@localhost:5432/gc?sslmode=disable
export GC_MASTER_KEY=$(openssl rand -hex 32)         # 32 字节主密钥（64 hex）
export GC_INITIAL_ADMIN_PASSWORD='ChangeMe-Strong-1' # ≥12 字符
go run ./cmd/gateway-center migrate-seed              # 幂等迁移 + 种子超管

# 3) 后端 + 前端
go run ./cmd/gateway-center serve --addr :8080
cd ../frontend && npm ci && npm run dev               # http://localhost:5173

# 4) 注册第一个节点（Web UI: Gateway Nodes → 新建）
#    base_url=http://localhost:8081  deploy_root=<共享卷绝对路径>  env_type=test
```

预期：`GET /api/v1/dashboard` 返回 `nodes_online ≥ 1`，节点详情显示 Traefik 版本号，探测状态 `online`（30s 内首轮）。

完整验证场景见 [`specs/.../quickstart.md`](./specs/001-gateway-management-platform/quickstart.md)（V-1~V-9）。

---

## 环境变量

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `GC_DATABASE_URL` | ✅ | — | PostgreSQL DSN |
| `GC_MASTER_KEY` | ✅ | — | 32 字节主密钥（64 hex），AES-256-GCM；不落库不落仓库（宪法 VIII） |
| `GC_JWT_SECRET` | — | 由 MasterKey 派生 | JWT 签名密钥 |
| `GC_ADDR` | — | `:8080` | 后端监听地址 |
| `GC_INITIAL_ADMIN_USERNAME` | — | `admin` | 初始超管用户名 |
| `GC_INITIAL_ADMIN_PASSWORD` | — | — | 初始超管密码（≥12 字符） |
| `GC_ACCESS_TOKEN_TTL` | — | `2h` | Access JWT 有效期 |
| `GC_REFRESH_TOKEN_TTL` | — | `168h` (7d) | Refresh token 有效期 |
| `GC_PROBE_INTERVAL` | — | `30s` | 节点探测周期 |
| `GC_SPA_DIR` | — | 空 | 前端构建产物目录（空=不服务前端，由 vite 独立提供） |

> 主密钥轮换流程见 [`docs/runbook.md`](./docs/runbook.md)。

---

## 测试与验证

```bash
cd backend && make test            # unit + contract + integration + 明文密钥扫描
cd backend && make test-pipeline   # 管线端到端：无旁路、原子性、回滚、校验失败标记
cd backend && make test-all        # 串行全量：unit+contract+integration+e2e+管线覆盖率门槛
cd backend && make cover-pipeline  # 管线相关包覆盖率 ≥80%（R17/宪法 IV 合规审查）
cd backend && make vet && make lint
cd frontend && npm run test        # Vitest 单测
cd frontend && npm run e2e          # Playwright（需后端 + vite 在线）
```

- 管线相关包覆盖率门槛 ≥80%（`scripts/cover-pipeline.sh`，`GC_COVER_THRESHOLD` 可覆盖）。
- 明文密钥扫描：运行时产物零命中 `-----BEGIN|token=|authorization:`（`scripts/secret-scan.sh`，NFR-SEC-01）。

---

## 宪法合规要点

| # | 约束 | 落地 |
|---|---|---|
| III | Static/Dynamic 职责分离 | 平台只写 `dynamic/`，只读 Traefik API |
| IV | 发布经强制管线 | Draft→Validate→Generate→Diff→Deploy→Verify；无「保存即生效」；原子 rename |
| V | 版本化与可回滚 | 不可变自包含快照；回滚走同一管线；版本/部署/审计不可删 |
| VII | 证书自动化 | 不读 `acme.json`；泛域名强制 DNS Challenge |
| VIII | Secret 保护 | AES-256-GCM；私钥/凭证仅指纹回显；日志脱敏 |
| X | 全量审计 | 仅追加；按月分区；UPDATE/DELETE 被 DB revoke |
| XI | 期望/实际分离 | 漂移检测；发布成功≠生效 |

---

## 文档索引

- [Quickstart & 验证指南](./specs/001-gateway-management-platform/quickstart.md)
- [数据模型](./specs/001-gateway-management-platform/data-model.md)
- [API 契约](./specs/001-gateway-management-platform/contracts/openapi.yaml)
- [技术决策（research）](./specs/001-gateway-management-platform/research.md)
- [宪法](./.specify/memory/constitution.md)
- [运维手册](./docs/runbook.md)
- [FR-042 UI 术语复查记录](./docs/verification/fr-042-terminology.md)
- [任务清单](./specs/001-gateway-management-platform/tasks.md)
