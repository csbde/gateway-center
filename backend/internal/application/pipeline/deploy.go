// deploy.go：DeployService——管线后半段（T038，FR-026/028/030、AC-008~010、宪章 IV/XI）。
// 强制顺序：确认闸（confirmed=false→422）→ 在线闸 → 同节点并发闸（409）→ 生产审批 Gate（US6 接线）
// → 原子落盘（FileDeployer，temp→fsync→rename）→ 运行时校验（1s/2s/4s 退避 ≤15s）→ success|failed。
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gateway-center/backend/internal/api/httperr"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/generate"
	"gateway-center/backend/internal/infrastructure/cryptox"
	"gateway-center/backend/internal/infrastructure/deployer"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/traefikapi"
)

// TraefikFactory 按节点构造只读 API client（集成测试注入 fake）。
type TraefikFactory func(n *domain.GatewayNode) (*traefikapi.Client, error)

// Gate production 审批闸（T077 接线前的可插拔点；nil=非 production 直接放行）。
type Gate interface {
	Check(ctx context.Context, node *domain.GatewayNode, versionID string, approvalID *string, actorID string) *httperr.APIError
}

type DeployService struct {
	vers    *pgstore.VersionRepo
	deploys *pgstore.DeploymentRepo
	nodes   *pgstore.NodeRepo
	certs   *pgstore.CertRepo
	cipher  *cryptox.Cipher
	dep     deployer.Deployer
	traefik TraefikFactory
	audit   *auditrec.Recorder
	gate    Gate
}

func NewDeployService(vers *pgstore.VersionRepo, deploys *pgstore.DeploymentRepo, nodes *pgstore.NodeRepo,
	certs *pgstore.CertRepo, cipher *cryptox.Cipher, dep deployer.Deployer, traefik TraefikFactory,
	audit *auditrec.Recorder, gate Gate) *DeployService {
	return &DeployService{vers: vers, deploys: deploys, nodes: nodes, certs: certs,
		cipher: cipher, dep: dep, traefik: traefik, audit: audit, gate: gate}
}

type DeployInput struct {
	NodeID     string  `json:"node_id"`
	VersionID  string  `json:"version_id"`
	Confirmed  bool    `json:"confirmed"` // false→422（AC-008：未确认绝不发布）
	ApprovalID *string `json:"approval_id,omitempty"`
}

type DeployResult struct {
	DeploymentID string         `json:"deployment_id"`
	Status       string         `json:"status"`
	Verification map[string]any `json:"verification_result,omitempty"`
}

// AsyncRunner 把执行体挂到受管理的后台（serve 优雅关停时等待；测试注入同步实现）。
type AsyncRunner func(fn func())

// Deploy 全链发布。任何闸不过都产生 failed/pending 记录之外零副作用（版本 ready 保留）。
// 门禁同步判定（422/409 即时返回），通过即 202 受理、执行体异步跑（openapi：POST /deployments→202）。
func (s *DeployService) Deploy(ctx context.Context, in DeployInput, actorID string, async AsyncRunner) (*DeployResult, *httperr.APIError) {
	node, err := s.nodes.Get(ctx, in.NodeID)
	if err != nil {
		return nil, mapNotFound(err, "节点")
	}
	// 节点禁用→冻结部署（T032）
	if !node.Enabled {
		return nil, httperr.PipelineBlocked("节点 " + node.Name + " 已禁用，禁止一切部署")
	}
	v, err := s.vers.Get(ctx, in.VersionID)
	if err != nil {
		return nil, mapNotFound(err, "配置版本")
	}
	if v.NodeID != node.ID {
		return nil, httperr.ValidationFailed("版本不属于该节点", httperr.Detail{Field: "version_id"})
	}
	// —— 无旁路闸 1：仅 ready 版本可部署（未经管线产物的直接引用即 422）——
	if v.Status != "ready" {
		return nil, httperr.PipelineBlocked("版本 v" + fmt.Sprint(v.Version) + " 状态为 " + v.Status + "，必须先生成并通过验证（ready）后方可发布")
	}
	// —— 无旁路闸 2：未确认 → 422（AC-008）——
	if !in.Confirmed {
		return nil, httperr.Newf(422, "DEPLOY_NOT_CONFIRMED", "发布前必须勾选变更确认（confirmed=true）——Diff 预览确认是管线的一部分")
	}
	// —— 无旁路闸 3：离线阻断（Edge Case：Traefik 不可达不发布）——
	if apiErr := s.assertOnline(ctx, node); apiErr != nil {
		return nil, apiErr
	}
	// —— 并发闸：同节点单活动部署（partial unique index 兜底，提前 409）——
	active, err := s.deploys.ActiveForNode(ctx, node.ID)
	if err != nil {
		return nil, httperr.Internal(err)
	}
	if active != nil {
		return nil, httperr.DeployInProgress(node.Name)
	}
	// —— 生产审批 Gate（US6 T077；当前 gate==nil 时 production 也仅记审计）——
	if s.gate != nil {
		if apiErr := s.gate.Check(ctx, node, v.ID, in.ApprovalID, actorID); apiErr != nil {
			return nil, apiErr
		}
	} else if node.EnvType == domain.EnvProduction {
		return nil, httperr.Newf(403, "FORBIDDEN", "生产环境发布必须经审批链（release_requests），当前未启用审批服务")
	}

	// 漂移期间要求先重新 validate（US5 T072：drift=true 时仅允许部署最新 ready 版本，
	// 避免硬阻断死锁——发布最新 ready 即覆盖漂移使其恢复一致）。
	if st, err := s.nodes.State(ctx, node.ID); err == nil && st.Drift {
		if latest, lerr := s.vers.LatestReady(ctx, node.ID); lerr != nil || latest.ID != v.ID {
			return nil, httperr.PipelineBlocked("节点 " + node.Name +
				" 存在配置漂移，请重新验证并生成新版本后再发布（仅允许部署最新 ready 版本以覆盖漂移）")
		}
	}

	now := time.Now().UTC()
	d := &domain.Deployment{
		NodeID: node.ID, ConfigVersionID: v.ID, Trigger: "deploy",
		Status: "pending", ConfirmedAt: &now, ConfirmedBy: &actorID, ApprovalID: in.ApprovalID,
	}
	d.SetActor(actorID)
	if err := s.deploys.Create(ctx, d); err != nil {
		if strings.Contains(err.Error(), "SQLSTATE 23505") {
			return nil, httperr.DeployInProgress(node.Name)
		}
		return nil, httperr.Internal(err)
	}
	s.audit.Record(ctx, nil, auditrec.Event{ActorID: actorID, Action: "deploy",
		ResourceType: "deployment", ResourceID: d.ID, ResourceName: fmt.Sprintf("%s v%d", node.Name, v.Version),
		After: map[string]any{"node_id": node.ID, "version_id": v.ID, "confirmed": true}})

	// 受理即 202：执行体脱离请求上下文异步运行（graceful shutdown 由 async 实现等待）
	exec := context.WithoutCancel(ctx)
	async(func() { _, _ = s.run(exec, node, v, d) })
	return &DeployResult{DeploymentID: d.ID, Status: "pending"}, nil
}

// assertOnline 离线阻断（Edge Case）：探测 Traefik API 可达性后才允许发布。
func (s *DeployService) assertOnline(ctx context.Context, node *domain.GatewayNode) *httperr.APIError {
	c, err := s.traefik(node)
	if err != nil {
		return httperr.GatewayUnreachable(node.Name, err)
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Live(pctx); err != nil {
		return httperr.GatewayUnreachable(node.Name, err)
	}
	return nil
}

// run 部署执行体：pending→validating（重新生成产物并自检）→ready→deploying（落盘）→success|failed。
func (s *DeployService) run(ctx context.Context, node *domain.GatewayNode, v *domain.ConfigVersion, d *domain.Deployment) (*DeployResult, *httperr.APIError) {
	fail := func(code, msg string) (*DeployResult, *httperr.APIError) {
		_ = s.deploys.SetStatus(ctx, d.ID, d.Status, "failed", "failed", map[string]any{"error_code": code, "error_message": msg})
		return &DeployResult{DeploymentID: d.ID, Status: "failed",
				Verification: map[string]any{"passed": false, "error_code": code}},
			httperr.Newf(502, code, msg)
	}
	// pending→validating：产物完整性自检（快照↔产物一致性 = 生成确定性的再验证）
	if err := s.deploys.SetStatus(ctx, d.ID, "pending", "validating", "validating", nil); err != nil {
		return nil, httperr.Internal(err)
	}
	d.Status = "validating"
	snap, ok := snapshotFrom(v.Snapshot)
	if !ok {
		return fail("SNAPSHOT_CORRUPT", "版本快照损坏，无法部署")
	}
	files, apiErr := s.materialize(ctx, v, snap)
	if apiErr != nil {
		return fail("ARTIFACT_INVALID", apiErr.Message)
	}
	if err := s.deploys.SetStatus(ctx, d.ID, "validating", "ready", "ready", nil); err != nil {
		return nil, httperr.Internal(err)
	}
	d.Status = "ready"
	// ready→deploying：可写性前置探测 → 原子替换
	if err := s.dep.ProbeWritable(ctx, node.DeployRoot); err != nil {
		return fail("DEPLOY_ROOT_NOT_WRITABLE", "落盘目录不可写: "+err.Error())
	}
	if err := s.deploys.SetStatus(ctx, d.ID, "ready", "deploying", "deploying", nil); err != nil {
		return nil, httperr.Internal(err)
	}
	d.Status = "deploying"
	if err := s.dep.Deploy(ctx, node.DeployRoot, files); err != nil {
		return fail("DEPLOY_WRITE_FAILED", "动态配置写入失败: "+err.Error())
	}
	// —— 部署后运行时校验（FR-030/AC-009/010，R5：1s/2s/4s 退避 ≤15s）——
	tcli, err := s.traefik(node)
	if err != nil {
		return fail("TRAEFIK_API_MISCONFIG", err.Error())
	}
	verif := verifyAgainst(ctx, tcli, snap)
	verif["expected"] = expectedCounts(snap)
	if !verif["passed"].(bool) {
		verif["last_known_good_version"] = s.lastKnownGood(ctx, node.ID)
		_ = s.deploys.SetStatus(ctx, d.ID, "deploying", "failed", "verification",
			map[string]any{"verification_result": verif, "error_code": "VERIFICATION_FAILED",
				"error_message": "配置已落盘但网关运行时与期望不一致（详见 verification_result.diff）"})
		return &DeployResult{DeploymentID: d.ID, Status: "failed", Verification: verif},
			httperr.GatewayUnreachable(node.Name, errors.New("运行时校验未通过"))
	}
	if err := s.deploys.SetStatus(ctx, d.ID, "deploying", "success", "success",
		map[string]any{"verification_result": verif}); err != nil {
		return nil, httperr.Internal(err)
	}
	d.Status = "success"
	// 成功即清除漂移并更新 actual/desired（宪章 XI；列级更新避免覆写 probesvc 探测字段）
	_ = s.nodes.UpdateDriftState(ctx, node.ID, false, nil, v.Version, v.Version)
	s.audit.Record(ctx, nil, auditrec.Event{Action: "deploy_success", ResourceType: "deployment",
		ResourceID: d.ID, ResourceName: fmt.Sprintf("%s v%d", node.Name, v.Version)})
	return &DeployResult{DeploymentID: d.ID, Status: "success", Verification: verif}, nil
}

// materialize 版本产物 → 部署文件；secret-ref 占位在最后一步解密（明文零入库，R7）。
func (s *DeployService) materialize(ctx context.Context, v *domain.ConfigVersion, snap *generate.Snapshot) ([]deployer.ArtifactFile, *httperr.APIError) {
	out := make([]deployer.ArtifactFile, 0, len(v.ArtifactFiles))
	keyRefs := map[string]string{} // certID → PEM
	for _, c := range snap.Certificates {
		cert, err := s.certs.Get(ctx, c.PrivateKeyRef)
		if err != nil {
			return nil, httperr.PipelineBlocked("证书私钥材料缺失: " + c.DomainName)
		}
		plain, err := s.cipher.Decrypt(cert.PrivateKeyEncrypted)
		if err != nil {
			return nil, httperr.Internal(err)
		}
		keyRefs[c.ID] = string(plain)
	}
	for path, content := range v.ArtifactFiles {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, ".key") {
			mode = 0o600
			if ref, ok := strings.CutPrefix(content, "secret-ref:"); ok {
				pem, found := keyRefs[ref]
				if !found {
					return nil, httperr.PipelineBlocked("产物引用的证书私钥不可解密: " + ref)
				}
				content = pem
			}
		}
		out = append(out, deployer.ArtifactFile{Path: path, Content: []byte(content), Mode: mode})
	}
	return out, nil
}

// verifyAgainst 拉取运行时集合与期望比对（routers 名称集合为准；带 1s/2s/4s 退避）。
func verifyAgainst(ctx context.Context, c *traefikapi.Client, snap *generate.Snapshot) map[string]any {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	want := map[string]bool{}
	for _, r := range snap.Routers {
		want[r.Name] = true
	}
	var last map[string]bool
	var lastErr string
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return map[string]any{"passed": false, "reason": "校验超时"}
			case <-time.After(backoffs[attempt-1]):
			}
		}
		rs, err := c.HTTPRouters(ctx)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		last = map[string]bool{}
		for name := range rs {
			last[name] = true
		}
		if setsEqual(want, last) {
			return map[string]any{"passed": true, "attempt": attempt + 1,
				"actual": namesOf(last), "traefik_checked_at": time.Now().UTC().Format(time.RFC3339)}
		}
		lastErr = ""
	}
	diff := map[string]any{"missing": missing(want, last), "unexpected": missing(last, want)}
	out := map[string]any{"passed": false, "diff": diff, "actual": namesOf(last)}
	if lastErr != "" {
		out["reason"] = lastErr
	}
	return out
}

func expectedCounts(snap *generate.Snapshot) map[string]int {
	mw := map[string]bool{}
	for _, r := range snap.Routers {
		for _, m := range r.MiddlewareNames {
			mw[m] = true
		}
	}
	svc := map[string]bool{}
	for _, r := range snap.Routers {
		svc[r.ServiceName] = true
	}
	return map[string]int{"routers": len(snap.Routers), "services": len(svc), "middlewares": len(mw)}
}

func setsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func missing(want, have map[string]bool) []string {
	var out []string
	for k := range want {
		if !have[k] {
			out = append(out, k)
		}
	}
	return out
}

func namesOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// lastKnownGood 失败时回指最近一次成功版本（Edge Case 要求 verification_result 附引导）。
func (s *DeployService) lastKnownGood(ctx context.Context, nodeID string) any {
	if v, err := s.vers.LatestSuccessByNode(ctx, nodeID); err == nil {
		return map[string]any{"version_id": v.ID, "version": v.Version}
	}
	return nil
}

func mapNotFound(err error, what string) *httperr.APIError {
	if errors.Is(err, pgstore.ErrNotFound) {
		return httperr.NotFound(what)
	}
	return httperr.Internal(err)
}
