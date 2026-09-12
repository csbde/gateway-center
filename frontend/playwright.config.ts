import { defineConfig, devices } from "@playwright/test";

// T008 · Playwright e2e（UF-1 发布链 / V-* 场景）
export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 60_000,
  expect: { timeout: 15_000 }, // 部署状态轮询（1s/2s/4s 退避 + 校验）需要宽松断言窗口
  fullyParallel: false, // 共享参考拓扑（compose），用例间有状态依赖
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:5173",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  // 后端与 compose 由 make/quickstart 预起；此处不自动 spawn，避免密钥注入耦合
});
