// T008 · 会话/RBAC 单测（占位链路可跑通 vitest）。
import { describe, it, expect, beforeEach } from "vitest";
import { can, hasRole, tokenStore } from "./session";
import type { User } from "./session";

const mk = (role: User["role"]): User => ({ id: "u", username: "u", display_name: "U", role });

describe("RBAC 助手（contracts/README.md 矩阵）", () => {
  beforeEach(() => tokenStore.clear());

  it("viewer 无业务写权限", () => {
    expect(hasRole(mk("viewer"), "developer")).toBe(false);
    expect(can.writeBusiness(mk("viewer"))).toBe(false);
  });

  it("developer 可写业务但不可回滚/管用户", () => {
    expect(can.writeBusiness(mk("developer"))).toBe(true);
    expect(can.rollback(mk("developer"))).toBe(false);
    expect(can.manageUsers(mk("developer"))).toBe(false);
  });

  it("gateway_admin 可回滚但不可管用户", () => {
    expect(can.rollback(mk("gateway_admin"))).toBe(true);
    expect(can.manageUsers(mk("gateway_admin"))).toBe(false);
  });

  it("super_admin 全权", () => {
    expect(can.manageUsers(mk("super_admin"))).toBe(true);
    expect(can.editSettings(mk("super_admin"))).toBe(true);
    expect(can.rollback(mk("super_admin"))).toBe(true);
  });

  it("null 用户零权限", () => {
    expect(can.writeBusiness(null)).toBe(false);
    expect(can.rollback(null)).toBe(false);
  });
});

describe("tokenStore", () => {
  it("set 后 access/refresh 可读回", () => {
    tokenStore.set({ access_token: "a", refresh_token: "r" });
    expect(tokenStore.access()).toBe("a");
    expect(tokenStore.refresh()).toBe("r");
  });
});
