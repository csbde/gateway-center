// T042 前置 · 会话核心（T005 数据层）：access/refresh 存储、旋转刷新、登出广播。
// 存储选择 localStorage（控制平面内网部署假设，R6；XSS 面由 CSP 与不渲染富文本约束）。
import type { components } from "./generated/schema";

export type User = components["schemas"]["User"];
export type Role = User["role"];

const ACCESS_KEY = "gc.access_token";
const REFRESH_KEY = "gc.refresh_token";
const USER_KEY = "gc.user";

export const AUTH_EVENT = "gc:auth-changed";

function emit() {
  window.dispatchEvent(new Event(AUTH_EVENT));
}

export const tokenStore = {
  access: () => localStorage.getItem(ACCESS_KEY),
  refresh: () => localStorage.getItem(REFRESH_KEY),
  set(tokens: { access_token: string; refresh_token: string }) {
    localStorage.setItem(ACCESS_KEY, tokens.access_token);
    localStorage.setItem(REFRESH_KEY, tokens.refresh_token);
    emit();
  },
  user: (): User | null => {
    try {
      const raw = localStorage.getItem(USER_KEY);
      return raw ? (JSON.parse(raw) as User) : null;
    } catch {
      return null;
    }
  },
  setUser: (u: User | null) => {
    if (u) localStorage.setItem(USER_KEY, JSON.stringify(u));
    else localStorage.removeItem(USER_KEY);
    emit();
  },
  clear: () => {
    localStorage.removeItem(ACCESS_KEY);
    localStorage.removeItem(REFRESH_KEY);
    localStorage.removeItem(USER_KEY);
    emit();
  },
};

export function getAccessToken(): string | null {
  return tokenStore.access();
}

let refreshing: Promise<boolean> | null = null;

/** 旋转刷新（并发去重：同时只有一个 /auth/refresh 在飞）。 */
export function refreshSession(): Promise<boolean> {
  refreshing ??= (async () => {
    const rt = tokenStore.refresh();
    if (!rt) return false;
    try {
      const res = await fetch("/api/v1/auth/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: rt }),
      });
      if (!res.ok) return false;
      const tokens = (await res.json()) as { access_token: string; refresh_token: string };
      tokenStore.set(tokens);
      return true;
    } catch {
      return false;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}

export async function login(username: string, password: string): Promise<User> {
  const res = await fetch("/api/v1/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    throw new Error(body?.error?.message ?? `登录失败（HTTP ${res.status}）`);
  }
  const pair = (await res.json()) as {
    access_token: string;
    refresh_token: string;
    user?: User;
  };
  tokenStore.set(pair);
  const me = pair.user ?? (await fetchMe());
  tokenStore.setUser(me);
  return me;
}

export async function fetchMe(): Promise<User> {
  const res = await fetch("/api/v1/me", {
    headers: { Authorization: `Bearer ${tokenStore.access() ?? ""}` },
  });
  if (!res.ok) throw new Error("获取用户信息失败");
  return (await res.json()) as User;
}

export async function logout(): Promise<void> {
  const rt = tokenStore.refresh();
  if (rt) {
    await fetch("/api/v1/auth/logout", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${tokenStore.access() ?? ""}`,
      },
      body: JSON.stringify({ refresh_token: rt }),
    }).catch(() => undefined);
  }
  hardLogout();
}

export function hardLogout(): void {
  tokenStore.clear();
}

// ---- RBAC 助手（contracts/README.md 权限矩阵；前端按钮态与后端守卫一致，US6/T078） ----
const rank: Record<Role, number> = { viewer: 0, developer: 1, gateway_admin: 2, super_admin: 3 };

export function hasRole(user: User | null, min: Role): boolean {
  return !!user && rank[user.role] >= rank[min];
}

export const can = {
  writeBusiness: (u: User | null) => hasRole(u, "developer"),
  rollback: (u: User | null) => hasRole(u, "gateway_admin"),
  advancedRoute: (u: User | null) => hasRole(u, "gateway_admin"),
  nodeCreate: (u: User | null) => hasRole(u, "gateway_admin"),
  approve: (u: User | null) => hasRole(u, "gateway_admin"),
  manageUsers: (u: User | null) => hasRole(u, "super_admin"),
  editSettings: (u: User | null) => hasRole(u, "super_admin"),
  auditRead: (u: User | null) => hasRole(u, "viewer"),
};
