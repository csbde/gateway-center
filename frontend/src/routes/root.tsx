// T005 · 根路由组件（Outlet 出口）。
import { Outlet, createRootRoute } from "@tanstack/react-router";

function RootLayout() {
  return <Outlet />;
}

export const RootRoute = createRootRoute({ component: RootLayout });
