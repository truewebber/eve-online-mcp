# ============================================================================
# Go: build, lint, generate, test
# ============================================================================

.PHONY: build run lint lint-fix gen generate test test-store ci

build: ## Build ./eve-mcp
	$(GO) build -o $(BINARY) $(CMD_PATH)

run: postgres migrate build ## Local Postgres, schema, then the binary
	./$(BINARY)

lint: ## golangci-lint
	$(GOLANGCI_LINT) run ./...

lint-fix: ## golangci-lint --fix
	$(GOLANGCI_LINT) run --fix ./...

gen: ## oapi-codegen from api/http.yaml
	$(OAPI_CODEGEN) -config api/http.cfg.yaml api/http.yaml

generate: ## go generate ./...
	$(GO) generate ./...

test: ## Offline tests; store tests skip without DATABASE_URL
	@echo "offline: recorded ESI fixtures; store and migration tests skip without DATABASE_URL — use make test-store"
	$(GO) test ./...

test-store: postgres migrate ## Tests that need DATABASE_URL
	DATABASE_URL=$(DATABASE_URL) $(GO) test ./internal/postgres ./internal/postgres/pgtest ./internal/adapter/sso ./internal/adapter/sso/http ./internal/adapter/esi ./internal/adapter/esi/http ./internal/usecase/oauth ./internal/usecase/session ./internal/usecase/sweep ./internal/domain/write ./internal/domain/character/pgx ./internal/domain/oauthclient/pgx ./internal/domain/loginstate/pgx ./internal/domain/authcode/pgx ./internal/domain/confirm/pgx ./internal/domain/mutation/pgx ./internal/domain/session/pgx -count=1

ci: lint ## Lint, offline tests, store tests, tests/
	DATABASE_URL= $(GO) test ./...
	$(MAKE) test-store
	DATABASE_URL=$(DATABASE_URL) $(GO) test ./tests/... -count=1
