// T063 · V-3 中间件与高级模式 E2E（quickstart §V-3，AC-005/015、FR-015~022）：
// 策略库建两类策略 → 路由绑定 → 被引用删除阻止（清单）→ 解绑后可删；
// 高级模式：风险横幅 + 非法表达式实时报错 + 合法保存回显「自定义条件」。
// 前置同 uf1-publish.spec.ts（栈起 :8089/:5173）。
import { test, expect } from "@playwright/test";

const ADMIN_USER = process.env.E2E_ADMIN_USER ?? "admin";
const ADMIN_PASS = process.env.E2E_ADMIN_PASSWORD ?? "ChangeMe-Strong-1";
const STAMP = Date.now();
const NODE_NAME = `e2e-mw-${STAMP}`;
const DEPLOY_ROOT = process.env.E2E_DEPLOY_ROOT ?? new URL("../../../deploy/compose", import.meta.url).pathname;
const TRAEFIK_API = process.env.E2E_TRAEFIK_API ?? "http://localhost:8081";

const navLink = (page: import("@playwright/test").Page, name: string) =>
  page.getByRole("navigation").getByRole("link", { name });

async function pickScope(page: import("@playwright/test").Page, label: string) {
  const scope = page.getByLabel("当前网关");
  await expect(scope).toBeVisible();
  await scope.selectOption({ label });
}

test("V-3 策略复用/删除阻止 + 高级模式实时验证", async ({ page }) => {
  test.setTimeout(3 * 60 * 1000);
  const SVC = `mw-svc-${STAMP}`;
  const DOMAIN = `mw-${STAMP}.example.com`;
  const ROUTE = `mw-main-${STAMP}`;
  const MW_A = `rate-${STAMP}`.slice(0, 40);
  const MW_B = `strip-${STAMP}`.slice(0, 40);

  await page.goto("/login");
  await page.getByLabel(/用户名/).fill(ADMIN_USER);
  await page.getByLabel(/密码/).fill(ADMIN_PASS);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(navLink(page, "网关节点")).toBeVisible();

  // —— 节点 + 最小资源 ——
  await navLink(page, "网关节点").click();
  await page.getByRole("button", { name: "新建网关" }).click();
  const nodeForm = page.getByRole("form", { name: "新建网关" });
  await nodeForm.getByLabel("名称").fill(NODE_NAME);
  await nodeForm.getByLabel(/Traefik API 地址/).fill(TRAEFIK_API);
  await nodeForm.getByLabel(/落盘根路径/).fill(DEPLOY_ROOT);
  await nodeForm.getByLabel("环境类型").selectOption("test");
  await nodeForm.getByRole("button", { name: "保存" }).click();
  const scope = `${NODE_NAME}（测试）`;

  await navLink(page, "服务").click();
  await pickScope(page, scope);
  await page.getByRole("button", { name: "新建服务" }).click();
  await page.getByRole("form", { name: "服务表单" }).getByLabel("服务名").fill(SVC);
  await page.getByRole("button", { name: "保存" }).click();
  await page.getByRole("row").filter({ hasText: SVC }).getByRole("button", { name: "管理目标" }).click();
  await page.getByLabel("目标地址").fill("http://sample-crm/mw");
  await page.getByRole("button", { name: "添加" }).click();

  await navLink(page, "域名").click();
  await pickScope(page, scope);
  await page.getByRole("button", { name: "新建域名" }).click();
  await page.getByRole("form", { name: "域名表单" }).getByLabel("域名").fill(DOMAIN);
  await page.getByRole("button", { name: "保存" }).click();

  // —— 策略库：建两条策略（限流 + 前缀剥离）——
  await navLink(page, "策略库").click();
  await pickScope(page, scope);
  const createMw = async (name: string, type: string, fill: () => Promise<void>) => {
    await page.getByRole("button", { name: "新建策略" }).click();
    const form = page.getByRole("form", { name: "中间件表单" });
    await form.getByLabel("策略类型").selectOption({ label: type });
    await form.getByLabel("策略标识").fill(name);
    await fill();
    await form.getByRole("button", { name: "保存" }).click();
    await expect(page.getByRole("row").filter({ hasText: name })).toBeVisible();
  };
  await createMw(MW_A, "限流保护", async () => {
    const form = page.getByRole("form", { name: "中间件表单" });
    await form.getByLabel("速率阈值（次）").fill("50");
    await form.getByLabel("突发上限（次）").fill("100");
  });
  await createMw(MW_B, "路径前缀剥离", async () => {
    await page.getByRole("form", { name: "中间件表单" }).getByLabel("要剥离的前缀").fill("/api");
  });

  // —— 路由绑定两条策略并排序（strip 在前、限流在后）——
  await navLink(page, "路由规则").click();
  await pickScope(page, scope);
  await page.getByRole("button", { name: "新建规则" }).click();
  const rtForm = page.getByRole("form", { name: "路由规则表单" });
  await rtForm.getByLabel("规则名称").fill(ROUTE);
  await rtForm.getByLabel("域名").selectOption({ label: DOMAIN });
  await rtForm.getByLabel("路径").fill("/");
  await rtForm.getByLabel("转发到服务").selectOption({ label: SVC });
  const picker = page.getByLabel("绑定策略");
  await picker.getByRole("button", { name: `+ ${MW_A}` }).click();
  await picker.getByRole("button", { name: `+ ${MW_B}` }).click();
  // 初始顺序 A→B；下移 A → B→A（顺序即执行语义，FR-018）
  await picker.getByRole("listitem").first().getByRole("button", { name: "下移" }).click();
  await expect(picker.getByRole("listitem").first()).toContainText(MW_B);
  await rtForm.getByRole("button", { name: "保存" }).click();
  const rtRow = page.getByRole("row").filter({ hasText: ROUTE });
  await expect(rtRow).toBeVisible();
  await rtRow.getByRole("button", { name: "启用" }).click();

  // —— 被引用删除阻止（清单 + 引导）——
  await navLink(page, "策略库").click();
  await pickScope(page, scope);
  page.once("dialog", (d) => void d.accept());
  await page.getByRole("row").filter({ hasText: MW_A }).getByRole("button", { name: "删除" }).click();
  await expect(page.getByRole("alert")).toContainText("该策略正被 1 条规则使用");
  await expect(page.getByRole("alert")).toContainText(ROUTE); // 引用清单逐条列出（AC-015）

  // —— 解绑后可删 ——
  await navLink(page, "路由规则").click();
  await pickScope(page, scope);
  await page.getByRole("row").filter({ hasText: ROUTE }).getByRole("button", { name: "编辑" }).click();
  const picker2 = page.getByLabel("绑定策略");
  await expect(picker2.getByRole("listitem")).toHaveCount(2);
  await picker2.getByRole("listitem").filter({ hasText: MW_A }).getByRole("button", { name: "解绑" }).click();
  await picker2.getByRole("listitem").filter({ hasText: MW_B }).getByRole("button", { name: "解绑" }).click();
  await page.getByRole("form", { name: "路由规则表单" }).getByRole("button", { name: "保存" }).click();

  await navLink(page, "策略库").click();
  await pickScope(page, scope);
  page.once("dialog", (d) => void d.accept());
  await page.getByRole("row").filter({ hasText: MW_A }).getByRole("button", { name: "删除" }).click();
  await expect(page.getByRole("row").filter({ hasText: MW_A })).toHaveCount(0);

  // —— 高级模式：风险横幅 + 实时验证 + 保存回显 ——
  await navLink(page, "路由规则").click();
  await pickScope(page, scope);
  await page.getByRole("button", { name: "新建规则" }).click();
  const form2 = page.getByRole("form", { name: "路由规则表单" });
  await form2.getByRole("button", { name: /自定义条件/ }).click();
  const adv = page.getByRole("group", { name: "自定义匹配条件" });
  await expect(adv.getByRole("alert")).toContainText("写错会导致流量无法匹配"); // 固定风险提示（FR-016）
  await adv.getByLabel("匹配条件表达式").fill("Foo(`x`)");
  await expect(adv.getByText("不在白名单")).toBeVisible({ timeout: 10_000 }); // 实时语法验证（FR-015）
  await adv.getByLabel("匹配条件表达式").fill(" Host(`adv.example.com`)  &&  PathPrefix(`/api`) ");
  await expect(adv.getByText("语法通过")).toBeVisible({ timeout: 10_000 });
  await expect(adv.getByText("Host(`adv.example.com`) && PathPrefix(`/api`)")).toBeVisible(); // 归一化预览
  await form2.getByLabel("规则名称").fill(`mw-adv-${STAMP}`);
  await form2.getByLabel("转发到服务").selectOption({ label: SVC });
  await form2.getByRole("button", { name: "保存" }).click();
  await expect(page.getByRole("row").filter({ hasText: `mw-adv-${STAMP}` }).getByText("自定义条件")).toBeVisible();
});
