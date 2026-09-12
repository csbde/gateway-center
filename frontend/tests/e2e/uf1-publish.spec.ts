// T051 · UF-1 发布主链路 E2E（quickstart V-1 步骤 1–9，SC-001 ≤15 分钟）。
// 前置（一次性手工/脚本准备，密钥不落仓库）：
//   1. cd deploy/compose && docker compose up -d          # postgres + traefik + sample-crm
//   2. cd backend && make build && bin/gateway-center migrate && \
//      GC_MASTER_KEY=$(openssl rand -hex 32) GC_INITIAL_ADMIN_PASSWORD=... bin/gateway-center migrate-seed && \
//      GC_MASTER_KEY=... bin/gateway-center serve          # :8080
//   3. cd frontend && npm run dev                          # :5173（代理 /api → :8080）
//   4. E2E_ADMIN_USER / E2E_ADMIN_PASSWORD 环境变量与种子一致；
//      E2E_DEPLOY_ROOT 默认 deploy/compose（其 dynamic/ 与 traefik 容器 bind mount 共享，
//      POSIX 同文件系统 rename——宪法 IV 前置）。
import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, statSync } from "node:fs";
import { tmpdir } from "node:os";

const ADMIN_USER = process.env.E2E_ADMIN_USER ?? "admin";
const ADMIN_PASS = process.env.E2E_ADMIN_PASSWORD ?? "ChangeMe-Strong-1";
const STAMP = Date.now();
const NODE_NAME = `e2e-gw-${STAMP}`;
const SVC_NAME = `crm-api-${STAMP}`;
const DOMAIN = `crm-${STAMP}.example.com`;
const ROUTE_NAME = `crm-main-${STAMP}`;
const DEPLOY_ROOT = process.env.E2E_DEPLOY_ROOT ?? new URL("../../../deploy/compose", import.meta.url).pathname;
// 参考拓扑默认 8081；本机端口被占时用 deploy/compose/docker-compose.e2e.yml 覆盖为 8091。
const TRAEFIK_API = process.env.E2E_TRAEFIK_API ?? "http://localhost:8081";

// 自签证书（仅测试用）：openssl 现场生成于 tmpdir，绝不入库。
function selfSignedCN(cn: string): { cert: string; key: string } {
  const dir = mkdtempSync(`${tmpdir()}/gc-e2e-`);
  execFileSync(
    "openssl",
    [
      "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
      "-subj", `/CN=${cn}`, "-addext", `subjectAltName=DNS:${cn}`,
      "-keyout", `${dir}/k.pem`, "-out", `${dir}/c.pem`,
    ],
    { stdio: "ignore" },
  );
  return { cert: readFileSync(`${dir}/c.pem`, "utf8"), key: readFileSync(`${dir}/k.pem`, "utf8") };
}

/** 侧栏链接（限定 navigation，避免与概览页内联链接重名冲突）。 */
const navLink = (page: import("@playwright/test").Page, name: string | RegExp) =>
  page.getByRole("navigation").getByRole("link", { name });

async function gotoSection(page: import("@playwright/test").Page, name: string) {
  await navLink(page, name).click();
}

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login");
  await page.getByLabel(/用户名/).fill(ADMIN_USER);
  await page.getByLabel(/密码/).fill(ADMIN_PASS);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(navLink(page, "网关节点")).toBeVisible();
}

/** 选「当前网关」作用域（NodeScope 是 <select aria-label=当前网关>，option 文本含环境后缀）。 */
async function pickScope(page: import("@playwright/test").Page, nodeLabel: string | RegExp) {
  const scope = page.getByLabel("当前网关");
  await expect(scope).toBeVisible();
  if (nodeLabel instanceof RegExp) await scope.selectOption(nodeLabel);
  else await scope.selectOption({ label: nodeLabel });
}

test("UF-1 smoke：登录并看到主导航", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel(/用户名/).fill(ADMIN_USER);
  await page.getByLabel(/密码/).fill(ADMIN_PASS);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(navLink(page, "网关节点")).toBeVisible();
  await expect(navLink(page, "路由规则")).toBeVisible();
});

test("UF-1 全链（节点→服务→域名→路由→发布→运行时校验）≤15 分钟", async ({ page }) => {
  test.setTimeout(16 * 60 * 1000); // SC-001：15 分钟预算 + 1 分钟调度余量
  const t0 = Date.now();

  await login(page);

  // ① 创建网关节点（AC-001）
  await gotoSection(page, "网关节点");
  await page.getByRole("button", { name: "新建网关" }).click();
  const nodeForm = page.getByRole("form", { name: "新建网关" });
  await nodeForm.getByLabel("名称").fill(NODE_NAME);
  await nodeForm.getByLabel(/Traefik API 地址/).fill(TRAEFIK_API);
  await nodeForm.getByLabel(/落盘根路径/).fill(DEPLOY_ROOT);
  await nodeForm.getByLabel("环境类型").selectOption("test");
  await nodeForm.getByRole("button", { name: "保存" }).click();
  await expect(page.getByRole("row").filter({ hasText: NODE_NAME })).toBeVisible();
  const scopeLabel = `${NODE_NAME}（测试）`;

  // ② 服务 + 3 个带权重 Target（AC-003/UF-2）
  await gotoSection(page, "服务");
  await pickScope(page, scopeLabel);
  await page.getByRole("button", { name: "新建服务" }).click();
  const svcForm = page.getByRole("form", { name: "服务表单" });
  await svcForm.getByLabel("服务名").fill(SVC_NAME);
  await svcForm.getByRole("button", { name: "保存" }).click();
  const svcRow = page.getByRole("row").filter({ hasText: SVC_NAME });
  await expect(svcRow).toBeVisible();
  await svcRow.getByRole("button", { name: "管理目标" }).click();
  for (const [i, w] of [3, 2, 1].entries()) {
    await page.getByLabel("目标地址").fill(`http://sample-crm/n${i}`); // 归一化后彼此不同（UF-2 允许）
    await page.getByLabel("权重").fill(String(w));
    await page.getByRole("button", { name: "添加" }).click();
  }
  await expect(page.getByText(`http://sample-crm/n2`)).toBeVisible();

  // ③ 域名 imported + 上传自有证书（AC-002；私钥永不回显）
  await gotoSection(page, "域名");
  await pickScope(page, scopeLabel);
  await page.getByRole("button", { name: "新建域名" }).click();
  const domForm = page.getByRole("form", { name: "域名表单" });
  await domForm.getByLabel("域名").fill(DOMAIN);
  await domForm.getByLabel("HTTPS 策略").selectOption("imported");
  await domForm.getByRole("button", { name: "保存" }).click();
  const domRow = page.getByRole("row").filter({ hasText: DOMAIN });
  await expect(domRow).toBeVisible();
  const pem = selfSignedCN(DOMAIN);
  await domRow.getByRole("button", { name: "上传证书" }).click();
  const certForm = page.getByRole("form", { name: "证书上传表单" });
  await certForm.getByLabel(/证书内容/).fill(pem.cert);
  await certForm.getByLabel(/私钥/).fill(pem.key);
  await certForm.getByRole("button", { name: "上传" }).click();
  // 上传成功 → 按钮变「更新证书」（证明 imported_cert_id 已关联）
  await expect(
    page.getByRole("row").filter({ hasText: DOMAIN }).getByRole("button", { name: "更新证书" }),
  ).toBeVisible({ timeout: 10_000 });

  // ④ 路由（简单模式 + 自动合成预览，零语法输入；AC-004/FR-014）
  await gotoSection(page, "路由规则");
  await pickScope(page, scopeLabel);
  await page.getByRole("button", { name: "新建规则" }).click();
  const rtForm = page.getByRole("form", { name: "路由规则表单" });
  await rtForm.getByLabel("规则名称").fill(ROUTE_NAME);
  await rtForm.getByLabel("域名").selectOption({ label: DOMAIN });
  await rtForm.getByLabel("路径").fill("/");
  await rtForm.getByLabel("匹配方式").selectOption("prefix");
  await rtForm.getByLabel("转发到服务").selectOption({ label: SVC_NAME });
  await rtForm.locator('input[type="checkbox"]').check();
  await expect(rtForm.getByText(/访问 .* → 转给/)).toBeVisible(); // 预览即人读规则
  await rtForm.getByRole("button", { name: "保存" }).click();
  const rtRow = page.getByRole("row").filter({ hasText: ROUTE_NAME });
  await expect(rtRow).toBeVisible();
  await rtRow.getByRole("button", { name: "启用" }).click(); // draft→enabled
  await expect(rtRow.getByText("启用中")).toBeVisible();

  // ⑤ 反向验证：禁用服务 → 检查报告出阻断项，向导不进第②步（AC-007/FR-012）
  await gotoSection(page, "服务");
  await pickScope(page, scopeLabel);
  await page.getByRole("row").filter({ hasText: SVC_NAME }).getByRole("button", { name: "停用" }).click();
  await gotoSection(page, "发布");
  await pickScope(page, scopeLabel);
  await page.getByRole("button", { name: "发起发布" }).click();
  const wizard = page.getByRole("group", { name: "发布向导" });
  await wizard.getByRole("button", { name: "开始检查" }).click();
  await expect(wizard.getByText("阻断")).toBeVisible();
  await expect(wizard.getByText(/绑定的服务 .* 已禁用/)).toBeVisible();
  await expect(wizard.getByRole("button", { name: "生成版本" })).toHaveCount(0);
  await page.getByRole("button", { name: "关闭" }).last().click(); // Dialog 关闭钮
  await gotoSection(page, "服务");
  await pickScope(page, scopeLabel);
  await page.getByRole("row").filter({ hasText: SVC_NAME }).getByRole("button", { name: "启用" }).click();

  // ⑥ 检查通过 → 生成版本（ready） → Diff → 未勾选确认时发布禁用（AC-008 UI 侧）
  await gotoSection(page, "发布");
  await pickScope(page, scopeLabel);
  await page.getByRole("button", { name: "发起发布" }).click();
  await wizard.getByRole("button", { name: "开始检查" }).click();
  await expect(wizard.getByText(/全部规则检查通过/)).toBeVisible();
  await wizard.getByRole("button", { name: "生成版本" }).click();
  await expect(wizard.getByText("待发布")).toBeVisible({ timeout: 30_000 });
  await wizard.getByRole("button", { name: "查看变更内容" }).click();
  const deployBtn = wizard.getByRole("button", { name: "发布", exact: true });
  await expect(deployBtn).toBeDisabled();
  await expect(wizard.getByText("请先勾选确认变更内容")).toBeVisible();
  // 服务端双重把关（confirmed=false → 422）已由契约测试 T049 固化，不在 UI 重复。

  // ⑦ 勾选确认 → 发布 → 轮询至 success 且运行时校验通过（AC-009/010）
  await wizard.getByLabel("确认变更内容").check();
  await deployBtn.click();
  await expect(wizard.getByText("排队中")).toBeVisible({ timeout: 15_000 });
  await expect(wizard.getByText("网关已确认生效")).toBeVisible({ timeout: 90_000 });

  // ⑧ 落盘检查（原子替换产物，每路由一文件，权限 0644）
  const routersDir = `${DEPLOY_ROOT}/dynamic/routers`;
  const files = readdirSync(routersDir);
  expect(files).toContain(`${ROUTE_NAME}.yml`);
  expect((statSync(`${routersDir}/${ROUTE_NAME}.yml`).mode & 0o777).toString(8)).toBe("644");
  const content = readFileSync(`${routersDir}/${ROUTE_NAME}.yml`, "utf8");
  expect(content).toContain(`Host(\`${DOMAIN}\`)`);
  expect(content).toContain("PathPrefix(`/`)");

  // SC-001 计时：全程 ≤15 分钟
  const minutes = (Date.now() - t0) / 60_000;
  expect(minutes).toBeLessThanOrEqual(15);
  console.log(`[SC-001] UF-1 全链耗时 ${minutes.toFixed(2)} 分钟`);
});
