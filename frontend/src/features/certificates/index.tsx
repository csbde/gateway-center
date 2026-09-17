// SSL 证书管理中心（向 Nginx Proxy Manager 学习）。
// 集中管理 Let's Encrypt 自动证书与自有证书；提供实时解析校验、到期倒计时健康徽标、证书探测更新与依赖安全删除保护。
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import {
  AlertCircle,
  CheckCircle2,
  Download,
  Lock,
  Plus,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  Trash2,
  Upload,
} from "lucide-react";
import { certificatesApi, credentialsApi, domainsApi } from "@/api/resources";
import type { CertificateDetail, CertificateParseResult, SecretCredential } from "@/api/types";
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
import { NodeScope, fieldErrors, humanError, useDependencyBlock, useNodeScope } from "../common";

const customCertSchema = z.object({
  name: z.string().min(1, "请输入证书别名").max(64),
  cert_pem: z.string().min(1, "请粘贴证书 PEM 内容（-----BEGIN CERTIFICATE-----）"),
  key_pem: z.string().min(1, "请粘贴私钥 PEM 内容（-----BEGIN ... PRIVATE KEY-----）"),
});

const letsEncryptSchema = z.object({
  domain_names: z.string().min(1, "请输入域名（多个域名用空格或逗号分隔）"),
  challenge_type: z.enum(["http", "dns"]),
  cert_resolver: z.string().optional(),
  dns_credential_id: z.string().optional(),
  email: z.string().email("请输入合法的邮箱地址").optional().or(z.literal("")),
  agree_tos: z.boolean().refine((v) => v === true, "必须同意 Let's Encrypt 服务条款"),
});

function AddCertificateModal({
  nodeId,
  onClose,
}: {
  nodeId: string;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [tab, setTab] = useState<"custom" | "letsencrypt">("custom");
  const [serverErr, setServerErr] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState("");

  // 自有证书表单
  const customForm = useForm<z.infer<typeof customCertSchema>>({
    resolver: zodResolver(customCertSchema),
    defaultValues: { name: "", cert_pem: "", key_pem: "" },
  });

  // Let's Encrypt 申请表单
  const leForm = useForm<z.infer<typeof letsEncryptSchema>>({
    resolver: zodResolver(letsEncryptSchema),
    defaultValues: {
      domain_names: "",
      challenge_type: "http",
      cert_resolver: "default",
      dns_credential_id: "",
      email: "",
      agree_tos: false,
    },
  });

  // 凭证列表（供 DNS 验证选择）
  const { data: credsData } = useQuery({
    queryKey: ["credentials"],
    queryFn: () => credentialsApi.list({ page_size: 100 }),
  });
  const dnsCreds = (credsData?.items ?? []).filter((c: SecretCredential) => c.kind === "dns_provider");

  // 即时解析校验（向 NPM 学习：用户粘贴 PEM 后立即呈现证书详情与私钥匹配情况）
  const certPEM = customForm.watch("cert_pem");
  const keyPEM = customForm.watch("key_pem");
  const [parseResult, setParseResult] = useState<CertificateParseResult | null>(null);
  const [parsing, setParsing] = useState(false);

  useEffect(() => {
    const trimmedCert = certPEM?.trim();
    if (!trimmedCert || !trimmedCert.includes("BEGIN CERTIFICATE")) {
      setParseResult(null);
      return;
    }
    setParsing(true);
    const timer = setTimeout(() => {
      certificatesApi
        .parse({ cert_pem: trimmedCert, key_pem: keyPEM?.trim() })
        .then((res) => {
          setParseResult(res);
          // 自动填充证书名称
          if (!customForm.getValues("name") && res.subject) {
            customForm.setValue("name", res.subject);
          }
        })
        .catch(() => setParseResult(null))
        .finally(() => setParsing(false));
    }, 400);
    return () => clearTimeout(timer);
  }, [certPEM, keyPEM, customForm]);

  // 文件读取助手
  const handleFileRead = (e: React.ChangeEvent<HTMLInputElement>, field: "cert_pem" | "key_pem") => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (event) => {
      const content = event.target?.result as string;
      customForm.setValue(field, content);
    };
    reader.readAsText(file);
  };

  const onSubmitCustom = customForm.handleSubmit(async (v) => {
    setServerErr({});
    setBanner("");
    try {
      await certificatesApi.create({ ...v, node_id: nodeId });
      await qc.invalidateQueries({ queryKey: ["certificates"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  const onSubmitLE = leForm.handleSubmit(async (v) => {
    setServerErr({});
    setBanner("");
    try {
      const domNames = v.domain_names
        .split(/[,，\s]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      if (domNames.length === 0) {
        setBanner("请输入至少一个域名");
        return;
      }
      for (const dom of domNames) {
        const isWild = dom.startsWith("*.");
        if (isWild && v.challenge_type === "http") {
          setBanner(`泛域名 ${dom} 必须采用 DNS 验证，无法使用 HTTP 验证`);
          return;
        }
        await domainsApi.create({
          node_id: nodeId,
          name: dom,
          https_policy: v.challenge_type === "dns" ? "acme_dns" : "acme_http",
          cert_resolver_ref: v.cert_resolver || "default",
          dns_credential_id: v.challenge_type === "dns" ? v.dns_credential_id || null : null,
          expiry_warn_days: 30,
        });
      }
      await qc.invalidateQueries({ queryKey: ["certificates"] });
      await qc.invalidateQueries({ queryKey: ["domains"] });
      onClose();
    } catch (e) {
      setServerErr(fieldErrors(e));
      setBanner(humanError(e).text);
    }
  });

  const challengeType = leForm.watch("challenge_type");

  return (
    <Modal open={true} title="添加 SSL 证书" onClose={onClose}>
      <div className="flex border-b mb-4">
        <button
          type="button"
          onClick={() => setTab("custom")}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            tab === "custom"
              ? "border-primary text-primary"
              : "border-transparent text-muted-foreground hover:text-foreground"
          }`}
        >
          导入自有证书 (Custom)
        </button>
        <button
          type="button"
          onClick={() => setTab("letsencrypt")}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            tab === "letsencrypt"
              ? "border-primary text-primary"
              : "border-transparent text-muted-foreground hover:text-foreground"
          }`}
        >
          申请 Let's Encrypt 证书
        </button>
      </div>

      {banner && <Alert className="mb-4">{banner}</Alert>}

      {tab === "custom" ? (
        <form onSubmit={onSubmitCustom} className="space-y-4">
          <Field
            label="证书别名"
            htmlFor="cert-name"
            hint="用于识别此证书，如「主站泛域名证书」"
            error={customForm.formState.errors.name?.message ?? serverErr.name}
          >
            <Input id="cert-name" placeholder="example.com SSL" {...customForm.register("name")} />
          </Field>

          <Field
            label="证书内容（Certificate PEM / Fullchain）"
            htmlFor="cert-pem"
            hint="可直接粘贴或上传 .crt/.pem 证书文件"
            error={customForm.formState.errors.cert_pem?.message ?? serverErr.cert_pem}
          >
            <div className="relative">
              <textarea
                id="cert-pem"
                rows={4}
                spellCheck={false}
                className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
                placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
                {...customForm.register("cert_pem")}
              />
              <div className="mt-1 flex items-center justify-between text-xs text-muted-foreground">
                <label className="cursor-pointer text-primary hover:underline flex items-center gap-1">
                  <Upload className="h-3 w-3" /> 上传证书文件
                  <input type="file" accept=".crt,.pem,.cer" className="hidden" onChange={(e) => handleFileRead(e, "cert_pem")} />
                </label>
                {parsing && <span className="text-xs text-muted-foreground">正在解析证书...</span>}
              </div>
            </div>
          </Field>

          <Field
            label="私钥内容（Private Key PEM）"
            htmlFor="key-pem"
            hint="提交后 AES-256-GCM 强加密存储，永不明文回显"
            error={customForm.formState.errors.key_pem?.message ?? serverErr.key_pem}
          >
            <div className="relative">
              <textarea
                id="key-pem"
                rows={4}
                spellCheck={false}
                autoComplete="off"
                className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
                placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
                {...customForm.register("key_pem")}
              />
              <div className="mt-1 flex items-center justify-between text-xs text-muted-foreground">
                <label className="cursor-pointer text-primary hover:underline flex items-center gap-1">
                  <Upload className="h-3 w-3" /> 上传私钥文件
                  <input type="file" accept=".key,.pem" className="hidden" onChange={(e) => handleFileRead(e, "key_pem")} />
                </label>
              </div>
            </div>
          </Field>

          {/* 即时解析校验反馈 */}
          {parseResult && (
            <Card className="p-3 bg-muted/40 border text-xs space-y-1.5">
              <div className="font-semibold flex items-center gap-1.5">
                {parseResult.valid ? (
                  <>
                    <ShieldCheck className="h-4 w-4 text-emerald-500" />
                    <span>证书解析成功</span>
                  </>
                ) : (
                  <>
                    <ShieldAlert className="h-4 w-4 text-destructive" />
                    <span>证书解析异常：{parseResult.error_message}</span>
                  </>
                )}
              </div>
              {parseResult.valid && (
                <>
                  <div className="grid grid-cols-2 gap-2 text-muted-foreground pt-1">
                    <div>主域名：<span className="font-mono text-foreground">{parseResult.subject}</span></div>
                    <div>颁发机构：<span className="text-foreground">{parseResult.issuer}</span></div>
                    <div>剩余天数：<span className="font-medium text-foreground">{parseResult.days_remaining} 天</span></div>
                    <div>签名算法：<span className="text-foreground">{parseResult.signature_algorithm}</span></div>
                  </div>
                  {parseResult.sans && parseResult.sans.length > 0 && (
                    <div className="text-muted-foreground">
                      覆盖域名 (SANs)：
                      <div className="flex flex-wrap gap-1 mt-1">
                        {parseResult.sans.map((s, idx) => (
                          <Badge key={idx} tone="info">{s}</Badge>
                        ))}
                      </div>
                    </div>
                  )}
                  <div className="pt-1 flex items-center gap-1">
                    {parseResult.key_matches ? (
                      <span className="text-emerald-600 font-medium flex items-center gap-1">
                        <CheckCircle2 className="h-3.5 w-3.5" /> 私钥与证书完全匹配
                      </span>
                    ) : keyPEM.trim() ? (
                      <span className="text-destructive font-medium flex items-center gap-1">
                        <AlertCircle className="h-3.5 w-3.5" /> 私钥与证书不匹配！
                      </span>
                    ) : (
                      <span className="text-muted-foreground">尚未输入私钥</span>
                    )}
                  </div>
                </>
              )}
            </Card>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={onClose}>
              取消
            </Button>
            <Button type="submit" loading={customForm.formState.isSubmitting}>
              保存证书
            </Button>
          </div>
        </form>
      ) : (
        <form onSubmit={onSubmitLE} className="space-y-4">
          <Field
            label="域名"
            htmlFor="le-domains"
            hint="可输入单域名、多域名或泛域名（如 blog.example.com, *.example.com）"
            error={leForm.formState.errors.domain_names?.message ?? serverErr.domain_names}
          >
            <Input id="le-domains" placeholder="blog.example.com, *.example.com" {...leForm.register("domain_names")} />
          </Field>

          <Field label="验证方式 (Challenge Type)" htmlFor="le-challenge">
            <Select id="le-challenge" {...leForm.register("challenge_type")}>
              <option value="http">HTTP-01 验证（公网可直接访问的主机）</option>
              <option value="dns">DNS-01 验证（支持泛域名与内网主机）</option>
            </Select>
          </Field>

          {challengeType === "dns" && (
            <>
              <Field
                label="DNS 提供商凭证"
                htmlFor="le-dns-cred"
                hint="泛域名申请必须走 DNS 验证，需在平台配置 DNS Provider API 凭据"
                error={serverErr.dns_credential_id}
              >
                <Select id="le-dns-cred" {...leForm.register("dns_credential_id")}>
                  <option value="">-- 选择 DNS 凭据 --</option>
                  {dnsCreds.map((c: SecretCredential) => (
                    <option key={c.id} value={c.id}>
                      {c.name} ({c.provider}) [{c.fingerprint}]
                    </option>
                  ))}
                </Select>
              </Field>
              {dnsCreds.length === 0 && (
                <p className="text-xs text-amber-600">
                  当前尚未配置 DNS Provider 凭证，请前往「平台设置 → 凭证」添加 Cloudflare/阿里云/腾讯云等 DNS API 密钥。
                </p>
              )}
            </>
          )}

          <Field
            label="通知邮箱 (可选)"
            htmlFor="le-email"
            hint="用于接收 Let's Encrypt 证书到期与重要安全通知"
            error={leForm.formState.errors.email?.message}
          >
            <Input id="le-email" type="email" placeholder="admin@example.com" {...leForm.register("email")} />
          </Field>

          <div className="pt-2">
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox" className="rounded" {...leForm.register("agree_tos")} />
              <span>我同意 Let's Encrypt 服务条款 (Terms of Service)</span>
            </label>
            {leForm.formState.errors.agree_tos && (
              <p className="text-xs text-destructive mt-1">{leForm.formState.errors.agree_tos.message}</p>
            )}
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={onClose}>
              取消
            </Button>
            <Button type="submit" loading={leForm.formState.isSubmitting}>
              申请证书
            </Button>
          </div>
        </form>
      )}
    </Modal>
  );
}

function CertificateDetailModal({
  certId,
  onClose,
}: {
  certId: string;
  onClose: () => void;
}) {
  const { data: cert, isPending } = useQuery({
    queryKey: ["certificates", certId],
    queryFn: () => certificatesApi.get(certId),
  });

  const downloadPEM = () => {
    if (!cert?.cert_pem) return;
    const blob = new Blob([cert.cert_pem], { type: "application/x-pem-file" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${cert.name || "certificate"}.pem`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <Modal open={true} title="SSL 证书详情" onClose={onClose}>
      {isPending || !cert ? (
        <div className="flex justify-center p-8"><Spinner /></div>
      ) : (
        <div className="space-y-4 text-sm">
          <div className="grid grid-cols-2 gap-3 border-b pb-3">
            <div>
              <span className="text-muted-foreground block text-xs">证书名称</span>
              <span className="font-medium">{cert.name}</span>
            </div>
            <div>
              <span className="text-muted-foreground block text-xs">颁发机构 (Issuer)</span>
              <span>{cert.issuer || "未知"}</span>
            </div>
            <div>
              <span className="text-muted-foreground block text-xs">生效时间 (Not Before)</span>
              <span>{cert.not_before ? new Date(cert.not_before).toLocaleDateString() : "-"}</span>
            </div>
            <div>
              <span className="text-muted-foreground block text-xs">截止时间 (Not After)</span>
              <span>{cert.not_after ? new Date(cert.not_after).toLocaleDateString() : "-"}</span>
            </div>
            <div>
              <span className="text-muted-foreground block text-xs">状态</span>
              <span className="flex items-center gap-1.5 mt-0.5">
                {cert.status === "valid" && <Badge tone="success">有效 (剩余 {cert.days_remaining} 天)</Badge>}
                {cert.status === "expiring_soon" && <Badge tone="warning">即将到期 (剩余 {cert.days_remaining} 天)</Badge>}
                {cert.status === "expired" && <Badge tone="danger">已过期</Badge>}
                {cert.status === "missing" && <Badge tone="neutral">缺失</Badge>}
                {cert.status === "unknown" && <Badge tone="neutral">未知</Badge>}
              </span>
            </div>
            <div>
              <span className="text-muted-foreground block text-xs">来源</span>
              <Badge tone="info">{cert.source === "imported" ? "自有证书 (Custom)" : "Let's Encrypt (ACME)"}</Badge>
            </div>
          </div>

          <div>
            <span className="text-muted-foreground block text-xs mb-1">覆盖域名 (SANs)</span>
            <div className="flex flex-wrap gap-1">
              {cert.sans && cert.sans.length > 0 ? (
                cert.sans.map((s, idx) => (
                  <Badge key={idx} tone={s.startsWith("*.") ? "warning" : "info"}>
                    {s}
                  </Badge>
                ))
              ) : (
                <span className="text-xs text-muted-foreground">未检测到 SANs</span>
              )}
            </div>
          </div>

          <div>
            <span className="text-muted-foreground block text-xs mb-1">绑定的域名清单</span>
            {cert.associated_domains && cert.associated_domains.length > 0 ? (
              <ul className="divide-y border rounded-md">
                {cert.associated_domains.map((dom) => (
                  <li key={dom.id} className="p-2 text-xs flex items-center justify-between">
                    <span className="font-mono">{dom.name}</span>
                    <Badge tone="neutral">已绑定</Badge>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-xs text-muted-foreground">当前暂无域名绑定此证书（闲置中）</p>
            )}
          </div>

          {cert.cert_pem && (
            <div className="pt-2">
              <Button size="sm" variant="outline" onClick={downloadPEM} className="flex items-center gap-1.5">
                <Download className="h-3.5 w-3.5" /> 下载公钥证书 (.pem)
              </Button>
            </div>
          )}

          <div className="flex justify-end pt-2">
            <Button variant="outline" onClick={onClose}>关闭</Button>
          </div>
        </div>
      )}
    </Modal>
  );
}

export function CertificatesPage() {
  const qc = useQueryClient();
  const writable = can.writeBusiness(tokenStore.user());
  const [nodeId, setNodeId] = useNodeScope();
  const [showAdd, setShowAdd] = useState(false);
  const [viewCertId, setViewCertId] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<string>("");
  const [search, setSearch] = useState("");
  const [banner, setBanner] = useState("");
  const dep = useDependencyBlock();

  const { data, isPending, error } = useQuery({
    queryKey: ["certificates", nodeId, statusFilter, search],
    queryFn: () =>
      certificatesApi.list({
        node_id: nodeId,
        status: statusFilter,
        q: search,
        page_size: 100,
      }),
  });

  const probeMutation = useMutation({
    mutationFn: (id: string) => certificatesApi.probe(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["certificates"] }),
    onError: (e) => setBanner(humanError(e).text),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => certificatesApi.remove(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["certificates"] }),
    onError: (e) => {
      if (!dep.openIfBlocked(e)) setBanner(humanError(e).text);
    },
  });

  const certs = data?.items ?? [];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold flex items-center gap-2">
            <Lock className="h-5 w-5 text-primary" />
            SSL 证书管理 (SSL Certificates)
          </h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            集中管理网关的 SSL/TLS 证书资产。向 NPM 学习，支持申请 Let's Encrypt 证书、导入自有证书，并实时监控有效期倒计时。
          </p>
        </div>
        <div className="flex items-center gap-3">
          <NodeScope value={nodeId} onChange={setNodeId} />
          {writable && (
            <Button onClick={() => setShowAdd(true)} className="flex items-center gap-1.5">
              <Plus className="h-4 w-4" /> 添加 SSL 证书
            </Button>
          )}
        </div>
      </div>

      {banner && <Alert>{banner}</Alert>}
      {dep.dialog}

      {/* 筛选过滤工具条 */}
      <div className="flex items-center justify-between gap-4 bg-card p-3 rounded-lg border">
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant={statusFilter === "" ? "default" : "outline"}
            onClick={() => setStatusFilter("")}
          >
            全部 ({certs.length})
          </Button>
          <Button
            size="sm"
            variant={statusFilter === "valid" ? "default" : "outline"}
            onClick={() => setStatusFilter("valid")}
          >
            有效
          </Button>
          <Button
            size="sm"
            variant={statusFilter === "expiring_soon" ? "default" : "outline"}
            onClick={() => setStatusFilter("expiring_soon")}
          >
            即将到期
          </Button>
          <Button
            size="sm"
            variant={statusFilter === "expired" ? "default" : "outline"}
            onClick={() => setStatusFilter("expired")}
          >
            已过期
          </Button>
        </div>
        <div className="w-64">
          <Input
            placeholder="搜索证书名称 / 域名 / 机构..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
      </div>

      {/* 证书列表 */}
      {isPending ? (
        <div className="flex justify-center p-12"><Spinner /></div>
      ) : error ? (
        <Alert>{humanError(error).text}</Alert>
      ) : certs.length === 0 ? (
        <Card className="p-12 text-center text-muted-foreground">
          <Lock className="h-10 w-10 mx-auto mb-3 opacity-30" />
          <p className="text-base font-medium">暂无 SSL 证书</p>
          <p className="text-xs mt-1">点击右上角「添加 SSL 证书」导入自有证书或自动申请 Let's Encrypt 证书。</p>
        </Card>
      ) : (
        <DataTable
          head={["证书别名 / 域名", "覆盖域名 (SANs)", "颁发机构", "类型", "到期状态", "绑定域名数", "操作"]}
        >
          {certs.map((c: CertificateDetail) => (
            <Tr key={c.id}>
              <Td>
                <div className="font-medium text-foreground">{c.name}</div>
                <div className="text-xs text-muted-foreground font-mono">
                  {c.sans?.[0] ?? "-"}
                </div>
              </Td>
              <Td>
                <div className="flex flex-wrap gap-1 max-w-xs">
                  {c.sans?.slice(0, 3).map((s, idx) => (
                    <Badge key={idx} tone={s.startsWith("*.") ? "warning" : "info"}>
                      {s}
                    </Badge>
                  ))}
                  {c.sans && c.sans.length > 3 && (
                    <Badge tone="neutral">+{c.sans.length - 3}</Badge>
                  )}
                </div>
              </Td>
              <Td className="text-xs">{c.issuer || "未知"}</Td>
              <Td>
                <Badge tone="neutral">
                  {c.source === "imported" ? "自有证书 (Custom)" : "Let's Encrypt"}
                </Badge>
              </Td>
              <Td>
                {c.status === "valid" && (
                  <Badge tone="success" className="gap-1">
                    <CheckCircle2 className="h-3 w-3 text-emerald-500" />
                    剩余 {c.days_remaining} 天
                  </Badge>
                )}
                {c.status === "expiring_soon" && (
                  <Badge tone="warning" className="gap-1">
                    <AlertCircle className="h-3 w-3 text-amber-500" />
                    即将到期 ({c.days_remaining} 天)
                  </Badge>
                )}
                {c.status === "expired" && (
                  <Badge tone="danger" className="gap-1">
                    <ShieldAlert className="h-3 w-3 text-destructive" />
                    已过期
                  </Badge>
                )}
                {(c.status === "missing" || c.status === "unknown") && (
                  <Badge tone="neutral">未检测</Badge>
                )}
              </Td>
              <Td>
                <span className="text-xs text-muted-foreground">
                  {c.associated_domains && c.associated_domains.length > 0
                    ? `被 ${c.associated_domains.length} 个域名使用`
                    : "未绑定"}
                </span>
              </Td>
              <Td>
                <div className="flex items-center gap-1.5">
                  <Button size="sm" variant="ghost" onClick={() => setViewCertId(c.id)}>
                    详情
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    loading={probeMutation.isPending && probeMutation.variables === c.id}
                    onClick={() => probeMutation.mutate(c.id)}
                    title="重新探测 TLS 证书有效状态"
                  >
                    <RefreshCw className="h-3.5 w-3.5" />
                  </Button>
                  {writable && (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive hover:text-destructive"
                      loading={deleteMutation.isPending && deleteMutation.variables === c.id}
                      onClick={() => deleteMutation.mutate(c.id)}
                      title="删除证书"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  )}
                </div>
              </Td>
            </Tr>
          ))}
        </DataTable>
      )}

      {showAdd && <AddCertificateModal nodeId={nodeId} onClose={() => setShowAdd(false)} />}
      {viewCertId && <CertificateDetailModal certId={viewCertId} onClose={() => setViewCertId(null)} />}
    </div>
  );
}
