// 网站代理（Proxy Hosts）管理中心（向 Nginx Proxy Manager 学习）。
// 提供一站式反向代理向导：统一配置域名、转发目标、SSL 证书、Websockets 与基础安全防护。
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import {
  ExternalLink,
  Globe,
  Lock,
  Plus,
  Server,
  Trash2,
  Unlock,
} from "lucide-react";
import { certificatesApi, credentialsApi, proxyHostsApi, servicesApi } from "@/api/resources";
import type { CertificateDetail, ProxyHostView, SecretCredential, Service } from "@/api/types";
import { can, tokenStore } from "@/api/session";
import {
  Alert,
  Badge,
  Button,
  Card,
  DataTable,
  Field,
  Input,
  Modal,
  Select,
  Spinner,
  Td,
  Tr,
} from "@/components/ui";
import { NodeScope, StatusBadge, fieldErrors, humanError, useDependencyBlock, useNodeScope } from "../common";

const proxyHostSchema = z.object({
  domain_names: z.string().min(1, "请输入域名（如 blog.example.com）"),
  use_existing_service: z.boolean(),
  service_id: z.string().optional(),
  forward_scheme: z.enum(["http", "https"]),
  forward_host: z.string().optional(),
  forward_port: z.coerce.number().int().min(1).max(65535).optional(),
  websockets: z.boolean(),
  block_common_exploits: z.boolean(),
  hsts: z.boolean(),
  force_ssl: z.boolean(),
  ssl_mode: z.enum(["none", "letsencrypt_http", "letsencrypt_dns", "custom", "upload"]),
  certificate_id: z.string().optional(),
  cert_resolver: z.string().optional(),
  dns_credential_id: z.string().optional(),
  cert_pem: z.string().optional(),
  key_pem: z.string().optional(),
  advanced_rule: z.string().optional(),
});

type ProxyHostFormValues = z.infer<typeof proxyHostSchema>;

function ProxyHostModal({
  host,
  nodeId,
  onClose,
}: {
  host: ProxyHostView | null;
  nodeId: string;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [activeTab, setActiveTab] = useState<"details" | "ssl" | "advanced">("details");
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");

  const { data: servicesData } = useQuery({
    queryKey: ["services", nodeId],
    queryFn: () => servicesApi.list({ node_id: nodeId, page_size: 100 }),
  });
  const services = servicesData?.items ?? [];

  const { data: certsData } = useQuery({
    queryKey: ["certificates", nodeId],
    queryFn: () => certificatesApi.list({ node_id: nodeId, page_size: 100 }),
  });
  const certs = certsData?.items ?? [];

  const { data: credsData } = useQuery({
    queryKey: ["credentials"],
    queryFn: () => credentialsApi.list({ page_size: 100 }),
  });
  const dnsCreds = (credsData?.items ?? []).filter((c: SecretCredential) => c.kind === "dns_provider");

  let initialSSLMode: "none" | "letsencrypt_http" | "letsencrypt_dns" | "custom" = "none";
  if (host?.ssl_policy === "acme_http") initialSSLMode = "letsencrypt_http";
  else if (host?.ssl_policy === "acme_dns") initialSSLMode = "letsencrypt_dns";
  else if (host?.ssl_policy === "imported") initialSSLMode = "custom";

  const form = useForm<ProxyHostFormValues>({
    resolver: zodResolver(proxyHostSchema),
    defaultValues: host
      ? {
          domain_names: host.domain_names?.join(", ") ?? "",
          use_existing_service: false,
          service_id: host.service_id,
          forward_scheme: (host.forward_scheme as "http" | "https") || "http",
          forward_host: host.forward_host,
          forward_port: host.forward_port || 80,
          websockets: host.websockets,
          block_common_exploits: host.block_common_exploits,
          hsts: host.hsts,
          force_ssl: host.force_ssl,
          ssl_mode: initialSSLMode,
          certificate_id: host.certificate_id ?? "",
          cert_resolver: "default",
          dns_credential_id: "",
          cert_pem: "",
          key_pem: "",
        }
      : {
          domain_names: "",
          use_existing_service: false,
          service_id: "",
          forward_scheme: "http",
          forward_host: "127.0.0.1",
          forward_port: 8080,
          websockets: true,
          block_common_exploits: true,
          hsts: false,
          force_ssl: false,
          ssl_mode: "none",
          certificate_id: "",
          cert_resolver: "default",
          dns_credential_id: "",
          cert_pem: "",
          key_pem: "",
        },
  });

  const useExisting = form.watch("use_existing_service");
  const sslMode = form.watch("ssl_mode");

  const onSubmit = form.handleSubmit(async (vals) => {
    setServerErr({});
    setBanner("");

    const domains = vals.domain_names
      .split(/[,，\s]+/)
      .map((s) => s.trim())
      .filter(Boolean);

    if (domains.length === 0) {
      setBanner("请输入至少一个域名");
      return;
    }

    const payload: Record<string, unknown> = {
      node_id: nodeId,
      domain_names: domains,
      forward_scheme: vals.forward_scheme,
      websockets: vals.websockets,
      block_common_exploits: vals.block_common_exploits,
      hsts: vals.hsts,
      force_ssl: vals.force_ssl,
      ssl_mode: vals.ssl_mode,
      certificate_id: vals.certificate_id,
      cert_resolver: vals.cert_resolver,
      dns_credential_id: vals.dns_credential_id,
      cert_pem: vals.cert_pem,
      key_pem: vals.key_pem,
      advanced_rule: vals.advanced_rule,
    };

    if (vals.use_existing_service && vals.service_id) {
      payload.service_id = vals.service_id;
    } else {
      payload.forward_host = vals.forward_host;
      payload.forward_port = vals.forward_port;
    }

    try {
      if (host) {
        await proxyHostsApi.update(host.id, payload);
      } else {
        await proxyHostsApi.create(payload);
      }
      await qc.invalidateQueries({ queryKey: ["proxy-hosts"] });
      await qc.invalidateQueries({ queryKey: ["domains"] });
      await qc.invalidateQueries({ queryKey: ["routes"] });
      await qc.invalidateQueries({ queryKey: ["services"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  return (
    <Modal open={true} title={host ? "编辑网站代理" : "新建网站代理 (Proxy Host)"} onClose={onClose}>
      <div className="flex border-b mb-4">
        <button
          type="button"
          onClick={() => setActiveTab("details")}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            activeTab === "details"
              ? "border-primary text-primary"
              : "border-transparent text-muted-foreground hover:text-foreground"
          }`}
        >
          基本配置 (Details)
        </button>
        <button
          type="button"
          onClick={() => setActiveTab("ssl")}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors flex items-center gap-1.5 ${
            activeTab === "ssl"
              ? "border-primary text-primary"
              : "border-transparent text-muted-foreground hover:text-foreground"
          }`}
        >
          <Lock className="h-3.5 w-3.5" />
          SSL 证书 (SSL)
        </button>
        <button
          type="button"
          onClick={() => setActiveTab("advanced")}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            activeTab === "advanced"
              ? "border-primary text-primary"
              : "border-transparent text-muted-foreground hover:text-foreground"
          }`}
        >
          高级设置 (Advanced)
        </button>
      </div>

      {banner && <Alert className="mb-4">{banner}</Alert>}

      <form onSubmit={onSubmit} className="space-y-4">
        {activeTab === "details" && (
          <div className="space-y-4">
            <Field
              label="域名 (Domain Names)"
              htmlFor="ph-domains"
              hint="支持单个或多个域名，用空格或逗号分隔，如 blog.example.com, www.example.com"
              error={form.formState.errors.domain_names?.message ?? serverErr.domain_names}
            >
              <Input
                id="ph-domains"
                placeholder="blog.example.com"
                {...form.register("domain_names")}
              />
            </Field>

            <div className="border rounded-lg p-3 bg-muted/20 space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-semibold uppercase text-muted-foreground">
                  转发目标 (Forward Target)
                </span>
                <label className="flex items-center gap-1.5 text-xs cursor-pointer">
                  <input
                    type="checkbox"
                    className="rounded"
                    {...form.register("use_existing_service")}
                  />
                  <span>选用已有服务</span>
                </label>
              </div>

              {useExisting ? (
                <Field
                  label="选择已有服务"
                  htmlFor="ph-service"
                  error={serverErr.service_id}
                >
                  <Select id="ph-service" {...form.register("service_id")}>
                    <option value="">-- 请选择服务 --</option>
                    {services.map((s: Service) => (
                      <option key={s.id} value={s.id}>
                        {s.name} ({s.description || "无描述"})
                      </option>
                    ))}
                  </Select>
                </Field>
              ) : (
                <div className="grid grid-cols-12 gap-2 items-start">
                  <div className="col-span-3">
                    <Field label="协议" htmlFor="ph-scheme">
                      <Select id="ph-scheme" {...form.register("forward_scheme")}>
                        <option value="http">http://</option>
                        <option value="https">https://</option>
                      </Select>
                    </Field>
                  </div>
                  <div className="col-span-6">
                    <Field
                      label="转发主机 / IP"
                      htmlFor="ph-host"
                      error={serverErr.forward_host}
                    >
                      <Input
                        id="ph-host"
                        placeholder="192.168.1.10 或 sample-crm"
                        {...form.register("forward_host")}
                      />
                    </Field>
                  </div>
                  <div className="col-span-3">
                    <Field
                      label="端口"
                      htmlFor="ph-port"
                      error={serverErr.forward_port}
                    >
                      <Input
                        id="ph-port"
                        type="number"
                        placeholder="8080"
                        {...form.register("forward_port")}
                      />
                    </Field>
                  </div>
                </div>
              )}
            </div>

            {/* 特性开关 */}
            <div className="border rounded-lg p-3 bg-muted/20 space-y-2.5">
              <span className="text-xs font-semibold uppercase text-muted-foreground block">
                常用特性开关 (向 NPM 学习)
              </span>
              <div className="grid grid-cols-2 gap-3 text-sm">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    className="rounded"
                    {...form.register("websockets")}
                  />
                  <span>Websockets 支持</span>
                </label>
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    className="rounded"
                    {...form.register("block_common_exploits")}
                  />
                  <span>常见攻击防御 (Block Exploits)</span>
                </label>
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    className="rounded"
                    {...form.register("force_ssl")}
                  />
                  <span>强制跳转 HTTPS (Force SSL)</span>
                </label>
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    className="rounded"
                    {...form.register("hsts")}
                  />
                  <span>开启 HSTS 安全标头</span>
                </label>
              </div>
            </div>
          </div>
        )}

        {activeTab === "ssl" && (
          <div className="space-y-4">
            <Field label="SSL 证书选择" htmlFor="ph-ssl-mode">
              <Select id="ph-ssl-mode" {...form.register("ssl_mode")}>
                <option value="none">无 (仅 HTTP 明文访问)</option>
                <option value="custom">选择已有证书 (从证书库选择)</option>
                <option value="letsencrypt_http">申请 Let's Encrypt 证书 (HTTP-01 验证)</option>
                <option value="letsencrypt_dns">申请 Let's Encrypt 证书 (DNS-01 验证 · 泛域名/内网)</option>
                <option value="upload">现场上传新的自有证书</option>
              </Select>
            </Field>

            {sslMode === "custom" && (
              <Field
                label="选择系统中的证书"
                htmlFor="ph-cert-id"
                hint="可从「SSL 证书管理」导入的证书或自动申请的证书中选择"
                error={serverErr.certificate_id}
              >
                <Select id="ph-cert-id" {...form.register("certificate_id")}>
                  <option value="">-- 请选择证书 --</option>
                  {certs.map((c: CertificateDetail) => (
                    <option key={c.id} value={c.id}>
                      {c.name} ({c.sans?.slice(0, 2).join(", ")}) [剩余 {c.days_remaining} 天]
                    </option>
                  ))}
                </Select>
              </Field>
            )}

            {sslMode === "letsencrypt_dns" && (
              <Field
                label="DNS 提供商凭据"
                htmlFor="ph-dns-cred"
                hint="泛域名申请必须走 DNS 验证"
                error={serverErr.dns_credential_id}
              >
                <Select id="ph-dns-cred" {...form.register("dns_credential_id")}>
                  <option value="">-- 选择 DNS 凭据 --</option>
                  {dnsCreds.map((c: SecretCredential) => (
                    <option key={c.id} value={c.id}>
                      {c.name} ({c.provider})
                    </option>
                  ))}
                </Select>
              </Field>
            )}

            {sslMode === "upload" && (
              <div className="space-y-3 border p-3 rounded-md bg-muted/20">
                <Field label="证书内容 (PEM)" htmlFor="ph-cert-pem" error={serverErr.cert_pem}>
                  <textarea
                    id="ph-cert-pem"
                    rows={3}
                    className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
                    placeholder="-----BEGIN CERTIFICATE-----..."
                    {...form.register("cert_pem")}
                  />
                </Field>
                <Field label="私钥内容 (PEM)" htmlFor="ph-key-pem" error={serverErr.key_pem}>
                  <textarea
                    id="ph-key-pem"
                    rows={3}
                    className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
                    placeholder="-----BEGIN PRIVATE KEY-----..."
                    {...form.register("key_pem")}
                  />
                </Field>
              </div>
            )}

            {sslMode !== "none" && (
              <div className="border rounded-md p-3 bg-muted/30 space-y-2">
                <label className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox"
                    className="rounded"
                    {...form.register("force_ssl")}
                  />
                  <span className="font-medium">强制 SSL (Force SSL)</span>
                </label>
                <p className="text-xs text-muted-foreground pl-5">
                  所有访问 HTTP (80 端口) 的请求将自动通过 301 重定向至 HTTPS (443 端口)。
                </p>

                <label className="flex items-center gap-2 text-sm cursor-pointer pt-2">
                  <input
                    type="checkbox"
                    className="rounded"
                    {...form.register("hsts")}
                  />
                  <span className="font-medium">启用 HSTS (Strict-Transport-Security)</span>
                </label>
                <p className="text-xs text-muted-foreground pl-5">
                  通知浏览器在未来 1 年内强制使用 HTTPS 连接，防御降级劫持。
                </p>
              </div>
            )}
          </div>
        )}

        {activeTab === "advanced" && (
          <div className="space-y-4">
            <Field
              label="自定义匹配规则 (可选)"
              htmlFor="ph-adv-rule"
              hint="留空则按域名默认前缀匹配；高级管理员可手写 Traefik 规则"
            >
              <textarea
                id="ph-adv-rule"
                rows={3}
                className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
                placeholder="Host(`blog.example.com`) && PathPrefix(`/`)"
                {...form.register("advanced_rule")}
              />
            </Field>
          </div>
        )}

        <div className="flex justify-end gap-2 pt-3 border-t">
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" loading={form.formState.isSubmitting}>
            保存代理
          </Button>
        </div>
      </form>
    </Modal>
  );
}

export function ProxyHostsPage() {
  const qc = useQueryClient();
  const writable = can.writeBusiness(tokenStore.user());
  const [nodeId, setNodeId] = useNodeScope();
  const [editing, setEditing] = useState<ProxyHostView | null | "new">(null);
  const [search, setSearch] = useState("");
  const [banner, setBanner] = useState("");
  const dep = useDependencyBlock();

  const { data, isPending, error } = useQuery({
    queryKey: ["proxy-hosts", nodeId, search],
    queryFn: () => proxyHostsApi.list({ node_id: nodeId, q: search, page_size: 100 }),
  });

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      enabled ? proxyHostsApi.enable(id) : proxyHostsApi.disable(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["proxy-hosts"] }),
    onError: (e) => setBanner(humanError(e).text),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => proxyHostsApi.remove(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["proxy-hosts"] }),
    onError: (e) => {
      if (!dep.openIfBlocked(e)) setBanner(humanError(e).text);
    },
  });

  const hosts = data?.items ?? [];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold flex items-center gap-2">
            <Globe className="h-5 w-5 text-primary" />
            网站代理 (Proxy Hosts)
          </h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            向 Nginx Proxy Manager 学习：一站式管理网站域名与反向代理。一键配置目标、SSL 证书与安全防护。
          </p>
        </div>
        <div className="flex items-center gap-3">
          <NodeScope value={nodeId} onChange={setNodeId} />
          {writable && (
            <Button onClick={() => setEditing("new")} className="flex items-center gap-1.5">
              <Plus className="h-4 w-4" /> 新建网站代理
            </Button>
          )}
        </div>
      </div>

      {banner && <Alert>{banner}</Alert>}
      {dep.dialog}

      {/* 搜索工具栏 */}
      <div className="flex items-center justify-between gap-4 bg-card p-3 rounded-lg border">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <span>共 {hosts.length} 个网站代理</span>
        </div>
        <div className="w-72">
          <Input
            placeholder="搜索域名或上游目标..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
      </div>

      {/* 列表呈现 */}
      {isPending ? (
        <div className="flex justify-center p-12"><Spinner /></div>
      ) : error ? (
        <Alert>{humanError(error).text}</Alert>
      ) : hosts.length === 0 ? (
        <Card className="p-12 text-center text-muted-foreground">
          <Globe className="h-10 w-10 mx-auto mb-3 opacity-30" />
          <p className="text-base font-medium">暂无网站代理</p>
          <p className="text-xs mt-1">点击右上角「新建网站代理」，体验像 Nginx Proxy Manager 一样轻松接入网站。</p>
        </Card>
      ) : (
        <DataTable
          head={["域名 (Domains)", "转发目标 (Forward Target)", "SSL 证书", "特性标签", "状态", "操作"]}
        >
          {hosts.map((h: ProxyHostView) => {
            const primaryDomain = h.domain_names?.[0] ?? h.name;
            const visitUrl = `${h.force_ssl || h.ssl_policy !== "off" ? "https" : "http"}://${primaryDomain}`;
            return (
              <Tr key={h.id}>
                <Td>
                  <div className="flex items-center gap-1.5 font-medium text-foreground">
                    <a
                      href={visitUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="hover:underline flex items-center gap-1 text-primary"
                    >
                      {primaryDomain}
                      <ExternalLink className="h-3 w-3 opacity-60" />
                    </a>
                  </div>
                  {h.domain_names && h.domain_names.length > 1 && (
                    <div className="text-xs text-muted-foreground flex gap-1 mt-0.5">
                      {h.domain_names.slice(1).map((d, i) => (
                        <span key={i} className="font-mono">+{d}</span>
                      ))}
                    </div>
                  )}
                </Td>
                <Td>
                  <div className="font-mono text-xs flex items-center gap-1.5">
                    <Server className="h-3.5 w-3.5 text-muted-foreground" />
                    {h.target_url || `${h.forward_scheme}://${h.forward_host}:${h.forward_port}`}
                  </div>
                  {h.service_name && (
                    <span className="text-xs text-muted-foreground block mt-0.5">
                      服务：{h.service_name}
                    </span>
                  )}
                </Td>
                <Td>
                  {h.ssl_policy === "off" ? (
                    <Badge tone="neutral" className="gap-1">
                      <Unlock className="h-3 w-3 opacity-60" /> 仅 HTTP
                    </Badge>
                  ) : (
                    <div className="space-y-0.5">
                      <Badge tone="success" className="gap-1">
                        <Lock className="h-3 w-3 text-emerald-600" />
                        {h.ssl_policy === "imported"
                          ? "自有证书"
                          : h.ssl_policy === "acme_dns"
                          ? "Let's Encrypt (DNS)"
                          : "Let's Encrypt (HTTP)"}
                      </Badge>
                      {h.cert_days_left !== undefined && h.cert_days_left > 0 && (
                        <div className="text-xs text-muted-foreground">
                          剩余 {h.cert_days_left} 天
                        </div>
                      )}
                    </div>
                  )}
                </Td>
                <Td>
                  <div className="flex flex-wrap gap-1">
                    {h.force_ssl && <Badge tone="info">Force SSL</Badge>}
                    {h.websockets && <Badge tone="neutral">Websockets</Badge>}
                    {h.block_common_exploits && <Badge tone="neutral">防漏洞标头</Badge>}
                    {h.hsts && <Badge tone="info">HSTS</Badge>}
                  </div>
                </Td>
                <Td>
                  <StatusBadge status={h.status} />
                </Td>
                <Td>
                  <div className="flex items-center gap-1.5">
                    {writable && (
                      <>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setEditing(h)}
                        >
                          编辑
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          loading={toggleMutation.isPending && toggleMutation.variables?.id === h.id}
                          onClick={() => toggleMutation.mutate({ id: h.id, enabled: !h.enabled })}
                        >
                          {h.enabled ? "停用" : "启用"}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-destructive hover:text-destructive"
                          loading={deleteMutation.isPending && deleteMutation.variables === h.id}
                          onClick={() => deleteMutation.mutate(h.id)}
                          title="删除网站代理"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </>
                    )}
                  </div>
                </Td>
              </Tr>
            );
          })}
        </DataTable>
      )}

      {editing !== null && (
        <ProxyHostModal
          host={editing === "new" ? null : editing}
          nodeId={nodeId}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}
