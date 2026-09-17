-- 0003_certificates_enhancement.sql：增强证书库（向 NPM 学习，支持独立证书资产、证书别名与多域名引用）。
-- +goose Up
ALTER TABLE certificates ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates ADD COLUMN IF NOT EXISTS node_id UUID REFERENCES gateway_nodes(id);
ALTER TABLE certificates ALTER COLUMN domain_id DROP NOT NULL;
DROP INDEX IF EXISTS certificates_domain_uniq;
CREATE INDEX IF NOT EXISTS certificates_domain_id_idx ON certificates (domain_id);
CREATE INDEX IF NOT EXISTS certificates_node_id_idx ON certificates (node_id);

-- +goose Down
DROP INDEX IF EXISTS certificates_node_id_idx;
DROP INDEX IF EXISTS certificates_domain_id_idx;
ALTER TABLE certificates DROP COLUMN IF EXISTS node_id;
ALTER TABLE certificates DROP COLUMN IF EXISTS name;
