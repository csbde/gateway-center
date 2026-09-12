// T051 · UF-1 发布主链路 E2E（quickstart V-1 步骤 1–9，SC-001 ≤15 分钟）。
// 前置：compose 已起（postgres/traefik/sample-crm）、后端 serve、migrate-seed 完成。
// 实装在 T043–T047 页面完成后填充选择器（当前 smoke：登录 → 壳导航可达）。
import { test, expect } from "@playwright/test";

const ADMIN_USER = process.env.E2E_ADMIN_USER ?? "admin";
const ADMIN_PASS = process.env.E2E_ADMIN_PASSWORD ?? "ChangeMe-Strong-1";

test("UF-1 smoke：登录并看到主导航", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel(/用户名/).fill(ADMIN_USER);
  await page.getByLabel(/密码/).fill(ADMIN_PASS);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("link", { name: "网关节点" })).toBeVisible();
  await expect(page.getByRole("link", { name: "路由规则" })).toBeVisible();
});

test("UF-1 全链（节点→服务→域名→路由→发布→运行时校验）", async () => {
  test.skip(true, "T043–T047 页面实装后启用（本用例计时断言 SC-001 ≤ 15 分钟）");
});
