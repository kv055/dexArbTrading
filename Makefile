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

.DEFAULT_GOAL := help
.PHONY: help staging-up staging-wait staging-down staging-logs staging-rebuild staging-ps staging-env-check \
	prune sonar-up sonar-scan sonar-down sonar-reset

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

staging-ps: ## Show local stack status
	$(COMPOSE) ps

staging-logs: ## Tail logs from the local stack
	$(COMPOSE) logs -f --tail=100

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
