// T074 · US5 节点运行态与漂移详情面板（宪章 XI：期望/实际分列 + 漂移差异可视化）。
// 双列布局：左=逐节点运行态列表（status 徽章 + 期望/实际版本 + drift 红标）；
// 右=选中节点的「平台意图 vs 网关加载确认」对比 + drift_detail 差异清单。
import { useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { nodesApi } from "@/api/resources";
import type { DriftEntry } from "@/api/types";
import { useNodes, StatusBadge } from "@/features/common";
import { Badge, Card, EmptyState, Spinner, cx } from "@/components/ui";

export function StatePanel() {
  const { data: nodesData, isPending } = useNodes();
  const nodes = nodesData?.items ?? [];
  const [selectedId, setSelectedId] = useState("");

  // 批量拉取各节点运行态（与探测周期同量级刷新，FR-002）
  const states = useQueries({
    queries: nodes.map((n) => ({
      queryKey: ["node-state", n.id] as const,
      queryFn: () => nodesApi.state(n.id),
      refetchInterval: 30_000,
    })),
  });

  // 自动选中：用户未选时优先取首个漂移节点，否则首个节点
  const effectiveId =
    selectedId ||
    states.find((s) => s.data?.drift)?.data?.node_id ||
    nodes[0]?.id ||
    "";

  if (isPending) return <Spinner label="加载节点状态…" />;
  if (nodes.length === 0)
    return (
      <EmptyState>
        尚无网关节点——先到 <Link to="/nodes" className="text-primary underline">网关节点</Link> 新建。
      </EmptyState>
    );

  const selectedIdx = nodes.findIndex((n) => n.id === effectiveId);
  const selectedState = selectedIdx >= 0 ? states[selectedIdx]?.data : undefined;

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {/* 左列：节点运行态列表 */}
      <Card>
        <div className="text-sm font-medium">节点运行态</div>
        <ul className="mt-2 space-y-1.5">
          {nodes.map((n, i) => {
            const st = states[i]?.data;
            const active = n.id === effectiveId;
            return (
              <li key={n.id}>
                <button
                  type="button"
                  onClick={() => setSelectedId(n.id)}
                  className={cx(
                    "flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm transition-colors",
                    active ? "border-primary bg-primary/5" : "border-transparent hover:bg-muted",
                  )}
                >
                  <span className="flex items-center gap-2">
                    <span className="font-medium">{n.name}</span>
                    {st ? <StatusBadge status={st.status} /> : <Badge tone="neutral">加载中</Badge>}
                  </span>
                  {st && (
                    <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
                      <span className="tabular-nums">v{st.desired_version}/{st.actual_version}</span>
                      {st.drift && <Badge tone="danger">漂移</Badge>}
                    </span>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      </Card>

      {/* 右列：平台意图 vs 网关加载 + drift_detail */}
      <Card>
        <div className="text-sm font-medium">平台意图 vs 网关加载确认</div>
        {selectedState ? (
          <div className="mt-2 space-y-3">
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-md border p-3">
                <div className="text-xs text-muted-foreground">平台期望版本</div>
                <div className="mt-1 text-lg font-semibold tabular-nums">v{selectedState.desired_version}</div>
              </div>
              <div className="rounded-md border p-3">
                <div className="text-xs text-muted-foreground">网关实际加载</div>
                <div className="mt-1 flex items-center gap-2">
                  <span className="text-lg font-semibold tabular-nums">v{selectedState.actual_version}</span>
                  {selectedState.drift ? (
                    <Badge tone="danger">不一致</Badge>
                  ) : (
                    <Badge tone="success">一致</Badge>
                  )}
                </div>
              </div>
            </div>
            {selectedState.drift ? (
              <DriftDiff detail={(selectedState.drift_detail ?? []) as DriftEntry[]} />
            ) : (
              <p className="text-sm text-muted-foreground">
                配置一致——网关加载的资源与平台期望完全匹配。
              </p>
            )}
            <div className="space-y-0.5 border-t pt-2 text-xs text-muted-foreground">
              <div>Traefik 版本：{selectedState.traefik_version || "未知"}</div>
              <div>
                最近在线：{selectedState.last_online_at ? new Date(selectedState.last_online_at).toLocaleString() : "从未在线"}
              </div>
              {selectedState.consecutive_failures > 0 && (
                <div>连续失败：{selectedState.consecutive_failures} 次</div>
              )}
            </div>
          </div>
        ) : (
          <Spinner label="加载状态详情…" />
        )}
      </Card>
    </div>
  );
}

/** drift_detail 差异清单：missing（平台有、网关无）/ unexpected（网关有、平台无）。 */
function DriftDiff({ detail }: { detail: DriftEntry[] }) {
  const missing = detail.filter((d) => d.type === "missing");
  const unexpected = detail.filter((d) => d.type === "unexpected");
  return (
    <div className="space-y-2">
      {missing.length > 0 && (
        <div>
          <div className="text-xs font-medium text-warning">缺失（平台期望但网关未加载）</div>
          <ul className="mt-1 space-y-1">
            {missing.map((d, i) => (
              <li key={i} className="flex items-center gap-2 text-sm">
                <Badge tone="warning">{d.resource}</Badge>
                <span className="font-mono text-xs">{d.name}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {unexpected.length > 0 && (
        <div>
          <div className="text-xs font-medium text-destructive">多余（网关加载但平台未纳管）</div>
          <ul className="mt-1 space-y-1">
            {unexpected.map((d, i) => (
              <li key={i} className="flex items-center gap-2 text-sm">
                <Badge tone="danger">{d.resource}</Badge>
                <span className="font-mono text-xs">{d.name}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
