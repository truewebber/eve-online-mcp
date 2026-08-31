.PHONY: build run lint lint-fix lint-check smoke eval gen postgres migrate down test-store

GO ?= go
COMPOSE ?= docker compose
OAPI_CODEGEN ?= $(GO) tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
GOLANGCI_LINT ?= $(GO) tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint
GOOSE ?= $(GO) tool github.com/pressly/goose/v3/cmd/goose
DATABASE_URL ?= postgres://eve:eve@127.0.0.1:5432/eve_mcp?sslmode=disable
SQL_DIR := sql

build:
	$(GO) build -o eve-mcp ./cmd/eve-mcp

run: postgres migrate build
	./eve-mcp

postgres:
	$(COMPOSE) up -d --wait postgres

migrate:
	$(GOOSE) -dir $(SQL_DIR) postgres "$(DATABASE_URL)" up

down:
	$(COMPOSE) down

lint:
	$(GOLANGCI_LINT) run ./...

lint-fix:
	$(GOLANGCI_LINT) run --fix ./...

smoke:
	$(GO) run ./evals smoke

eval:
	$(GO) run ./evals all

gen:
	$(OAPI_CODEGEN) -config api/http.cfg.yaml api/http.yaml

test-store: postgres migrate
	DATABASE_URL=$(DATABASE_URL) $(GO) test ./internal/adapter/store ./internal/adapter/store/storetest ./internal/adapter/sso ./internal/adapter/sso/http ./internal/adapter/esi ./internal/adapter/esi/http ./internal/usecase/oauth ./internal/usecase/session ./internal/domain/write ./internal/domain/character/pgx ./internal/domain/oauthclient/pgx ./internal/domain/loginstate/pgx ./internal/domain/authcode/pgx ./internal/domain/confirm/pgx -count=1
