// T043–T047 支撑 · 错误→文案映射的回归（NFR-USE-01：冲突/阻断必须给出下一步引导）。
import { describe, expect, it } from "vitest";
import { ApiError } from "@/api/client";
import { fieldErrors, humanError } from "./common";

const err = (code: string, message: string, details?: unknown[]) =>
  new ApiError(400, { error: { code, message, request_id: "r1", details } as never });

describe("fieldErrors", () => {
  it("按 details[].field 归组字段错误", () => {
    const e = err("VALIDATION_FAILED", "字段校验失败", [
      { field: "base_url", message: "需为 http(s)://…", hint: "示例：http://10.0.0.2:8081" },
      { field: "name", message: "必填" },
    ]);
    expect(fieldErrors(e)).toEqual({
      base_url: "需为 http(s)://…；示例：http://10.0.0.2:8081",
      name: "必填",
    });
  });
  it("非 ApiError / 无 details → 空映射", () => {
    expect(fieldErrors(new Error("x"))).toEqual({});
    expect(fieldErrors(err("NOT_FOUND", "不存在"))).toEqual({});
  });
});

describe("humanError", () => {
  it("乐观锁冲突引导刷新", () => {
    const r = humanError(err("CONCURRENT_EDIT", "row_version 不一致"));
    expect(r.text).toContain("刷新");
    expect(r.tone).toBe("warning");
  });
  it("网关不可达为 danger 且提示保护线上", () => {
    const r = humanError(err("GATEWAY_UNREACHABLE", "无法连接 Traefik API"));
    expect(r.tone).toBe("danger");
    expect(r.text).toContain("阻断");
  });
  it("权限不足统一人读文案", () => {
    expect(humanError(err("FORBIDDEN", "role developer")).text).toContain("无权");
  });
  it("未知错误回退 message / Error.message", () => {
    expect(humanError(err("INTERNAL", "内部错误"))).toEqual({ text: "内部错误", tone: "danger" });
    expect(humanError("str").text).toBe("未知错误");
  });
});
