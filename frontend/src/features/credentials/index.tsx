// T069 · DNS/API 凭证管理页（FR-035）：CRUD + verify。
// 值永不出 API（仅 fingerprint 回显）；编辑即轮换（重新输入全部键值，指纹变更、绑定不变）；
// 删除被域名引用→409 DEPENDENCY_BLOCKED 展示引用清单。
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { credentialsApi } from "@/api/resources";
import { ApiError } from "@/api/client";
import type { CredentialVerifyResult, SecretCredential } from "@/api/types";
import { can, tokenStore } from "@/api/session";
import { Alert, Badge, Button, DataTable, Field, Input, Modal, Select, Spinner, Td, Tr } from "@/components/ui";
import { humanError } from "../common";

const KIND_LABEL: Record<SecretCredential["kind"], string> = {
  dns_provider: "DNS Provider",
  traefik_api: "Traefik API",
  other: "其他",
};

const PROVIDERS = [
  "cloudflare", "alidns", "tencentcloud", "dnspod", "route53",
  "godaddy", "namedotcom", "azure", "googlecloud", "digitalocean",
];

const credSchema = z.object({
  name: z.string().min(1, "名称必填").max(64, "名称 ≤64 字符"),
  kind: z.enum(["dns_provider", "traefik_api", "other"]),
  provider: z.string(),
});

type CredFormValues = z.infer<typeof credSchema>;

/** 键值对编辑器：data 永不可读回（加密），编辑即重新输入全部。 */
function DataRows({ rows, setRows }: { rows: { key: string; value: string }[]; setRows: (r: { key: string; value: string }[]) => void }) {
  const update = (i: number, field: "key" | "value", v: string) => {
    const next = rows.map((r, idx) => (idx === i ? { ...r, [field]: v } : r));
    setRows(next);
  };
  return (
    <div className="space-y-2">
      <div className="text-sm text-muted-foreground">凭证键值（写入即加密，永不回显）</div>
      {rows.map((r, i) => (
        <div key={i} className="flex gap-2">
          <Input
            className="flex-1"
            placeholder="键（如 token / api_key）"
            value={r.key}
            onChange={(e) => update(i, "key", e.target.value)}
            aria-label={`键 ${i + 1}`}
          />
          <Input
            className="flex-1"
            placeholder="值（明文，保存后不可读取）"
            value={r.value}
            onChange={(e) => update(i, "value", e.target.value)}
            aria-label={`值 ${i + 1}`}
          />
          <Button size="sm" variant="outline" type="button" onClick={() => setRows(rows.filter((_, idx) => idx !== i))}>
            ✕
          </Button>
        </div>
      ))}
      <Button size="sm" variant="outline" type="button" onClick={() => setRows([...rows, { key: "", value: "" }])}>
        + 添加键值
      </Button>
    </div>
  );
}

function CredentialForm({
  cred,
  onClose,
}: {
  cred: SecretCredential | null;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [rows, setRows] = useState([{ key: "", value: "" }]);
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [formErr, setFormErr] = useState("");

  const {
    register,
    handleSubmit,
    watch,
    formState: { errors },
  } = useForm<CredFormValues>({
    resolver: zodResolver(credSchema),
    defaultValues: cred
      ? { name: cred.name, kind: cred.kind, provider: cred.provider }
      : { name: "", kind: "dns_provider", provider: "cloudflare" },
  });
  const kind = watch("kind");

  const save = useMutation({
    mutationFn: async (v: CredFormValues) => {
      const data: Record<string, string> = {};
      for (const r of rows) {
        const k = r.key.trim();
        if (k) data[k] = r.value;
      }
      if (Object.keys(data).length === 0) throw new Error("至少需要一组键值");
      const body = { name: v.name, kind: v.kind, provider: v.provider, data };
      return cred
        ? credentialsApi.update(cred.id, { ...body, expected_version: cred.row_version })
        : credentialsApi.create(body);
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["credentials"] });
      onClose();
    },
    onError: (e) => {
      if (e instanceof ApiError) {
        setServerErr(
          Object.fromEntries((e.details ?? []).filter((d) => d.field).map((d) => [d.field!, [d.message, d.hint].filter(Boolean).join("；")])),
        );
        setFormErr(e.message);
      } else {
        setFormErr(e instanceof Error ? e.message : "保存失败");
      }
    },
  });

  return (
    <form
      onSubmit={handleSubmit((v) => save.mutate(v))}
      className="space-y-4"
      aria-label={cred ? "凭证轮换表单" : "凭证创建表单"}
    >
      {cred && (
        <Alert>
          编辑即轮换：重新输入全部键值后保存，加密内容与指纹更新，域名绑定关系不变。
          当前指纹：<span className="font-mono">{cred.fingerprint}</span>
        </Alert>
      )}
      <Field label="名称" htmlFor="cred-name" error={errors.name?.message ?? serverErr.name}>
        <Input id="cred-name" {...register("name")} placeholder="如 cloudflare-prod" />
      </Field>
      <Field label="类型" htmlFor="cred-kind" error={errors.kind?.message ?? serverErr.kind}>
        <Select id="cred-kind" {...register("kind")}>
          <option value="dns_provider">DNS Provider（ACME DNS 验证）</option>
          <option value="traefik_api">Traefik API 凭证</option>
          <option value="other">其他</option>
        </Select>
      </Field>
      {kind === "dns_provider" && (
        <Field label="Provider" htmlFor="cred-provider" error={serverErr.provider}>
          <Select id="cred-provider" {...register("provider")}>
            {PROVIDERS.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </Select>
        </Field>
      )}
      <DataRows rows={rows} setRows={setRows} />
      {formErr && <Alert>{formErr}</Alert>}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onClose}>
          取消
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "保存中…" : cred ? "保存轮换" : "创建"}
        </Button>
      </div>
    </form>
  );
}

export function CredentialsPage() {
  const qc = useQueryClient();
  const writable = can.writeBusiness(tokenStore.user());
  const [editing, setEditing] = useState<SecretCredential | null | "new">(null);
  const [banner, setBanner] = useState("");
  const [verifyResult, setVerifyResult] = useState<Record<string, CredentialVerifyResult>>({});

  const { data, isPending, error } = useQuery({
    queryKey: ["credentials"],
    queryFn: () => credentialsApi.list({ page_size: 100 }),
  });

  const verify = useMutation({
    mutationFn: (id: string) => credentialsApi.verify(id),
    onSuccess: (res, id) => setVerifyResult((p) => ({ ...p, [id]: res })),
    onError: (e) => setBanner(humanError(e).text),
  });

  const remove = useMutation({
    mutationFn: (id: string) => credentialsApi.remove(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["credentials"] }),
    onError: (e) => setBanner(humanError(e).text),
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">DNS 凭证</h1>
          <p className="text-sm text-muted-foreground">用于 ACME DNS 验证与 Traefik API 认证；值加密存储，仅指纹回显。</p>
        </div>
        {writable && <Button onClick={() => setEditing("new")}>新建凭证</Button>}
      </div>
      {banner && <Alert>{banner}</Alert>}
      {isPending ? (
        <Spinner />
      ) : error ? (
        <Alert>{humanError(error).text}</Alert>
      ) : (
        <DataTable head={["名称", "类型", "Provider", "指纹", "操作"]}>
          {(data.items ?? []).map((c) => (
            <Tr key={c.id}>
              <Td className="font-medium">{c.name}</Td>
              <Td>{KIND_LABEL[c.kind]}</Td>
              <Td>{c.provider || "—"}</Td>
              <Td>
                <span className="font-mono text-xs">{c.fingerprint}</span>
              </Td>
              <Td>
                <div className="flex flex-wrap items-center gap-1">
                  <Button size="sm" variant="outline" onClick={() => verify.mutate(c.id)} disabled={verify.isPending}>
                    验证
                  </Button>
                  {writable && (
                    <>
                      <Button size="sm" variant="outline" onClick={() => setEditing(c)}>
                        轮换
                      </Button>
                      <Button
                        size="sm"
                        variant="danger"
                        onClick={() => {
                          if (window.confirm(`删除凭证「${c.name}」？被域名引用时将被阻止。`)) remove.mutate(c.id);
                        }}
                      >
                        删除
                      </Button>
                    </>
                  )}
                  {verifyResult[c.id] && (
                    <Badge tone={verifyResult[c.id].ok ? "success" : "danger"}>
                      {verifyResult[c.id].ok ? "可用" : `不可用：${verifyResult[c.id].reason ?? ""}`}
                    </Badge>
                  )}
                </div>
              </Td>
            </Tr>
          ))}
          {(data.items ?? []).length === 0 && (
            <Tr>
              <Td colSpan={5} className="py-8 text-center text-muted-foreground">
                尚无凭证。新建后将获得 8 位指纹用于域名绑定。
              </Td>
            </Tr>
          )}
        </DataTable>
      )}
      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing === "new" ? "新建凭证" : `轮换：${editing?.name ?? ""}`}
        wide
      >
        {editing !== null && (
          <CredentialForm cred={editing === "new" ? null : editing} onClose={() => setEditing(null)} />
        )}
      </Modal>
    </div>
  );
}
