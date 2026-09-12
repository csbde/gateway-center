// T043–T047 · 功能页共享件：节点作用域选择、错误→字段定位映射、乐观锁冲突引导。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/api/client";
import { nodesApi } from "@/api/resources";
import type { Node } from "@/api/types";
import { Badge, Select, type BadgeTone } from "@/components/ui";

export const ENV_LABEL: Record<Node["env_type"], string> = {
  development: "开发",
  test: "测试",
  staging: "预发",
  production: "生产",
};

export function NodeBadge({ node }: { node: Node }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      {node.name}
      <Badge tone={node.env_type === "production" ? "danger" : "info"}>{ENV_LABEL[node.env_type]}</Badge>
    </span>
  );
}

export function useNodes() {
  return useQuery({ queryKey: ["nodes"], queryFn: () => nodesApi.list({ page_size: 100 }) });
}

const SCOPE_KEY = "gc.node_scope";

/** 全局"当前网关"作用域：localStorage 记忆，各资源页共享同一选中节点。 */
export function useNodeScope(): [string, (id: string) => void] {
  const [id, setId] = useState(() => localStorage.getItem(SCOPE_KEY) ?? "");
  const set = (v: string) => {
    setId(v);
    try {
      if (v) localStorage.setItem(SCOPE_KEY, v);
      else localStorage.removeItem(SCOPE_KEY);
    } catch {
      /* 隐私模式写入失败仅丢记忆 */
    }
  };
  return [id, set];
}

/** 节点作用域下拉（US1 各页顶栏复用；列表/表单都依赖它）。 */
export function NodeScope({ value, onChange }: { value: string; onChange: (id: string) => void }) {
  const { data } = useNodes();
  const items = data?.items ?? [];
  return (
    <div className="flex items-center gap-2">
      <span className="text-sm text-muted-foreground">当前网关</span>
      <Select className="w-64" value={value} onChange={(e) => onChange(e.target.value)} aria-label="当前网关">
        <option value="">请选择网关…</option>
        {items.map((n) => (
          <option key={n.id} value={n.id}>
            {n.name}（{ENV_LABEL[n.env_type]}）{n.enabled ? "" : " · 已停用"}
          </option>
        ))}
      </Select>
    </div>
  );
}

/** 服务端错误 → 表单字段错误（contracts/README.md details[].field）。 */
export function fieldErrors(e: unknown): Record<string, string> {
  if (!(e instanceof ApiError)) return {};
  const out: Record<string, string> = {};
  for (const d of e.details ?? []) {
    if (d.field) out[d.field] = [d.message, d.hint].filter(Boolean).join("；") || e.message;
  }
  return out;
}

/** 人读错误文案（409 乐观锁/422 管线阻断给出下一步引导，NFR-USE-01）。 */
export function humanError(e: unknown): { text: string; tone: BadgeTone } {
  if (e instanceof ApiError) {
    switch (e.code) {
      case "CONCURRENT_EDIT":
        return { text: "该记录已被他人修改，请刷新后重试", tone: "warning" };
      case "DEPENDENCY_BLOCKED":
        return { text: e.message, tone: "danger" };
      case "PIPELINE_BLOCKED":
      case "DEPLOY_NOT_CONFIRMED":
        return { text: e.message, tone: "warning" };
      case "GATEWAY_UNREACHABLE":
        return { text: "目标网关当前不可达，发布已阻断（保护线上）", tone: "danger" };
      case "FORBIDDEN":
        return { text: "当前角色无权执行此操作", tone: "warning" };
      default:
        return { text: e.message, tone: "danger" };
    }
  }
  return { text: e instanceof Error ? e.message : "未知错误", tone: "danger" };
}

export const statusTone: Record<string, BadgeTone> = {
  enabled: "success",
  active: "success",
  online: "success",
  up: "success",
  success: "success",
  ready: "success",
  draft: "neutral",
  unknown: "neutral",
  pending: "neutral",
  validating: "info",
  deploying: "info",
  disabled: "warning",
  degraded: "warning",
  expiring_soon: "warning",
  offline: "danger",
  down: "danger",
  failed: "danger",
  archived: "neutral",
};

export function StatusBadge({ status }: { status: string }) {
  const zh: Record<string, string> = {
    enabled: "启用中",
    disabled: "已停用",
    archived: "已归档",
    draft: "草稿",
    online: "在线",
    offline: "离线",
    degraded: "部分异常",
    unknown: "未探测",
    success: "成功",
    failed: "失败",
    pending: "排队中",
    validating: "校验中",
    deploying: "下发中",
    ready: "待发布",
    up: "健康",
    down: "不可用",
  };
  return <Badge tone={statusTone[status] ?? "neutral"}>{zh[status] ?? status}</Badge>;
}
