// T043 · 网关节点页（US1/AC-001）：列表/新建/编辑/启停；状态徽章；删除守卫。
// 术语：本页允许出现"网关/Traefik API 地址"等运维词汇（节点即服务器，非业务用户页）。
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { nodesApi } from "@/api/resources";
import type { Node } from "@/api/types";
import { tokenStore, can } from "@/api/session";
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
import { ENV_LABEL, StatusBadge, fieldErrors, humanError } from "../common";

const schema = z.object({
  name: z.string().min(1, "请输入名称").max(64),
  base_url: z.string().regex(/^https?:\/\/.+/, "需为 http(s)://host:port"),
  deploy_root: z.string().regex(/^\//, "必须为绝对路径，如 /etc/traefik"),
  env_type: z.enum(["development", "test", "staging", "production"]),
  remark: z.string().max(200).optional(),
  api_auth: z.string().optional(),
});
type FormValues = z.input<typeof schema>;

function NodeForm({ node, onClose }: { node: Node | null; onClose: () => void }) {
  const qc = useQueryClient();
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: node
      ? { name: node.name, base_url: node.base_url, deploy_root: node.deploy_root, env_type: node.env_type, remark: node.remark }
      : { name: "", base_url: "http://localhost:8081", deploy_root: "/etc/traefik", env_type: "test", remark: "" },
  });
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState<string>("");

  const onSubmit = handleSubmit(async (v) => {
    setServerErr({});
    setBanner("");
    try {
      if (node) {
        await nodesApi.update(node.id, { ...v, expected_version: node.row_version });
      } else {
        await nodesApi.create(v);
      }
      await qc.invalidateQueries({ queryKey: ["nodes"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  return (
    <form onSubmit={onSubmit} className="space-y-4" aria-label={node ? "编辑网关" : "新建网关"}>
      {banner && <Alert>{banner}</Alert>}
      <Field label="名称" htmlFor="name" error={errors.name?.message ?? serverErr.name}>
        <Input id="name" {...register("name")} aria-invalid={!!(errors.name ?? serverErr.name)} />
      </Field>
      <Field
        label="Traefik API 地址"
        htmlFor="base_url"
        hint="用于读取运行状态与发布校验（只读 API），如 http://localhost:8081"
        error={errors.base_url?.message ?? serverErr.base_url}
      >
        <Input id="base_url" {...register("base_url")} aria-invalid={!!(errors.base_url ?? serverErr.base_url)} />
      </Field>
      <Field
        label="配置落盘根路径"
        htmlFor="deploy_root"
        hint="平台仅写该目录下 dynamic/ 子树（静态配置只读）"
        error={errors.deploy_root?.message ?? serverErr.deploy_root}
      >
        <Input id="deploy_root" {...register("deploy_root")} aria-invalid={!!(errors.deploy_root ?? serverErr.deploy_root)} />
      </Field>
      <Field label="环境类型" htmlFor="env_type" error={errors.env_type?.message ?? serverErr.env_type}>
        <Select id="env_type" {...register("env_type")}>
          {Object.entries(ENV_LABEL).map(([k, label]) => (
            <option key={k} value={k}>
              {label}
            </option>
          ))}
        </Select>
      </Field>
      <Field
        label={node ? "API 凭据（留空则不修改）" : "API 凭据（可选）"}
        htmlFor="api_auth"
        hint="user:pass 格式的只读凭据；保存后永不回显"
        error={serverErr.api_auth}
      >
        <Input id="api_auth" type="password" autoComplete="off" {...register("api_auth")} />
      </Field>
      <Field label="备注" htmlFor="remark" error={errors.remark?.message ?? serverErr.remark}>
        <Input id="remark" {...register("remark")} />
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

export function NodesPage() {
  const qc = useQueryClient();
  const user = tokenStore.user();
  const writable = can.writeBusiness(user);
  const { data, isPending, error } = useQuery({
    queryKey: ["nodes"],
    queryFn: () => nodesApi.list({ page_size: 100 }),
    refetchInterval: 30_000, // 与探测周期同量级，徽章保持新鲜（FR-002）
  });
  const [editing, setEditing] = useState<Node | null | "new">(null);
  const [banner, setBanner] = useState("");

  const toggle = useMutation({
    mutationFn: ({ node, enabled }: { node: Node; enabled: boolean }) =>
      enabled ? nodesApi.enable(node.id, node.row_version) : nodesApi.disable(node.id, node.row_version),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["nodes"] }),
    onError: (e) => setBanner(humanError(e).text),
  });
  const remove = useMutation({
    mutationFn: (id: string) => nodesApi.remove(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["nodes"] }),
    onError: (e) => setBanner(humanError(e).text),
  });
  const stateOf = useMutation({ mutationFn: (id: string) => nodesApi.state(id) });
  const [probe, setProbe] = useState<{ id: string; text: string } | null>(null);

  if (isPending) return <Spinner />;
  if (error) return <Alert>{humanError(error).text}</Alert>;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">网关节点</h1>
          <p className="text-sm text-muted-foreground">纳管的 Traefik 实例：平台生成配置并原子下发，运行时逐路由器校验。</p>
        </div>
        {writable && <Button onClick={() => setEditing("new")}>新建网关</Button>}
      </div>
      {banner && <Alert>{banner}</Alert>}

      <DataTable head={["名称", "环境", "API 地址", "落盘根路径", "运行状态", "启用", "操作"]}>
        {(data.items ?? []).map((n) => (
          <Tr key={n.id}>
            <Td className="font-medium">
              {n.name}
              {n.has_api_auth && (
                <Badge tone="info">
                  <span aria-label="已配置 API 凭据">凭据 ****</span>
                </Badge>
              )}
            </Td>
            <Td>
              <Badge tone={n.env_type === "production" ? "danger" : "info"}>{ENV_LABEL[n.env_type]}</Badge>
            </Td>
            <Td className="font-mono text-xs">{n.base_url}</Td>
            <Td className="font-mono text-xs">{n.deploy_root}</Td>
            <Td>
              {probe?.id === n.id ? (
                probe.text
              ) : (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={async () => {
                    try {
                      const st = await stateOf.mutateAsync(n.id);
                      setProbe({
                        id: n.id,
                        text: `${st.status} · 期望 v${st.desired_version}/实际 v${st.actual_version}${st.drift ? " · 漂移!" : ""}`,
                      });
                    } catch (e) {
                      setProbe({ id: n.id, text: humanError(e).text });
                    }
                  }}
                >
                  查看状态
                </Button>
              )}
            </Td>
            <Td>
              <StatusBadge status={n.enabled ? "enabled" : "disabled"} />
            </Td>
            <Td>
              {writable && (
                <div className="flex gap-1">
                  <Button size="sm" variant="outline" onClick={() => setEditing(n)}>
                    编辑
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => toggle.mutate({ node: n, enabled: !n.enabled })}
                  >
                    {n.enabled ? "停用" : "启用"}
                  </Button>
                  <Button
                    size="sm"
                    variant="danger"
                    onClick={() => {
                      if (window.confirm(`删除网关「${n.name}」？存在部署记录时将被阻止（历史不可删）。`))
                        remove.mutate(n.id);
                    }}
                  >
                    删除
                  </Button>
                </div>
              )}
            </Td>
          </Tr>
        ))}
        {(data.items ?? []).length === 0 && (
          <Tr>
            <Td colSpan={7} className="py-8 text-center text-muted-foreground">
              尚无网关，先新建一台再走发布流程。
            </Td>
          </Tr>
        )}
      </DataTable>

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing === "new" ? "新建网关节点" : `编辑：${editing?.name ?? ""}`}
      >
        {editing !== null && <NodeForm node={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
      </Modal>
    </div>
  );
}
