// T069 · 域名证书状态卡（FR-033/008）：GET /domains/{id}/certificate 只读观测。
// 展示来源/状态/到期/颁发者/SAN；expired→红、expiring_soon→黄。无 PEM/私钥（后端已裁剪）。
import { useQuery } from "@tanstack/react-query";
import { domainsApi } from "@/api/resources";
import type { CertificateView } from "@/api/types";
import { Alert, Badge, Spinner, type BadgeTone } from "@/components/ui";

const SOURCE_LABEL: Record<CertificateView["source"], string> = {
  acme: "自动签发（ACME）",
  imported: "自有导入",
  none: "无证书",
};

const STATUS_LABEL: Record<string, string> = {
  valid: "有效",
  expiring_soon: "即将到期",
  expired: "已过期",
  missing: "缺失",
  unknown: "未知",
};

const certTone: Record<string, BadgeTone> = {
  valid: "success",
  expiring_soon: "warning",
  expired: "danger",
  missing: "danger",
  unknown: "neutral",
};

function fmt(s?: string | null): string {
  return s ? new Date(s).toLocaleDateString() : "—";
}

export function CertCard({ domainId, domainName }: { domainId: string; domainName: string }) {
  const { data, isPending, error } = useQuery({
    queryKey: ["domain-cert", domainId],
    queryFn: () => domainsApi.certificate(domainId),
    enabled: !!domainId,
  });

  if (isPending) return <Spinner label="读取证书观测…" />;
  if (error) return <Alert>读取证书状态失败：{(error as Error).message}</Alert>;
  if (!data) return <Alert>暂无证书观测数据。</Alert>;

  const urgent = data.status === "expired" || data.status === "missing";

  return (
    <div className="space-y-3">
      {urgent && (
        <Alert className="border-danger/40 text-danger">
          该域名证书{STATUS_LABEL[data.status] ?? data.status}，请尽快处理以免影响 HTTPS 访问。
        </Alert>
      )}
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
        <Field label="域名" value={domainName} />
        <Field label="来源" value={SOURCE_LABEL[data.source]} />
        <span className="flex items-center gap-1.5">
          <span className="text-muted-foreground">状态</span>
          <Badge tone={certTone[data.status] ?? "neutral"}>{STATUS_LABEL[data.status] ?? data.status}</Badge>
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
        <Field label="颁发者" value={data.issuer || "—"} />
        <Field label="生效自" value={fmt(data.not_before)} />
        <Field label="到期于" value={fmt(data.not_after)} />
        <Field label="最近观测" value={fmt(data.observed_at)} />
      </div>
      {data.sans && data.sans.length > 0 && (
        <div className="text-sm">
          <span className="text-muted-foreground">SAN（覆盖域名）</span>
          <div className="mt-1 flex flex-wrap gap-1">
            {data.sans.map((s) => (
              <Badge key={s} tone="neutral">{s}</Badge>
            ))}
          </div>
        </div>
      )}
      {data.source === "acme" && (
        <p className="text-xs text-muted-foreground">
          证书由网关 ACME 自动签发与续期，平台仅观测实际下发证书的到期时间（不读取 acme.json）。
        </p>
      )}
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium">{value}</span>
    </span>
  );
}
