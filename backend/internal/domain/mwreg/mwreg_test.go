// T062 [US3] · mwreg 五类参数单元：字段级错误定位（FR-021/NFR-USE-01）与
// ToTraefik 生成语义（data-model §7 逐项）。纯函数测试，无 IO。
package mwreg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gateway-center/backend/internal/domain/mwreg"
)

func errFields(errs []mwreg.FieldError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Field)
	}
	return out
}

func TestRegistryHasFiveTypes(t *testing.T) {
	for _, typ := range []string{"security_headers", "ip_allowlist", "rate_limit", "redirect", "strip_prefix"} {
		_, err := mwreg.Get(typ)
		require.NoError(t, err, "一期必须注册 %s（FR-021）", typ)
	}
	_, err := mwreg.Get("bogus")
	require.Error(t, err)
	assert.Len(t, mwreg.Validate("bogus", nil), 1, "未知类型即字段错误（field=type）")
	assert.Equal(t, "type", mwreg.Validate("bogus", nil)[0].Field)
}

// ---- security_headers ----

func TestSecurityHeaders(t *testing.T) {
	// 合法：STS + frame_options 齐备
	assert.Empty(t, mwreg.Validate("security_headers", map[string]any{
		"sts_seconds": float64(31536000), "frame_options": "deny",
		"referrer_policy": "strict-origin-when-cross-origin",
	}))
	// sts_seconds 越界（上限 63072000 = 2 年）
	fe := mwreg.Validate("security_headers", map[string]any{"sts_seconds": float64(63072001), "frame_options": "deny"})
	assert.Equal(t, []string{"sts_seconds"}, errFields(fe))
	// 负数（连带 frame_options 必填规则也报出，逐字段全列）
	assert.Equal(t, []string{"sts_seconds", "frame_options"},
		errFields(mwreg.Validate("security_headers", map[string]any{"sts_seconds": float64(-1)})))
	// STS>0 必须带 frame_options（防半开配置）
	fe = mwreg.Validate("security_headers", map[string]any{"sts_seconds": float64(100)})
	assert.Equal(t, []string{"frame_options"}, errFields(fe))
	// 非枚举 frame_options
	assert.Equal(t, []string{"frame_options"},
		errFields(mwreg.Validate("security_headers", map[string]any{"frame_options": "sometimes"})))
	// 非枚举 referrer_policy
	assert.Equal(t, []string{"referrer_policy"},
		errFields(mwreg.Validate("security_headers", map[string]any{"referrer_policy": "aggressive"})))
	// 全空 params 合法（全部可选，默认不强制加密）
	assert.Empty(t, mwreg.Validate("security_headers", map[string]any{}))
}

func TestSecurityHeadersToTraefik(t *testing.T) {
	conf, err := mwreg.ToTraefik("security_headers", map[string]any{
		"sts_seconds": float64(31536000), "sts_include_subdomains": true, "frame_options": "deny",
	})
	require.NoError(t, err)
	h := conf["headers"].(map[string]any)
	assert.EqualValues(t, 31536000, h["stsSeconds"])
	assert.Equal(t, true, h["stsIncludeSubdomains"])
	assert.Equal(t, true, h["frameDeny"])
	assert.Equal(t, true, h["contentTypeNosniff"], "缺省开启 nosniff")

	conf, err = mwreg.ToTraefik("security_headers", map[string]any{"frame_options": "sameorigin"})
	require.NoError(t, err)
	h = conf["headers"].(map[string]any)
	assert.EqualValues(t, 0, h["stsSeconds"], "未启用 STS 时显式置 0")
	assert.Equal(t, "frame-ancestors 'self'", h["contentSecurityPolicy"], "sameorigin 走 CSP 表达")
}

// ---- ip_allowlist ----

func TestIPAllowlist(t *testing.T) {
	assert.Empty(t, mwreg.Validate("ip_allowlist", map[string]any{
		"cidrs": []any{"203.0.113.0/24", "10.20.30.40", "2001:db8::/32"},
	}))
	// 空列表 / 非列表
	assert.Equal(t, []string{"cidrs"}, errFields(mwreg.Validate("ip_allowlist", map[string]any{})))
	assert.Equal(t, []string{"cidrs"}, errFields(mwreg.Validate("ip_allowlist", map[string]any{"cidrs": "1.2.3.4"})))
	// 非法项定位到下标（cidrs[i]）
	fe := mwreg.Validate("ip_allowlist", map[string]any{"cidrs": []any{"10.0.0.0/8", "not-an-ip"}})
	assert.Equal(t, []string{"cidrs[1]"}, errFields(fe))
	// 上限 100
	big := make([]any, 101)
	for i := range big {
		big[i] = "10.0.0.1"
	}
	assert.Equal(t, []string{"cidrs"}, errFields(mwreg.Validate("ip_allowlist", map[string]any{"cidrs": big})))
}

func TestIPAllowlistToTraefikNormalizes(t *testing.T) {
	conf, err := mwreg.ToTraefik("ip_allowlist", map[string]any{"cidrs": []any{"10.20.30.40", "203.0.113.0/24"}})
	require.NoError(t, err)
	al := conf["ipAllowList"].(map[string]any)
	// 单 IP 补全 /32（Traefik sourceRange 仅收 CIDR）
	assert.Equal(t, []string{"10.20.30.40/32", "203.0.113.0/24"}, al["sourceRange"])
}

// ---- rate_limit ----

func TestRateLimit(t *testing.T) {
	assert.Empty(t, mwreg.Validate("rate_limit", map[string]any{
		"average": float64(100), "burst": float64(200), "period": "10s",
	}))
	// average/burst 缺失或 <1（US3-AC1：速率阈值必须 ≥1）
	fe := mwreg.Validate("rate_limit", map[string]any{})
	assert.Equal(t, []string{"average", "burst"}, errFields(fe))
	// burst < average
	fe = mwreg.Validate("rate_limit", map[string]any{"average": float64(50), "burst": float64(10)})
	assert.Equal(t, []string{"burst"}, errFields(fe))
	// 非白名单 period
	fe = mwreg.Validate("rate_limit", map[string]any{"average": 1, "burst": 1, "period": "90s"})
	assert.Equal(t, []string{"period"}, errFields(fe))
	// Go int 亦被接受（数组合 JSON float64 同权）
	assert.Empty(t, mwreg.Validate("rate_limit", map[string]any{"average": 1, "burst": 1}))
	// 一期仅 ip 维度
	fe = mwreg.Validate("rate_limit", map[string]any{"average": 1, "burst": 1, "source": "user"})
	assert.Equal(t, []string{"source"}, errFields(fe))
}

func TestRateLimitToTraefik(t *testing.T) {
	conf, err := mwreg.ToTraefik("rate_limit", map[string]any{"average": float64(100), "burst": float64(200)})
	require.NoError(t, err)
	rl := conf["rateLimit"].(map[string]any)
	assert.EqualValues(t, 100, rl["average"])
	assert.Equal(t, "1s", rl["period"], "period 缺省 1s")
	assert.NotNil(t, rl["sourceCriterion"], "必须按来源 IP 计数")
}

// ---- redirect ----

func TestRedirect(t *testing.T) {
	assert.Empty(t, mwreg.Validate("redirect", map[string]any{"scheme": "https"}))
	assert.Empty(t, mwreg.Validate("redirect", map[string]any{"host": "www.example.com"}))
	// scheme 与 host 至少一项（二选一语义）
	fe := mwreg.Validate("redirect", map[string]any{"permanent": true})
	assert.Equal(t, []string{"scheme"}, errFields(fe))
	// 非白名单 scheme
	assert.Equal(t, []string{"scheme"}, errFields(mwreg.Validate("redirect", map[string]any{"scheme": "ftp"})))
}

// ---- strip_prefix ----

func TestStripPrefix(t *testing.T) {
	assert.Empty(t, mwreg.Validate("strip_prefix", map[string]any{"prefixes": []any{"/api", "/v2"}}))
	assert.Equal(t, []string{"prefixes"}, errFields(mwreg.Validate("strip_prefix", map[string]any{})))
	fe := mwreg.Validate("strip_prefix", map[string]any{"prefixes": []any{"/ok", "bad"}})
	assert.Equal(t, []string{"prefixes[1]"}, errFields(fe))
}

func TestStripPrefixToTraefik(t *testing.T) {
	conf, err := mwreg.ToTraefik("strip_prefix", map[string]any{"prefixes": []any{"/api"}})
	require.NoError(t, err)
	sp := conf["stripPrefix"].(map[string]any)
	assert.Equal(t, []string{"/api"}, sp["prefixes"])
	assert.Equal(t, false, sp["forceSlash"])
}

// ---- 命名约束（Traefik 资源 ID） ----

func TestValidSlug(t *testing.T) {
	for _, ok := range []string{"a", "strict-csp", "rate-1", strings63()} {
		assert.True(t, mwreg.ValidSlug(ok), ok)
	}
	for _, bad := range []string{"", "A-bad", "1rate", "_x", "a b", "a_b", "a." + strings63()} {
		assert.False(t, mwreg.ValidSlug(bad), bad)
	}
}

func strings63() string {
	s := "a"
	for range 62 {
		s += "b"
	}
	return s // 63 字符，恰好合规（上限 63）
}
