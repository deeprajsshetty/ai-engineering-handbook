# Common tasks. Run `make help`.
SHELL := /bin/bash
COMPOSE := docker compose -f deploy/compose/docker-compose.yml
PY := cd sentinel && uv run

.PHONY: help setup lint test cov e2e load up up-discovery down logs demo-errors demo-latency status k8s-render dashboards clean discovery-lint discovery-test

help:  ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-14s %s\n",$$1,$$2}'

setup:  ## Install dev dependencies (uv) and pre-commit hooks
	cd sentinel && uv sync --frozen --extra dev
	-pre-commit install

lint:  ## Ruff, formatting check, mypy
	$(PY) ruff check src tests && $(PY) ruff format --check src && $(PY) mypy

test:  ## Unit + integration tests
	$(PY) pytest

discovery-lint:  ## Discovery service (Go + web): gofmt, vet, ESLint
	$(MAKE) -C discovery lint

discovery-test:  ## Discovery service tests (Go + web)
	$(MAKE) -C discovery test

cov:  ## Tests with coverage report
	$(PY) pytest --cov --cov-report=term-missing

up:  ## Start the full stack (generates secrets on first run)
	scripts/bootstrap-env.sh
	$(COMPOSE) up -d --build

up-discovery:  ## Full stack plus the discovery service (infrastructure context for RCA)
	scripts/bootstrap-env.sh
	SENTINEL_DISCOVERY_URL=http://discovery:8080 SENTINEL_DISCOVERY_TOKEN=dev-viewer \
	  $(COMPOSE) --profile discovery up -d --build

down:  ## Stop the stack (keeps volumes)
	$(COMPOSE) --profile discovery down

logs:  ## Tail platform logs
	$(COMPOSE) logs -f sentinel-api sentinel-worker

e2e:  ## End-to-end test against the running stack
	$(PY) pytest -m e2e tests/e2e -s

load:  ## 60s load test of the Sentinel API (needs the stack running)
	set -a; source deploy/compose/.env.keys; set +a; \
	cd sentinel && uv run locust -f tests/load/locustfile.py --headless -u 50 -r 10 -t 60s \
	  --host http://localhost:8000 --csv ../reports/load --only-summary

demo-errors:  ## Inject a 5xx fault into payments
	scripts/demo.sh fault payments errors

demo-latency:  ## Inject a latency fault into inventory
	scripts/demo.sh fault inventory latency

status:  ## Show active incidents
	scripts/demo.sh status

k8s-render:  ## Render the Kubernetes overlays
	kubectl kustomize deploy/k8s/overlays/prod

dashboards:  ## Regenerate Grafana dashboards from source
	python3 deploy/compose/grafana/build_dashboards.py

clean:  ## Remove the stack AND its data volumes
	$(COMPOSE) --profile discovery down -v
