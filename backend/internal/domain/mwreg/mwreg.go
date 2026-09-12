// Package mwreg 是 Middleware 类型注册表（T056 提前至 Foundational：generate/validate 依赖）。
// NFR-MNT-01 扩展点：新增类型 = 注册 Validate/ToTraefik 两方法，管线与表不动（research R9）。
package mwreg

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// FieldError 定位到具体字段的校验错误（FR-021/NFR-USE-01）。
type FieldError struct {
	Field string `json:"field"` // 如 "cidrs[2]"
	Hint  string `json:"hint"`
}

// Definition 是一类 middleware 的参数契约。
type Definition struct {
	Type      string
	Validate  func(params map[string]any) []FieldError
	ToTraefik func(params map[string]any) (map[string]any, error)
}

var registry = map[string]Definition{}

func Register(d Definition) {
	if _, dup := registry[d.Type]; dup {
		panic("duplicate middleware type: " + d.Type)
	}
	registry[d.Type] = d
}

// Types 返回已注册类型清单（API 文档/前端 schema 对齐用）。
func Types() []string {
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	return out
}

func Get(mwType string) (Definition, error) {
	d, ok := registry[mwType]
	if !ok {
		return Definition{}, fmt.Errorf("未知的中间件类型 %q（一期支持：%s）", mwType, strings.Join(Types(), "/"))
	}
	return d, nil
}

// Validate 按类型校验参数（未知类型即错误）。
func Validate(mwType string, params map[string]any) []FieldError {
	d, err := Get(mwType)
	if err != nil {
		return []FieldError{{Field: "type", Hint: err.Error()}}
	}
	return d.Validate(params)
}

// ToTraefik 生成 Traefik 动态配置片段。
func ToTraefik(mwType string, params map[string]any) (map[string]any, error) {
	d, err := Get(mwType)
	if err != nil {
		return nil, err
	}
	return d.ToTraefik(params)
}

// ---- 参数存取辅助 ----

func str(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

func boolDefault(params map[string]any, key string, def bool) bool {
	if v, ok := params[key].(bool); ok {
		return v
	}
	return def
}

// num 兼容 JSON 解析出的 float64 与 Go int。
func num(params map[string]any, key string) (int64, bool) {
	switch v := params[key].(type) {
	case float64:
		return int64(v), true
	case int:
		return int64(v), true
	case int64:
		return v, true
	}
	return 0, false
}

func strList(params map[string]any, key string) ([]string, bool) {
	raw, ok := params[key].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, it := range raw {
		s, ok := it.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

var cidrOrIP = func(s string) bool {
	if _, ipnet, err := net.ParseCIDR(s); err == nil {
		return ipnet != nil
	}
	return net.ParseIP(s) != nil
}

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// ValidSlug 供 domain 层校验资源命名（Traefik 资源名约束）。
func ValidSlug(s string) bool { return slugRe.MatchString(s) }
