// k6 列表延迟压测（T088，SC-006：列表 p95 ≤ 3s）。
//
// 前置：先注入规模数据——
//   go run ./cmd/tools/seed
// 并启动 serve（GC_DATABASE_URL 指向已注入库）。
//
// 运行：
//   k6 run backend/tests/perf/list-latency.js \
//     -e BASE_URL=http://localhost:8089 -e USERNAME=admin -e PASSWORD=...
//
// 通过判据：list_p95 < 3000ms（SC-006）。
// 生成耗时 <5s 断言由集成测试 pipeline_e2e（T050）在规模数据下另行覆盖。
import http from "k6/http";
import { check, group } from "k6";
import { Rate } from "k6/metrics";

const BASE = __ENV.BASE_URL || "http://localhost:8089";
const USERNAME = __ENV.USERNAME || "admin";
const PASSWORD = __ENV.PASSWORD || "";

export const options = {
  scenarios: {
    list_load: {
      executor: "ramping-vus",
      startVUs: 1,
      stages: [
        { duration: "30s", target: 10 },
        { duration: "1m", target: 10 },
        { duration: "10s", target: 0 },
      ],
      gracefulRampDown: "5s",
    },
  },
  thresholds: {
    // SC-006：列表接口 p95 ≤ 3s
    "http_req_duration{tag:list}": ["p(95)<3000"],
    http_req_failed: ["rate<0.01"],
  },
};

const failRate = new Rate("auth_fail");

const ENDPOINTS = [
  { path: "/nodes", tag: "nodes" },
  { path: "/domains?page_size=100", tag: "domains" },
  { path: "/services?page_size=100", tag: "services" },
  { path: "/routes?page_size=100", tag: "routes" },
  { path: "/middlewares?page_size=100", tag: "middlewares" },
];

export function setup() {
  const res = http.post(`${BASE}/api/v1/auth/login`, JSON.stringify({ username: USERNAME, password: PASSWORD }), {
    headers: { "Content-Type": "application/json" },
  });
  if (res.status !== 200) {
    failRate.add(1);
    throw new Error(`登录失败 HTTP ${res.status}: ${res.body}`);
  }
  failRate.add(0);
  return { token: res.json("access_token") };
}

export default function (data) {
  const ep = ENDPOINTS[__ITER % ENDPOINTS.length];
  group(`list ${ep.tag}`, () => {
    const res = http.get(`${BASE}/api/v1${ep.path}`, {
      headers: { Authorization: `Bearer ${data.token}` },
      tags: { list: "1", resource: ep.tag },
    });
    check(res, {
      "status 200": (r) => r.status === 200,
      "has items": (r) => {
        const body = r.json();
        return body && Array.isArray(body.items);
      },
    });
  });
}
