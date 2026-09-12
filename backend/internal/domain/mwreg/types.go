// 一期五类中间件（FR-021）：security_headers / ip_allowlist / rate_limit / redirect / strip_prefix
// params 校验规则逐项对应 data-model §7 表格（AC/Edge Case 依据）。
package mwreg

import (
	"fmt"
	"net"
	"strings"
	"time"
)

func init() {
	registerSecurityHeaders()
	registerIPAllowlist()
	registerRateLimit()
	registerRedirect()
	registerStripPrefix()
}

// ---- security_headers ----

var frameOptions = map[string]bool{"deny": true, "sameorigin": true, "allow-from": true}

func registerSecurityHeaders() {
	Register(Definition{
		Type: "security_headers",
		Validate: func(p map[string]any) []FieldError {
			var errs []FieldError
			if v, ok := num(p, "sts_seconds"); ok {
				if v < 0 || v > 63072000 {
					errs = append(errs, FieldError{"sts_seconds", "取值范围 0–63072000 秒"})
				}
			}
			fo := str(p, "frame_options")
			if fo != "" && !frameOptions[fo] {
				errs = append(errs, FieldError{"frame_options", "必须为 deny / sameorigin / allow-from"})
			}
			sts, _ := num(p, "sts_seconds")
			if sts != 0 && fo == "" {
				errs = append(errs, FieldError{"frame_options", "启用 STS 时 frame_options 必填"})
			}
			if rp := str(p, "referrer_policy"); rp != "" {
				switch rp {
				case "no-referrer", "no-referrer-when-downgrade", "origin", "origin-when-cross-origin", "same-origin", "strict-origin", "strict-origin-when-cross-origin", "unsafe-url", "":
				default:
					errs = append(errs, FieldError{"referrer_policy", "不在 Referrer-Policy 允许值中"})
				}
			}
			return errs
		},
		ToTraefik: func(p map[string]any) (map[string]any, error) {
			fo := str(p, "frame_options")
			sh := map[string]any{
				"contentTypeNosniff": boolDefault(p, "content_type_nosniff", true),
				"browserXssFilter":   true,
			}
			sts, _ := num(p, "sts_seconds")
			if sts > 0 {
				sh["stsSeconds"] = sts
				sh["stsIncludeSubdomains"] = boolDefault(p, "sts_include_subdomains", false)
			} else {
				sh["stsSeconds"] = 0
			}
			switch fo {
			case "deny":
				sh["frameDeny"] = true
			case "sameorigin":
				sh["contentSecurityPolicy"] = "frame-ancestors 'self'"
			case "allow-from":
				sh["contentSecurityPolicy"] = fmt.Sprintf("frame-ancestors %s", str(p, "frame_allow_from"))
			}
			if rp := str(p, "referrer_policy"); rp != "" {
				sh["referrerPolicy"] = rp
			}
			return map[string]any{"headers": sh}, nil
		},
	})
}

// ---- ip_allowlist ----

func registerIPAllowlist() {
	Register(Definition{
		Type: "ip_allowlist",
		Validate: func(p map[string]any) []FieldError {
			cidrs, ok := strList(p, "cidrs")
			if !ok || len(cidrs) == 0 || len(cidrs) > 100 {
				return []FieldError{{"cidrs", "需要 1–100 条 CIDR 或 IP"}}
			}
			var errs []FieldError
			for i, c := range cidrs {
				if err := validCIDROrIP(c); err != nil {
					errs = append(errs, FieldError{fmt.Sprintf("cidrs[%d]", i), err.Error()})
				}
			}
			return errs
		},
		ToTraefik: func(p map[string]any) (map[string]any, error) {
			cidrs, _ := strList(p, "cidrs")
			normalized := make([]string, 0, len(cidrs))
			for _, c := range cidrs {
				normalized = append(normalized, normalizeCIDR(c))
			}
			return map[string]any{"ipAllowList": map[string]any{"sourceRange": normalized}}, nil
		},
	})
}

func validCIDROrIP(s string) error {
	if _, _, err := net.ParseCIDR(s); err == nil {
		return nil
	}
	if net.ParseIP(s) != nil {
		return nil
	}
	return fmt.Errorf("%q 不是合法 IPv4/IPv6 CIDR 或 IP", s)
}

// normalizeCIDR：单 IP 补全为主机段，Traefik sourceRange 仅接受 CIDR。
func normalizeCIDR(s string) string {
	if strings.Contains(s, "/") {
		return s
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.To4() != nil {
			return s + "/32"
		}
		return s + "/128"
	}
	return s
}

// ---- rate_limit ----

var allowedPeriods = func() map[string]int64 {
	m := map[string]int64{}
	for _, s := range []string{"1s", "2s", "5s", "10s", "15s", "20s", "30s", "1m"} {
		d, _ := time.ParseDuration(s)
		m[s] = int64(d / time.Second)
	}
	return m
}()

func registerRateLimit() {
	Register(Definition{
		Type: "rate_limit",
		Validate: func(p map[string]any) []FieldError {
			var errs []FieldError
			avg, ok := num(p, "average")
			if !ok || avg < 1 {
				errs = append(errs, FieldError{"average", "速率阈值必须 ≥1（US3-AC1）"})
			}
			burst, ok := num(p, "burst")
			if !ok || burst < 1 {
				errs = append(errs, FieldError{"burst", "burst 必须 ≥1"})
			} else if ok && avg >= 1 && burst < avg {
				errs = append(errs, FieldError{"burst", "burst 必须 ≥ average"})
			}
			period := str(p, "period")
			if period == "" {
				period = "1s"
			}
			if _, ok := allowedPeriods[period]; !ok {
				errs = append(errs, FieldError{"period", "必须为 1s/2s/5s/10s/15s/20s/30s/1m 之一"})
			}
			switch str(p, "source") {
			case "", "ip":
			default:
				errs = append(errs, FieldError{"source", "一期仅支持 ip 维度"})
			}
			return errs
		},
		ToTraefik: func(p map[string]any) (map[string]any, error) {
			avg, _ := num(p, "average")
			burst, _ := num(p, "burst")
			period := str(p, "period")
			if period == "" {
				period = "1s"
			}
			return map[string]any{"rateLimit": map[string]any{
				"average": avg, "burst": burst, "period": period,
				"sourceCriterion": map[string]any{"ipStrategy": map[string]any{"depth": 1}},
			}}, nil
		},
	})
}

// ---- redirect（http→https 等，FR-021）----

func registerRedirect() {
	Register(Definition{
		Type: "redirect",
		Validate: func(p map[string]any) []FieldError {
			scheme := str(p, "scheme")
			host := str(p, "host")
			if scheme == "" && host == "" {
				return []FieldError{{"scheme", "scheme 与 host 至少填一项（二选一语义）"}}
			}
			if scheme != "" && scheme != "https" && scheme != "http" {
				return []FieldError{{"scheme", "仅支持 https / http"}}
			}
			if host != "" && !ValidSlug(strings.ReplaceAll(host, ".", "-")) && net.ParseIP(host) == nil {
				if !strings.Contains(host, ".") {
					return []FieldError{{"host", "host 须为合法域名或 IP"}}
				}
			}
			return nil
		},
		ToTraefik: func(p map[string]any) (map[string]any, error) {
			rd := map[string]any{"permanent": boolDefault(p, "permanent", false)}
			if s := str(p, "scheme"); s != "" {
				rd["scheme"] = s
			}
			if h := str(p, "host"); h != "" {
				rd["host"] = h
			}
			if ph := str(p, "path"); ph != "" {
				rd["path"] = ph
			}
			return map[string]any{"redirectRegex": rd}, nil
		},
	})
}

// ---- strip_prefix ----

func registerStripPrefix() {
	Register(Definition{
		Type: "strip_prefix",
		Validate: func(p map[string]any) []FieldError {
			prefixes, ok := strList(p, "prefixes")
			if !ok || len(prefixes) == 0 || len(prefixes) > 10 {
				return []FieldError{{"prefixes", "需要 1–10 条前缀"}}
			}
			var errs []FieldError
			for i, s := range prefixes {
				if !strings.HasPrefix(s, "/") {
					errs = append(errs, FieldError{fmt.Sprintf("prefixes[%d]", i), "前缀必须以 / 开头"})
				}
			}
			return errs
		},
		ToTraefik: func(p map[string]any) (map[string]any, error) {
			prefixes, _ := strList(p, "prefixes")
			return map[string]any{"stripPrefix": map[string]any{
				"prefixes":   prefixes,
				"forceSlash": false,
			}}, nil
		},
	})
}
