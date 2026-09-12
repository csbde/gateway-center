// T061 · 策略库页（US3/AC-005、FR-017/020/021）：五类中间件类型化表单（zod per-type，
// 错误定位字段）、引用清单（referenced_by）、启停与被引用删除阻止（409 DEPENDENCY_BLOCKED）。
// 全页零 Traefik 术语（FR-042）：底层名称仅以 slug 形式出现在"策略标识"字段。
import { useState } from "react";
import { useForm, type UseFormRegister } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z, type ZodType } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { middlewaresApi } from "@/api/resources";
import type { Middleware, MiddlewareType } from "@/api/types";
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
import { ApiError } from "@/api/client";
import { NodeScope, StatusBadge, fieldErrors, humanError, useNodeScope } from "../common";

export const MW_TYPE_LABEL: Record<MiddlewareType, string> = {
  security_headers: "安全响应头",
  ip_allowlist: "IP 白名单",
  rate_limit: "限流保护",
  redirect: "访问重定向",
  strip_prefix: "路径前缀剥离",
};

const MW_TYPE_HINT: Record<MiddlewareType, string> = {
  security_headers: "为响应补齐安全头（防点击劫持、强制加密访问等）",
  ip_allowlist: "只允许名单内的来源地址访问",
  rate_limit: "限制单来源在时间窗内的请求次数，超出即拒绝",
  redirect: "把访问自动跳转到目标协议/域名/路径",
  strip_prefix: "转发前去掉路径前缀（如 /api）",
};

const REFERRER_POLICIES = [
  "no-referrer",
  "no-referrer-when-downgrade",
  "origin",
  "origin-when-cross-origin",
  "same-origin",
  "strict-origin",
  "strict-origin-when-cross-origin",
  "unsafe-url",
] as const;

const PERIODS = ["1s", "2s", "5s", "10s", "15s", "20s", "30s", "1m"] as const;

// ---- 五类参数 zod（与 backend mwreg 字段级规则一一对应，data-model §7） ----
// Select 的"不设置"以 "" 表达；提交前统一剔除空值。

const securityHeadersSchema = z
  .object({
    sts_seconds: z.coerce.number().int().min(0, "不能为负").max(63072000, "上限 63072000 秒（2 年）"),
    frame_options: z.enum(["", "deny", "sameorigin", "allow-from"]),
    sts_include_subdomains: z.boolean(),
    referrer_policy: z.enum(["", ...REFERRER_POLICIES]),
    content_type_nosniff: z.boolean(),
  })
  .superRefine((v, ctx) => {
    if (v.sts_seconds !== 0 && !v.frame_options)
      ctx.addIssue({ code: "custom", path: ["frame_options"], message: "启用强制加密访问时必须选择点击保护方式" });
  });

const linesToList = (s: string) =>
  s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);

const ipAllowlistSchema = z.object({
  cidrs: z
    .string()
    .min(1, "请至少填写一条 CIDR 或 IP")
    .transform(linesToList)
    .pipe(
      z
        .array(z.string().regex(/^([0-9]{1,3}(\.[0-9]{1,3}){3}(\/\d{1,2})?|[0-9a-fA-F:]+(\/\d{1,3})?)$/, "不是合法 IP 或 CIDR"))
        .min(1, "需要 1–100 条")
        .max(100, "最多 100 条"),
    ),
});

const rateLimitSchema = z
  .object({
    average: z.coerce.number().int().min(1, "速率阈值必须 ≥1"),
    burst: z.coerce.number().int().min(1, "突发上限必须 ≥1"),
    period: z.enum(PERIODS),
  })
  .superRefine((v, ctx) => {
    if (v.burst < v.average) ctx.addIssue({ code: "custom", path: ["burst"], message: "突发上限不得低于速率阈值" });
  });

const redirectSchema = z
  .object({
    scheme: z.enum(["", "https", "http"]),
    host: z.string().max(253),
    path: z.string(),
    permanent: z.boolean(),
  })
  .superRefine((v, ctx) => {
    if (!v.scheme && !v.host.trim())
      ctx.addIssue({ code: "custom", path: ["scheme"], message: "目标协议与目标域名至少填一项" });
    if (v.path && !v.path.startsWith("/"))
      ctx.addIssue({ code: "custom", path: ["path"], message: "路径必须以 / 开头" });
  });

const stripPrefixSchema = z.object({
  prefixes: z
    .string()
    .min(1, "请至少填写一条前缀")
    .transform(linesToList)
    .pipe(
      z
        .array(z.string().regex(/^\//, "前缀必须以 / 开头"))
        .min(1, "需要 1–10 条")
        .max(10, "最多 10 条"),
    ),
});

/** 表单值域：文本框里的列表字段以换行/逗号原始文本承载，zod transform 后成为数组。 */
type MwFormValues = {
  name: string;
  sts_seconds?: number | string;
  frame_options?: string;
  sts_include_subdomains?: boolean;
  referrer_policy?: string;
  content_type_nosniff?: boolean;
  cidrs?: string;
  average?: number | string;
  burst?: number | string;
  period?: string;
  scheme?: string;
  host?: string;
  path?: string;
  permanent?: boolean;
  prefixes?: string;
};

const SCHEMAS: Record<MiddlewareType, ZodType<MwFormValues>> = {
  security_headers: securityHeadersSchema as unknown as ZodType<MwFormValues>,
  ip_allowlist: ipAllowlistSchema as unknown as ZodType<MwFormValues>,
  rate_limit: rateLimitSchema as unknown as ZodType<MwFormValues>,
  redirect: redirectSchema as unknown as ZodType<MwFormValues>,
  strip_prefix: stripPrefixSchema as unknown as ZodType<MwFormValues>,
};

const DEFAULT_PARAMS: Record<MiddlewareType, Partial<MwFormValues>> = {
  security_headers: { sts_seconds: 31536000, frame_options: "sameorigin", sts_include_subdomains: false, referrer_policy: "", content_type_nosniff: true },
  ip_allowlist: { cidrs: "" },
  rate_limit: { average: 100, burst: 200, period: "1s" },
  redirect: { scheme: "https", host: "", path: "", permanent: true },
  strip_prefix: { prefixes: "" },
};

/** 列表页参数摘要：把 params 折算成人读一句话（零底层术语）。 */
export function mwParamsSummary(type: MiddlewareType, p: Record<string, unknown>): string {
  const list = (k: string) => ((p[k] as string[] | undefined) ?? []).join("、");
  switch (type) {
    case "security_headers": {
      const sts = Number(p.sts_seconds ?? 0);
      return [sts > 0 ? `强制加密访问 ${sts}s` : "不强制加密", p.frame_options ? `点击保护：${p.frame_options}` : ""]
        .filter(Boolean)
        .join("，");
    }
    case "ip_allowlist":
      return `${((p.cidrs as string[] | undefined) ?? []).length} 条允许地址`;
    case "rate_limit":
      return `每 ${p.period === "1m" ? "1 分钟" : String(p.period)} 平均 ${p.average} 次（突发 ${p.burst}）`;
    case "redirect":
      return `跳转 → ${p.scheme ? `${p.scheme}://` : ""}${String(p.host ?? "")}${String(p.path ?? "")}（${p.permanent ? "永久" : "临时"}）`;
    case "strip_prefix":
      return `去掉前缀 ${list("prefixes")}`;
  }
}

/** 编辑弹窗中已有的 params → 表单值（列表字段回展为换行文本）。 */
function paramsToForm(type: MiddlewareType, p: Record<string, unknown>): Partial<MwFormValues> {
  const v: Record<string, unknown> = { ...p };
  if (type === "ip_allowlist") v.cidrs = ((p.cidrs as string[] | undefined) ?? []).join("\n");
  if (type === "strip_prefix") v.prefixes = ((p.prefixes as string[] | undefined) ?? []).join("\n");
  for (const k of ["frame_options", "referrer_policy", "scheme", "host", "path"])
    if (v[k] == null) v[k] = "";
  return v as Partial<MwFormValues>;
}

// ---- 类型化参数字段（按类型渲染；错误本地 zod + 服务端 fieldErrors 双源定位） ----

function ParamsFields({
  type,
  register,
  errors,
  serverErr,
}: {
  type: MiddlewareType;
  register: UseFormRegister<MwFormValues>;
  errors: Record<string, { message?: string } | undefined>;
  serverErr: Record<string, string>;
}) {
  const err = (k: string) => errors[k]?.message ?? serverErr[k];
  switch (type) {
    case "security_headers":
      return (
        <div className="grid grid-cols-2 gap-3">
          <Field
            label="强制加密访问时长（秒）"
            htmlFor="p-sts"
            hint="0 表示不启用；上限 63072000（2 年）"
            error={err("sts_seconds") ?? err("frame_options")}
          >
            <Input id="p-sts" type="number" {...register("sts_seconds")} />
          </Field>
          <Field label="点击保护方式" htmlFor="p-fo" error={err("frame_options")}>
            <Select id="p-fo" {...register("frame_options")}>
              <option value="">不设置</option>
              <option value="deny">完全禁止被嵌套</option>
              <option value="sameorigin">仅允许同源嵌套</option>
              <option value="allow-from">允许指定来源嵌套</option>
            </Select>
          </Field>
          <Field label="来源引用策略" htmlFor="p-rp" error={err("referrer_policy")}>
            <Select id="p-rp" {...register("referrer_policy")}>
              <option value="">不设置</option>
              {REFERRER_POLICIES.map((rp) => (
                <option key={rp} value={rp}>
                  {rp}
                </option>
              ))}
            </Select>
          </Field>
          <div className="flex flex-col justify-end gap-2 pb-1 text-sm">
            <label className="flex items-center gap-2">
              <input type="checkbox" {...register("sts_include_subdomains")} /> 含全部子域名
            </label>
            <label className="flex items-center gap-2">
              <input type="checkbox" {...register("content_type_nosniff")} /> 禁止内容类型嗅探
            </label>
          </div>
        </div>
      );
    case "ip_allowlist":
      return (
        <Field
          label="允许的来源地址"
          htmlFor="p-cidrs"
          hint="每行一条 CIDR 或 IP（逗号分隔亦可），1–100 条"
          error={err("cidrs") ?? serverErr.cidrs}
        >
          <textarea
            id="p-cidrs"
            rows={5}
            className="w-full rounded-md border bg-background p-2 font-mono text-sm"
            placeholder={"203.0.113.0/24\n10.20.30.40"}
            {...register("cidrs")}
          />
        </Field>
      );
    case "rate_limit":
      return (
        <div className="grid grid-cols-3 gap-3">
          <Field label="速率阈值（次）" htmlFor="p-avg" hint="时间窗内平均允许次数" error={err("average")}>
            <Input id="p-avg" type="number" {...register("average")} />
          </Field>
          <Field label="突发上限（次）" htmlFor="p-burst" hint="允许瞬时冲高，须 ≥ 阈值" error={err("burst")}>
            <Input id="p-burst" type="number" {...register("burst")} />
          </Field>
          <Field label="时间窗" htmlFor="p-period" error={err("period")}>
            <Select id="p-period" {...register("period")}>
              {PERIODS.map((s) => (
                <option key={s} value={s}>
                  {s === "1m" ? "1 分钟" : s}
                </option>
              ))}
            </Select>
          </Field>
        </div>
      );
    case "redirect":
      return (
        <div className="grid grid-cols-2 gap-3">
          <Field label="目标协议" htmlFor="p-scheme" error={err("scheme")}>
            <Select id="p-scheme" {...register("scheme")}>
              <option value="">不改变</option>
              <option value="https">https（加密）</option>
              <option value="http">http</option>
            </Select>
          </Field>
          <Field label="目标域名" htmlFor="p-host" hint="留空则仅改协议/路径" error={err("host")}>
            <Input id="p-host" placeholder="www.example.com" {...register("host")} />
          </Field>
          <Field label="目标路径（可选）" htmlFor="p-path" error={err("path")}>
            <Input id="p-path" placeholder="/new-home" {...register("path")} />
          </Field>
          <label className="flex items-center gap-2 self-end pb-2 text-sm">
            <input type="checkbox" {...register("permanent")} /> 永久跳转（301）
          </label>
        </div>
      );
    case "strip_prefix":
      return (
        <Field
          label="要剥离的前缀"
          htmlFor="p-prefixes"
          hint="每行一条、以 / 开头，1–10 条"
          error={err("prefixes") ?? serverErr.prefixes}
        >
          <textarea
            id="p-prefixes"
            rows={4}
            className="w-full rounded-md border bg-background p-2 font-mono text-sm"
            placeholder="/api"
            {...register("prefixes")}
          />
        </Field>
      );
  }
}

function MiddlewareForm({
  mw,
  nodeId,
  type,
  onTypeChange,
  onClose,
}: {
  mw: Middleware | null;
  nodeId: string;
  type: MiddlewareType;
  onTypeChange?: (t: MiddlewareType) => void;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<MwFormValues>({
    resolver: zodResolver(SCHEMAS[type]),
    defaultValues: {
      name: mw?.name ?? "",
      ...(mw ? paramsToForm(mw.type as MiddlewareType, mw.params ?? {}) : DEFAULT_PARAMS[type]),
    },
  });

  const onSubmit = handleSubmit(async (v) => {
    setServerErr({});
    setBanner("");
    const body = { ...(v as Record<string, unknown>) };
    const name = String(body.name ?? "");
    delete body.name;
    // 空值参数不上行（后端按缺省处理），避免 "" 进入枚举。
    for (const k of Object.keys(body)) if (body[k] === "" || body[k] == null) delete body[k];
    try {
      if (mw) await middlewaresApi.update(mw.id, { name, params: body, expected_version: mw.row_version });
      else await middlewaresApi.create({ node_id: nodeId, name, type, params: body });
      await qc.invalidateQueries({ queryKey: ["middlewares"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  return (
    <form onSubmit={onSubmit} className="space-y-4" aria-label="中间件表单">
      {banner && <Alert>{banner}</Alert>}
      <div className="grid grid-cols-2 gap-3">
        <Field
          label="策略标识"
          htmlFor="mw-name"
          hint="小写字母开头，可用 a-z 0-9 -（作为网关内部资源名）"
          error={errors.name?.message ?? serverErr.name}
        >
          <Input id="mw-name" placeholder="strict-csp" {...register("name")} disabled={!!mw} />
        </Field>
        <Field
          label="策略类型"
          htmlFor="mw-type"
          hint={mw ? "创建后类型不可变更" : undefined}
          error={serverErr.type}
        >
          <Select
            id="mw-type"
            value={type}
            disabled={!!mw || !onTypeChange}
            onChange={(e) => onTypeChange?.(e.target.value as MiddlewareType)}
          >
            {(Object.keys(MW_TYPE_LABEL) as MiddlewareType[]).map((t) => (
              <option key={t} value={t}>
                {MW_TYPE_LABEL[t]}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <p className="text-xs text-muted-foreground">{MW_TYPE_HINT[type]}</p>
      <ParamsFields
        type={type}
        register={register}
        errors={errors as Record<string, { message?: string } | undefined>}
        serverErr={serverErr}
      />
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

/** 引用清单单元格：详情端点带 referenced_by（路由名列表，FR-020）。 */
function RefsCell({ id }: { id: string }) {
  const { data } = useQuery({
    queryKey: ["middlewares", id, "refs"],
    queryFn: () => middlewaresApi.get(id),
    staleTime: 30_000,
  });
  const refs = data?.referenced_by ?? [];
  if (refs.length === 0) return <span className="text-xs text-muted-foreground">未使用</span>;
  return (
    <span className="flex flex-wrap gap-1" title={refs.join("、")}>
      {refs.slice(0, 2).map((r) => (
        <Badge key={r} tone="neutral">
          {r}
        </Badge>
      ))}
      {refs.length > 2 && <Badge tone="neutral">+{refs.length - 2}</Badge>}
    </span>
  );
}

export function MiddlewaresPage() {
  const qc = useQueryClient();
  const writable = can.writeBusiness(tokenStore.user());
  const [nodeId, setNodeId] = useNodeScope();
  const [editing, setEditing] = useState<Middleware | null | "new">(null);
  const [newType, setNewType] = useState<MiddlewareType>("security_headers");
  const [banner, setBanner] = useState("");

  const { data, isPending, error } = useQuery({
    queryKey: ["middlewares", nodeId],
    queryFn: () => middlewaresApi.list({ node_id: nodeId, page_size: 100 }),
    enabled: !!nodeId,
  });
  const toggle = useMutation({
    mutationFn: ({ mw, enabled }: { mw: Middleware; enabled: boolean }) =>
      enabled ? middlewaresApi.enable(mw.id, mw.row_version) : middlewaresApi.disable(mw.id, mw.row_version),
    onSuccess: async () => {
      setBanner("");
      await qc.invalidateQueries({ queryKey: ["middlewares"] });
    },
    onError: (e) => setBanner(humanError(e).text),
  });
  const remove = useMutation({
    mutationFn: (id: string) => middlewaresApi.remove(id),
    onSuccess: async () => {
      setBanner("");
      await qc.invalidateQueries({ queryKey: ["middlewares"] });
      await qc.invalidateQueries({ queryKey: ["routes"] });
    },
    onError: (e) => {
      if (e instanceof ApiError && e.code === "DEPENDENCY_BLOCKED") {
        const refs = (e.details ?? []).map((d) => d.message).filter(Boolean);
        setBanner(`该策略正被 ${refs.length} 条规则使用：${refs.join("、")}。请先在路由规则中解除绑定再删除。`);
      } else setBanner(humanError(e).text);
    },
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">策略库</h1>
          <p className="text-sm text-muted-foreground">可复用的访问保护策略；在路由规则上按需绑定，多条规则共享同一份定义。</p>
        </div>
        <div className="flex items-center gap-3">
          <NodeScope value={nodeId} onChange={setNodeId} />
          {writable && nodeId && (
            <Button
              onClick={() => {
                setNewType("security_headers");
                setEditing("new");
              }}
            >
              新建策略
            </Button>
          )}
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
        <DataTable head={["策略", "类型", "参数概要", "被引用", "状态", "操作"]}>
          {(data.items ?? []).map((mw) => (
            <Tr key={mw.id}>
              <Td className="font-mono text-xs">{mw.name}</Td>
              <Td>
                <Badge tone="info">{MW_TYPE_LABEL[mw.type as MiddlewareType] ?? mw.type}</Badge>
              </Td>
              <Td className="text-sm text-muted-foreground">{mwParamsSummary(mw.type as MiddlewareType, mw.params ?? {})}</Td>
              <Td>
                <RefsCell id={mw.id} />
              </Td>
              <Td>
                <StatusBadge status={mw.enabled ? "enabled" : "disabled"} />
              </Td>
              <Td>
                <div className="flex gap-1">
                  {writable && (
                    <>
                      <Button size="sm" variant="outline" onClick={() => setEditing(mw)}>
                        编辑
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => toggle.mutate({ mw, enabled: !mw.enabled })}>
                        {mw.enabled ? "停用" : "启用"}
                      </Button>
                      <Button
                        size="sm"
                        variant="danger"
                        onClick={() => {
                          if (window.confirm(`删除策略「${mw.name}」？被规则引用时将被阻止。`)) remove.mutate(mw.id);
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
                该网关下尚无策略——建一条限流或白名单，再绑定到路由规则。
              </Td>
            </Tr>
          )}
        </DataTable>
      )}

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing && editing !== "new" ? `编辑：${editing.name}` : "新建策略"}
        wide
      >
        {editing !== null && (
          <MiddlewareForm
            // key：新建时切类型即换表单实例（zod resolver 与默认值随类型重建）。
            key={editing === "new" ? newType : editing.id}
            mw={editing === "new" ? null : editing}
            nodeId={nodeId}
            type={editing === "new" ? newType : (editing.type as MiddlewareType)}
            onTypeChange={editing === "new" ? setNewType : undefined}
            onClose={() => setEditing(null)}
          />
        )}
      </Modal>
    </div>
  );
}
