// T081 · 审计日志检索页（US6/FR-038）：actor/action/resource_type/时间范围 过滤 + 分页。
// 任何已认证角色可读（can.auditRead 对 viewer 返回 true；后端 GET /audit-logs 在 viewer+ 读组）。
// 审计表为 append-only（宪法 X），本页只读检索，无任何写入口。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { auditApi } from "@/api/resources";
import type { AuditLog } from "@/api/types";
import { Badge, Button, Card, DataTable, Field, Input, Spinner, Td, Tr } from "@/components/ui";
import { humanError } from "@/features/common";

const PAGE_SIZE = 20;

/** datetime-local 值 → RFC3339（后端 time.Parse(time.RFC3339)）。空串透传为 undefined。 */
function toRFC3339(local: string): string | undefined {
  if (!local) return undefined;
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

function JsonBlock({ label, data }: { label: string; data: Record<string, unknown> | null | undefined }) {
  if (!data || Object.keys(data).length === 0) return null;
  return (
    <div className="mt-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      <pre className="mt-0.5 max-h-40 overflow-auto rounded bg-muted/50 p-2 text-xs">{JSON.stringify(data, null, 2)}</pre>
    </div>
  );
}

export function AuditsPage() {
  // 编辑态过滤（输入框绑定，点查询才生效）
  const [fActor, setFActor] = useState("");
  const [fAction, setFAction] = useState("");
  const [fType, setFType] = useState("");
  const [fFrom, setFFrom] = useState("");
  const [fTo, setFTo] = useState("");
  // 生效过滤 + 分页（驱动查询）
  const [applied, setApplied] = useState<Record<string, string | number>>({ page: 1 });
  const [page, setPage] = useState(1);

  const query = { ...applied, page, page_size: PAGE_SIZE };
  const { data, isPending, error, isFetching } = useQuery({
    queryKey: ["audit-logs", query],
    queryFn: () => auditApi.list(query),
  });

  function applyFilters() {
    const f: Record<string, string | number> = {};
    if (fActor.trim()) f.actor_id = fActor.trim();
    if (fAction.trim()) f.action = fAction.trim();
    if (fType.trim()) f.resource_type = fType.trim();
    const from = toRFC3339(fFrom);
    const to = toRFC3339(fTo);
    if (from) f.from = from;
    if (to) f.to = to;
    setApplied(f);
    setPage(1);
  }

  function resetFilters() {
    setFActor("");
    setFAction("");
    setFType("");
    setFFrom("");
    setFTo("");
    setApplied({});
    setPage(1);
  }

  const items = data?.items ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold">审计日志</h1>
        <p className="text-sm text-muted-foreground">全量操作留痕，仅追加不可篡改（可按操作人、动作、资源、时间检索）。</p>
      </div>

      <Card>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
          <Field label="操作人 ID" htmlFor="f-actor">
            <Input id="f-actor" value={fActor} onChange={(e) => setFActor(e.target.value)} placeholder="UUID" />
          </Field>
          <Field label="动作" htmlFor="f-action">
            <Input id="f-action" value={fAction} onChange={(e) => setFAction(e.target.value)} placeholder="如 node.create" />
          </Field>
          <Field label="资源类型" htmlFor="f-type">
            <Input id="f-type" value={fType} onChange={(e) => setFType(e.target.value)} placeholder="如 node / route" />
          </Field>
          <Field label="起始时间" htmlFor="f-from">
            <Input id="f-from" type="datetime-local" value={fFrom} onChange={(e) => setFFrom(e.target.value)} />
          </Field>
          <Field label="截止时间" htmlFor="f-to">
            <Input id="f-to" type="datetime-local" value={fTo} onChange={(e) => setFTo(e.target.value)} />
          </Field>
        </div>
        <div className="mt-3 flex gap-2">
          <Button size="sm" onClick={applyFilters}>查询</Button>
          <Button size="sm" variant="outline" onClick={resetFilters}>重置</Button>
        </div>
      </Card>

      {isPending ? (
        <Spinner />
      ) : error ? (
        <Card className="border-destructive/40">{humanError(error).text}</Card>
      ) : (
        <>
          <DataTable head={["时间", "操作人", "动作", "资源", "IP", "详情"]}>
            {items.map((a) => (
              <AuditRow key={`${a.id}-${a.occurred_at}`} a={a} />
            ))}
            {items.length === 0 && (
              <Tr>
                <Td colSpan={6} className="py-8 text-center text-muted-foreground">
                  没有匹配的审计记录。
                </Td>
              </Tr>
            )}
          </DataTable>
          <div className="flex items-center justify-between text-sm text-muted-foreground">
            <span>
              共 {total} 条{isFetching ? "（查询中…）" : ""}
            </span>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
                上一页
              </Button>
              <span>
                {page} / {totalPages}
              </span>
              <Button size="sm" variant="outline" disabled={page >= totalPages} onClick={() => setPage((p) => p + 1)}>
                下一页
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function AuditRow({ a }: { a: AuditLog }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Tr className="cursor-pointer hover:bg-muted/40" onClick={() => setOpen((o) => !o)}>
        <Td className="whitespace-nowrap text-xs">{new Date(a.occurred_at).toLocaleString()}</Td>
        <Td className="text-xs">
          {a.actor_username || <span className="font-mono">{a.actor_id.slice(0, 8)}…</span>}
        </Td>
        <Td>
          <Badge tone="info">{a.action}</Badge>
        </Td>
        <Td className="text-xs">
          {a.resource_type}
          {a.resource_name ? ` · ${a.resource_name}` : ""}
        </Td>
        <Td className="font-mono text-xs">{a.ip || "—"}</Td>
        <Td className="text-xs text-muted-foreground">{open ? "收起" : "展开"}</Td>
      </Tr>
      {open && (
        <Tr>
          <Td colSpan={6}>
            <div className="space-y-1 text-xs">
              <div className="flex flex-wrap gap-4 text-muted-foreground">
                <span>资源 ID：{a.resource_id || "—"}</span>
                <span>请求 ID：{a.request_id || "—"}</span>
                <span>User-Agent：{a.user_agent || "—"}</span>
              </div>
              <JsonBlock label="变更前" data={a.before} />
              <JsonBlock label="变更后" data={a.after} />
            </div>
          </Td>
        </Tr>
      )}
    </>
  );
}
