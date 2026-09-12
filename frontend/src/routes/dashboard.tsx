// T048 · Dashboard 占位（US1）：资源计数卡；在线/漂移/证书预警由 US5/US4 补全。
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Card } from "@/components/ui";
import { domainsApi, routesApi, servicesApi } from "@/api/resources";
import { useNodes } from "@/features/common";

function CountCard({ label, to, value, sub }: { label: string; to: "/nodes" | "/domains" | "/services" | "/routes"; value: number | "…"; sub?: string }) {
  return (
    <Card className="transition-colors hover:border-primary/50">
      <Link to={to} className="block">
        <div className="text-sm text-muted-foreground">{label}</div>
        <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
        {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
      </Link>
    </Card>
  );
}

/** 计数取 page_size=1 的 total（SC-006：不为聚合拉全量）。 */
const countQuery = (key: string, fn: () => Promise<{ total: number }>) => ({
  queryKey: [key, "count"],
  queryFn: fn,
});

export function DashboardPage() {
  const nodesQ = useNodes();
  const nodeCount = nodesQ.data?.total ?? 0;
  const onlineCount = (nodesQ.data?.items ?? []).filter((n) => n.enabled).length;

  const domainsQ = useQuery(countQuery("domains", () => domainsApi.list({ page_size: 1 })));
  const servicesQ = useQuery(countQuery("services", () => servicesApi.list({ page_size: 1 })));
  const routesQ = useQuery(countQuery("routes", () => routesApi.list({ page_size: 1 })));

  const v = (x: { data?: { total: number }; isPending: boolean }) => (x.isPending ? "…" : x.data?.total ?? 0);

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold">概览</h1>
        <p className="text-sm text-muted-foreground">平台当前纳管情况；运行状态与漂移检测在后续版本接入。</p>
      </div>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <CountCard label="网关节点" to="/nodes" value={nodesQ.isPending ? "…" : nodeCount} sub={`启用中 ${onlineCount} 台`} />
        <CountCard label="域名" to="/domains" value={v(domainsQ)} />
        <CountCard label="服务" to="/services" value={v(servicesQ)} />
        <CountCard label="路由规则" to="/routes" value={v(routesQ)} />
      </div>
      <Card>
        <div className="text-sm font-medium">下一步</div>
        <p className="mt-1 text-sm text-muted-foreground">
          {nodeCount === 0 ? (
            <>
              还没有纳管网关——先到 <Link to="/nodes" className="text-primary underline">网关节点</Link>{" "}
              新建一台，再依次配置域名、服务与路由规则。
            </>
          ) : (
            <>
              资源就绪后，到 <Link to="/deployments" className="text-primary underline">发布</Link>{" "}
              页发起一次发布：检查 → 版本 → 变更确认 → 生效校验。
            </>
          )}
        </p>
      </Card>
    </div>
  );
}
