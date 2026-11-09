# ==============================================================================
# SettleCore Monorepo Build, Verification & Chaos Orchestration Makefile
# ==============================================================================

SHELL := /bin/bash
.DEFAULT_GOAL := help

CMAKE_PRESET ?= release
COMPOSE := docker compose
UV := uv

.PHONY: help up down build-engine build-services build-contracts build-web \
        test test-contracts test-engine test-services \
        bench bench-charts demo-capture demo-render demo-gif \
        lint fmt migrate-up migrate-down sqlc chaos-reorg clean

help: ## Display available build, test, benchmark, chaos, and video targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

up: ## Start full local stack (PostgreSQL 18, Anvil, RPC proxies, Services, Web, Prometheus, Grafana)
	$(COMPOSE) up -d --build

down: ## Stop all containers and remove anonymous volumes
	$(COMPOSE) down --remove-orphans

build-engine: ## Configure and build C++17 CLOB shared/static libraries and benchmarks (CMAKE_PRESET=release)
	cmake --preset $(CMAKE_PRESET) -S engine
	cmake --build --preset $(CMAKE_PRESET)

build-services: ## Compile all 7 Go 1.27 backend binaries with cgo linked against libsettlecore_engine
	cd services && CGO_ENABLED=1 go build -trimpath -o bin/gateway ./cmd/gateway
	cd services && CGO_ENABLED=0 go build -trimpath -o bin/watcher ./cmd/watcher
	cd services && CGO_ENABLED=0 go build -trimpath -o bin/relayer ./cmd/relayer
	cd services && CGO_ENABLED=0 go build -trimpath -o bin/settlement ./cmd/settlement
	cd services && CGO_ENABLED=0 go build -trimpath -o bin/market-maker ./cmd/market-maker
	cd services && CGO_ENABLED=0 go build -trimpath -o bin/reconciler ./cmd/reconciler
	cd services && CGO_ENABLED=1 go build -trimpath -o bin/replay-cli ./cmd/replay-cli

build-contracts: ## Compile Solidity 0.8.38 EscrowVault and test suites via Foundry
	cd contracts && forge build --sizes

build-web: ## Build Next.js 16.4 trading and chaos dashboard
	cd web && npm run build

test: test-contracts test-engine test-services ## Run all unit, fuzz, invariant, and race-detector test suites

test-contracts: ## Run Foundry unit, fuzz (1,000 runs), and invariant suites
	cd contracts && forge test -vvv

test-engine: ## Build and run C++17 GoogleTest suite under ASan + UBSan
	cmake --preset asan-ubsan -S engine
	cmake --build --preset asan-ubsan
	ctest --preset asan-ubsan --output-on-failure

test-services: ## Run Go 1.27 test suite with race detector enabled
	cd services && CGO_ENABLED=1 go test -race -v ./...

bench: ## Execute C++17 1M-order throughput and HdrHistogram tail-latency benchmarks
	bash scripts/bench.sh

bench-charts: ## Convert raw benchmark JSON/HDR CSV outputs into dark/light SVGs under docs/assets/benchmarks/
	cd benchmarks/charts && $(UV) run python generate_charts.py \
		--input-dir ../results \
		--output-dir ../../docs/assets/benchmarks

demo-capture: ## Record live Next.js interactions at 2560x1440 @ 60fps via Playwright
	cd demo-video && npm run capture

demo-render: ## Transcode captures and render 1920x1080 @ 60fps launch MP4 (NVENC -> QSV -> libx264)
	bash scripts/render-video.sh --mode mp4

demo-gif: ## Render launch video and export <5 MB animated WebP loop to docs/assets/media/settlecore-demo.webp
	bash scripts/render-video.sh --mode all

lint: ## Run static analysis across Solidity, C++17, Go 1.27, Next.js, Python charts, and Demo Video
	cd contracts && forge fmt --check
	cd services && golangci-lint run ./...
	cd web && npm run lint
	cd benchmarks/charts && $(UV) run ruff check .
	cd demo-video && npm run typecheck

fmt: ## Format Solidity, C++17, Go 1.27, Python, and TypeScript sources
	cd contracts && forge fmt
	find engine/include engine/src engine/tests engine/benchmarks -type f \( -name '*.hpp' -o -name '*.h' -o -name '*.cpp' \) -exec clang-format -i {} +
	cd services && gofmt -s -w .
	cd benchmarks/charts && $(UV) run ruff format .

migrate-up: ## Apply all PostgreSQL 18 schema migrations
	migrate -path services/migrations -database "$${DATABASE_URL:-postgres://settlecore:settlecore_local_dev@localhost:5432/settlecore?sslmode=disable}" up

migrate-down: ## Roll back the most recent PostgreSQL 18 schema migration
	migrate -path services/migrations -database "$${DATABASE_URL:-postgres://settlecore:settlecore_local_dev@localhost:5432/settlecore?sslmode=disable}" down 1

sqlc: ## Regenerate type-safe Go 1.27 database queries from services/queries/*.sql
	cd services && sqlc generate

chaos-reorg: ## Trigger deterministic 3-block Anvil reorg drill against live watcher/ledger
	bash scripts/chaos/trigger-reorg.sh

clean: ## Remove build artifacts, CMake directories, compiled binaries, and temporary video renders
	rm -rf engine/build contracts/out contracts/cache services/bin web/.next demo-video/test-results
