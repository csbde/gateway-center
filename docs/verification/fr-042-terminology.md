# FR-042 UI 术语复查记录（T096）

**日期**：2026-09-13
**范围**：`frontend/src/features/**` 与 `frontend/src/routes/**`
**规则**：FR-042 —— 普通界面 MUST 以「域名 → 路径 → 目标服务 → 安全策略」语言表达；Traefik 专业术语 MUST 仅出现在高级模式、技术详情与 Debug 视图。

## 扫描模式

```
traefik | ingressroute | whoami | entrypoint | certresolver
dynamic config | static config | file provider
stripprefix | basicauth | forwardauth | redirectscheme
router | 路由器 | provider | resolver | ingress
```

## 业务页（零 Traefik 术语要求）

| 页面 | 结果 |
|------|------|
| `features/routes/` | ✓ 零 Traefik 术语；「自动合成规则预览」用产品语言；高级模式表达式受独立门禁（T060），属高级模式允许区 |
| `features/domains/` | ✓ 修复 1 处（见下）；HTTPS 策略用「HTTP 验证 / DNS 验证 / 自有证书 / 证书签发服务名」产品语言 |
| `features/services/` | ✓ 零 Traefik 术语 |
| `features/deployments/` | ✓ 零 Traefik 术语 |
| `features/versions/` | ✓ 零 Traefik 术语 |
| `routes/dashboard.tsx` | ✓ 零 Traefik 术语（`react-router` 为前端路由库，非 Traefik） |

### 修复项

1. **`features/domains/index.tsx`** —— acme_dns 表单 hint 原文「网关静态配置中预置的**解析器**名称」含 Traefik resolver 术语；改为「运维在网关静态配置中预置的**签发服务名**」（与字段标签「证书签发服务名」一致）。字段内部名 `cert_resolver_ref` / HTML id `dom-resolver` 非用户可见，保留。

## 运维 / 技术详情页（FR-042 允许 Traefik 术语）

| 页面 | 出现术语 | 判定 |
|------|----------|------|
| `features/nodes/index.tsx` | 「Traefik API 地址」「Traefik 实例」「/etc/traefik 部署路径」 | ✓ 节点页为运维页（line 2 注释明确允许「网关/Traefik API 地址」等运维词汇） |
| `features/nodes/StatePanel.tsx` | 「Traefik 版本」 | ✓ 状态面板属技术详情/Debug 视图（「平台意图 vs 网关加载确认」双列） |
| `features/credentials/index.tsx` | 「Traefik API 凭证」「Traefik API 认证」 | ✓ 凭证管理属平台设置/技术详情（ACME DNS 验证与 API 认证凭证） |

### 修复项（运维页，清晰度优化）

2. **`features/nodes/index.tsx`** —— 说明文案「运行时逐**路由器**校验」含 Traefik router 术语；改为「运行时逐**路由**校验生效」（产品语言，节点页仍保留其余运维词汇）。

## 误报排除

- `resolver: zodResolver(...)` —— react-hook-form 表单钩子配置，非 ACME resolver。
- `import { Link } from "@tanstack/react-router"` —— 前端路由库，非 Traefik。
- `features/common.test.ts` —— 测试文件，非 UI。

## 结论

✓ **PASS** —— 普通业务页面零 Traefik 术语；Traefik 术语仅出现在节点（运维）/状态面板（技术详情）/凭证（设置）页，符合 FR-042。typecheck 通过。
