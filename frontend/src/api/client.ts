// T005 · 类型化 API 客户端：错误体契约 {error:{code,message,request_id,details[]}}（contracts/README.md）。
import { getAccessToken, refreshSession, hardLogout } from "./session";
import type { components } from "./generated/schema";

export type ApiErrorBody = components["schemas"]["Error"];

export class ApiError extends Error {
  readonly code: string;
  readonly requestId: string;
  readonly status: number;
  readonly details: ApiErrorBody["error"]["details"];

  constructor(status: number, body: ApiErrorBody) {
    super(body.error.message);
    this.code = body.error.code;
    this.requestId = body.error.request_id ?? "";
    this.status = status;
    this.details = body.error.details ?? [];
  }
}

export interface ListEnvelope<T> {
  items: T[];
  page: number;
  page_size: number;
  total: number;
}

export type Query = Record<string, string | number | boolean | undefined>;

async function parse<T>(res: Response): Promise<T> {
  if (res.status === 204) return undefined as T;
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    throw new ApiError(res.status, { error: { code: "INTERNAL", message: `响应解析失败（HTTP ${res.status}）` } });
  }
  if (!res.ok) {
    const e = body as ApiErrorBody;
    if (e?.error?.code) throw new ApiError(res.status, e);
    throw new ApiError(res.status, { error: { code: `HTTP_${res.status}`, message: res.statusText } });
  }
  return body as T;
}

async function request<T>(
  method: string,
  path: string,
  opts: { body?: unknown; query?: Query; retryOn401?: boolean } = {},
): Promise<T> {
  const url = new URL(`/api/v1${path}`, window.location.origin);
  for (const [k, v] of Object.entries(opts.query ?? {})) {
    if (v !== undefined) url.searchParams.set(k, String(v));
  }
  const headers: Record<string, string> = { Accept: "application/json" };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  let payload: BodyInit | undefined;
  if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(opts.body);
  }
  const res = await fetch(url, { method, headers, body: payload });
  if (res.status === 401 && opts.retryOn401 !== false && token) {
    // 旋转刷新一次并重放（access 过期常见路径；失败即登出）
    const fresh = await refreshSession().catch(() => null);
    if (!fresh) {
      hardLogout();
      throw new ApiError(401, { error: { code: "UNAUTHENTICATED", message: "会话已过期，请重新登录" } });
    }
    return request<T>(method, path, { ...opts, retryOn401: false });
  }
  return parse<T>(res);
}

export const api = {
  get: <T>(path: string, query?: Query) => request<T>("GET", path, { query }),
  post: <T>(path: string, body?: unknown, query?: Query) =>
    request<T>("POST", path, { body, query }),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, { body }),
  delete: <T>(path: string) => request<T>("DELETE", path),
};
