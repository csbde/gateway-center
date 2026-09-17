// T048/T074 · Dashboard：平台概览 + 运行状态 + 漂移汇总
// （US1 计数 + US4 证书预警 + US5 节点状态/漂移分列，宪章 XI）。
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Alert, Badge, Card, Spinner } from "@/components/ui";
import { dashboardApi } from "@/api/resources";
import { humanError, StatusBadge } from "@/features/common";
import { StatePanel } from "@/features/nodes/StatePanel";

type RouteTo = "/" | "/proxy-hosts" | "/certificates" | "/nodes" | "/domains" | "/services" | "/routes" | "/middlewares" | "/deployments";

function StatCard({
  label,
  value,
  to,
  highlight,
}: {
  label: string;
  value: number;
  to?: RouteTo;
  highlight?: "danger" | "warning";
}) {
  const cls =
    highlight && value > 0
      ? highlight === "danger"
        ? "border-destructive/50"
        : "border-warning/50"
      : undefined;
  const body = (
    <>
      <div className="text-sm text-muted-foreground">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
    </>
  );
  return (
    <Card className={cls}>
      {to ? (
        <Link to={to} className="block hover:opacity-80 transition-opacity">
          {body}
        </Link>
      ) : (
        body
      )}
    </Card>
  );
}

export function DashboardPage() {
  const { data: d, isPending, error } = useQuery({
    queryKey: ["dashboard"],
    queryFn: () => dashboardApi.get(),
    refetchInterval: 30_000,
  });

  if (isPending) return <Spinner label="加载概览…" />;
  if (error) return <Alert>{humanError(error).text}</Alert>;
  if (!d) return null;

  const expiring = d.expiring_certificates ?? [];
  const recent = d.recent_deployments ?? [];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold">概览</h1>
          <p className="text-sm text-muted-foreground">平台纳管资源与网关运行状态实时汇总。</p>
        </div>
        <div className="flex items-center gap-2">
          <Link
            to="/proxy-hosts"
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            快速代理网站
          </Link>
          <Link
            to="/certificates"
            className="inline-flex items-center gap-1.5 rounded-md border bg-card px-3 py-1.5 text-sm font-medium hover:bg-muted"
          >
            管理 SSL 证书
          </Link>
        </div>
      </div>

      {/* 网关运行状态（US5/T074：真实探测计数，非 enabled 估算） */}
      <div>
        <div className="mb-2 text-sm font-medium text-muted-foreground">网关运行状态</div>
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatCard label="在线" value={d.nodes_online} to="/nodes" />
          <StatCard label="离线" value={d.nodes_offline} to="/nodes" highlight="danger" />
          <StatCard label="部分异常" value={d.nodes_degraded} to="/nodes" highlight="warning" />
          <StatCard label="配置漂移" value={d.nodes_drift} to="/nodes" highlight="danger" />
        </div>
      </div>

      {/* 纳管资源计数 */}
      <div>
        <div className="mb-2 text-sm font-medium text-muted-foreground">纳管资源</div>
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
          <StatCard label="网关节点" value={d.counts.nodes} to="/nodes" />
          <StatCard label="域名" value={d.counts.domains} to="/domains" />
          <StatCard label="服务" value={d.counts.services} to="/services" />
          <StatCard label="路由规则" value={d.counts.routes} to="/routes" />
          <StatCard label="中间件" value={d.counts.middlewares} to="/middlewares" />
        </div>
      </div>

      {/* 证书到期预警（US4，FR-008） */}
      {expiring.length > 0 && (
        <Alert>
          <div className="font-medium">证书到期预警（{expiring.length}）</div>
          <ul className="mt-2 space-y-1 text-sm">
            {expiring.map((c) => (
              <li key={c.domain_id} className="flex items-center gap-2">
                <Badge tone={c.status === "expired" ? "danger" : "warning"}>
                  {c.status === "expired" ? "已过期" : "即将到期"}
                </Badge>
                <Link to="/domains" className="font-medium underline">
                  {c.domain_name}
                </Link>
                <span className="text-muted-foreground">
                  {c.not_after ? `到期于 ${new Date(c.not_after).toLocaleDateString()}` : "无到期信息"}
                </span>
              </li>
            ))}
          </ul>
        </Alert>
      )}

      {/* 最近发布时间线 */}
      {recent.length > 0 && (
        <Card>
          <div className="text-sm font-medium">最近发布</div>
          <ul className="mt-2 space-y-2 text-sm">
            {recent.map((dep) => (
              <li key={dep.id} className="flex items-center gap-2">
                <StatusBadge status={dep.status} />
                <Link to="/deployments" className="font-medium underline">
                  {dep.node_name}
                </Link>
                <span className="text-muted-foreground">
                  {dep.trigger} · {new Date(dep.created_at).toLocaleString()}
                </span>
              </li>
            ))}
          </ul>
        </Card>
      )}

      {/* 节点运行态 + 漂移详情（US5/T074，宪章 XI 期望/实际分列） */}
      <div>
        <div className="mb-2 text-sm font-medium text-muted-foreground">节点运行态与漂移检测</div>
        <StatePanel />
      </div>

      {/* 下一步引导 */}
      <Card>
        <div className="text-sm font-medium">下一步</div>
        <p className="mt-1 text-sm text-muted-foreground">
          {d.counts.nodes === 0 ? (
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
