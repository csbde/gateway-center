# 运维手册（Runbook）

Gateway Center 一期运维操作：备份、主密钥轮换、故障排查。
宪法约束见 [`../.specify/memory/constitution.md`](../.specify/memory/constitution.md)。

---

## 1. 备份

### 1.1 PostgreSQL（业务态 + 审计 + 版本快照）

```bash
# 全量逻辑备份（含审计分区）
pg_dump --format=custom \
  --host=localhost --port=5432 --username=gc \
  gc > backup/gc-$(date +%Y%m%d-%H%M%S).dump

# 恢复
pg_restore --clean --if-exists -d gc < backup/gc-YYYYMMDD-HHMMSS.dump
```

- `config_versions`（含 `snapshot` / `generated_artifacts` 大字段）与 `audit_logs` 是不可删的不可变记录（宪法 V/X），必须纳入备份。
- 审计表按月 RANGE 分区（`0002_audit_partitions.sql`），`pg_dump` 自动包含分区子表。

### 1.2 动态配置（Traefik data plane）

`<deploy_root>/dynamic/` 下是平台原子下发的 YAML。每版本产物已落 `config_versions.generated_artifacts`（自包含快照），**动态目录本身无需单独备份**——可从任意历史版本重建。如需保留运行时实态，按节点 tar 即可：

```bash
tar czf backup/dynamic-$(date +%Y%m%d).tgz -C <deploy_root> dynamic/
```

### 1.3 主密钥

`GC_MASTER_KEY` 是所有加密 secret 的根密钥。**丢失即全部凭证/私钥不可恢复**。

- 存于密钥管理设施（Vault / KMS / 密封信封），不入 Git、不入数据库。
- 备份两份离线介质，与 PostgreSQL 备份分开保管。
- 轮换流程见 §2。

---

## 2. 主密钥轮换（GC_MASTER_KEY）

> ⚠️ 此操作会解密并重加密全部存储 secret。**必须在维护窗口、后端停服状态下进行**。旧密钥丢失则无法轮换。

### 受影响表（AES-256-GCM，密文格式 `nonce||ciphertext`）

| 表 | 字段 | 内容 |
|---|---|---|
| `secret_credentials` | `data_encrypted` | DNS Provider API 凭证 |
| `certificates` | `private_key_encrypted` | 导入证书私钥 |
| `gateway_nodes` | `api_auth_encrypted` | Traefik API Basic 凭据 |

### 流程

1. **停服**：`make` 无停止目标，直接终止 `serve` 进程（或置维护页）。

2. **生成新密钥**：
   ```bash
   export GC_MASTER_KEY_NEW=$(openssl rand -hex 32)
   ```

3. **重加密**（用 cryptox 旧→新；平台未内置 rotate-key 命令，以一次性 Go 脚本执行）：

   ```go
   // cmd/tools/rotate-key（示例骨架，复用 internal/infrastructure/cryptox）
   old, _ := cryptox.NewCipher(decodeHex(oldKeyHex))
   new, _ := cryptox.NewCipher(decodeHex(newKeyHex))
   for _, t := range encryptedRows {        // 三张表的加密字段
       plain, err := old.Decrypt(t.cipher)  // 旧密钥解密
       if err != nil { return err }          // 解密失败=旧密钥不对，中止
       re, _ := new.Encrypt(plain)           // 新密钥重加密
       db.Update(t.table, t.id, t.field, re)
       // Fingerprint(plain) 轮换前后不变，用于核对
   }
   ```

   - **指纹核对**：`cryptox.Fingerprint(plaintext)` 是明文 SHA-256 前 4 字节 hex；轮换前后每行指纹 MUST 不变（`secret_credentials.data_fingerprint` 已存，比对即可确认密文正确迁移）。

4. **切换密钥**：更新部署环境的 `GC_MASTER_KEY=$GC_MASTER_KEY_NEW`。

5. **重启并验证**：
   ```bash
   go run ./cmd/gateway-center serve --addr :8080
   # 核对：凭证 verify 端点、域名证书视图、节点探测 online
   curl -H "Authorization: Bearer $TOKEN" localhost:8080/api/v1/credentials/<id>/verify
   ```

6. **归档旧密钥**：轮换成功后方可销毁旧密钥的在线副本（保留离线备份至下一轮换周期）。

> 一次轮换只换一把密钥；不要并行多轮换。若中途失败，保持旧密钥在线、回滚已重加密的行（用新密钥解密、旧密钥重加密）。

---

## 3. 故障排查

### 3.1 节点 offline

- **现象**：Dashboard 节点计数 `offline`；节点详情 `last_online_at` 停滞。
- **排查**：
  1. Traefik 容器是否运行：`docker ps | grep traefik`。
  2. `base_url` 是否可达：`curl -u <api_auth> <base_url>/api/overview`。
  3. 连续 3 次探测失败才转 offline（FR-003）；偶发失败会先 `degraded`。
- **恢复**：修复 Traefik/API 可达性；下一轮探测（默认 30s）自动转回 `online`。`last_online_at` 保留。

### 3.2 配置漂移（Drift）

- **现象**：节点详情 `drift=true`、双列「平台意图 vs 网关加载确认」出现差异（红标）。
- **含义**：网关实际加载集合 ≠ 平台最近成功版本（宪法 XI：发布成功≠生效）。
- **处置**：
  1. 查看 `drift_detail`（资源集合差异）。
  2. 若为人为改动 dynamic 目录：平台重新 **validate + 发布**（漂移期间 deploy 要求先重新 validate）。
  3. 成功发布 + 校验后自动清除 drift。
- 漂移可见延迟 ≤ 探测周期（默认 30s）。

### 3.3 发布失败 / 校验未通过

- **现象**：Deployment `status=failed`，`verification_result` 含差异集合。
- **管线不可旁路**（宪法 IV）：未 validate / 未确认 / 节点离线 → 一律 `422`，无捷径。
- **处置**：
  1. 看 `verification_result`：部署后 1s/2s/4s 退避比对集合，失败会标注差异资源。
  2. 若 `last_known_good_version` 附带（回滚失败时）→ 用该版本回滚。
  3. 修复后重新走完整管线（validate → generate → diff → confirmed deploy → verify）。
- **节点禁用冻结部署**：节点 `disabled` 时禁止一切部署（`PIPELINE_BLOCKED`）。

### 3.4 依赖删除被阻止（DEPENDENCY_BLOCKED）

- **现象**：删除 Domain/Service/Middleware 返回 `409 DEPENDENCY_BLOCKED`，`details` 列出引用方路由。
- **处置**（宪法 VI）：先解除引用（解绑或归档引用路由），再删除。前端弹出依赖阻断对话框并引导。
- 资源不可物理删除；走 Disable → Archive → 软删处置流。

### 3.5 乐观锁冲突（409 ConcurrentEdit）

- **现象**：并发编辑同一资源返回 `409`，含 `row_version`。
- **处置**：前端刷新取最新版本后重试；版本/部署/审计记录的 DELETE 一律 `403`（宪法 V，不可删）。

---

## 4. 审计与合规

- **仅追加**（宪法 X）：`audit_logs` 按月 RANGE 分区，`UPDATE/DELETE` 权限已被 DB revoke。所有写操作经 `AuditRecorder` 脱敏后落库。
- **脱敏**：`token/password/private_key/authorization` 等键在日志中强制打码（`SensitiveAttrHandler`）。
- **明文密钥扫描**：`make secret-scan` 扫描运行时产物零命中 `-----BEGIN|token=|authorization:`。
- **管线覆盖率**：`make cover-pipeline` 断言管线相关包 ≥80%（R17/宪法 IV 合规审查）。
- **审计检索**：`GET /api/v1/audit-logs`（actor/action/resource_type/from/to 分页），`viewer`+ 可读。

---

## 5. 关键命令速查

| 场景 | 命令 |
|---|---|
| 启动数据面 | `docker compose -f deploy/compose/docker-compose.yml up -d` |
| 迁移 + 超管 | `go run ./cmd/gateway-center migrate-seed` |
| 启动后端 | `go run ./cmd/gateway-center serve --addr :8080` |
| 全量测试 | `cd backend && make test-all` |
| 管线专项 | `cd backend && make test-pipeline` |
| 覆盖率门槛 | `cd backend && make cover-pipeline` |
| 密钥扫描 | `cd backend && make secret-scan` |
| 规模压测种子 | `go run ./cmd/tools/seed --nodes 10 --routes-per-node 1000` |
