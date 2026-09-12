// Package handlers HTTP 控制器层（宪章 XII：Controller 仅参数装配/响应写出，业务在 application）。
// handlers.go 是各资源 handler 的共享助手（actor/错误写出/列表解析）。
package handlers

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/api/middleware"
	"gateway-center/backend/internal/api/queryutil"
	"gateway-center/backend/internal/application/approvalsvc"
	"gateway-center/backend/internal/application/authsvc"
	"gateway-center/backend/internal/application/certsvc"
	"gateway-center/backend/internal/application/credsvc"
	"gateway-center/backend/internal/application/dashboardsvc"
	"gateway-center/backend/internal/application/domainsvc"
	"gateway-center/backend/internal/application/mwsvc"
	"gateway-center/backend/internal/application/nodesvc"
	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/application/routesvc"
	"gateway-center/backend/internal/application/servicesvc"
	"gateway-center/backend/internal/application/settingsvc"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"github.com/go-chi/chi/v5"
)

// Handler 全部资源控制器的依赖集（router.go 构造一次注入）。
type Handler struct {
	Auth        *authsvc.Service
	Nodes       *nodesvc.Service
	Domains     *domainsvc.Service
	Certs       *certsvc.Service
	Credentials *credsvc.Service
	Dash        *dashboardsvc.Service
	Services    *servicesvc.Service
	Routes      *routesvc.Service
	Middlewares *mwsvc.Service
	Versions    *pipeline.VersionService
	Deploys     *pipeline.DeployService
	Settings    *settingsvc.Service
	Approvals   *approvalsvc.Service
	Vers        *pgstore.VersionRepo
	Deps        *pgstore.DeploymentRepo
	wg          sync.WaitGroup // 在途部署（graceful shutdown 等待）
}

// WaitBackground 阻塞至全部后台部署结束（serve 关停时调用）。
func (h *Handler) WaitBackground() { h.wg.Wait() }

func actorOf(r *http.Request) nodesvc.Actor {
	id, username, _ := middleware.ActorFrom(r.Context())
	return nodesvc.Actor{ID: id, Username: username, IP: clientIP(r),
		UserAgent: r.UserAgent(), RequestID: middleware.RequestIDFrom(r.Context())}
}

func actorID(r *http.Request) string {
	id, _, _ := middleware.ActorFrom(r.Context())
	return id
}

// principalOf 给需要角色判定/service 层再校验的用例（高级模式 T060）。
func principalOf(r *http.Request) routesvc.Principal {
	id, _, role := middleware.ActorFrom(r.Context())
	return routesvc.Principal{ID: id, Role: string(role)}
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// writeErr 统一错误出口：request_id 注入 + 敏感信息不外泄（Internal 已处理）。
func writeErr(w http.ResponseWriter, r *http.Request, e *httperr.APIError) {
	httperr.Write(w, r, e, middleware.RequestIDFrom(r.Context()))
}

// listQ 解析列表参数（page/page_size/q/sort/node_id 白名单）。
func listQ(w http.ResponseWriter, r *http.Request) (pgstore.ListQuery, bool) {
	q, e := queryutil.Parse(r)
	if e != nil {
		writeErr(w, r, e)
		return q, false
	}
	return q, true
}

func urlParam(r *http.Request, key string) string { return chi.URLParam(r, key) }

func internalOf(err error) *httperr.APIError { return httperr.Internal(err) }

func notFoundOrInternal(err error, what string) *httperr.APIError {
	if errors.Is(err, pgstore.ErrNotFound) {
		return httperr.NotFound(what)
	}
	return httperr.Internal(err)
}

func requestIDOf(r *http.Request) string { return middleware.RequestIDFrom(r.Context()) }

func badRequest(err error) *httperr.APIError {
	return httperr.ValidationFailed("请求体解析失败: "+err.Error(), httperr.Detail{Hint: "仅接受已知字段的 JSON"})
}

func nowUTC() *time.Time { t := time.Now().UTC(); return &t }
