.PHONY: build run lint lint-check smoke eval gen postgres down test-store

GO ?= go
COMPOSE ?= docker compose
OAPI_CODEGEN ?= $(GO) tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
GOLANGCI_LINT ?= $(GO) tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint
DATABASE_URL ?= postgres://eve:eve@127.0.0.1:5432/eve_mcp?sslmode=disable

build:
	$(GO) build -o eve-mcp ./cmd/eve-mcp

run: postgres build
	./eve-mcp

postgres:
	$(COMPOSE) up -d --wait postgres

down:
	$(COMPOSE) down

lint:
	$(GOLANGCI_LINT) run --fix ./...

lint-check:
	$(GOLANGCI_LINT) run ./...

smoke:
	$(GO) run ./evals smoke

eval:
	$(GO) run ./evals all

gen:
	$(OAPI_CODEGEN) -config api/http.cfg.yaml api/http.yaml

test-store: postgres
	DATABASE_URL=$(DATABASE_URL) $(GO) test ./internal/adapter/store ./internal/adapter/sso ./internal/usecase/oauth ./internal/usecase/session ./internal/domain/write -count=1
