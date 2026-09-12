# Implementation Plan: Gateway Center 统一网关管理中心（一期）

**Branch**: `001-gateway-management-platform` | **Date**: 2026-09-12 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-gateway-management-platform/spec.md`

## Summary

Gateway Center 是企业级 Traefik 可视化网关**控制平面**：以业务实体（Gateway Node / Domain / Service / Target / Route / Middleware / Certificate Policy）管理期望状态，经强制管线 `Draft → Validation → Generation → Diff Preview → Deploy → Runtime Verification` 单向生成并原子下发 Traefik Dynamic Configuration，提供版本化与回滚、Drift 检测、RBAC 四角色 + 生产审批、全量审计。

技术路线（详见 [research.md](./research.md)）：Go（chi + GORM）后端 + React 18/TypeScript/TailwindCSS 前端 + PostgreSQL 16 存储；一期下发通道为共享卷 `FileDeployer`（临时文件→校验→原子 rename，Deployer 接口为二期 Agent 预留）；实际态经 Traefik 只读 API 采集并做集合级 diff 验证。

## Technical Context

**Language/Version**: Go 1.24（backend）；TypeScript 5.x / React 18（frontend）

**Primary Dependencies**: chi、GORM、robfig/cron、golang-jwt、`crypto/aes`(GCM)、slog；前端 Vite、TailwindCSS 4、shadcn/ui、TanStack Router/Query、react-hook-form、zod

**Storage**: PostgreSQL 16+（JSONB 存类型化 Middleware 参数与快照；审计表按月分区；敏感材料 AES-256-GCM 加密列）

**Testing**: Go testing + testify（unit）、testcontainers-go（集成）、表驱动契约测试、Playwright + Vitest（前端 E2E/组件）

**Target Platform**: Linux 服务器单二进制 + SPA（同源部署），与目标 Traefik 共享 dynamic 配置卷；Docker Compose 为参考部署

**Project Type**: Web application（backend API service + frontend SPA，monorepo）

**Performance Goals**: SC-006 规模（≥10 节点 / 200 域名 / 1000 路由 / 100 中间件）下列表与详情 p95 ≤ 3s；1000 路由生成 <5s；探测周期 30s、并发上限 32 节点

**Constraints**: 强制管线无旁路（宪法 IV）；发布原子化；期望/实际态分离（宪法 XI）；Secret 全链路脱敏（宪法 VIII）；一期单实例部署、单/多节点管理模型；平台不触碰 Static 配置与 acme.json（宪法 III/VII）

**Scale/Scope**: 一期 10 个页面模块、11 类核心实体、约 60 个 REST 端点、5 类 Middleware（research R16）

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原则 | 评估 | 结论 |
|---|---|---|
| I. 模型与 YAML 分离 | DB 持久化业务实体；YAML 仅为生成产物（R8 单向纯函数生成，不回解析） | PASS |
| II. 声明式 + 双模式 | 简单模式合成规则、高级模式白名单语法验证，产物互不影响（R8）；高级模式仅 Gateway Admin+ | PASS |
| III. Static/Dynamic 分离 | 仅写 `dynamic/` 目录树（按节点隔离），Static/acme.json 只读（R4/R14） | PASS |
| IV. 强制管线 | 单一编排代码路径 + 测试固化"无旁路"（R15）；原子 rename（R4）；无 SSH、无 Agent（R4） | PASS |
| V. 版本化与回滚 | 自包含不可变快照（R8）；回滚复用同一管线（R15）；四类记录 revoke 删除（R12） | PASS |
| VI. 统一抽象与依赖保护 | Service 统一抽象、Route 只绑 Service；Disable→Archive→软删除 + 依赖检查（data-model 状态机与删除规则） | PASS |
| VII. 证书约束 | 泛域名强制 DNS Challenge；到期经 TLS 握手观测、不读 acme.json（R14） | PASS |
| VIII. Secret 保护 | AES-256-GCM + 白名单序列化 + 集中 redactor 覆盖日志/审计/Diff（R7） | PASS |
| IX. RBAC 与审批 | 四角色 + production ApproveGate（提交人≠批准人）；角色每请求复核（R6/R15） | PASS |
| X. 全量审计 | 事务内 append-only + 分区 + revoke 改删；高级模式使用强制关联路由（R12） | PASS |
| XI. 状态分离与 Drift | 期望/实际双源存储；验证=API 集合 diff；漂移持续标红（R5） | PASS |
| XII. API First / 分层 / 范围 | UI 纯 API 消费方；目录映射五层调用链；Deployer 接口预留而非提前实现 Agent；无一期外能力（R16） | PASS |

**Gate 结果：全部 PASS，无违规，无需 Complexity Tracking。**

## Project Structure

### Documentation (this feature)

```text
specs/001-gateway-management-platform/
├── plan.md              # 本文件
├── research.md          # Phase 0 输出（R1–R18，未知项全部消解）
├── data-model.md        # Phase 1 输出：实体、字段、状态机、校验与删除规则
├── quickstart.md        # Phase 1 输出：端到端验证指南
├── contracts/           # Phase 1 输出
│   ├── README.md        # 契约总览、通用约定（错误、分页、乐观锁、权限矩阵）
│   └── openapi.yaml     # REST API 契约（OpenAPI 3.1）
└── tasks.md             # Phase 2 输出（/speckit-tasks，本命令不创建）
```

### Source Code (repository root)

```text
backend/
├── cmd/gateway-center/          # main：装配、embed SPA、优雅退出
├── internal/
│   ├── api/                     # HTTP 层：router、handlers、middleware(auth/rbac/audit/redact)、DTO
│   ├── application/             # 用例编排：pipeline.go（强制管线）、deploy/、approval/、probe scheduler
│   ├── domain/                  # 实体、状态机、校验器、依赖检查、diff（纯业务，无 IO）
│   ├── generate/                # 业务快照 → Traefik v3 dynamic YAML（纯函数，宪法 I/MNT-01 收敛层）
│   └── infrastructure/          # pgstore(GORM)、cryptox(AES-GCM+redactor)、traefikapi(只读采集)、
│                                #   deployer/（FileDeployer + Deployer 接口）、certwatch(TLS 握手探测)
├── migrations/                  # goose SQL（含审计分区、revoke 语句）
└── tests/
    ├── contract/                # handler 表驱动契约测试
    ├── integration/             # testcontainers：管线端到端、无旁路、回滚
    └── unit/

frontend/
├── src/
│   ├── api/                     # openapi 生成的类型化 client（API First 对齐）
│   ├── routes/                  # 10 个页面：dashboard/nodes/domains/services/routes/
│   │                            #   middlewares/deployments/versions/audits/settings
│   ├── features/                # 按域的表单、diff 视图、middleware 参数 schema(zod)
│   └── components/              # shadcn/ui 基础件
└── tests/ (vitest + playwright)

deploy/
├── compose/                     # gateway-center + postgres + traefik 参考拓扑
└── traefik/                     # 示例 static 配置（运维预配样本，平台只读）
```

**Structure Decision**: Web application monorepo。`backend/internal` 包边界即宪法 XII 调用链 `api → application → domain → generate → infrastructure`；`internal/generate` 独立成包将 Traefik 版本升级影响收敛在生成层（NFR-MNT-01）；`infrastructure/deployer` 接口化是二期 Agent 的唯一扩展点。

## Complexity Tracking

> 无宪法违规，本节为空。
