#!/usr/bin/env bash
# cover-pipeline.sh — 管线相关包覆盖率门槛（T091，R17，宪法 IV 合规审查）。
#
# 宪法 IV「管线不可旁路」链路上的包：validate → generate → deployer → pipeline 编排；
# state 守卫迁移、depcheck 守卫删除。这些包的测试行覆盖率须 ≥ 阈值（默认 80%）。
#
# 以 -coverpkg 归因：跑 internal/tests/cmd 全量（含 contract/integration 标签），
# 仅统计管线包自身代码的行覆盖，集成测试对管线的深度覆盖一并计入。
# 低于阈值即失败（R17/宪法 IV 合规审查未达标）。
#
# 用法：bash scripts/cover-pipeline.sh
# 覆盖：GC_COVER_THRESHOLD（默认 80）、GC_BACKEND_DIR（默认脚本相对 ../backend）
set -euo pipefail

THRESHOLD="${GC_COVER_THRESHOLD:-80}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BACKEND_DIR="${GC_BACKEND_DIR:-$SCRIPT_DIR/../backend}"

cd "$BACKEND_DIR"

# 管线相关包（模块 gateway-center/backend；宪法 IV 链路 + state/depcheck 守卫）
PIPELINE_PKGS=(
  gateway-center/backend/internal/application/pipeline
  gateway-center/backend/internal/generate
  gateway-center/backend/internal/domain/validate
  gateway-center/backend/internal/domain/diff
  gateway-center/backend/internal/domain/state
  gateway-center/backend/internal/infrastructure/deployer
  gateway-center/backend/internal/domain/depcheck
)
COVERPKG="$(IFS=,; echo "${PIPELINE_PKGS[*]}")"

PROFILE="$(mktemp)"
trap 'rm -f "$PROFILE"' EXIT

printf -- '▶ 管线覆盖率归因运行（-coverpkg 管线包，含 contract/integration 标签）…\n'
go test -tags 'contract integration' -coverprofile="$PROFILE" -covermode=atomic \
  -coverpkg="$COVERPKG" \
  ./internal/... ./tests/contract/... ./tests/integration/... ./cmd/...

# 总覆盖率（go tool cover -func 末行 total:）
TOTAL="$(go tool cover -func="$PROFILE" | awk '/^total:/ {gsub(/%/,"",$NF); print $NF}')"

if [ -z "${TOTAL:-}" ]; then
  printf -- '✗ cover-pipeline 失败：无法解析覆盖率（profile 为空？检查测试是否运行）\n' >&2
  exit 1
fi

printf -- '管线相关包覆盖率：%s%%（阈值 %s%%）\n' "$TOTAL" "$THRESHOLD"
if awk -v t="$TOTAL" -v th="$THRESHOLD" 'BEGIN { exit (t+0 < th+0) ? 1 : 0 }'; then
  printf -- '✓ cover-pipeline 通过：管线包覆盖率 %s%% ≥ %s%%（R17/宪法 IV）\n' "$TOTAL" "$THRESHOLD"
else
  printf -- '✗ cover-pipeline 失败：管线包覆盖率 %s%% < %s%%（R17/宪法 IV 合规审查未达标）\n' "$TOTAL" "$THRESHOLD" >&2
  exit 1
fi
