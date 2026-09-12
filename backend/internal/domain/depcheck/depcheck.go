// Package depcheck 资源依赖图查询（US7/T083，FR-039/AC-015）。
// Domain/Service/Middleware 删除前置检查：引用该资源的未归档路由清单。
// 处置路径 Disable → Archive → 软删（FR-039）：archived 路由视为已处置，
// 不计入引用阻断（pgstore.ReferencingRoutes 已过滤 status <> 'archived'）。
//
// 本包为 domain 层纯函数，不依赖 api/httperr（宪法 XII 分层）；引用方摘要以
// RouteRef 中性结构暴露，由 application 层转为 httperr.Detail 写入 409 响应。
package depcheck

import "gateway-center/backend/internal/domain"

// RouteRef 引用方路由摘要（删除阻断 409 details 的数据源，V-7 逐条列出）。
type RouteRef struct {
	ID   string
	Name string
}

// RouteRefs 从路由实体提取引用摘要。
// archived 路由已由 pgstore.ReferencingRoutes 过滤，此处仅做视图转换。
func RouteRefs(routes []domain.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))
	for i := range routes {
		out = append(out, RouteRef{ID: routes[i].ID, Name: routes[i].Name})
	}
	return out
}

// AnyRefs 存在引用方即需阻断删除（FR-039；空清单方可进入软删）。
func AnyRefs(refs []RouteRef) bool { return len(refs) > 0 }
