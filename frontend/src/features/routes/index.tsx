// T046 · 路由简单模式向导（US1/AC-004、FR-014/042）：
// 网关→域名→路径→服务→加密访问 五步收敛为一张表单；展示"自动合成规则预览"，全页零 Traefik 术语。
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { domainsApi, routesApi, servicesApi } from "@/api/resources";
import type { Route, RouteView } from "@/api/types";
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

const routeSchema = z.object({
  name: z.string().min(1, "请输入规则名称").max(64),
  domain_id: z.string().min(1, "请选择域名"),
  path: z.string().min(1, "请输入路径").regex(/^\//, "路径必须以 / 开头"),
  match_type: z.enum(["prefix", "exact"]),
  service_id: z.string().min(1, "请选择服务"),
  https: z.boolean(),
});
type FormValues = z.input<typeof routeSchema>;

function RouteWizardForm({ route, nodeId, onClose }: { route: RouteView | null; nodeId: string; onClose: () => void }) {
  const qc = useQueryClient();
  const {
    register,
    handleSubmit,
    watch,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(routeSchema),
    defaultValues: route
      ? {
          name: route.name,
          domain_id: route.domain_id ?? "",
          path: route.path ?? "/",
          match_type: (route.match_type as "prefix" | "exact") ?? "prefix",
          service_id: route.service_id,
          https: route.https,
        }
      : { name: "", domain_id: "", path: "/", match_type: "prefix", service_id: "", https: false },
  });
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");
  const [preview, setPreview] = useState(route?.generated_rule_preview ?? "");

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
    if (!v.path) return "";
    const host = (domains?.items ?? []).find((d) => d.id === v.domain_id)?.name ?? "（选择域名后显示）";
    const match = v.match_type === "exact" ? `访问 ${host}${v.path}（完整匹配）` : `访问 ${host}${v.path === "/" ? "" : v.path} 下的任意地址`;
    const svc = (services?.items ?? []).find((s) => s.id === v.service_id)?.name ?? "（选择服务后显示）";
    return `${match} → 转给「${svc}」${v.https ? "，仅允许加密访问" : ""}`;
  })();

  const onSubmit = handleSubmit(async (vals) => {
    setServerErr({});
    setBanner("");
    try {
      if (route) {
        const updated = await routesApi.update(route.id, { ...vals, node_id: nodeId, expected_version: route.row_version });
        setPreview(updated.generated_rule_preview);
      } else {
        await routesApi.create({ ...vals, node_id: nodeId, mode: "simple" });
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
      <Field label="规则名称" htmlFor="rt-name" error={errors.name?.message ?? serverErr.name}>
        <Input id="rt-name" placeholder="如 CRM 主站" {...register("name")} />
      </Field>
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
        <Field label="匹配方式" htmlFor="rt-match" error={serverErr.match_type}>
          <Select id="rt-match" {...register("match_type")}>
            <option value="prefix">前缀（含子路径）</option>
            <option value="exact">完整路径</option>
          </Select>
        </Field>
      </div>
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
                        onClick={() =>
                          toggle.mutate({ r, enabled: !(r.status === "enabled") })
                        }
                      >
                        {r.status === "enabled" ? "停用" : "启用"}
                      </Button>
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
