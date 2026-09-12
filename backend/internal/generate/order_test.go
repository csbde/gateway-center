// T062 [US3] · 生成保序单元：routers/*.yml 的 middlewares 数组顺序 == 快照传入顺序
// （快照顺序即 route_middlewares.position，FR-018/AC-005）；绑定顺序不同 → 产物不同。
package generate_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"gateway-center/backend/internal/generate"
)

func snapWithOrder(mwNames ...string) *generate.Snapshot {
	return &generate.Snapshot{
		NodeID: "n1", NodeName: "edge-1", EnvType: "test",
		Services: []generate.ServiceSnap{{
			Name: "crm", Targets: []generate.TargetSnap{{URL: "http://10.0.0.1:80", Weight: 1}},
		}},
		Middlewares: []generate.MiddlewareSnap{
			{Name: "rate-1", Type: "rate_limit", Params: map[string]any{"average": 100, "burst": 200}},
			{Name: "sec-1", Type: "security_headers", Params: map[string]any{"frame_options": "deny"}},
			{Name: "strip-1", Type: "strip_prefix", Params: map[string]any{"prefixes": []any{"/api"}}},
		},
		Routers: []generate.RouterSnap{{
			Name: "crm", Mode: "simple", DomainName: "crm.example.com", Path: "/",
			MatchType: "prefix", ServiceName: "crm", EntryPoint: "web",
			MiddlewareNames: mwNames, Priority: 10,
		}},
	}
}

func routerMiddlewares(t *testing.T, snap *generate.Snapshot) []string {
	t.Helper()
	arts, err := generate.Generate(snap)
	require.NoError(t, err)
	var content string
	for _, a := range arts {
		if a.Path == "routers/crm.yml" {
			content = string(a.Content)
		}
	}
	require.NotEmpty(t, content, "必须产出 routers/<name>.yml")
	var doc struct {
		HTTP struct {
			Routers map[string]struct {
				Middlewares []string `yaml:"middlewares"`
			} `yaml:"routers"`
		} `yaml:"http"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(content), &doc))
	return doc.HTTP.Routers["crm"].Middlewares
}

func TestMiddlewareOrderPreserved(t *testing.T) {
	// 给定顺序 rate→sec→strip 必须原样落到 YAML 数组（链式执行语义）
	assert.Equal(t, []string{"rate-1", "sec-1", "strip-1"}, routerMiddlewares(t, snapWithOrder("rate-1", "sec-1", "strip-1")))
	// 调换绑定顺序 → 产物顺序随动（顺序即语义，FR-018）
	assert.Equal(t, []string{"strip-1", "rate-1"}, routerMiddlewares(t, snapWithOrder("strip-1", "rate-1")))
}

func TestNoMiddlewaresOmitsKey(t *testing.T) {
	assert.Nil(t, routerMiddlewares(t, snapWithOrder()), "未绑定则不输出 middlewares 键")
}

func TestMiddlewareArtifactsAreSelfContained(t *testing.T) {
	arts, err := generate.Generate(snapWithOrder("rate-1"))
	require.NoError(t, err)
	seen := map[string]string{}
	for _, a := range arts {
		seen[a.Path] = string(a.Content)
	}
	for _, p := range []string{"routers/crm.yml", "services/crm.yml", "middlewares/rate-1.yml",
		"middlewares/sec-1.yml", "middlewares/strip-1.yml"} {
		assert.Contains(t, seen, p, "快照内全部资源都要有独立产物（宪法 V 自包含）")
	}
	assert.Contains(t, seen["middlewares/rate-1.yml"], "rateLimit")
}
