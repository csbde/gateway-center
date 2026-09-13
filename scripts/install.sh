#!/usr/bin/env bash
# Gateway Center 安装/初始化脚本（适用于 linux/amd64 发布包）
#
# 职责（仅幂等初始化，不做破坏性操作、不自动启动服务）：
#   1) 首次运行时生成 .env（含随机 GC_MASTER_KEY，宪法 VIII：主密钥仅落本地文件）
#   2) 运行 migrate-seed：建表（幂等）+ 创建初始 Super Admin
#   3) 打印启动命令
#
# 用法：
#   ./install.sh
# 依赖：openssl（生成主密钥）、PostgreSQL 可达（GC_DATABASE_URL 指向的库）
set -euo pipefail

cd "$(dirname "$0")"

ENV_FILE=".env"

# --- 1) 生成/补全 .env ---
if [ ! -f "$ENV_FILE" ]; then
  : > "$ENV_FILE"
fi

ensure_env() {  # key default
  local key="$1" default="$2"
  if ! grep -q "^${key}=" "$ENV_FILE" 2>/dev/null; then
    echo "${key}=${default}" >> "$ENV_FILE"
  fi
}

if ! grep -q '^GC_MASTER_KEY=' "$ENV_FILE" 2>/dev/null; then
  MASTER_KEY="$(openssl rand -hex 32)"
  echo "GC_MASTER_KEY=${MASTER_KEY}" >> "$ENV_FILE"
  echo "• 已生成 GC_MASTER_KEY 并写入 ${ENV_FILE}（请离线备份，丢失即无法恢复凭证）"
fi

ensure_env GC_DATABASE_URL 'postgres://gc:gc@localhost:5432/gc?sslmode=disable'
ensure_env GC_ADDR ':8080'
ensure_env GC_INITIAL_ADMIN_USERNAME 'admin'
ensure_env GC_INITIAL_ADMIN_PASSWORD 'ChangeMe-Strong-1'

# 发布包内自带前端产物
export GC_SPA_DIR="$(pwd)/dist"

# --- 2) 加载 .env 并初始化 ---
set -a
# shellcheck disable=SC1091
. ./"$ENV_FILE"
set +a

echo "==> 运行 migrate-seed（建表 + 初始超管，幂等）..."
./gateway-center migrate-seed

# --- 3) 完成提示 ---
cat <<EOF

✅ 初始化完成。

下一步：
  1) （可选）先起数据面：
       docker compose -f deploy/compose/docker-compose.yml up -d postgres traefik sample-crm
  2) 启动控制平面（已内置托管前端 dist/）：
       ./gateway-center serve --addr \${GC_ADDR}
  3) 浏览器打开 http://<host>:8080 ，用初始超管登录：
       用户名: \${GC_INITIAL_ADMIN_USERNAME}
       密码:   \${GC_INITIAL_ADMIN_PASSWORD}
  4) 在 UI 注册第一个 Traefik 节点：
       base_url  = http://localhost:8081   # Traefik 只读 API（compose 映射 8081→8080）
       deploy_root = <与 Traefik 同主机 bind 的 dynamic 目录绝对路径>

配置见 .env；修改后重启 serve 生效。
EOF
