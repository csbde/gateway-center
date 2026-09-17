// T005/T042 · 应用外壳：侧栏导航 + 会话出口（角色隐藏写入口按钮在 US6 T078 前端接线）。
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { can, tokenStore, logout } from "@/api/session";

const NAV = [
  { to: "/", label: "概览" },
  { to: "/proxy-hosts", label: "网站代理" },
  { to: "/certificates", label: "SSL 证书" },
  { to: "/nodes", label: "网关节点" },
  { to: "/domains", label: "域名" },
  { to: "/services", label: "服务" },
  { to: "/routes", label: "路由规则" },
  { to: "/middlewares", label: "策略库" },
  { to: "/config-versions", label: "配置版本" },
  { to: "/deployments", label: "发布记录" },
  { to: "/audits", label: "审计日志" },
  { to: "/users", label: "用户管理", gate: can.manageUsers },
  { to: "/settings", label: "平台设置" },
] as const;

export function AppShell() {
  const navigate = useNavigate();
  const user = tokenStore.user();

  return (
    <div className="flex min-h-screen">
      <aside className="w-52 shrink-0 border-r bg-card p-4">
        <div className="text-sm font-semibold">Gateway Center</div>
        <nav className="mt-4 space-y-1">
          {NAV.filter((item) => !("gate" in item) || item.gate(user)).map((item) => (
            <Link
              key={item.to}
              to={item.to}
              className="block rounded-md px-3 py-1.5 text-sm hover:bg-muted"
              activeProps={{ className: "block rounded-md px-3 py-1.5 text-sm bg-primary text-primary-foreground" }}
            >
              {item.label}
            </Link>
          ))}
        </nav>
      </aside>
      <div className="flex-1">
        <header className="flex items-center justify-end gap-3 border-b bg-card px-6 py-2 text-sm">
          {user && (
            <span className="text-muted-foreground">
              {user.display_name}（{user.role}）
            </span>
          )}
          <button
            type="button"
            className="rounded-md border px-3 py-1 hover:bg-muted"
            onClick={async () => {
              await logout();
              void navigate({ to: "/login" });
            }}
          >
            退出
          </button>
        </header>
        <main className="p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
