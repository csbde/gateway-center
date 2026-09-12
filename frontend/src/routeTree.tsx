// T005 · 10 页面路由树（code-based）。守卫：除 /login 外全部要求已登录
// （beforeLoad 检查本地会话；服务端仍逐请求复核，R6）。
import { createRoute, createRouter, redirect } from "@tanstack/react-router";
import { RootRoute } from "./routes/root";
import { LoginPage } from "./routes/login";
import { AppShell } from "./routes/app-shell";
import { Placeholder } from "./routes/placeholder";
import { DashboardPage } from "./routes/dashboard";
import { NodesPage } from "@/features/nodes";
import { DomainsPage } from "@/features/domains";
import { ServicesPage } from "@/features/services";
import { RoutesPage } from "@/features/routes";
import { DeploymentsPage } from "@/features/deployments";
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

const shellRoute = createRoute({
  getParentRoute: () => RootRoute,
  path: "/",
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
  component: () => <Placeholder title="Policy Library" />,
});
const versions = createRoute({
  getParentRoute: () => shellRoute,
  path: "/config-versions",
  component: () => <Placeholder title="Config Versions" />,
});
const deployments = createRoute({
  getParentRoute: () => shellRoute,
  path: "/deployments",
  component: DeploymentsPage,
});
const audits = createRoute({
  getParentRoute: () => shellRoute,
  path: "/audits",
  component: () => <Placeholder title="Audit Logs" />,
});
const settings = createRoute({
  getParentRoute: () => shellRoute,
  path: "/settings",
  component: () => <Placeholder title="Settings" />,
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
    settings,
  ]),
]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
