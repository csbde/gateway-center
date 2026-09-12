// gate.go：production 部署审批闸（T077，FR-037 / R15 ApproveGate）。
// 实现 pipeline.Gate（鸭子类型，无需导入 pipeline——签名与 pipeline.Gate.Check 一致）：
// production 节点必须携带与版本匹配的已批准 release_request；非 production 放行
// （test/staging/development 可直发，但 ready/confirmed 验证不可跳——由 Deploy 前置闸保证）。
package approvalsvc

import (
	"context"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

// ProductionGate production 环境部署审批闸。
type ProductionGate struct {
	approvals *pgstore.ApprovalRepo
}

func NewProductionGate(approvals *pgstore.ApprovalRepo) *ProductionGate {
	return &ProductionGate{approvals: approvals}
}

// Check production 节点须携带已批准且与版本匹配的 approval_id，否则 403（FR-037）。
// 自批守卫已在审批环节（approvalsvc.review）+ DB CHECK 双层强制，此处仅校验「存在且匹配」。
func (g *ProductionGate) Check(ctx context.Context, node *domain.GatewayNode, versionID string, approvalID *string, _ string) *httperr.APIError {
	if node.EnvType != domain.EnvProduction {
		return nil // 非 production 直发；验证不可跳由 Deploy 前置 ready/confirmed 闸
	}
	if approvalID == nil || *approvalID == "" {
		return httperr.Forbidden("生产环境发布必须携带已批准的 approval_id（release_requests 审批链，FR-037）")
	}
	rr, err := g.approvals.ApprovedForVersion(ctx, versionID)
	if err != nil {
		return httperr.Forbidden("该配置版本无已批准的发布申请：需先经 release_requests 审批链批准后携带 approval_id 发布")
	}
	if rr.ID != *approvalID {
		return httperr.Forbidden("approval_id 与该版本已批准的发布申请不匹配")
	}
	return nil
}
