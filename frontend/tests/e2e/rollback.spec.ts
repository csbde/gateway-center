// T055 · V-2 回滚 E2E（quickstart §V-2，SC-003 ≤3 分钟）：
// 发布 v1 → 改路由再发布 v2 → 配置版本页回滚 v1 → 网关恢复 v1 行为（落盘规则回到 PathPrefix(/)）。
// 前置同 uf1-publish.spec.ts（compose 栈 + serve :8089 + vite :5173）。
import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

const ADMIN_USER = process.env.E2E_ADMIN_USER ?? "admin";
const ADMIN_PASS = process.env.E2E_ADMIN_PASSWORD ?? "ChangeMe-Strong-1";
const STAMP = Date.now();
const NODE_NAME = `e2e-rb-${STAMP}`;
const DEPLOY_ROOT = process.env.E2E_DEPLOY_ROOT ?? new URL("../../../deploy/compose", import.meta.url).pathname;
const TRAEFIK_API = process.env.E2E_TRAEFIK_API ?? "http://localhost:8081";

const navLink = (page: import("@playwright/test").Page, name: string) =>
  page.getByRole("navigation").getByRole("link", { name });

async function pickScope(page: import("@playwright/test").Page, label: string) {
  const scope = page.getByLabel("当前网关");
  await expect(scope).toBeVisible();
  await scope.selectOption({ label });
}

/** 发布向导走完 检查→生成→Diff→确认→发布 直至网关确认生效。 */
async function publishViaWizard(page: import("@playwright/test").Page) {
  await navLink(page, "发布记录").click();
  await page.getByRole("button", { name: "发起发布" }).click();
  const wizard = page.getByRole("group", { name: "发布向导" });
  await wizard.getByRole("button", { name: "开始检查" }).click();
  await expect(wizard.getByText(/全部规则检查通过/)).toBeVisible({ timeout: 30_000 });
  await wizard.getByRole("button", { name: "生成版本" }).click();
  await expect(wizard.getByText("待发布")).toBeVisible({ timeout: 30_000 });
  await wizard.getByRole("button", { name: "查看变更内容" }).click();
  await wizard.getByLabel("确认变更内容").check();
  await wizard.getByRole("button", { name: "发布", exact: true }).click();
  await expect(wizard.getByText("网关已确认生效")).toBeVisible({ timeout: 90_000 });
  await page.getByRole("button", { name: "关闭" }).last().click();
}

test("V-2 回滚：发布两次后回滚 v1，≤3 分钟恢复旧行为", async ({ page }) => {
  test.setTimeout(4 * 60 * 1000); // 3 分钟预算 + 调度余量
  const t0 = Date.now();
  const SVC = `rb-svc-${STAMP}`;
  const DOMAIN = `rb-${STAMP}.example.com`;
  const ROUTE = `rb-main-${STAMP}`;

  await page.goto("/login");
  await page.getByLabel(/用户名/).fill(ADMIN_USER);
  await page.getByLabel(/密码/).fill(ADMIN_PASS);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(navLink(page, "网关节点")).toBeVisible();

  // —— 最小资源集（policy=off，无需证书）——
  await navLink(page, "网关节点").click();
  await page.getByRole("button", { name: "新建网关" }).click();
  const nodeForm = page.getByRole("form", { name: "新建网关" });
  await nodeForm.getByLabel("名称").fill(NODE_NAME);
  await nodeForm.getByLabel(/Traefik API 地址/).fill(TRAEFIK_API);
  await nodeForm.getByLabel(/落盘根路径/).fill(DEPLOY_ROOT);
  await nodeForm.getByLabel("环境类型").selectOption("test");
  await nodeForm.getByRole("button", { name: "保存" }).click();
  await expect(page.getByRole("row").filter({ hasText: NODE_NAME })).toBeVisible();
  const scope = `${NODE_NAME}（测试）`;

  await navLink(page, "服务").click();
  await pickScope(page, scope);
  await page.getByRole("button", { name: "新建服务" }).click();
  const svcForm = page.getByRole("form", { name: "服务表单" });
  await svcForm.getByLabel("服务名").fill(SVC);
  await svcForm.getByRole("button", { name: "保存" }).click();
  const svcRow = page.getByRole("row").filter({ hasText: SVC });
  await expect(svcRow).toBeVisible();
  await svcRow.getByRole("button", { name: "管理目标" }).click();
  await page.getByLabel("目标地址").fill("http://sample-crm/rb");
  await page.getByRole("button", { name: "添加" }).click();

  await navLink(page, "域名").click();
  await pickScope(page, scope);
  await page.getByRole("button", { name: "新建域名" }).click();
  const domForm = page.getByRole("form", { name: "域名表单" });
  await domForm.getByLabel("域名").fill(DOMAIN);
  await domForm.getByRole("button", { name: "保存" }).click();

  await navLink(page, "路由规则").click();
  await pickScope(page, scope);
  await page.getByRole("button", { name: "新建规则" }).click();
  const rtForm = page.getByRole("form", { name: "路由规则表单" });
  await rtForm.getByLabel("规则名称").fill(ROUTE);
  await rtForm.getByLabel("域名").selectOption({ label: DOMAIN });
  await rtForm.getByLabel("路径").fill("/");
  await rtForm.getByLabel("转发到服务").selectOption({ label: SVC });
  await rtForm.getByRole("button", { name: "保存" }).click();
  const rtRow = page.getByRole("row").filter({ hasText: ROUTE });
  await expect(rtRow).toBeVisible();
  await rtRow.getByRole("button", { name: "启用" }).click();

  // —— 发布 v1 ——
  await pickScope(page, scope); // 发布页作用域记忆可能在导航间保持，重设保险
  await publishViaWizard(page);
  const routerFile = `${DEPLOY_ROOT}/dynamic/routers/${ROUTE}.yml`;
  expect(readFileSync(routerFile, "utf8")).toContain("PathPrefix(`/`)");

  // —— 改路由 path=/second → 发布 v2 ——
  await navLink(page, "路由规则").click();
  await pickScope(page, scope);
  await page.getByRole("row").filter({ hasText: ROUTE }).getByRole("button", { name: "编辑" }).click();
  const editForm = page.getByRole("form", { name: "路由规则表单" });
  await editForm.getByLabel("路径").fill("/second");
  await editForm.getByRole("button", { name: "保存" }).click();
  await publishViaWizard(page);
  expect(readFileSync(routerFile, "utf8")).toContain("PathPrefix(`/second`)");

  // —— 配置版本页：回滚到 v1 ——
  await navLink(page, "配置版本").click();
  await pickScope(page, scope);
  const v1Row = page.getByRole("row").filter({ hasText: /\bv1\b/ });
  await expect(v1Row).toBeVisible();
  page.once("dialog", (d) => void d.accept()); // window.confirm 预确认
  await v1Row.getByRole("button", { name: "回滚到此版本" }).click();
  const rbGroup = page.getByRole("group", { name: "回滚确认" });
  await expect(rbGroup).toBeVisible();
  await rbGroup.getByRole("button", { name: /确认回滚/ }).click();
  await expect(rbGroup.getByText("回滚成功")).toBeVisible({ timeout: 120_000 });
  await rbGroup.getByRole("button", { name: "关闭" }).click();

  // 新版本 v3（origin=回滚）出现在历史里
  await expect(page.getByRole("row").filter({ hasText: /\bv3\b/ })).toBeVisible();

  // 网关恢复 v1 行为：落盘规则回到 PathPrefix(/)
  expect(readFileSync(routerFile, "utf8")).toContain("PathPrefix(`/`)");

  const minutes = (Date.now() - t0) / 60_000;
  expect(minutes).toBeLessThanOrEqual(3); // SC-003
  console.log(`[SC-003] V-2 回滚耗时 ${minutes.toFixed(2)} 分钟`);
});
