// Package api 路由装配（T026，宪章 XII：Controller→Application→Domain→Generate→Deployer 单向链）。
// RBAC 与 contracts/README.md 权限矩阵逐组对齐（T078 矩阵化测试复核本表）。
package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"gateway-center/backend/internal/api/handlers"
	"gateway-center/backend/internal/api/middleware"
)

// NewRouter 装配 /api/v1 全部契约端点。auth 为 middleware.Authenticate 实例化结果（serve 构造）。
func NewRouter(h *handlers.Handler, auth func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(chimw.Timeout(60 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/api/v1", func(r chi.Router) {
		// —— 认证（公开）——
		r.Post("/auth/login", h.Login)
		r.Post("/auth/refresh", h.Refresh)

		// —— 以下全部需 Bearer（角色每请求复核，R6）——
		r.Group(func(r chi.Router) {
			r.Use(auth)
			r.Post("/auth/logout", h.Logout)
			r.Get("/me", h.Me)

			// 读：任何认证角色（viewer+）
			r.Get("/nodes", h.ListNode)
			r.Get("/nodes/{id}", h.GetNode)
			r.Get("/nodes/{id}/state", h.NodeState)
			r.Get("/domains", h.ListDomain)
			r.Get("/domains/{id}", h.GetDomain)
			r.Get("/services", h.ListService)
			r.Get("/services/{id}", h.GetService)
			r.Get("/services/{id}/targets", h.ListTargets)
			r.Get("/routes", h.ListRoute)
			r.Get("/routes/{id}", h.GetRoute)
			r.Get("/nodes/{id}/versions", h.ListVersions)
			r.Get("/versions/{id}", h.GetVersion)
			r.Get("/versions/{id}/diff", h.DiffVersion)
			r.Get("/deployments", h.ListDeployments)
			r.Get("/deployments/{id}", h.GetDeployment)
			r.Get("/settings", h.GetSettings)

			// 业务实体增删改 + 管线：developer+（Viewer 零写入口，US6-AC1；
			// production 发布/高级模式等更细门禁在服务层与 Gate 中强制）
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireWritable)
				r.Post("/nodes", h.CreateNode)
				r.Put("/nodes/{id}", h.UpdateNode)
				r.Post("/nodes/{id}/enable", h.EnableNode)
				r.Post("/nodes/{id}/disable", h.DisableNode)
				r.Delete("/nodes/{id}", h.DeleteNode)

				r.Post("/domains", h.CreateDomain)
				r.Put("/domains/{id}", h.UpdateDomain)
				r.Post("/domains/{id}/enable", h.EnableDomain)
				r.Post("/domains/{id}/disable", h.DisableDomain)
				r.Delete("/domains/{id}", h.DeleteDomain)
				r.Post("/certificates/import", h.ImportCertificate)

				r.Post("/services", h.CreateService)
				r.Put("/services/{id}", h.UpdateService)
				r.Post("/services/{id}/enable", h.EnableService)
				r.Post("/services/{id}/disable", h.DisableService)
				r.Delete("/services/{id}", h.DeleteService)
				r.Post("/services/{id}/targets", h.AddTarget)
				r.Put("/services/{id}/targets/{targetId}", h.UpdateTarget)
				r.Delete("/services/{id}/targets/{targetId}", h.DeleteTarget)
				r.Post("/services/{id}/targets/{targetId}/enable", h.EnableTarget)
				r.Post("/services/{id}/targets/{targetId}/disable", h.DisableTarget)

				r.Post("/routes", h.CreateRoute)
				r.Put("/routes/{id}", h.UpdateRoute)
				r.Post("/routes/{id}/enable", h.EnableRoute)
				r.Post("/routes/{id}/disable", h.DisableRoute)
				r.Delete("/routes/{id}", h.DeleteRoute)

				// 管线：验证/生成版本/发布 developer+（production 审批门禁在 DeployService Gate）
				r.Post("/nodes/{id}/validate", h.ValidateNode)
				r.Post("/nodes/{id}/versions", h.CreateVersion)
				r.Post("/deployments", h.CreateDeployment)
			})

			// 治理动作：gateway_admin+（回滚=FR-031 权限矩阵；归档=US7 处置流）
			r.Group(func(r chi.Router) {
				r.Use(middleware.GatewayAdminOrAbove)
				r.Post("/deployments/rollback", h.Rollback)
				r.Post("/routes/{id}/archive", h.ArchiveRoute)
			})

			// 不可变记录：任何角色 DELETE 即 403（FR-032）
			r.Delete("/versions/{id}", h.DeleteVersionRejected)
			r.Delete("/deployments/{id}", h.DeleteDeploymentRejected)

			// 平台设置：super_admin（FR-004）
			r.Group(func(r chi.Router) {
				r.Use(middleware.SuperAdminOnly)
				r.Put("/settings", h.UpdateSettings)
			})
		})
	})
	return r
}
