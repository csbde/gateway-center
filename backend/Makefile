# T007 · 后端任务入口（quickstart §3：make test / test-pipeline / vet / lint）
GO      ?= go
PKGS    := ./...
COVER   := coverage.out

.PHONY: build test test-unit test-contract test-integration test-pipeline vet lint cover run-migrate secret-scan

build:
	$(GO) build -o bin/gateway-center ./cmd/gateway-center

# 全量：unit + contract + integration + 明文密钥扫描（testcontainers 需要 Docker，research R17；NFR-SEC-01）
test: test-unit test-contract test-integration secret-scan

test-unit:
	$(GO) test ./internal/... ./cmd/...

test-contract:
	$(GO) test -tags contract ./tests/contract/...

test-integration:
	$(GO) test -tags integration -count=1 ./tests/integration/...

# 管线专项：无旁路（三 422 路径）/ 原子性 / 回滚 / 校验失败标记（宪法 IV、R17）
test-pipeline:
	$(GO) test -tags integration -run 'TestPipeline|TestRollback|TestNoBypass|TestAtomic' -count=1 -v ./tests/integration/...

vet:
	$(GO) vet $(PKGS)

lint:
	golangci-lint run

cover:
	$(GO) test -tags 'contract integration' -coverprofile=$(COVER) -covermode=atomic $(PKGS)
	$(GO) tool cover -func=$(COVER) | tail -1

# 迁移 + 初始 Super Admin（无需 goose 二进制时：migrate-seed 自带幂等迁移，见 seed.go 注释）
run-migrate:
	$(GO) run ./cmd/gateway-center migrate-seed

# 明文密钥扫描（NFR-SEC-01/宪法 VIII）：扫描运行时产物零命中 -----BEGIN/token=/authorization:
secret-scan:
	@cd .. && bash scripts/secret-scan.sh
