// T046 · 路由简单模式向导（US1/AC-004、FR-014/042）：
// 网关→域名→路径→服务→加密访问 五步收敛为一张表单；展示"自动合成规则预览"，全页零 Traefik 术语。
// T061 · US3 扩展：策略（中间件）多选 + 顺序调整（FR-018）；高级模式编辑器
// （仅 gateway_admin 可见：风险横幅 + 实时语法验证 + 归一化预览，FR-015/016；宪法 II 双模式隔离）。
import { useEffect, useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { domainsApi, middlewaresApi, routesApi, servicesApi } from "@/api/resources";
import type { Middleware, Route, RouteView } from "@/api/types";
import { can, tokenStore } from "@/api/session";
import {
  Alert,
  Badge,
  Button,
  DataTable,
  Field,
  Input,
  Modal,
  Select,
  Spinner,
  Td,
  Tr,
} from "@/components/ui";
import { NodeScope, StatusBadge, fieldErrors, humanError, useNodeScope } from "../common";

const routeSchema = z
  .object({
    name: z.string().min(1, "请输入规则名称").max(64),
    mode: z.enum(["simple", "advanced"]),
    advanced_rule: z.string(),
    domain_id: z.string(),
    path: z.string(),
    match_type: z.enum(["prefix", "exact"]),
    service_id: z.string().min(1, "请选择服务"),
    https: z.boolean(),
  })
  .superRefine((v, ctx) => {
    if (v.mode === "simple") {
      if (!v.domain_id) ctx.addIssue({ code: "custom", path: ["domain_id"], message: "请选择域名" });
      if (!v.path) ctx.addIssue({ code: "custom", path: ["path"], message: "请输入路径" });
      else if (!v.path.startsWith("/"))
        ctx.addIssue({ code: "custom", path: ["path"], message: "路径必须以 / 开头" });
    }
  });
type FormValues = z.input<typeof routeSchema>;

// ---- 策略（中间件）有序多选（FR-018：绑定顺序即执行顺序） ----

function MiddlewarePicker({
  nodeId,
  value,
  onChange,
}: {
  nodeId: string;
  value: string[];
  onChange: (ids: string[]) => void;
}) {
  const { data } = useQuery({
    queryKey: ["middlewares", nodeId, "options"],
    queryFn: () => middlewaresApi.list({ node_id: nodeId, page_size: 100 }),
    enabled: !!nodeId,
  });
  const all = data?.items ?? [];
  const byId = useMemo(() => new Map(all.map((m: Middleware) => [m.id, m])), [all]);
  const selected = value.map((id) => byId.get(id)).filter((m): m is Middleware => !!m);
  const available = all.filter((m) => !value.includes(m.id));

  const move = (idx: number, dir: -1 | 1) => {
    const next = [...value];
    const j = idx + dir;
    if (j < 0 || j >= next.length) return;
    [next[idx], next[j]] = [next[j], next[idx]];
    onChange(next);
  };

  return (
    <div className="rounded-md border bg-muted/30 p-3" aria-label="绑定策略">
      <div className="text-xs font-medium uppercase text-muted-foreground">附加保护策略（按顺序执行）</div>
      {selected.length === 0 && <p className="mt-1 text-sm text-muted-foreground">未绑定——上方"可用策略"点击即添加。</p>}
      <ol className="mt-2 space-y-1">
        {selected.map((m, i) => (
          <li key={m.id} className="flex items-center gap-2 text-sm">
            <Badge tone={m.enabled ? "info" : "warning"}>{i + 1}</Badge>
            <span className="font-mono text-xs">{m.name}</span>
            {!m.enabled && <span className="text-xs text-destructive">已停用（发布前需启用或解绑）</span>}
            <span className="ml-auto flex gap-1">
              <Button size="sm" variant="ghost" aria-label="上移" disabled={i === 0} onClick={() => move(i, -1)}>
                ↑
              </Button>
              <Button
                size="sm"
                variant="ghost"
                aria-label="下移"
                disabled={i === selected.length - 1}
                onClick={() => move(i, 1)}
              >
                ↓
              </Button>
              <Button size="sm" variant="ghost" aria-label="解绑" onClick={() => onChange(value.filter((x) => x !== m.id))}>
                ✕
              </Button>
            </span>
          </li>
        ))}
      </ol>
      {available.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1">
          {available.map((m) => (
            <Button key={m.id} size="sm" variant="outline" onClick={() => onChange([...value, m.id])}>
              + {m.name}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}

// ---- 高级模式编辑器（T060/T061：风险横幅 + 实时语法验证 + 归一化预览） ----

function AdvancedEditor({
  rule,
  onRuleChange,
  serverErr,
}: {
  rule: string;
  onRuleChange: (v: string) => void;
  serverErr?: string;
}) {
  const [state, setState] = useState<"idle" | "checking" | "done">("idle");
  const [result, setResult] = useState<Awaited<ReturnType<typeof routesApi.validateAdvanced>> | null>(null);

  // 防抖 400ms 实时验证（FR-015"保存即校验"的前置即时反馈；后端保存路径仍强校验）。
  useEffect(() => {
    const trimmed = rule.trim();
    if (!trimmed) {
      setState("idle");
      setResult(null);
      return;
    }
    setState("checking");
    const t = setTimeout(() => {
      routesApi
        .validateAdvanced(trimmed)
        .then((r) => {
          setResult(r);
          setState("done");
        })
        .catch(() => setState("idle"));
    }, 400);
    return () => clearTimeout(t);
  }, [rule]);

  return (
    <div className="space-y-2" role="group" aria-label="自定义匹配条件">
      <Alert>
        {result?.risk_notice ??
          "高级模式直接编写网关匹配条件：写错会导致流量无法匹配；保存与每次修改都会进入审计，且需要管理员以上权限。"}
      </Alert>
      <textarea
        aria-label="匹配条件表达式"
        rows={3}
        className="w-full rounded-md border bg-background p-2 font-mono text-sm"
        placeholder="Host(`crm.example.com`) && PathPrefix(`/api`)"
        value={rule}
        onChange={(e) => onRuleChange(e.target.value)}
      />
      {serverErr && <p className="text-xs text-destructive">{serverErr}</p>}
      {state === "checking" && <p className="text-xs text-muted-foreground">正在检查语法…</p>}
      {state === "done" && result && (
        <>
          {result.valid ? (
            <p className="text-xs text-success" data-testid="adv-valid">
              ✓ 语法通过。实际生效条件为：<code className="font-mono">{result.normalized_preview}</code>
            </p>
          ) : (
            <ul className="text-xs text-destructive">
              {result.issues.map((it, i) => (
                <li key={i}>· {it.message}</li>
              ))}
            </ul>
          )}
        </>
      )}
      <p className="text-xs text-muted-foreground">
        支持条件：域名 / 域名模式 / 路径 / 路径前缀 / 请求头 / 方法，可用「且」「或」与括号组合。
      </p>
    </div>
  );
}

function RouteWizardForm({ route, nodeId, onClose }: { route: RouteView | null; nodeId: string; onClose: () => void }) {
  const qc = useQueryClient();
  const advancedAllowed = can.advancedRoute(tokenStore.user());
  const [mode, setMode] = useState<"simple" | "advanced">(route?.mode ?? "simple");
  const [rule, setRule] = useState(route?.advanced_rule ?? "");
  const [mwIds, setMwIds] = useState<string[]>(
    [...(route?.middlewares ?? [])].sort((a, b) => a.Position - b.Position).map((m) => m.MiddlewareID),
  );
  const {
    register,
    handleSubmit,
    setValue,
    watch,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(routeSchema),
    defaultValues: route
      ? {
          name: route.name,
          mode: route.mode,
          advanced_rule: route.advanced_rule ?? "",
          domain_id: route.domain_id ?? "",
          path: route.path ?? (route.mode === "advanced" ? "" : "/"),
          match_type: (route.match_type as "prefix" | "exact") ?? "prefix",
          service_id: route.service_id,
          https: route.https,
        }
      : {
          name: "",
          mode: "simple",
          advanced_rule: "",
          domain_id: "",
          path: "/",
          match_type: "prefix",
          service_id: "",
          https: false,
        },
  });
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");
  const [preview, setPreview] = useState(route?.generated_rule_preview ?? "");

  useEffect(() => {
    setValue("mode", mode);
    setValue("advanced_rule", rule);
  }, [mode, rule, setValue]);

  const { data: domains } = useQuery({
    queryKey: ["domains", nodeId, "options"],
    queryFn: () => domainsApi.list({ node_id: nodeId, page_size: 100 }),
    enabled: !!nodeId,
  });
  const { data: services } = useQuery({
    queryKey: ["services", nodeId, "options"],
    queryFn: () => servicesApi.list({ node_id: nodeId, page_size: 100 }),
    enabled: !!nodeId,
  });

  const v = watch();
  // 本地即时预览（与后端 generated_rule_preview 同语义；保存后以服务端为准）。
  const localPreview = (() => {
    if (mode === "advanced") return rule ? "按自定义条件匹配请求" : "";
    if (!v.path) return "";
    const host = (domains?.items ?? []).find((d) => d.id === v.domain_id)?.name ?? "（选择域名后显示）";
    const match =
      v.match_type === "exact"
        ? `访问 ${host}${v.path}（完整匹配）`
        : `访问 ${host}${v.path === "/" ? "" : v.path} 下的任意地址`;
    const svc = (services?.items ?? []).find((s) => s.id === v.service_id)?.name ?? "（选择服务后显示）";
    return `${match} → 转给「${svc}」${v.https ? "，仅允许加密访问" : ""}`;
  })();

  const onSubmit = handleSubmit(async (vals) => {
    setServerErr({});
    setBanner("");
    const body: Record<string, unknown> = {
      name: vals.name,
      mode,
      service_id: vals.service_id,
      https: vals.https,
      middleware_ids: mwIds,
    };
    if (mode === "advanced") body.advanced_rule = rule;
    else {
      body.domain_id = vals.domain_id;
      body.path = vals.path;
      body.match_type = vals.match_type;
    }
    try {
      if (route) {
        const updated = await routesApi.update(route.id, { ...body, node_id: nodeId, expected_version: route.row_version });
        setPreview(updated.generated_rule_preview);
      } else {
        await routesApi.create({ ...body, node_id: nodeId });
      }
      await qc.invalidateQueries({ queryKey: ["routes"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  return (
    <form onSubmit={onSubmit} className="space-y-4" aria-label="路由规则表单">
      {banner && <Alert>{banner}</Alert>}
      {advancedAllowed && (
        <div className="flex gap-2" role="tablist" aria-label="匹配方式">
          <Button type="button" size="sm" variant={mode === "simple" ? "default" : "outline"} onClick={() => setMode("simple")}>
            常用设置
          </Button>
          <Button
            type="button"
            size="sm"
            variant={mode === "advanced" ? "default" : "outline"}
            onClick={() => setMode("advanced")}
          >
            自定义条件（管理员）
          </Button>
        </div>
      )}
      <Field label="规则名称" htmlFor="rt-name" error={errors.name?.message ?? serverErr.name}>
        <Input id="rt-name" placeholder="如 CRM 主站" {...register("name")} />
      </Field>
      {mode === "simple" ? (
        <>
          <Field label="域名" htmlFor="rt-domain" error={errors.domain_id?.message ?? serverErr.domain_id}>
            <Select id="rt-domain" {...register("domain_id")}>
              <option value="">请选择域名…</option>
              {(domains?.items ?? []).map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name}
                  {d.enabled ? "" : " · 已停用"}
                </option>
              ))}
            </Select>
          </Field>
          <div className="grid grid-cols-[2fr_1fr] gap-3">
            <Field label="路径" htmlFor="rt-path" error={errors.path?.message ?? serverErr.path}>
              <Input id="rt-path" placeholder="/crm" {...register("path")} />
            </Field>
            <Field label="匹配方式" htmlFor="rt-match" error={errors.match_type?.message ?? serverErr.match_type}>
              <Select id="rt-match" {...register("match_type")}>
                <option value="prefix">前缀（含子路径）</option>
                <option value="exact">完整路径</option>
              </Select>
            </Field>
          </div>
        </>
      ) : (
        <AdvancedEditor rule={rule} onRuleChange={setRule} serverErr={serverErr.advanced_rule} />
      )}
      <Field label="转发到服务" htmlFor="rt-svc" error={errors.service_id?.message ?? serverErr.service_id}>
        <Select id="rt-svc" {...register("service_id")}>
          <option value="">请选择服务…</option>
          {(services?.items ?? []).map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
              {s.enabled ? "" : " · 已停用"}
            </option>
          ))}
        </Select>
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" {...register("https")} /> 仅允许加密访问（HTTP 自动跳转 HTTPS）
      </label>
      <MiddlewarePicker nodeId={nodeId} value={mwIds} onChange={setMwIds} />
      <div className="rounded-md border bg-muted/40 p-3">
        <div className="text-xs font-medium uppercase text-muted-foreground">访问规则预览</div>
        <p className="mt-1 text-sm">{preview || localPreview || "填写上方选项后自动生成"}</p>
        <p className="mt-1 text-xs text-muted-foreground">平台按此说明自动生成全部底层配置，无需手写任何文件。</p>
      </div>
      <div className="flex justify-end gap-2 pt-2">
        <Button variant="outline" onClick={onClose}>
          取消
        </Button>
        <Button type="submit" loading={isSubmitting}>
          保存
        </Button>
      </div>
    </form>
  );
}

export function RoutesPage() {
  const qc = useQueryClient();
  const writable = can.writeBusiness(tokenStore.user());
  const [nodeId, setNodeId] = useNodeScope();
  const [editing, setEditing] = useState<RouteView | null | "new">(null);
  const [banner, setBanner] = useState("");

  const { data, isPending, error } = useQuery({
    queryKey: ["routes", nodeId],
    queryFn: () => routesApi.list({ node_id: nodeId, page_size: 100 }),
    enabled: !!nodeId,
  });
  const detail = useMutation({ mutationFn: (id: string) => routesApi.get(id) });
  const toggle = useMutation({
    mutationFn: ({ r, enabled }: { r: Pick<Route, "id" | "row_version">; enabled: boolean }) =>
      enabled ? routesApi.enable(r.id, r.row_version) : routesApi.disable(r.id, r.row_version),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["routes"] }),
    onError: (e) => setBanner(humanError(e).text),
  });
  const remove = useMutation({
    mutationFn: (id: string) => routesApi.remove(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["routes"] }),
    onError: (e) => setBanner(humanError(e).text),
  });
  const archive = useMutation({
    mutationFn: (r: Pick<Route, "id" | "row_version">) => routesApi.archive(r.id, r.row_version),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["routes"] }),
    onError: (e) => setBanner(humanError(e).text),
  });

  const openEdit = async (id: string) => {
    try {
      setEditing(await detail.mutateAsync(id));
    } catch (e) {
      setBanner(humanError(e).text);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">路由规则</h1>
          <p className="text-sm text-muted-foreground">"谁访问哪里，交给哪个服务"——选择即可，平台自动合成底层配置。</p>
        </div>
        <div className="flex items-center gap-3">
          <NodeScope value={nodeId} onChange={setNodeId} />
          {writable && nodeId && <Button onClick={() => setEditing("new")}>新建规则</Button>}
        </div>
      </div>
      {banner && <Alert>{banner}</Alert>}
      {!nodeId ? (
        <Alert>请先在上方选择一台网关。</Alert>
      ) : isPending ? (
        <Spinner />
      ) : error ? (
        <Alert>{humanError(error).text}</Alert>
      ) : (
        <DataTable head={["规则", "路径匹配", "加密", "优先级", "状态", "操作"]}>
          {(data.items ?? []).map((r) => (
            <Tr key={r.id}>
              <Td className="font-medium">{r.name}</Td>
              <Td>
                <span className="font-mono text-xs">
                  {r.path}
                  {r.match_type === "prefix" ? "/*" : ""}
                </span>
                {r.mode === "advanced" && <Badge tone="warning">自定义条件</Badge>}
              </Td>
              <Td>{r.https ? "仅加密" : "不强制"}</Td>
              <Td>{r.priority}</Td>
              <Td>
                <StatusBadge status={r.status} />
              </Td>
              <Td>
                <div className="flex gap-1">
                  {writable && (
                    <>
                      <Button size="sm" variant="outline" onClick={() => void openEdit(r.id)}>
                        编辑
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={r.status === "archived"}
                        onClick={() =>
                          toggle.mutate({ r, enabled: !(r.status === "enabled") })
                        }
                      >
                        {r.status === "enabled" ? "停用" : "启用"}
                      </Button>
                      {can.archiveRoute(tokenStore.user()) && (
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={r.status === "archived"}
                          onClick={() => {
                            if (window.confirm(`归档规则「${r.name}」？归档后不再生成配置，且不可恢复启用（FR-039 处置终态）。`))
                              archive.mutate(r);
                          }}
                        >
                          归档
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="danger"
                        onClick={() => {
                          if (window.confirm(`删除规则「${r.name}」？`)) remove.mutate(r.id);
                        }}
                      >
                        删除
                      </Button>
                    </>
                  )}
                </div>
              </Td>
            </Tr>
          ))}
          {(data.items ?? []).length === 0 && (
            <Tr>
              <Td colSpan={6} className="py-8 text-center text-muted-foreground">
                该网关下尚无路由规则——先建好域名与服务，再来这里连线。
              </Td>
            </Tr>
          )}
        </DataTable>
      )}

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing === "new" ? "新建访问规则" : `编辑：${editing?.name ?? ""}`}
        wide
      >
        {editing !== null && (
          <RouteWizardForm route={editing === "new" ? null : editing} nodeId={nodeId} onClose={() => setEditing(null)} />
        )}
      </Modal>
    </div>
  );
}
