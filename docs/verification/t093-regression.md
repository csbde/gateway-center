# T093 · quickstart V-1~V-9 端到端全量回归记录

**Feature**: 001-gateway-management-platform
**验证日期**: 2026-09-13
**依据**: quickstart.md §2（全新环境可复现性）+ §3（自动化验证入口）+ §4（V-1~V-9 场景）

---

## §3 自动化验证入口

### 后端测试

| 目标 | 命令 | 结果 | 备注 |
|---|---|---|---|
| test-unit | `make test-unit` | ✓ PASS | advrule 0.98s / mwreg 1.48s / generate 2.00s / traefikapi 2.54s；其余 internal 包无独立单测（测试集中于 contract/integration 层） |
| test-contract | `make test-contract` | ✓ PASS | 16.57s；契约测试覆盖 /nodes /domains /services /routes /versions /deployments 状态码、错误体、409 乐观锁、脱敏断言 |
| test-integration | `make test-integration` | ⏳ 进行中 | testcontainers PG16；含 T082/T085/T087/pipeline_e2e/rollback/middleware/cert/drift |
| test-pipeline | `make test-pipeline` | ⏳ 待跑 | 管线专项：无旁路三 422 / 原子性 / 回滚 / 校验失败标记 |
| secret-scan | `make secret-scan` | ⏳ 待跑 | NFR-SEC-01 零命中 |
| vet | `go vet ./...` | ✓ PASS | 见 T092 评估 |

### 前端测试

| 目标 | 命令 | 结果 |
|---|---|---|
| vitest 单测 | `npm run test` | ⏳ 待跑 |
| Playwright e2e | `npm run e2e` | ⏳ 待跑（需后端 :8089 + vite :5173 在线） |
| typecheck | `npm run typecheck` | ⏳ 待跑 |

---

## §4 V-1~V-9 端到端场景

（待自动化测试通过后，按场景逐项执行并记录）

| 场景 | 用户故事 | 验收标准 | 结果 |
|---|---|---|---|
| V-1 反向代理全流程 | US1 | AC-001~004/007~010/SC-001 | ⏳ |
| V-2 版本历史与回滚 | US2 | AC-006/011/012/SC-003 | ⏳ |
| V-3 中间件复用与顺序 | US3 | AC-005/015 | ⏳ |
| V-4 HTTPS 与证书策略 | US4 | FR-007/008/033~035 | ⏳ |
| V-5 状态可见性与 Drift | US5 | AC-013/SC-004 | ⏳ |
| V-6 RBAC 审批与审计 | US6 | AC-014/FR-036/037 | ⏳ |
| V-7 依赖保护与删除 | US7 | AC-015/FR-039 | ⏳ |
| V-8 并发失败与恢复 | Edge | NFR-REL-01 | ⏳ |
| V-9 性能与规模 | SC-006 | p95≤3s / 生成<5s | ⏳ |

---

## §2 全新环境可复现性

（待验证：docker compose up + goose up + migrate-seed + serve + frontend dev 全流程可复现）
