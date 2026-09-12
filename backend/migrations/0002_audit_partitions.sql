-- 0002_audit_partitions.sql：审计表按月 RANGE 分区 + append-only 强制（T079，FR-038/NFR-AUD-01/宪法 X）。
-- 0001 已建 audit_logs PARTITION BY RANGE (occurred_at) + DEFAULT 分区；
-- 本迁移补建按月具体分区（数据落月分区便于归档/裁剪），并以触发器强制仅追加
-- （REVOKE 对非 owner 生效；触发器对 owner 也生效——BEFORE UPDATE/DELETE RAISE）。

-- +goose Up

-- 按月分区：前 1 月 + 未来 24 月（覆盖约 2 年；超期时段落 DEFAULT 兜底）。
-- 首次部署 DEFAULT 为空，CREATE PARTITION OF 无冲突。
-- +goose StatementBegin
DO $$
DECLARE
  start_month date := date_trunc('month', now())::date;
  d date;
  i int;
BEGIN
  FOR i IN -1..24 LOOP
    d := (start_month + make_interval(months => i))::date;
    EXECUTE format(
      'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_logs FOR VALUES FROM (%L) TO (%L)',
      'audit_logs_' || to_char(d, 'YYYY_MM'), d, (d + interval '1 month')::date
    );
  END LOOP;
END $$;
-- +goose StatementEnd

-- 宪法 X：append-only。REVOKE 限制非 owner 角色；触发器为 owner 兜底（owner 绕过 GRANT 但不绕过触发器）。
REVOKE UPDATE, DELETE ON audit_logs FROM PUBLIC;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_logs_append_only() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'audit_logs 为仅追加表（宪法 X / FR-038），禁止 UPDATE/DELETE';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- 父表触发器自动传播至所有子分区（含未来新建月分区）。
CREATE TRIGGER audit_logs_no_update_delete
  BEFORE UPDATE OR DELETE ON audit_logs
  FOR EACH ROW EXECUTE FUNCTION audit_logs_append_only();

-- +goose Down

DROP TRIGGER IF EXISTS audit_logs_no_update_delete ON audit_logs;
DROP FUNCTION IF EXISTS audit_logs_append_only();
-- 月分区数据保留（Down 仅解除 append-only 强制，不丢弃审计数据）。
