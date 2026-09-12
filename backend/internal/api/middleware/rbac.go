// RBAC 认证与角色守卫（T016，research R6、宪法 IX）。
// 每请求复核用户当前角色与 status（降权/禁用即时生效）；RequireRoles 与
// contracts/README.md 权限矩阵逐端点对齐（T078 矩阵化时在此接线）。
package middleware

import (
	"context"
	"net/http"
	"strings"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/tokensx"
)

type Principal struct {
	UserID, Username string
	Role             domain.Role
}

func (p Principal) Can(advanced, production bool) bool {
	return domain.RoleCan(p.Role, advanced, production)
}

// ActorFrom 返回写审计用的 actor（中间件链在认证后取）。
func ActorFrom(ctx context.Context) (id, username string, role domain.Role) {
	if p, ok := ctx.Value(CtxPrincipal).(Principal); ok {
		return p.UserID, p.Username, p.Role
	}
	return "", "", ""
}

// Authenticate Bearer 校验 → 解析 JWT → 复核 DB 角色/status。
// optional=true 用于混合端点（当前实现仅全量保护 /api/v1，/auth/* 除外）。
func Authenticate(tm *tokensx.Manager, users *pgstore.UserRepo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || token == "" {
				httperr.Write(w, r, httperr.Unauthenticated(), RequestIDFrom(r.Context()))
				return
			}
			claims, err := tm.Parse(token)
			if err != nil {
				httperr.Write(w, r, httperr.Unauthenticated(), RequestIDFrom(r.Context()))
				return
			}
			u, err := users.Get(r.Context(), claims.UserID) // 每请求复核（R6）
			if err != nil || u.Status != "active" {
				httperr.Write(w, r, httperr.Unauthenticated(), RequestIDFrom(r.Context()))
				return
			}
			p := Principal{UserID: u.ID, Username: u.Username, Role: u.Role}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), CtxPrincipal, p)))
		})
	}
}

// RequireRoles 路由组级角色守卫；拒绝时 403 FORBIDDEN（FR-036/US6-AC1）。
func RequireRoles(roles ...domain.Role) func(http.Handler) http.Handler {
	allow := map[domain.Role]bool{}
	for _, r := range roles {
		allow[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := r.Context().Value(CtxPrincipal).(Principal)
			if !ok || !allow[p.Role] {
				httperr.Write(w, r, httperr.Forbidden(roleNames(roles)), RequestIDFrom(r.Context()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireWritable：业务实体增删改最低 developer（Viewer 一切写入口 403，US6-AC1）。
var RequireWritable = RequireRoles(domain.RoleDeveloper, domain.RoleGatewayAdmin, domain.RoleSuperAdmin)

// 治理动作（高级模式/回滚/审批/删除/发布 production）：gateway_admin+
var (
	GatewayAdminOrAbove = RequireRoles(domain.RoleGatewayAdmin, domain.RoleSuperAdmin)
	SuperAdminOnly      = RequireRoles(domain.RoleSuperAdmin)
	AnyAuthenticated    = func(h http.Handler) http.Handler { return h }
)

func roleNames(roles []domain.Role) string {
	parts := make([]string, len(roles))
	for i, r := range roles {
		parts[i] = string(r)
	}
	return "需要角色 " + strings.Join(parts, "/")
}
