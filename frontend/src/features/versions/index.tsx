// T054 · 配置版本页（US2/AC-006/011/012）：历史列表、双版本 Diff 三色视图、
// 回滚确认对话框（仅 gateway_admin）+ 部署状态轮询。版本不可删（FR-032）。
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { nodesApi, pipelineApi } from "@/api/resources";
import type { ConfigVersion, DiffResult, Node } from "@/api/types";
import { can, tokenStore } from "@/api/session";
import { Alert, Badge, Button, Card, DataTable, Modal, Spinner, Td, Tr } from "@/components/ui";
import { NodeScope, StatusBadge, humanError, useNodes, useNodeScope } from "../common";

const CHANGE_LABEL = { added: "新增", modified: "修改", removed: "移除" } as const;
const CHANGE_TONE = { added: "success", modified: "info", removed: "danger" } as const;

const ORIGIN_LABEL: Record<string, string> = { forward: "业务发布", rollback: "回滚" };

function DiffView({ diff }: { diff: DiffResult }) {
  return (
    <div className="space-y-2">
      <div className="flex gap-2 text-xs">
        {Object.entries(diff.summary).map(([k, n]) => (
          <Badge key={k} tone={k === "added" ? "success" : k === "removed" ? "danger" : "info"}>
            {CHANGE_LABEL[k as keyof typeof CHANGE_LABEL] ?? k} {n}
          </Badge>
        ))}
      </div>
      <DataTable head={["资源", "名称", "变化"]}>
        {diff.items.map((it, i) => (
          <Tr key={i}>
            <Td className="font-mono text-xs">{it.kind}</Td>
            <Td>{it.name}</Td>
            <Td>
              <Badge tone={CHANGE_TONE[it.change]}>
                {CHANGE_LABEL[it.change]}
                {it.change === "modified" && it.before && it.after ? (
                  <details className="mt-0.5">
                    <summary className="cursor-pointer text-xs text-muted-foreground">详情</summary>
                    <pre className="mt-1 max-h-40 overflow-auto rounded bg-muted/50 p-2 text-[11px]">
                      {JSON.stringify({ before: it.before, after: it.after }, null, 2)}
                    </pre>
                  </details>
                ) : null}
              </Badge>
            </Td>
          </Tr>
        ))}
        {diff.items.length === 0 && (
          <Tr>
            <Td colSpan={3} className="py-4 text-center text-muted-foreground">
              两个版本没有资源级差异。
            </Td>
          </Tr>
        )}
      </DataTable>
    </div>
  );
}

export function VersionsPage() {
  const qc = useQueryClient();
  const user = tokenStore.user();
  const mayRollback = can.rollback(user);
  const [nodeId, setNodeId] = useNodeScope();
  const [compareA, setCompareA] = useState(""); // 基线版本 id（against）
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [banner, setBanner] = useState("");
  const [rollbackTo, setRollbackTo] = useState<ConfigVersion | null>(null);
  const [rbDeploymentId, setRbDeploymentId] = useState("");

  const { data: nodesData } = useNodes();
  const nodes: Node[] = nodesData?.items ?? [];
  const node = nodes.find((n) => n.id === nodeId);

  const { data, isPending, error } = useQuery({
    queryKey: ["versions", nodeId],
    queryFn: () => nodesApi.versions(nodeId),
    enabled: !!nodeId,
  });
  const versions = data?.items ?? [];

  const deployQ = useQuery({
    queryKey: ["deployment", rbDeploymentId],
    queryFn: () => pipelineApi.deployment(rbDeploymentId),
    enabled: !!rbDeploymentId,
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return s === "success" || s === "failed" ? false : 1500;
    },
    refetchOnWindowFocus: false,
  });

  useEffect(() => {
    setDiff(null);
    setCompareA("");
    setRbDeploymentId("");
  }, [nodeId]);

  async function showDiff(v: ConfigVersion) {
    setBanner("");
    try {
      setDiff(await pipelineApi.diff(v.id, compareA || undefined));
    } catch (e) {
      setBanner(humanError(e).text);
    }
  }

  async function doRollback(v: ConfigVersion) {
    setBanner("");
    try {
      // AC-008 同源门禁：回滚同样要求显式确认。
      const res = await pipelineApi.rollback({ node_id: v.node_id, version_id: v.id, confirmed: true });
      setRbDeploymentId(res.deployment_id ?? res.id ?? "");
      await qc.invalidateQueries({ queryKey: ["versions", nodeId] });
    } catch (e) {
      setBanner(humanError(e).text);
      setRollbackTo(null);
    }
  }

  const rbStatus = deployQ.data?.status;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">配置版本</h1>
          <p className="text-sm text-muted-foreground">每次发布固化一份不可变快照；历史可随时对比，成功过的版本可回滚。</p>
        </div>
        <NodeScope value={nodeId} onChange={setNodeId} />
      </div>
      {banner && <Alert>{banner}</Alert>}
      {!nodeId ? (
        <Alert>请先在上方选择一台网关。</Alert>
      ) : isPending ? (
        <Spinner />
      ) : error ? (
        <Alert>{humanError(error).text}</Alert>
      ) : (
        <DataTable head={["版本", "来源", "状态", "创建时间", "对比", "操作"]}>
          {versions.map((v) => (
            <Tr key={v.id}>
              <Td className="font-mono font-medium">v{v.version}</Td>
              <Td>
                <Badge tone={v.origin === "rollback" ? "warning" : "neutral"}>
                  {ORIGIN_LABEL[v.origin] ?? v.origin}
                </Badge>
              </Td>
              <Td>
                <StatusBadge status={v.status} />
              </Td>
              <Td className="text-xs">{new Date(v.created_at).toLocaleString()}</Td>
              <Td>
                <div className="flex items-center gap-1">
                  <Button
                    size="sm"
                    variant={compareA === v.id ? "default" : "outline"}
                    onClick={() => setCompareA(compareA === v.id ? "" : v.id)}
                  >
                    {compareA === v.id ? "作为基线 ✓" : "设为基线"}
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => void showDiff(v)} disabled={v.status !== "ready"}>
                    查看差异
                  </Button>
                </div>
              </Td>
              <Td>
                {mayRollback && v.status === "ready" && (
                  <Button
                    size="sm"
                    variant="danger"
                    onClick={() => {
                      if (window.confirm(`回滚到 v${v.version}？将以此快照生成新版本并走同一发布管线。`)) {
                        setRollbackTo(v);
                      }
                    }}
                  >
                    回滚到此版本
                  </Button>
                )}
              </Td>
            </Tr>
          ))}
          {versions.length === 0 && (
            <Tr>
              <Td colSpan={6} className="py-8 text-center text-muted-foreground">
                该网关还没有配置版本——完成一次发布后这里会出现历史。
              </Td>
            </Tr>
          )}
        </DataTable>
      )}

      {diff && (
        <Card>
          <h3 className="text-sm font-semibold">
            差异：v{diff.from_version} → v{diff.to_version}
            {compareA ? "（指定基线）" : "（vs 最近成功版本）"}
          </h3>
          <div className="mt-2">
            <DiffView diff={diff} />
          </div>
        </Card>
      )}

      <Modal
        open={rollbackTo !== null}
        onClose={() => {
          setRollbackTo(null);
          setRbDeploymentId("");
        }}
        title={rollbackTo ? `回滚到 v${rollbackTo.version}` : "回滚"}
      >
        {rollbackTo && (
          <div className="space-y-3" role="group" aria-label="回滚确认">
            {!rbDeploymentId ? (
              <>
                <Alert>
                  回滚将把网关配置恢复到 v{rollbackTo.version}
                  的快照内容（不回读当前库），并以新版本号走完整发布管线——这不删除历史。
                </Alert>
                <div className="flex justify-end gap-2">
                  <Button variant="outline" onClick={() => setRollbackTo(null)}>
                    取消
                  </Button>
                  <Button variant="danger" loading={deployQ.isFetching} onClick={() => void doRollback(rollbackTo)}>
                    确认回滚{node?.env_type === "production" ? "（生产）" : ""}
                  </Button>
                </div>
              </>
            ) : (
              <>
                <div className="flex items-center gap-2 text-sm">
                  部署状态 <StatusBadge status={deployQ.data?.status ?? "pending"} />
                </div>
                {rbStatus !== "success" && rbStatus !== "failed" && <Spinner label="回滚执行中，等待网关确认…" />}
                {rbStatus === "success" && (
                  <Alert className="border-success/50 bg-success/10 text-success">
                    回滚成功：网关已加载 v{rollbackTo.version} 的快照（新记录见上表）。
                  </Alert>
                )}
                {rbStatus === "failed" && (
                  <Alert>
                    回滚失败：
                    {deployQ.data?.error_message ?? "详见发布记录"}
                  </Alert>
                )}
                {(rbStatus === "success" || rbStatus === "failed") && (
                  <div className="flex justify-end">
                    <Button
                      variant="outline"
                      onClick={() => {
                        setRollbackTo(null);
                        setRbDeploymentId("");
                      }}
                    >
                      关闭
                    </Button>
                  </div>
                )}
              </>
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}
