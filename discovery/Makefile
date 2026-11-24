# Observability Platform — developer entry points. CI calls these same
# targets, so local and CI behaviour match. Run `make help`.
SHELL := /bin/bash
PLATFORM := platform
WEB := platform/web
# Explicit Go package roots: `./...` would descend into web/node_modules.
GOPKGS := ./cmd/... ./contracts/... ./internal/...
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X github.com/deeprajsshetty/observability-platform/platform/internal/api.Version=$(VERSION) -X github.com/deeprajsshetty/observability-platform/platform/internal/api.Commit=$(COMMIT)
IMAGE ?= obsplatform:$(VERSION)
DB_URL = $(shell scripts/dev-postgres.sh url)

.PHONY: help
help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n",$$1,$$2}'

.PHONY: deps
deps: ## Install pinned dependencies (go.sum, package-lock.json)
	cd $(PLATFORM) && go mod download && go mod verify
	cd $(WEB) && npm ci --no-audit --no-fund

.PHONY: demo demo-oracle demo-stop
demo: ## One command: local DB, build, migrate, seed, serve, smoke test
	scripts/demo.sh
demo-oracle: ## Same demo with Oracle Database Free as the platform store (needs Docker)
	OBS_STORE_DRIVER=oracle scripts/demo.sh
demo-stop: ## Stop the demo server and the local databases
	scripts/demo.sh stop

.PHONY: oracle-up oracle-down oracle-destroy test-oracle
oracle-up: ## Start local Oracle Database Free (Docker, 127.0.0.1:15210)
	scripts/dev-oracle.sh start
oracle-down: ## Stop local Oracle
	scripts/dev-oracle.sh stop
oracle-destroy: ## Delete the local Oracle container and its data
	scripts/dev-oracle.sh destroy
test-oracle: oracle-up ## Store contract and API end-to-end suite against Oracle
	cd $(PLATFORM) && export OBS_TEST_ORACLE_ADMIN_URL="$$(../scripts/dev-oracle.sh admin-url)" OBS_TEST_REQUIRE_ORACLE=1 OBS_TEST_REQUIRE_DB=1 && \
		go test -race -count=1 ./internal/store/... ./internal/dbengine/... && \
		OBS_TEST_STORE=oracle go test -race -count=1 ./internal/api/

.PHONY: db-up db-down db-destroy db-up-docker db-down-docker
db-up: ## Start local PostgreSQL from installed binaries (.local/pg)
	scripts/dev-postgres.sh start
db-down: ## Stop local PostgreSQL
	scripts/dev-postgres.sh stop
db-destroy: ## Delete local PostgreSQL data (asks nothing; local only)
	scripts/dev-postgres.sh destroy
db-up-docker: ## Start PostgreSQL in Docker instead (deploy/compose)
	docker compose -f deploy/compose/docker-compose.yml up -d postgres
db-down-docker: ## Stop the Docker PostgreSQL
	docker compose -f deploy/compose/docker-compose.yml down

.PHONY: migrate migrate-status seed run web-dev
migrate: ## Apply migrations to the local DB
	cd $(PLATFORM) && OBS_DATABASE_URL="$(DB_URL)" go run ./cmd/obsplatform migrate up
migrate-status: ## Show migration state of the local DB
	cd $(PLATFORM) && OBS_DATABASE_URL="$(DB_URL)" go run ./cmd/obsplatform migrate status
seed: ## Create the demo connection
	cd $(PLATFORM) && OBS_DATABASE_URL="$(DB_URL)" go run ./cmd/obsplatform seed
run: web-build ## Run the server against the local DB, serving the built UI
	cd $(PLATFORM) && OBS_DATABASE_URL="$(DB_URL)" OBS_WEB_DIR=web/dist OBS_LOG_FORMAT=text go run ./cmd/obsplatform serve
web-dev: ## Vite dev server on :5173 proxying the API on :8080
	cd $(WEB) && npm run dev

.PHONY: fmt lint typecheck test test-go test-web smoke check
fmt: ## Format Go code
	cd $(PLATFORM) && gofmt -w cmd contracts internal
lint: ## gofmt check, go vet, ESLint
	@cd $(PLATFORM) && test -z "$$(gofmt -l cmd contracts internal)" || { echo "gofmt needed:"; gofmt -l cmd contracts internal; exit 1; }
	cd $(PLATFORM) && go vet $(GOPKGS)
	cd $(WEB) && npm run lint
typecheck: ## TypeScript type check
	cd $(WEB) && npm run typecheck
test: test-go test-web ## All tests
test-go: ## Go unit, contract, migration and API integration tests (real PostgreSQL)
	mkdir -p .local
	cd $(PLATFORM) && OBS_TEST_REQUIRE_DB=$${OBS_TEST_REQUIRE_DB:-1} go test -race -count=1 -coverpkg=./internal/... -coverprofile=../.local/coverage.out $(GOPKGS)
	cd $(PLATFORM) && go tool cover -func=../.local/coverage.out | tail -1
test-web: ## Frontend component and API-client tests
	cd $(WEB) && npm test
smoke: ## End-to-end smoke test against a running server (URL=...)
	cd $(PLATFORM) && go run ./cmd/smoke -url $${URL:-http://localhost:8080}
check: lint typecheck test ## Everything CI runs before packaging

.PHONY: build web-build docker-build
web-build: ## Build the web UI into platform/web/dist
	cd $(WEB) && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && npm run build
build: web-build ## Build the static server binary into bin/
	cd $(PLATFORM) && CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/obsplatform ./cmd/obsplatform
docker-build: ## Build the OCI image
	docker build -f deploy/docker/Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

.PHONY: vuln sbom licenses
vuln: ## Go vulnerability scan (govulncheck) and npm audit of production deps
	cd $(PLATFORM) && go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 $(GOPKGS)
	cd $(WEB) && npm audit --omit=dev
sbom: ## CycloneDX SBOM of the web app and Go module inventory into dist/sbom
	mkdir -p dist/sbom
	cd $(WEB) && npm sbom --sbom-format cyclonedx --omit dev > ../../dist/sbom/web.cdx.json
	scripts/go-sbom.sh > dist/sbom/go-modules.json
licenses: ## Regenerate the dependency and license inventory
	scripts/license-inventory.sh > docs/inventory/dependencies-and-licenses.md

.PHONY: clean
clean: ## Remove build outputs in bin/, dist/ and platform/web/dist/ (keeps .local data)
	rm -rf ./bin ./dist ./platform/web/dist
