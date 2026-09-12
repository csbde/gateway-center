// T043–T047 · US1 资源端点封装（与 contracts/openapi.yaml 路径一一对应）。
import { api } from "./client";
import type {
  ConfigVersion,
  Deployment,
  DiffResult,
  Domain,
  List,
  Middleware,
  MiddlewareView,
  Node,
  NodeState,
  PlatformSettings,
  Route,
  RouteView,
  Service,
  Target,
  ValidateResult,
} from "./types";

// ---- nodes ----
export const nodesApi = {
  list: (q?: Record<string, string | number>) => api.get<List<Node>>("/nodes", q),
  get: (id: string) => api.get<Node>(`/nodes/${id}`),
  state: (id: string) => api.get<NodeState>(`/nodes/${id}/state`),
  create: (body: Record<string, unknown>) => api.post<Node>("/nodes", body),
  update: (id: string, body: Record<string, unknown>) => api.put<Node>(`/nodes/${id}`, body),
  enable: (id: string, expectedVersion: number) =>
    api.post<Node>(`/nodes/${id}/enable`, { expected_version: expectedVersion }),
  disable: (id: string, expectedVersion: number) =>
    api.post<Node>(`/nodes/${id}/disable`, { expected_version: expectedVersion }),
  remove: (id: string) => api.delete<undefined>(`/nodes/${id}`),
  validate: (id: string) => api.post<ValidateResult>(`/nodes/${id}/validate`),
  versions: (id: string) => api.get<List<ConfigVersion>>(`/nodes/${id}/versions`),
  createVersion: (id: string) => api.post<ConfigVersion>(`/nodes/${id}/versions`),
};

// ---- domains ----
export const domainsApi = {
  list: (q?: Record<string, string | number>) => api.get<List<Domain>>("/domains", q),
  create: (body: Record<string, unknown>) => api.post<Domain>("/domains", body),
  update: (id: string, body: Record<string, unknown>) => api.put<Domain>(`/domains/${id}`, body),
  enable: (id: string, ev: number) => api.post<Domain>(`/domains/${id}/enable`, { expected_version: ev }),
  disable: (id: string, ev: number) => api.post<Domain>(`/domains/${id}/disable`, { expected_version: ev }),
  remove: (id: string) => api.delete<undefined>(`/domains/${id}`),
};

// ---- services / targets ----
export const servicesApi = {
  list: (q?: Record<string, string | number>) => api.get<List<Service>>("/services", q),
  create: (body: Record<string, unknown>) => api.post<Service>("/services", body),
  update: (id: string, body: Record<string, unknown>) => api.put<Service>(`/services/${id}`, body),
  enable: (id: string, ev: number) => api.post<Service>(`/services/${id}/enable`, { expected_version: ev }),
  disable: (id: string, ev: number) => api.post<Service>(`/services/${id}/disable`, { expected_version: ev }),
  remove: (id: string) => api.delete<undefined>(`/services/${id}`),
  targets: (id: string) => api.get<List<Target>>(`/services/${id}/targets`),
  addTarget: (id: string, body: Record<string, unknown>) => api.post<Target>(`/services/${id}/targets`, body),
  updateTarget: (id: string, tid: string, body: Record<string, unknown>) =>
    api.put<Target>(`/services/${id}/targets/${tid}`, body),
  deleteTarget: (id: string, tid: string) => api.delete<undefined>(`/services/${id}/targets/${tid}`),
};

// ---- routes ----
export const routesApi = {
  list: (q?: Record<string, string | number>) => api.get<List<Route>>("/routes", q),
  get: (id: string) => api.get<RouteView>(`/routes/${id}`),
  create: (body: Record<string, unknown>) => api.post<RouteView>("/routes", body),
  update: (id: string, body: Record<string, unknown>) => api.put<RouteView>(`/routes/${id}`, body),
  enable: (id: string, ev: number) => api.post<RouteView>(`/routes/${id}/enable`, { expected_version: ev }),
  disable: (id: string, ev: number) => api.post<RouteView>(`/routes/${id}/disable`, { expected_version: ev }),
  remove: (id: string) => api.delete<undefined>(`/routes/${id}`),
  validateAdvanced: (rule: string) =>
    api.post<import("./types").AdvancedValidateResult>("/routes/validate-advanced", { rule }),
};

// ---- middlewares（US3/T057：CRUD + 启停；GET/写响应均带 referenced_by） ----
export const middlewaresApi = {
  list: (q?: Record<string, string | number>) => api.get<List<Middleware>>("/middlewares", q),
  get: (id: string) => api.get<MiddlewareView>(`/middlewares/${id}`),
  create: (body: Record<string, unknown>) => api.post<MiddlewareView>("/middlewares", body),
  update: (id: string, body: Record<string, unknown>) => api.put<MiddlewareView>(`/middlewares/${id}`, body),
  enable: (id: string, ev: number) => api.post<MiddlewareView>(`/middlewares/${id}/enable`, { expected_version: ev }),
  disable: (id: string, ev: number) => api.post<MiddlewareView>(`/middlewares/${id}/disable`, { expected_version: ev }),
  remove: (id: string) => api.delete<undefined>(`/middlewares/${id}`),
};

// ---- pipeline: versions / deployments ----
export const pipelineApi = {
  version: (id: string) => api.get<ConfigVersion>(`/versions/${id}`),
  diff: (id: string, against?: string) =>
    api.get<DiffResult>(`/versions/${id}/diff`, against ? { against } : undefined),
  deploy: (body: { node_id: string; version_id: string; confirmed: boolean }) =>
    api.post<Deployment>("/deployments", body),
  rollback: (body: { node_id: string; version_id: string; confirmed: boolean }) =>
    api.post<Deployment>("/deployments/rollback", body),
  deployment: (id: string) => api.get<Deployment>(`/deployments/${id}`),
  deployments: (q?: Record<string, string | number>) => api.get<List<Deployment>>("/deployments", q),
};

export const settingsApi = {
  get: () => api.get<PlatformSettings>("/settings"),
  update: (body: Record<string, unknown>) => api.put<PlatformSettings>("/settings", body),
};
