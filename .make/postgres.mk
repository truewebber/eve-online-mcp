# ============================================================================
# Local Postgres + goose
# ============================================================================

.PHONY: postgres migrate down

postgres: ## Start Compose Postgres and wait until ready
	$(COMPOSE) up -d --wait postgres

migrate: ## Apply sql/ with goose (never the binary)
	$(GOOSE) -dir $(SQL_DIR) postgres "$(DATABASE_URL)" up

down: ## Stop Compose; keep the volume
	$(COMPOSE) down
