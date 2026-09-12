//go:build contract

// T070 · US4 契约测试：/credentials 端点全链脱敏断言（FR-035/宪章 VIII/NFR-SEC-01）。
// 凭证明文值在任何响应体（create/get/list/rotate/verify/delete）中零命中；
// 仅 fingerprint 回显；RBAC：viewer 写入口 403。
package contract

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uniqueSecret 每次测试生成唯一明文，避免跨用例残留干扰 grep。
const secretValue = "live-credential-secret-7Qx2mk"

func TestCredentials_NeverEchoValueAndRBAC(t *testing.T) {
	a := newApp(t)
	tok := sa(t, a)

	// ---- create ----
	body := map[string]any{
		"name":     "cf-prod-contract",
		"kind":     "dns_provider",
		"provider": "cloudflare",
		"data":     map[string]any{"api_token": secretValue},
	}
	r := a.do(t, tok, "POST", "/api/v1/credentials", body)
	require.Equal(t, 201, r.status, "create: %s", r.raw)
	require.NotContains(t, string(r.raw), secretValue, "create 响应不得回显明文")
	fp, ok := r.body["fingerprint"].(string)
	require.True(t, ok && len(fp) >= 8, "create 须返回 fingerprint: %s", r.raw)
	id, _ := r.body["id"].(string)
	require.NotEmpty(t, id)

	// ---- get ----
	r = a.do(t, tok, "GET", "/api/v1/credentials/"+id, nil)
	require.Equal(t, 200, r.status)
	require.NotContains(t, string(r.raw), secretValue, "get 响应不得回显明文")
	_, hasData := r.body["data"]
	_, hasEnc := r.body["data_encrypted"]
	assert.False(t, hasData || hasEnc, "get 不得含 data/data_encrypted 字段: %s", r.raw)

	// ---- list ----
	r = a.do(t, tok, "GET", "/api/v1/credentials?page_size=100", nil)
	require.Equal(t, 200, r.status)
	require.NotContains(t, string(r.raw), secretValue, "list 响应不得回显明文")

	// ---- rotate（PUT）指纹变更，明文仍不外泄 ----
	r = a.do(t, tok, "PUT", "/api/v1/credentials/"+id, map[string]any{
		"name":     "cf-prod-contract",
		"kind":     "dns_provider",
		"provider": "cloudflare",
		"data":     map[string]any{"api_token": "rotated-secret-9Kz"},
	})
	require.Equal(t, 200, r.status, "rotate: %s", r.raw)
	require.NotContains(t, string(r.raw), "rotated-secret-9Kz")
	assert.NotEqual(t, fp, r.body["fingerprint"], "轮换后指纹应变")
	assert.Equal(t, id, r.body["id"], "轮换不改 id（绑定关系不变）")

	// ---- verify 仅回原因 ----
	r = a.do(t, tok, "POST", "/api/v1/credentials/"+id+"/verify", nil)
	require.Equal(t, 200, r.status, "verify: %s", r.raw)
	require.NotContains(t, string(r.raw), "rotated-secret-9Kz")
	assert.NotEmpty(t, r.body["ok"], "verify 须返回 ok: %s", r.raw)
	assert.NotEmpty(t, r.body["reason"], "verify 须返回 reason: %s", r.raw)

	// ---- RBAC：viewer 写入口 403 ----
	r = a.do(t, viewer(t, a), "POST", "/api/v1/credentials", body)
	requireErrorBody(t, r, 403, "FORBIDDEN")

	// ---- delete（未引用）204 ----
	r = a.do(t, tok, "DELETE", "/api/v1/credentials/"+id, nil)
	require.Equal(t, 204, r.status, "delete: %s", r.raw)
}
