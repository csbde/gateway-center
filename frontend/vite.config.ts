import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// T004/T008 · Vite + React18 + Tailwind4 + Vitest
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  server: {
    port: 5173,
    proxy: {
      // 开发期同源免 CORS：/api → 后端（默认 :8080；本机端口被占时用 VITE_API_PROXY 覆盖，e2e 亦依赖此变量）
      "/api": { target: process.env.VITE_API_PROXY ?? "http://localhost:8080", changeOrigin: true },
      "/healthz": { target: process.env.VITE_API_PROXY ?? "http://localhost:8080", changeOrigin: true },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: "./src/test/setup.ts",
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
