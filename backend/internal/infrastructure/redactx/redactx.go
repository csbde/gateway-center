// Package redactx 提供集中脱敏能力（T003/T017 共用；research R7、宪法 VIII）。
// 判定「敏感键名」的规则只写这一处，日志 Handler、审计 before/after、Diff 预览全部复用，
// 避免逐字段遗漏。
package redactx

import "strings"

// MaskValue 是脱敏后的占位值（审计中该字段值即为 "****"，quickstart V-4④）。
const MaskValue = "****"

// sensitiveKeys 为小写、去分隔符后的键名子串匹配集合。
var sensitiveKeys = []string{
	"password", "passwd", "token", "authorization", "privatekey", "privkey",
	"secret", "apikey", "accesskey", "credential", "cert", "key",
}

// allowKeys 是包含敏感子串但本身不敏感的白名单（如 fingerprint/base_url 含 "key"/"url"）。
var allowKeys = []string{
	"fingerprint", "keyid", "key_version", "monkey", "keyboard", "purgekey",
	"baseurl", "callbackurl", "issuerurl",
}

// IsSensitiveKey 判定字段名是否携带明文秘密。
func IsSensitiveKey(name string) bool {
	norm := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(name))
	for _, allow := range allowKeys {
		if strings.Contains(norm, allow) {
			return false
		}
	}
	for _, s := range sensitiveKeys {
		if strings.Contains(norm, s) {
			return true
		}
	}
	return false
}

// RedactMap 深拷贝并脱敏 map（用于审计 before/after 与 Diff 输出）。
func RedactMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if IsSensitiveKey(k) {
			out[k] = MaskValue
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return RedactMap(t)
	case []any:
		arr := make([]any, len(t))
		for i, e := range t {
			arr[i] = redactValue(e)
		}
		return arr
	default:
		return v
	}
}

// RedactText 扫描任意文本（日志消息、Diff 字符串）中的明文私钥块与 Bearer 令牌并替换。
// 覆盖「日志行里出现 PEM」的场景（NFR-SEC-01 / quickstart V-4④ grep 零命中）。
func RedactText(s string) string {
	if strings.Contains(s, "-----BEGIN") {
		return MaskValue
	}
	for _, prefix := range []string{"Bearer ", "bearer "} {
		if i := strings.Index(s, prefix); i >= 0 {
			return s[:i+len(prefix)] + MaskValue
		}
	}
	return s
}
