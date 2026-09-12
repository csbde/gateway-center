// T005 · 10 页面路由树（code-based）。守卫：除 /login 外全部要求已登录
// （beforeLoad 检查本地会话；服务端仍逐请求复核，R6）。
import { createRoute, createRouter, redirect } from "@tanstack/react-router";
import { RootRoute } from "./routes/root";
import { LoginPage } from "./routes/login";
import { AppShell } from "./routes/app-shell";
import { DashboardPage } from "./routes/dashboard";
import { AuditsPage } from "./routes/audits";
import { UsersPage } from "./routes/users";
import { NodesPage } from "@/features/nodes";
import { DomainsPage } from "@/features/domains";
import { ServicesPage } from "@/features/services";
import { RoutesPage } from "@/features/routes";
import { MiddlewaresPage } from "@/features/middlewares";
import { DeploymentsPage } from "@/features/deployments";
import { VersionsPage } from "@/features/versions";
import { SettingsPage } from "./routes/settings";
import { tokenStore } from "./api/session";

function requireAuth() {
  if (!tokenStore.access()) {
    throw redirect({ to: "/login" });
  }
}

const loginRoute = createRoute({
  getParentRoute: () => RootRoute,
  path: "/login",
  component: LoginPage,
});

// 壳层用无路径（pathless）布局路由：children 各自声明 path，"/" 仅属 dash，避免与壳层 id 冲突。
const shellRoute = createRoute({
  getParentRoute: () => RootRoute,
  id: "shell",
  component: AppShell,
  beforeLoad: requireAuth,
});

const dash = createRoute({
  getParentRoute: () => shellRoute,
  path: "/",
  component: DashboardPage,
});
const nodes = createRoute({
  getParentRoute: () => shellRoute,
  path: "/nodes",
  component: NodesPage,
});
const domains = createRoute({
  getParentRoute: () => shellRoute,
  path: "/domains",
  component: DomainsPage,
});
const services = createRoute({
  getParentRoute: () => shellRoute,
  path: "/services",
  component: ServicesPage,
});
const routes = createRoute({
  getParentRoute: () => shellRoute,
  path: "/routes",
  component: RoutesPage,
});
const middlewares = createRoute({
  getParentRoute: () => shellRoute,
  path: "/middlewares",
  component: MiddlewaresPage,
});
const versions = createRoute({
  getParentRoute: () => shellRoute,
  path: "/config-versions",
  component: VersionsPage,
});
const deployments = createRoute({
  getParentRoute: () => shellRoute,
  path: "/deployments",
  component: DeploymentsPage,
});
const audits = createRoute({
  getParentRoute: () => shellRoute,
  path: "/audits",
  component: AuditsPage,
});
const users = createRoute({
  getParentRoute: () => shellRoute,
  path: "/users",
  component: UsersPage,
});
const settings = createRoute({
  getParentRoute: () => shellRoute,
  path: "/settings",
  component: SettingsPage,
});

export const routeTree = RootRoute.addChildren([
  loginRoute,
  shellRoute.addChildren([
    dash,
    nodes,
    domains,
    services,
    routes,
    middlewares,
    versions,
    deployments,
    audits,
    users,
    settings,
  ]),
]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
