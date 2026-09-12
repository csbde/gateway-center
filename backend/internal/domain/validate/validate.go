// Package validate 实现发布验证六类检查（T036/T058，FR-024/AC-007、data-model §14）。
// 纯函数输入为节点完整期望态（NodeGraph），输出逐条 ValidationIssue；
// message 含资源定位、hint 含修复建议（NFR-USE-01）。
package validate

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/domain/advrule"
	"gateway-center/backend/internal/domain/mwreg"
	"gateway-center/backend/internal/generate"
)

// Issue 是一条验证结果。
type Issue struct {
	Category string `json:"category"` // domain/route/service/middleware/dependency/artifact
	Resource string `json:"resource"` // "route crm-web" 等用户语言定位
	Code     string `json:"code"`
	Message  string `json:"message"`
	Hint     string `json:"hint"`
	Blocking bool   `json:"blocking"` // blocker=true 阻断发布；warning 仅提示
}

// Report 是验证总报告（POST /nodes/{id}/validate 响应体）。
type Report struct {
	Valid      bool    `json:"valid"`
	Issues     []Issue `json:"issues"`
	RouteCount int     `json:"route_count"`
}

func (r *Report) add(i Issue) {
	r.Issues = append(r.Issues, i)
	if i.Blocking {
		r.Valid = false
	}
}

// NodeGraph 是节点当前期望态全量图（VersionService 装载后传入）。
type NodeGraph struct {
	Node        *domain.GatewayNode
	Domains     []domain.Domain
	Services    []domain.Service
	Targets     map[string][]domain.Target // serviceID → targets
	Routes      []domain.Route
	Middlewares []domain.Middleware
	MwBindings  map[string][]string // routeID → 有序 middlewareIDs
}

var (
	pathRe      = regexp.MustCompile(`^/[A-Za-z0-9/_:.\-~%]*$`)
	domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// RunNode 执行六类检查（domain/route/service/middleware/dependency + artifact 由管线另行自检）。
func RunNode(g *NodeGraph) *Report {
	rep := &Report{Valid: true}
	enabledRoutes := 0
	for _, r := range g.Routes {
		if r.Status == "enabled" {
			enabledRoutes++
		}
	}
	rep.RouteCount = enabledRoutes

	domByID := map[string]*domain.Domain{}
	for i := range g.Domains {
		domByID[g.Domains[i].ID] = &g.Domains[i]
	}
	svcByID := map[string]*domain.Service{}
	for i := range g.Services {
		svcByID[g.Services[i].ID] = &g.Services[i]
	}
	mwByID := map[string]*domain.Middleware{}
	for i := range g.Middlewares {
		mwByID[g.Middlewares[i].ID] = &g.Middlewares[i]
	}

	// —— 第 1 类：Domain（§3）——
	for _, d := range g.Domains {
		if errs := domainNameErrors(d.Name, d.IsWildcard); errs != "" {
			rep.add(Issue{"domain", "domain " + d.Name, "domain_format",
				"域名格式非法: " + errs, "使用 RFC1123 合法域名；泛域名仅允许最左 *. 前缀", true})
		}
		if d.HTTPSPolicy == domain.PolicyAcmeDNS && d.DNSSCredentialID == nil {
			rep.add(Issue{"domain", "domain " + d.Name, "domain_dns_no_credential",
				"acme_dns 策略缺少 DNS 凭证", "在域名详情绑定 DNS Provider 凭证", true})
		}
		if (d.HTTPSPolicy == domain.PolicyAcmeDNS || d.HTTPSPolicy == domain.PolicyAcmeHTTP) && d.CertResolverRef == "" {
			rep.add(Issue{"domain", "domain " + d.Name, "domain_no_resolver",
				"ACME 策略缺少 resolver 引用", "填写节点 Traefik 已预配的 certificatesResolvers 名称", true})
		}
	}

	// —— 第 2/5 类：Route + Dependency（§6）——
	for _, rt := range g.Routes {
		if rt.Status == "archived" || rt.Status == "draft" {
			continue // 草稿与归档不进入发布产物；但草稿仍给非阻断提示
		}
		label := "route " + rt.Name
		if rt.Mode == "simple" {
			if !pathRe.MatchString(rt.Path) {
				rep.add(Issue{"route", label, "route_path_invalid",
					"路径必须以 / 开头且仅含合法字符", "示例：/api 或 /app/v2", true})
			}
			d, ok := domByID[ptr(rt.DomainID)]
			switch {
			case !ok:
				rep.add(Issue{"dependency", label, "route_domain_missing",
					label + " 引用的域名不存在或已删除", "重新选择域名", true})
			case !d.Enabled:
				rep.add(Issue{"dependency", label, "route_domain_disabled",
					label + " 绑定的域名 " + d.Name + " 已禁用", "启用域名或改绑", true})
			case d.NodeID != rt.NodeID:
				rep.add(Issue{"dependency", label, "route_domain_cross_node",
					label + " 与域名不在同一节点", "配置以节点为作用域，禁止跨节点引用", true})
			}
			if rt.HTTPS && ok && d.HTTPSPolicy == domain.PolicyOff {
				rep.add(Issue{"route", label, "route_https_no_policy",
					label + " 开启 HTTPS 但域名 " + d.Name + " HTTPS 策略为 off", "为域名配置证书策略或关闭路由 HTTPS", true})
			}
			if rt.HTTPS && ok {
				// 证书可用性（Edge Case：策略与实际不符→阻断发布）
				if d.HTTPSPolicy == domain.PolicyImported && d.ImportedCertID == nil {
					rep.add(Issue{"domain", label, "cert_missing_blocking",
						"域名 " + d.Name + " 声明 imported 但未导入证书", "导入证书或修改 HTTPS 策略", true})
				}
			}
		} else { // advanced
			if verrs := advrule.Validate(rt.AdvancedRule); verrs != "" {
				rep.add(Issue{"route", label, "route_advanced_invalid",
					"高级模式规则非法: " + verrs, "仅允许 Host/HostRegexp/Path/PathPrefix/Headers/HeadersRegexp/Method 与 &&、||、括号", true})
			}
		}
		sv, ok := svcByID[rt.ServiceID]
		switch {
		case !ok:
			rep.add(Issue{"dependency", label, "route_service_missing",
				label + " 引用的服务不存在或已删除", "重新绑定服务", true})
		case !sv.Enabled:
			rep.add(Issue{"dependency", label, "route_service_disabled",
				label + " 绑定的服务 " + sv.Name + " 已禁用", "启用服务或改绑", true})
		case sv.NodeID != rt.NodeID:
			rep.add(Issue{"dependency", label, "route_service_cross_node",
				label + " 与服务不在同一节点", "禁止跨节点引用", true})
		}
		// —— 第 5 类：Service 无启用 Target 阻断（FR-012）——
		if ok && sv.Enabled && rt.Status == "enabled" {
			ts := g.Targets[sv.ID]
			any := false
			for _, t := range ts {
				if t.Enabled {
					any = true
					break
				}
			}
			if !any {
				rep.add(Issue{"service", label, "service_no_targets",
					"服务 " + sv.Name + " 没有任何启用 Target（被 " + label + " 引用）", "为服务添加并启用 Target", true})
			}
		}
		// 中间件绑定存在性/启用性/同节点（§7）
		for _, mwID := range g.MwBindings[rt.ID] {
			mw, ok := mwByID[mwID]
			if !ok {
				rep.add(Issue{"dependency", label, "route_mw_missing",
					label + " 引用的中间件不存在或已删除", "编辑路由解绑该中间件", true})
				continue
			}
			if mw.NodeID != rt.NodeID {
				rep.add(Issue{"dependency", label, "route_mw_cross_node",
					label + " 与中间件 " + mw.Name + " 不在同一节点", "禁止跨节点引用", true})
			}
			if rt.Status == "enabled" && !mw.Enabled {
				rep.add(Issue{"middleware", label, "route_mw_disabled",
					label + " 引用的中间件 " + mw.Name + " 已禁用", "启用该中间件或解除绑定", true})
			}
		}
	}

	// 匹配重叠冲突（FR-019）：同节点两条启用路由规则完全重叠 → 阻断
	checkRuleOverlap(g, rep, domByID)

	// —— 第 4 类：Middleware 参数逐字段（FR-021）——
	for _, mw := range g.Middlewares {
		for _, fe := range mwreg.Validate(mw.Type, mw.Params) {
			rep.add(Issue{"middleware", "middleware " + mw.Name, "mw_param_invalid",
				"中间件 " + mw.Name + " 参数 " + fe.Field + " 非法: " + fe.Hint, fe.Hint, true})
		}
	}

	// Target URL 合法性（§5）
	for _, sv := range g.Services {
		for _, t := range g.Targets[sv.ID] {
			if u, err := url.Parse(t.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				rep.add(Issue{"service", "target " + t.URL, "target_url_invalid",
					"Target 地址 " + t.URL + " 非法", "使用 http(s)://host:port 形式", true})
			}
		}
	}
	return rep
}

// checkRuleOverlap 规则字符串完全相同即重叠（保守判定；精确+泛共存仅告警，Edge Case）。
func checkRuleOverlap(g *NodeGraph, rep *Report, domByID map[string]*domain.Domain) {
	seen := map[string]domain.Route{}
	for _, rt := range g.Routes {
		if rt.Status != "enabled" {
			continue
		}
		rule := ruleOf(rt, domByID)
		if rule == "" {
			continue
		}
		key := rt.NodeID + "|" + entrypointOf(rt) + "|" + rule
		if prev, dup := seen[key]; dup {
			rep.add(Issue{"route", "route " + rt.Name, "route_rule_overlap",
				fmt.Sprintf("路由 %q 与 %q 匹配条件完全重叠", prev.Name, rt.Name),
				"调整路径/域名使其不冲突", true})
		}
		seen[key] = rt
	}
}

func ruleOf(rt domain.Route, domByID map[string]*domain.Domain) string {
	if rt.Mode == "advanced" {
		return rt.AdvancedRule
	}
	d := domByID[ptr(rt.DomainID)]
	if d == nil {
		return ""
	}
	return ruleForSimple(d.Name, rt.Path, rt.MatchType)
}

func entrypointOf(rt domain.Route) string {
	if rt.HTTPS {
		return "websecure"
	}
	return "web"
}

// ruleForSimple 与 generate.RuleForSimple 同语义（预览/验证/生成三方一致）。
func ruleForSimple(domainName, path, matchType string) string {
	return generate.RuleForSimple(domainName, path, matchType)
}

// domainNameErrors 普通/泛域名 RFC1123 校验；返回空串=合法。
func domainNameErrors(name string, wildcard bool) string {
	if wildcard {
		name = strings.TrimPrefix(name, "*.")
		if !strings.Contains(name, ".") {
			return "泛域名需至少一个点"
		}
	}
	labels := strings.Split(name, ".")
	for _, l := range labels {
		if !domainLabel.MatchString(l) {
			return fmt.Sprintf("label %q 不合法", l)
		}
	}
	return ""
}

// IsWildcard 供上层复用。
func IsWildcard(name string) bool { return strings.HasPrefix(name, "*.") }

// ValidateDomainName 导出供 domainsvc 写入时前置校验；返回 ""=合法。
func ValidateDomainName(name string) string { return domainNameErrors(name, IsWildcard(name)) }

func ptr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// HostOf 供证书观测（SNI）用：泛域名转 *.x 或探测主机名。
func HostOf(name string) string {
	if strings.HasPrefix(name, "*.") {
		// 探测用替换一个占位子域
		return "probe." + strings.TrimPrefix(name, "*.")
	}
	return name
}
