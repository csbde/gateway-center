// T044 · 服务页（US1/AC-003）：Service 表格 + Target 子表（zod 地址/权重校验，错误定位字段）。
import { Fragment, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { servicesApi } from "@/api/resources";
import type { Service } from "@/api/types";
import { can, tokenStore } from "@/api/session";
import {
  Alert,
  Button,
  DataTable,
  Field,
  Input,
  Modal,
  Spinner,
  Td,
  Tr,
  Badge,
} from "@/components/ui";
import { NodeScope, StatusBadge, fieldErrors, humanError, useDependencyBlock, useNodeScope } from "../common";

const svcSchema = z.object({
  name: z.string().min(1, "请输入服务名").max(64),
  description: z.string().max(200).optional(),
  expected_codes: z.string().min(1, "如 2xx-3xx"),
  interval_sec: z.coerce.number().int().min(5).max(3600),
  timeout_sec: z.coerce.number().int().min(1).max(60),
  healthcheck_path: z.string().optional(),
});
const targetSchema = z.object({
  url: z.string().regex(/^https?:\/\/.+/, "目标地址需为 http(s)://…"),
  weight: z.coerce.number().int().min(1).max(65535),
});

function ServiceForm({ service, nodeId, onClose }: { service: Service | null; nodeId: string; onClose: () => void }) {
  const qc = useQueryClient();
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<z.input<typeof svcSchema>>({
    resolver: zodResolver(svcSchema),
    defaultValues: service
      ? {
          name: service.name,
          description: service.description,
          expected_codes: service.expected_codes,
          interval_sec: service.interval_sec,
          timeout_sec: service.timeout_sec,
          healthcheck_path: service.healthcheck_path,
        }
      : { name: "", description: "", expected_codes: "2xx-3xx", interval_sec: 30, timeout_sec: 2, healthcheck_path: "" },
  });
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");

  const onSubmit = handleSubmit(async (v) => {
    setServerErr({});
    setBanner("");
    try {
      if (service) await servicesApi.update(service.id, { ...v, expected_version: service.row_version });
      else await servicesApi.create({ ...v, node_id: nodeId });
      await qc.invalidateQueries({ queryKey: ["services"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  return (
    <form onSubmit={onSubmit} className="space-y-4" aria-label="服务表单">
      {banner && <Alert>{banner}</Alert>}
      <Field label="服务名" htmlFor="svc-name" error={errors.name?.message ?? serverErr.name}>
        <Input id="svc-name" {...register("name")} aria-invalid={!!(errors.name ?? serverErr.name)} />
      </Field>
      <Field label="说明" htmlFor="svc-desc" error={serverErr.description}>
        <Input id="svc-desc" {...register("description")} />
      </Field>
      <div className="grid grid-cols-3 gap-3">
        <Field label="健康码范围" htmlFor="svc-codes" hint="如 2xx-3xx" error={errors.expected_codes?.message ?? serverErr.expected_codes}>
          <Input id="svc-codes" {...register("expected_codes")} />
        </Field>
        <Field label="探测间隔（秒）" htmlFor="svc-int" error={errors.interval_sec?.message ?? serverErr.interval_sec}>
          <Input id="svc-int" type="number" {...register("interval_sec")} />
        </Field>
        <Field label="超时（秒）" htmlFor="svc-to" error={errors.timeout_sec?.message ?? serverErr.timeout_sec}>
          <Input id="svc-to" type="number" {...register("timeout_sec")} />
        </Field>
      </div>
      <Field label="健康检查路径（可选）" htmlFor="svc-hc" hint="留空则探测根路径" error={serverErr.healthcheck_path}>
        <Input id="svc-hc" placeholder="/healthz" {...register("healthcheck_path")} />
      </Field>
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

function TargetsPanel({ service }: { service: Service }) {
  const qc = useQueryClient();
  const writable = can.writeBusiness(tokenStore.user());
  const { data, isPending } = useQuery({
    queryKey: ["targets", service.id],
    queryFn: () => servicesApi.targets(service.id),
  });
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<z.input<typeof targetSchema>>({
    resolver: zodResolver(targetSchema),
    defaultValues: { url: "http://", weight: 1 },
  });
  const [serverErr, setServerErr] = useState<Record<string, string>>({});

  const add = useMutation({
    mutationFn: (v: z.input<typeof targetSchema>) => servicesApi.addTarget(service.id, v),
    onSuccess: async () => {
      setServerErr({});
      reset({ url: "http://", weight: 1 });
      await qc.invalidateQueries({ queryKey: ["targets", service.id] });
      await qc.invalidateQueries({ queryKey: ["services"] });
    },
    onError: (e) => setServerErr(fieldErrors(e)),
  });
  const del = useMutation({
    mutationFn: (tid: string) => servicesApi.deleteTarget(service.id, tid),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["targets", service.id] }),
  });

  return (
    <div className="space-y-2 rounded-md border bg-muted/30 p-3">
      <div className="text-xs font-medium uppercase text-muted-foreground">转发目标（Target）</div>
      {isPending && <Spinner label="加载目标…" />}
      {(data?.items ?? []).map((t) => (
        <div key={t.id} className="flex items-center gap-2 text-sm">
          <span className="font-mono">{t.url}</span>
          <Badge tone="neutral">权重 {t.weight}</Badge>
          <StatusBadge status={t.enabled ? (t.health_status === "up" ? "up" : t.health_status === "down" ? "down" : "unknown") : "disabled"} />
          {writable && (
            <Button size="sm" variant="ghost" className="ml-auto" onClick={() => del.mutate(t.id)}>
              移除
            </Button>
          )}
        </div>
      ))}
      {(data?.items ?? []).length === 0 && !isPending && (
        <p className="text-sm text-muted-foreground">尚无目标——服务至少需要一个可用目标才能转发。</p>
      )}
      {writable && (
        <form onSubmit={handleSubmit((v) => add.mutate(v))} className="mt-2 flex items-end gap-2">
          <div className="flex-1">
            <Input aria-label="目标地址" placeholder="http://10.0.0.5:8080" {...register("url")} />
            {(errors.url ?? serverErr.url) && (
              <p className="mt-1 text-xs text-destructive">{(errors.url?.message ?? serverErr.url) as string}</p>
            )}
          </div>
          <div className="w-24">
            <Input aria-label="权重" type="number" {...register("weight")} />
            {(errors.weight ?? serverErr.weight) && (
              <p className="mt-1 text-xs text-destructive">{(errors.weight?.message ?? serverErr.weight) as string}</p>
            )}
          </div>
          <Button type="submit" size="sm" loading={add.isPending}>
            添加
          </Button>
        </form>
      )}
    </div>
  );
}

export function ServicesPage() {
  const qc = useQueryClient();
  const writable = can.writeBusiness(tokenStore.user());
  const [nodeId, setNodeId] = useNodeScope();
  const [editing, setEditing] = useState<Service | null | "new">(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [banner, setBanner] = useState("");
  const dep = useDependencyBlock();

  const { data, isPending, error } = useQuery({
    queryKey: ["services", nodeId],
    queryFn: () => servicesApi.list({ node_id: nodeId, page_size: 100 }),
    enabled: !!nodeId,
  });
  const toggle = useMutation({
    mutationFn: ({ svc, enabled }: { svc: Service; enabled: boolean }) =>
      enabled ? servicesApi.enable(svc.id, svc.row_version) : servicesApi.disable(svc.id, svc.row_version),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["services"] }),
    onError: (e) => setBanner(humanError(e).text),
  });
  const remove = useMutation({
    mutationFn: (id: string) => servicesApi.remove(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["services"] }),
    onError: (e) => {
      if (!dep.openIfBlocked(e)) setBanner(humanError(e).text);
    },
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">服务</h1>
          <p className="text-sm text-muted-foreground">一组后端地址（Target）的转发池；路由把流量按路径交给服务。</p>
        </div>
        <div className="flex items-center gap-3">
          <NodeScope value={nodeId} onChange={setNodeId} />
          {writable && nodeId && <Button onClick={() => setEditing("new")}>新建服务</Button>}
        </div>
      </div>
      {banner && <Alert>{banner}</Alert>}
      {dep.dialog}
      {!nodeId ? (
        <Alert>请先在上方选择一台网关。</Alert>
      ) : isPending ? (
        <Spinner />
      ) : error ? (
        <Alert>{humanError(error).text}</Alert>
      ) : (
        <DataTable head={["服务名", "健康码", "探测", "状态", "操作"]}>
          {(data.items ?? []).map((s) => (
            <Fragment key={s.id}>
              <Tr>
                <Td className="font-medium">{s.name}</Td>
                <Td>{s.expected_codes}</Td>
                <Td>
                  {s.interval_sec}s / {s.timeout_sec}s
                </Td>
                <Td>
                  <StatusBadge status={s.enabled ? "enabled" : "disabled"} />
                </Td>
                <Td>
                  <div className="flex gap-1">
                    <Button size="sm" variant="ghost" onClick={() => setExpanded(expanded === s.id ? null : s.id)}>
                      {expanded === s.id ? "收起目标" : "管理目标"}
                    </Button>
                    {writable && (
                      <>
                        <Button size="sm" variant="outline" onClick={() => setEditing(s)}>
                          编辑
                        </Button>
                        <Button size="sm" variant="outline" onClick={() => toggle.mutate({ svc: s, enabled: !s.enabled })}>
                          {s.enabled ? "停用" : "启用"}
                        </Button>
                        <Button
                          size="sm"
                          variant="danger"
                          onClick={() => {
                            if (window.confirm(`删除服务「${s.name}」？被路由引用时将被阻止。`)) remove.mutate(s.id);
                          }}
                        >
                          删除
                        </Button>
                      </>
                    )}
                  </div>
                </Td>
              </Tr>
              {expanded === s.id && (
                <Tr>
                  <Td colSpan={5}>
                    <TargetsPanel service={s} />
                  </Td>
                </Tr>
              )}
            </Fragment>
          ))}
          {(data.items ?? []).length === 0 && (
            <Tr>
              <Td colSpan={5} className="py-8 text-center text-muted-foreground">
                该网关下尚无服务。
              </Td>
            </Tr>
          )}
        </DataTable>
      )}

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing === "new" ? "新建服务" : `编辑：${editing?.name ?? ""}`}
      >
        {editing !== null && (
          <ServiceForm service={editing === "new" ? null : editing} nodeId={nodeId} onClose={() => setEditing(null)} />
        )}
      </Modal>
    </div>
  );
}
