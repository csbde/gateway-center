// Package generate 将业务快照单向生成 Traefik v3 file provider YAML（T020，宪法 I）。
// 纯函数、可单测、可重放；Traefik 版本升级影响收敛在本层（NFR-MNT-01）。
// MUST NOT 存在反向解析（库内不存 YAML 事实来源，宪法 I）。
package generate

import (
	"fmt"
	"strings"

	"gateway-center/backend/internal/domain/mwreg"
	"gopkg.in/yaml.v3"
)

// Artifact 是一个生成文件：相对 dynamic/ 的路径 + 内容。
type Artifact struct {
	Path    string
	Content []byte
	Mode    uint32 // 0644；私钥材料 0600
}

// Generate 产出分文件产物：routers/services/middlewares 各资源一文件（文件名=资源名），
// tls/ 下为导入证书（公钥+部署时解密的私钥占位引用）。
func Generate(snap *Snapshot) ([]Artifact, error) {
	var out []Artifact

	for _, r := range snap.Routers {
		body, err := routerYAML(r)
		if err != nil {
			return nil, fmt.Errorf("router %q: %w", r.Name, err)
		}
		out = append(out, Artifact{Path: "routers/" + r.Name + ".yml", Content: body, Mode: 0o644})
	}
	for _, s := range snap.Services {
		body, err := serviceYAML(s)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", s.Name, err)
		}
		out = append(out, Artifact{Path: "services/" + s.Name + ".yml", Content: body, Mode: 0o644})
	}
	for _, m := range snap.Middlewares {
		body, err := middlewareYAML(m)
		if err != nil {
			return nil, fmt.Errorf("middleware %q: %w", m.Name, err)
		}
		out = append(out, Artifact{Path: "middlewares/" + m.Name + ".yml", Content: body, Mode: 0o644})
	}
	if len(snap.Certificates) > 0 {
		var certItems []map[string]string
		for _, c := range snap.Certificates {
			out = append(out, Artifact{Path: "tls/" + c.Name + ".pem", Content: []byte(c.CertPEM), Mode: 0o644})
			// 私钥以引用形式入库（artifact_files 脱敏），Deploy 时解密写同目录 .key（0600）
			out = append(out, Artifact{Path: "tls/" + c.Name + ".key", Content: []byte(SecretRefPrefix + c.PrivateKeyRef), Mode: 0o600})
			certItems = append(certItems, map[string]string{
				"certFile": "/etc/traefik/dynamic/tls/" + c.Name + ".pem",
				"keyFile":  "/etc/traefik/dynamic/tls/" + c.Name + ".key",
			})
		}
		tlsYAML, err := yaml.Marshal(map[string]any{
			"tls": map[string]any{
				"certificates": certItems,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("tls certificates yaml: %w", err)
		}
		out = append(out, Artifact{Path: "tls/certificates.yml", Content: tlsYAML, Mode: 0o644})
	}

	// 产物零明文 secret 断言（R7/R8）：除 .key 引用占位与 .pem 公钥外，任何文件不得含私钥头。
	for _, a := range out {
		if strings.HasSuffix(a.Path, ".key") || strings.HasSuffix(a.Path, ".pem") {
			continue
		}
		if strings.Contains(string(a.Content), "PRIVATE KEY") {
			return nil, fmt.Errorf("产物 %s 含明文私钥，拒绝入库（宪法 VIII）", a.Path)
		}
	}
	return out, nil
}

const SecretRefPrefix = "secret-ref:"

// RuleForSimple 合成简单模式规则（FR-014：用户零语法输入；预览与产物同源一处实现）。
func RuleForSimple(domain, path, matchType string) string {
	if matchType == "exact" {
		return fmt.Sprintf("Host(`%s`) && Path(`%s`)", domain, path)
	}
	return fmt.Sprintf("Host(`%s`) && PathPrefix(`%s`)", domain, path)
}

func routerYAML(r RouterSnap) ([]byte, error) {
	rule := r.AdvancedRule
	if r.Mode == "simple" || rule == "" {
		if r.DomainName == "" {
			return nil, fmt.Errorf("缺少域名")
		}
		rule = RuleForSimple(r.DomainName, r.Path, r.MatchType)
	}
	http := map[string]any{}
	router := map[string]any{
		"rule":        rule,
		"entryPoints": []string{r.EntryPoint},
		"service":     r.ServiceName,
		"priority":    r.Priority,
	}
	if len(r.MiddlewareNames) > 0 {
		router["middlewares"] = r.MiddlewareNames
	}
	if r.HTTPS {
		tls := map[string]any{}
		switch r.CertMode {
		case "acme_http", "acme_dns":
			if r.Resolver == "" {
				return nil, fmt.Errorf("acme 策略缺少 resolver 引用")
			}
			tls["certResolver"] = r.Resolver
		} // imported/off：空 tls 段，依赖已加载证书（SNI 匹配）
		router["tls"] = tls
	}
	http["routers"] = map[string]any{r.Name: router}
	return yaml.Marshal(map[string]any{"http": http})
}

func serviceYAML(s ServiceSnap) ([]byte, error) {
	if len(s.Targets) == 0 {
		return nil, fmt.Errorf("无可用 Target")
	}
	servers := make([]any, 0, len(s.Targets))
	for _, t := range s.Targets {
		servers = append(servers, map[string]any{"url": t.URL})
	}
	lb := map[string]any{"servers": servers}
	// 多 Target 且权重非全 1 → serversWeighted（WRR，data-model §5）
	if len(s.Targets) > 1 {
		weighted := make([]any, 0, len(s.Targets))
		allOne := true
		for _, t := range s.Targets {
			if t.Weight != 1 {
				allOne = false
			}
			weighted = append(weighted, map[string]any{"url": t.URL, "weight": max(t.Weight, 1)})
		}
		if !allOne {
			lb = map[string]any{"serversWeighted": weighted}
		}
	}
	return yaml.Marshal(map[string]any{
		"http": map[string]any{"services": map[string]any{s.Name: map[string]any{"loadBalancer": lb}}},
	})
}

func middlewareYAML(m MiddlewareSnap) ([]byte, error) {
	conf, err := mwreg.ToTraefik(m.Type, m.Params)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(map[string]any{
		"http": map[string]any{"middlewares": map[string]any{m.Name: conf}},
	})
}

// ArtifactToFiles 把产物转成 {path: content} 映射（入库 artifact_files JSONB，调用方需先脱敏）。
func ArtifactToFiles(arts []Artifact) map[string]string {
	m := make(map[string]string, len(arts))
	for _, a := range arts {
		m[a.Path] = string(a.Content)
	}
	return m
}
