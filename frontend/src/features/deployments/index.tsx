// T047 · 发布向导（US1/AC-007/008/010）：验证报告逐条 → 生成版本 → Diff 预览 → 勾选确认才启用发布 →
// 轮询部署状态与运行时校验结果。生产类节点未经审批在 API 层 403（此处仅呈现错误引导）。
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { nodesApi, pipelineApi } from "@/api/resources";
import type { ConfigVersion, Deployment, DiffResult, ValidateResult } from "@/api/types";
import { can, tokenStore } from "@/api/session";
import { Alert, Badge, Button, Card, DataTable, Modal, Spinner, Td, Tr } from "@/components/ui";
import { ENV_LABEL, NodeScope, StatusBadge, humanError, useNodes, useNodeScope } from "../common";

const CHANGE_LABEL = { added: "新增", modified: "修改", removed: "移除" } as const;
const CHANGE_TONE = { added: "success", info: "info", modified: "info", removed: "danger" } as const;

function DiffTable({ diff }: { diff: DiffResult }) {
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
              <Badge tone={CHANGE_TONE[it.change]}>{CHANGE_LABEL[it.change]}</Badge>
            </Td>
          </Tr>
        ))}
        {diff.items.length === 0 && (
          <Tr>
            <Td colSpan={3} className="py-4 text-center text-muted-foreground">
              与当前生效版本没有差异。
            </Td>
          </Tr>
        )}
      </DataTable>
    </div>
  );
}

function VerificationSummary({ result }: { result: Record<string, unknown> }) {
  const passed = result.passed === true;
  return (
    <Card className={passed ? "border-success/50" : "border-destructive/50"}>
      <div className="flex items-center gap-2">
        <Badge tone={passed ? "success" : "danger"}>{passed ? "网关已确认生效" : "生效校验未通过"}</Badge>
        <span className="text-sm text-muted-foreground">
          {typeof result.message === "string" ? result.message : ""}
        </span>
      </div>
      <pre className="mt-2 max-h-48 overflow-auto rounded bg-muted/50 p-2 text-xs">
        {JSON.stringify(result, null, 2)}
      </pre>
    </Card>
  );
}

function DeployWizard({ nodeId, onClose }: { nodeId: string; onClose: () => void }) {
  const { data: nodesData } = useNodes();
  const node = (nodesData?.items ?? []).find((n) => n.id === nodeId);
  const [report, setReport] = useState<ValidateResult | null>(null);
  const [version, setVersion] = useState<ConfigVersion | null>(null);
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [deploymentId, setDeploymentId] = useState("");
  const [banner, setBanner] = useState("");
  const [busy, setBusy] = useState<"" | "validate" | "version" | "diff" | "deploy">("");

  const deployQ = useQuery({
    queryKey: ["deployment", deploymentId],
    queryFn: () => pipelineApi.deployment(deploymentId),
    enabled: !!deploymentId,
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return s === "success" || s === "failed" ? false : 1500;
    },
    refetchOnWindowFocus: false,
  });
  const deployment: Deployment | undefined = deployQ.data;
  const terminal = deployment?.status === "success" || deployment?.status === "failed";

  async function run(fn: () => Promise<void>, label: typeof busy) {
    setBusy(label);
    setBanner("");
    try {
      await fn();
    } catch (e) {
      setBanner(humanError(e).text);
    } finally {
      setBusy("");
    }
  }

  const blockers = (report?.issues ?? []).filter((i) => i.blocking);
  const warnings = (report?.issues ?? []).filter((i) => !i.blocking);

  return (
    <div className="space-y-4" aria-label="发布向导">
      {banner && <Alert>{banner}</Alert>}

      {/* ① 验证 */}
      <Card>
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold">① 发布前检查{report && ` · ${blockers.length ? "未通过" : "通过"}`}</h3>
          <Button size="sm" variant="outline" loading={busy === "validate"} onClick={() => void run(async () => setReport(await nodesApi.validate(nodeId)), "validate")}>
            {report ? "重新检查" : "开始检查"}
          </Button>
        </div>
        {report && (
          <ul className="mt-2 space-y-1.5 text-sm">
            {report.issues.length === 0 && <li className="text-muted-foreground">全部规则检查通过，共 {report.route_count} 条路由。</li>}
            {[...blockers, ...warnings].map((i, idx) => (
              <li key={idx} className="flex flex-wrap items-center gap-2">
                <Badge tone={i.blocking ? "danger" : "warning"}>{i.blocking ? "阻断" : "提醒"}</Badge>
                <span>
                  {i.resource ? `${i.resource}：` : ""}
                  {i.message}
                </span>
                {i.hint && <span className="text-xs text-muted-foreground">建议：{i.hint}</span>}
              </li>
            ))}
          </ul>
        )}
      </Card>

      {/* ② 生成版本 */}
      {report && blockers.length === 0 && (
        <Card>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">② 生成配置版本</h3>
            {!version && (
              <Button size="sm" loading={busy === "version"} onClick={() => void run(async () => setVersion(await nodesApi.createVersion(nodeId)), "version")}>
                生成版本
              </Button>
            )}
          </div>
          {version && (
            <p className="mt-2 text-sm">
              版本 <b>v{version.version}</b> <StatusBadge status={version.status} />
              {version.status === "ready" && !diff && (
                <Button size="sm" variant="outline" className="ml-2" loading={busy === "diff"} onClick={() => void run(async () => setDiff(await pipelineApi.diff(version.id)), "diff")}>
                  查看变更内容
                </Button>
              )}
            </p>
          )}
          {version && version.status !== "ready" && version.status !== "failed" && (
            <Spinner label="正在校验并生成产物…" />
          )}
          {version?.status === "failed" && <Alert className="mt-2">版本生成失败，请修正检查项后重试。</Alert>}
        </Card>
      )}

      {/* ③ Diff + 确认发布 */}
      {diff && !deployment && (
        <Card>
          <h3 className="text-sm font-semibold">③ 变更内容（v{diff.from_version} → v{diff.to_version}）</h3>
          <div className="mt-2">
            <DiffTable diff={diff} />
          </div>
          <label className="mt-3 flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(e) => setConfirmed(e.target.checked)}
              aria-label="确认变更内容"
            />
            我已逐条核对以上变更，确认发布到「{node?.name}（{node ? ENV_LABEL[node.env_type] : ""}）」
          </label>
          <div className="mt-3 flex items-center gap-2">
            {/* AC-008：未勾选则发布按钮不可用；确认标志同样随请求上送，服务端双重把关。 */}
            <Button
              variant={node?.env_type === "production" ? "danger" : "default"}
              disabled={!confirmed}
              loading={busy === "deploy"}
              onClick={() =>
                void run(async () => {
                  const res = await pipelineApi.deploy({ node_id: nodeId, version_id: version!.id, confirmed: true });
                  setDeploymentId(res.deployment_id ?? res.id ?? "");
                }, "deploy")
              }
            >
              发布
            </Button>
            {!confirmed && <span className="text-xs text-muted-foreground">请先勾选确认变更内容</span>}
          </div>
        </Card>
      )}

      {/* ④ 部署进度与运行时校验 */}
      {deployment && (
        <Card>
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold">④ 发布进度</h3>
            <StatusBadge status={deployment.status} />
            {!terminal && <Spinner label="网关正在加载，运行时校验中…" />}
          </div>
          {deployment.error_message && (
            <Alert className="mt-2">
              {deployment.error_code ? `${deployment.error_code}：` : ""}
              {deployment.error_message}
            </Alert>
          )}
          {terminal && deployment.verification_result && (
            <div className="mt-2">
              <VerificationSummary result={deployment.verification_result} />
            </div>
          )}
          {terminal && (
            <div className="mt-3 flex justify-end">
              <Button variant="outline" onClick={onClose}>
                {deployment.status === "success" ? "完成" : "关闭"}
              </Button>
            </div>
          )}
        </Card>
      )}
    </div>
  );
}

export function DeploymentsPage() {
  const writable = can.writeBusiness(tokenStore.user());
  const [nodeId, setNodeId] = useNodeScope();
  const [wizardOpen, setWizardOpen] = useState(false);
  const [resetKey, setResetKey] = useState(0);

  const { data: recent, isPending } = useQuery({
    queryKey: ["deployments", nodeId],
    queryFn: () => pipelineApi.deployments({ node_id: nodeId, page_size: 10 }),
    enabled: !!nodeId,
  });

  useEffect(() => {
    if (nodeId) setWizardOpen(false);
  }, [nodeId]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">发布</h1>
          <p className="text-sm text-muted-foreground">检查 → 版本 → 变更确认 → 发布 → 网关侧生效校验，一步不少。</p>
        </div>
        <div className="flex items-center gap-3">
          <NodeScope value={nodeId} onChange={setNodeId} />
          {writable && nodeId && (
            <Button
              onClick={() => {
                setResetKey((k) => k + 1); // 每次打开都从"检查"重新起步
                setWizardOpen(true);
              }}
            >
              发起发布
            </Button>
          )}
        </div>
      </div>

      {nodeId && (
        isPending ? <Spinner /> : (
          <DataTable head={["时间", "版本", "触发方式", "状态", "错误"]}>
            {(recent?.items ?? []).map((d) => (
              <Tr key={d.id ?? d.deployment_id}>
                <Td className="whitespace-nowrap text-xs">{new Date(d.created_at).toLocaleString()}</Td>
                <Td className="font-mono text-xs">{d.config_version_id.slice(0, 8)}…</Td>
                <Td>{d.trigger === "rollback" ? "回滚" : "发布"}</Td>
                <Td>
                  <StatusBadge status={d.status} />
                </Td>
                <Td className="max-w-64 truncate text-xs text-muted-foreground">{d.error_message ?? "—"}</Td>
              </Tr>
            ))}
            {(recent?.items ?? []).length === 0 && (
              <Tr>
                <Td colSpan={5} className="py-8 text-center text-muted-foreground">
                  该网关还没有发布记录。
                </Td>
              </Tr>
            )}
          </DataTable>
        )
      )}

      <Modal open={wizardOpen} onClose={() => setWizardOpen(false)} title="发布向导" wide>
        {nodeId && <DeployWizard key={resetKey} nodeId={nodeId} onClose={() => setWizardOpen(false)} />}
      </Modal>
    </div>
  );
}
