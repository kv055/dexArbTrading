# Local staging environment.
#
# This is the dedicated place to run the full stack on your own machine before
# opening a PR into `develop`. Unlike prod (which pulls pre-built images from
# GHCR), these targets BUILD every service from your local source, so what you
# run is exactly your working tree.
#
# Prereq: the decrypted .env files must exist locally (run `make staging-env-check`).
# CI/prod are unaffected — the deploy job always calls compose with an explicit
# `-f docker-compose.yml`, so it never picks up this Makefile.

COMPOSE ?= docker compose
SONAR_COMPOSE := deploy/sonarqube/docker-compose.yml
ENV_FILES := .env dexUnifiedApi/.env dexMonitor/.env
BACKEND_TOOLS_DIR ?= /tmp/dexarb-backend-tools
STATICCHECK_VERSION := 2026.1
DEADCODE_VERSION := v0.48.0

.DEFAULT_GOAL := help
.PHONY: help staging-up staging-wait staging-down staging-logs staging-rebuild staging-ps staging-env-check \
	prune docker-reclaim sonar-up sonar-scan sonar-down sonar-reset compose-validate backend-tools backend-check \
	api-install api-bundle api-lint api-route-check api-generate api-check

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort | awk 'BEGIN {FS = ":.*?## "} {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

staging-env-check: ## Verify the decrypted .env files needed locally are present
	@missing=0; for f in $(ENV_FILES); do \
		if [ ! -f "$$f" ]; then echo "✖ missing $$f"; missing=1; fi; \
	done; \
	if [ "$$missing" -eq 1 ]; then \
		echo "Decrypt them first, e.g.: dotenvx decrypt -f .env.enc (see deploy/README.md)"; \
		exit 1; \
	fi; \
	echo "✓ all required .env files present"

staging-up: staging-env-check ## Build from source and start the full stack locally
	$(COMPOSE) up -d --build

staging-wait: staging-env-check ## Build, start, and block until every service is healthy
	$(COMPOSE) up -d --build --wait --wait-timeout 600

staging-rebuild: staging-env-check ## Force a clean rebuild + recreate of every service
	$(COMPOSE) up -d --build --force-recreate

staging-down: ## Stop and remove the local stack (keeps named volumes)
	$(COMPOSE) down

# Safe local disk reclaim. Keeps named compose volumes (e.g. dexarbtrading_postgres-data,
# bisq data). Does not remove tagged images you may still want.
docker-reclaim: ## Free Docker build cache, stopped containers, and anonymous volumes
	@ids=$$(docker ps -aq --filter label=org.testcontainers=true 2>/dev/null); \
	if [ -n "$$ids" ]; then docker rm -f $$ids; fi
	@ids=$$(docker ps -aq --filter name='^act-' 2>/dev/null); \
	if [ -n "$$ids" ]; then docker rm -f $$ids; fi
	docker container prune -f
	docker volume prune -f
	@for v in $$(docker volume ls -q | grep -E '^act'); do \
		docker volume rm "$$v" 2>/dev/null || true; \
	done
	docker builder prune -af
	docker image prune -f
	@echo "Named compose volumes were kept. Run: docker system df"

staging-ps: ## Show local stack status
	$(COMPOSE) ps

staging-logs: ## Tail logs from the local stack
	$(COMPOSE) logs -f --tail=100

compose-validate: ## Assert docker-compose topology (no host networking, DNS-based service wiring)
	@bash scripts/compose-validate.sh

backend-tools: ## Install pinned backend maintainability tools under /tmp
	@mkdir -p $(BACKEND_TOOLS_DIR)
	GOBIN=$(BACKEND_TOOLS_DIR) go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	GOBIN=$(BACKEND_TOOLS_DIR) go install golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)

backend-check: ## Test, vet, lint, and report dead backend Go code in every module
	@BACKEND_TOOLS_DIR=$(BACKEND_TOOLS_DIR) bash scripts/backend-check.sh

api-install: ## Install the pinned OpenAPI toolchain
	npm --prefix tools/openapi ci
	npm --prefix dexMonitor/react-frontend ci
	go -C tools/openapi mod download

api-bundle: ## Build self-contained OpenAPI bundles for generators and Swagger
	node tools/openapi/contracts.mjs bundle

api-lint: ## Bundle and lint every OpenAPI contract
	node tools/openapi/contracts.mjs lint

api-route-check: ## Compare registered runtime routes with source contracts
	node tools/openapi/contracts.mjs route-check

api-generate: ## Regenerate committed Go and TypeScript API artifacts
	node tools/openapi/contracts.mjs generate

api-check: ## Verify OpenAPI lint, route coverage, and committed generated artifacts
	node tools/openapi/contracts.mjs check

prune: ## Drop stale remote refs and delete local branches already merged into develop
	@git fetch origin --prune
	@merged=$$(git branch --merged origin/develop | grep -vE '^\*|^\+|^\s*(main|develop)$$' | xargs); \
	if [ -n "$$merged" ]; then \
		echo "$$merged" | xargs -n1 git branch -d; \
	else \
		echo "no locally-merged branches to prune"; \
	fi
	@echo "(squash-merged branches aren't detected by --merged; remove those with: git branch -D <name>)"

sonar-up: ## Start local SonarQube (Docker); first run: login admin/admin, create token in .env.sonar
	@map_count=$$(sysctl -n vm.max_map_count 2>/dev/null || echo 0); \
	if [ "$$map_count" -lt 262144 ] 2>/dev/null; then \
		echo "⚠ vm.max_map_count=$$map_count (need >= 262144). Run: sudo sysctl -w vm.max_map_count=262144"; \
	fi
	$(COMPOSE) -f $(SONAR_COMPOSE) up -d
	@bash scripts/sonar-wait.sh

sonar-scan: ## Analyze this repo against local SonarQube (requires sonar-up + .env.sonar)
	@bash scripts/sonar-scan.sh

sonar-down: ## Stop local SonarQube containers (keeps scan history in Docker volumes)
	$(COMPOSE) -f $(SONAR_COMPOSE) down

sonar-reset: ## Stop SonarQube and wipe all scan history (docker compose down -v)
	$(COMPOSE) -f $(SONAR_COMPOSE) down -v
