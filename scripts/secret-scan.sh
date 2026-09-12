#!/usr/bin/env bash
# secret-scan.sh — 明文密钥泄漏扫描（T089，NFR-SEC-01，宪法 VIII）。
#
# 扫描运行时产物（日志、生成的 dynamic 配置、Diff 样本、响应捕获），
# 命中以下任一模式即失败（预期零命中）：
#   -----BEGIN          私钥/证书 PEM 头——生成的 dynamic 配置不得内联私钥
#   token=              日志/URL 中的 token 赋值
#   authorization:      日志中的认证头值
#
# 仅扫运行时产物，不扫源码（源码中 -----BEGIN 系 redactor/证书处理逻辑的匹配串，
# 非泄漏）。路径可经环境变量覆盖：GC_LOG_DIR / DEPLOY_ROOT / DIFF_SAMPLES / RESP_SAMPLES。
#
# 用法：bash scripts/secret-scan.sh
set -euo pipefail

PATTERN='-----BEGIN|token=|authorization:'
FAIL=0

scan() {
  local label="$1" path="$2"
  if [ ! -e "$path" ]; then
    printf -- '- %s：路径不存在，跳过（%s）\n' "$label" "$path"
    return 0
  fi
  local hits
  hits=$(grep -rEn -e "$PATTERN" --include='*.log' --include='*.yml' --include='*.yaml' --include='*.json' --include='*.txt' "$path" 2>/dev/null || true)
  if [ -n "$hits" ]; then
    printf -- '✗ %s：命中明文密钥模式\n' "$label" >&2
    printf -- '%s\n' "$hits" >&2
    FAIL=1
  else
    printf -- '✓ %s：零命中\n' "$label"
  fi
}

scan "日志"       "${GC_LOG_DIR:-./logs}"
scan "动态配置"   "${DEPLOY_ROOT:-./deploy/traefik/dynamic}"
scan "Diff 样本"  "${DIFF_SAMPLES:-./tmp/diff-samples}"
scan "响应捕获"   "${RESP_SAMPLES:-./tmp/responses}"

if [ "$FAIL" -ne 0 ]; then
  printf -- '✗ secret-scan 失败：运行时产物发现明文密钥模式（NFR-SEC-01 违规）\n' >&2
  exit 1
fi
printf -- '✓ secret-scan 通过：运行时产物零明文密钥命中\n'
