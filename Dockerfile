# Gateway Center 单镜像
# 后端 serve 经 GC_SPA_DIR 在同一端口同时托管前端 SPA 与 API，
# 故仅暴露一个端口（默认 8080）即可访问「UI + API」。
#
# 构建：docker build -t gateway-center .
# 运行（需注入 GC_DATABASE_URL 与 GC_MASTER_KEY）：
#   docker run -p 8080:8080 \
#     -e GC_DATABASE_URL='postgres://gc:gc@postgres:5432/gc?sslmode=disable' \
#     -e GC_MASTER_KEY="$(openssl rand -hex 32)" \
#     gateway-center
#
# deploy_root 通过运行时挂载共享卷提供（平台只写其 dynamic/ 子树，宪法 III/IV）。

# ---- frontend ----
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- backend（linux/amd64 静态二进制）----
FROM golang:1.25 AS backend
WORKDIR /src
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/gateway-center ./cmd/gateway-center

# ---- runtime ----
FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=backend /out/gateway-center /app/gateway-center
COPY --from=frontend /src/frontend/dist /app/dist
COPY backend/migrations /app/migrations
COPY deploy /app/deploy
ENV GC_ADDR=:8080 \
    GC_SPA_DIR=/app/dist
EXPOSE 8080
ENTRYPOINT ["/app/gateway-center"]
CMD ["serve"]
