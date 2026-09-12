// T045 · 域名页（US1/AC-002）：域名 CRUD + HTTPS 策略；imported 策略走证书上传（私钥仅上行、永不回显）。
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { domainsApi } from "@/api/resources";
import { api } from "@/api/client";
import type { Domain } from "@/api/types";
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

const POLICY_LABEL: Record<Domain["https_policy"], string> = {
  off: "仅 HTTP",
  acme_http: "自动证书（HTTP 验证）",
  acme_dns: "自动证书（DNS 验证）",
  imported: "自有证书",
};

const domainSchema = z.object({
  name: z
    .string()
    .min(1, "请输入域名")
    .max(253)
    .regex(/^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$/i, "需为合法域名，泛域名以 *. 开头"),
  https_policy: z.enum(["off", "acme_http", "acme_dns", "imported"]),
  cert_resolver_ref: z.string().optional(),
  expiry_warn_days: z.coerce.number().int().min(1).max(365),
});

const certSchema = z.object({
  cert_pem: z.string().min(1, "请粘贴证书 PEM（-----BEGIN CERTIFICATE-----…）"),
  key_pem: z.string().min(1, "请粘贴私钥 PEM（-----BEGIN … PRIVATE KEY-----…）"),
});

function DomainForm({ domain, nodeId, onClose }: { domain: Domain | null; nodeId: string; onClose: () => void }) {
  const qc = useQueryClient();
  const {
    register,
    handleSubmit,
    watch,
    formState: { errors, isSubmitting },
  } = useForm<z.input<typeof domainSchema>>({
    resolver: zodResolver(domainSchema),
    defaultValues: domain
      ? {
          name: domain.name,
          https_policy: domain.https_policy,
          cert_resolver_ref: domain.cert_resolver_ref ?? "",
          expiry_warn_days: domain.expiry_warn_days,
        }
      : { name: "", https_policy: "off", cert_resolver_ref: "", expiry_warn_days: 21 },
  });
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");
  const policy = watch("https_policy");
  const wildcard = watch("name").startsWith("*.");

  const onSubmit = handleSubmit(async (v) => {
    setServerErr({});
    setBanner("");
    try {
      if (domain)
        await domainsApi.update(domain.id, { ...v, node_id: nodeId, expected_version: domain.row_version });
      else await domainsApi.create({ ...v, node_id: nodeId });
      await qc.invalidateQueries({ queryKey: ["domains"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  return (
    <form onSubmit={onSubmit} className="space-y-4" aria-label="域名表单">
      {banner && <Alert>{banner}</Alert>}
      <Field
        label="域名"
        htmlFor="dom-name"
        hint={wildcard ? "泛域名只能通过 DNS 验证签发证书" : undefined}
        error={errors.name?.message ?? serverErr.name}
      >
        <Input id="dom-name" placeholder="crm.example.com 或 *.example.com" {...register("name")} />
      </Field>
      <Field label="HTTPS 策略" htmlFor="dom-policy" error={errors.https_policy?.message ?? serverErr.https_policy}>
        <Select id="dom-policy" {...register("https_policy")}>
          {Object.entries(POLICY_LABEL).map(([k, label]) => (
            <option key={k} value={k}>
              {label}
            </option>
          ))}
        </Select>
      </Field>
      {policy === "acme_http" && (
        <p className="text-xs text-muted-foreground">
          网关将自动申请并续期证书（HTTP 验证）。泛域名不支持此方式，请改用 DNS 验证或自有证书。
        </p>
      )}
      {policy === "acme_dns" && (
        <>
          <Field
            label="证书签发服务名"
            htmlFor="dom-resolver"
            hint="网关静态配置中预置的解析器名称"
            error={serverErr.cert_resolver_ref ?? serverErr.dns_credential_id}
          >
            <Input id="dom-resolver" placeholder="默认签发服务" {...register("cert_resolver_ref")} />
          </Field>
          <Alert>
            <span className="normal-case">DNS 验证所需的云账号凭证由平台管理员在「平台设置 → 凭证」中托管；若未配置，保存会被拒绝。</span>
          </Alert>
        </>
      )}
      {policy === "imported" && (
        <p className="text-xs text-muted-foreground">保存后请在列表中点「上传证书」粘贴证书与私钥；私钥加密存储且永不回显。</p>
      )}
      <Field label="到期提前预警（天）" htmlFor="dom-warn" error={errors.expiry_warn_days?.message ?? serverErr.expiry_warn_days}>
        <Input id="dom-warn" type="number" {...register("expiry_warn_days")} />
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

function CertUploadForm({ domain, onClose }: { domain: Domain; onClose: () => void }) {
  const qc = useQueryClient();
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<z.input<typeof certSchema>>({ resolver: zodResolver(certSchema), defaultValues: { cert_pem: "", key_pem: "" } });
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");

  const onSubmit = handleSubmit(async (v) => {
    setServerErr({});
    setBanner("");
    try {
      await api.post("/certificates/import", { domain_id: domain.id, ...v, expected_version: domain.row_version });
      await qc.invalidateQueries({ queryKey: ["domains"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  return (
    <form onSubmit={onSubmit} className="space-y-4" aria-label="证书上传表单">
      {banner && <Alert>{banner}</Alert>}
      <Field label="证书内容（PEM）" htmlFor="cert-pem" error={errors.cert_pem?.message ?? serverErr.cert_pem}>
        <textarea
          id="cert-pem"
          rows={5}
          spellCheck={false}
          className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
          placeholder={"-----BEGIN CERTIFICATE-----\n…"}
          {...register("cert_pem")}
        />
      </Field>
      <Field
        label="私钥（PEM）"
        htmlFor="key-pem"
        hint="提交后 AES-256-GCM 加密存储，任何界面与日志均不再回显"
        error={errors.key_pem?.message ?? serverErr.key_pem}
      >
        <textarea
          id="key-pem"
          rows={5}
          spellCheck={false}
          autoComplete="off"
          className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
          placeholder={"-----BEGIN PRIVATE KEY-----\n…"}
          {...register("key_pem")}
        />
      </Field>
      <div className="flex justify-end gap-2 pt-2">
        <Button variant="outline" onClick={onClose}>
          取消
        </Button>
        <Button type="submit" loading={isSubmitting}>
          上传
        </Button>
      </div>
    </form>
  );
}

export function DomainsPage() {
  const qc = useQueryClient();
  const writable = can.writeBusiness(tokenStore.user());
  const [nodeId, setNodeId] = useNodeScope();
  const [editing, setEditing] = useState<Domain | null | "new">(null);
  const [certFor, setCertFor] = useState<Domain | null>(null);
  const [banner, setBanner] = useState("");

  const { data, isPending, error } = useQuery({
    queryKey: ["domains", nodeId],
    queryFn: () => domainsApi.list({ node_id: nodeId, page_size: 100 }),
    enabled: !!nodeId,
  });
  const toggle = useMutation({
    mutationFn: ({ d, enabled }: { d: Domain; enabled: boolean }) =>
      enabled ? domainsApi.enable(d.id, d.row_version) : domainsApi.disable(d.id, d.row_version),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["domains"] }),
    onError: (e) => setBanner(humanError(e).text),
  });
  const remove = useMutation({
    mutationFn: (id: string) => domainsApi.remove(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["domains"] }),
    onError: (e) => setBanner(humanError(e).text),
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">域名</h1>
          <p className="text-sm text-muted-foreground">每台网关下按域名收敛入口；HTTPS 策略决定证书来源。</p>
        </div>
        <div className="flex items-center gap-3">
          <NodeScope value={nodeId} onChange={setNodeId} />
          {writable && nodeId && <Button onClick={() => setEditing("new")}>新建域名</Button>}
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
        <DataTable head={["域名", "HTTPS 策略", "预警天数", "状态", "操作"]}>
          {(data.items ?? []).map((d) => (
            <Tr key={d.id}>
              <Td className="font-medium">
                {d.name}
                {d.is_wildcard && <Badge tone="info">泛域名</Badge>}
              </Td>
              <Td>{POLICY_LABEL[d.https_policy]}</Td>
              <Td>{d.expiry_warn_days} 天</Td>
              <Td>
                <StatusBadge status={d.enabled ? "enabled" : "disabled"} />
              </Td>
              <Td>
                <div className="flex gap-1">
                  {writable && (
                    <>
                      {d.https_policy === "imported" && (
                        <Button size="sm" variant="outline" onClick={() => setCertFor(d)}>
                          {d.imported_cert_id ? "更新证书" : "上传证书"}
                        </Button>
                      )}
                      <Button size="sm" variant="outline" onClick={() => setEditing(d)}>
                        编辑
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => toggle.mutate({ d, enabled: !d.enabled })}>
                        {d.enabled ? "停用" : "启用"}
                      </Button>
                      <Button
                        size="sm"
                        variant="danger"
                        onClick={() => {
                          if (window.confirm(`删除域名「${d.name}」？被路由引用时将被阻止。`)) remove.mutate(d.id);
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
              <Td colSpan={5} className="py-8 text-center text-muted-foreground">
                该网关下尚无域名。
              </Td>
            </Tr>
          )}
        </DataTable>
      )}

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing === "new" ? "新建域名" : `编辑：${editing?.name ?? ""}`}
      >
        {editing !== null && (
          <DomainForm domain={editing === "new" ? null : editing} nodeId={nodeId} onClose={() => setEditing(null)} />
        )}
      </Modal>
      <Modal open={certFor !== null} onClose={() => setCertFor(null)} title={`上传证书：${certFor?.name ?? ""}`} wide>
        {certFor && <CertUploadForm domain={certFor} onClose={() => setCertFor(null)} />}
      </Modal>
    </div>
  );
}
