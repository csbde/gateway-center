// traefikapi 单元测试：实际态资源名归一化（@file 剥离 / @internal 剔除）。
package traefikapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlatformName(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"crm-main@file", "crm-main", true}, // file provider 后缀剥离（与快照裸名比对）
		{"crm-main", "crm-main", true},      // 裸名（fake/直连）
		{"api@internal", "", false},         // Traefik 内建：治理域外
		{"dashboard@internal", "", false},
		{"@file", "", false},         // 异常空名
		{"tcp-x@tcp", "tcp-x", true}, // 非 file 后缀保留基础名（不误剔）
	}
	for _, c := range cases {
		got, ok := platformName(c.in)
		assert.Equal(t, c.ok, ok, c.in)
		assert.Equal(t, c.want, got, c.in)
	}
}

func TestHTTPRouters_NormalizesAndFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/http/routers", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"name": "api@internal"},
			{"name": "crm-main@file"},
			{"name": "other@file"},
		}})
	}))
	defer srv.Close()
	m, err := NewClient(srv.URL, "", "").HTTPRouters(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"crm-main": true, "other": true}, toSet(m))
}

func toSet(m map[string]map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}
