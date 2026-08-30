.PHONY: build install uninstall run lint smoke eval gen postgres down

GO ?= go
COMPOSE ?= docker compose
OAPI_CODEGEN ?= $(GO) tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen

build:
	$(GO) build -o eve-mcp ./cmd/eve-mcp

install: build
	./eve-mcp install

uninstall:
	./eve-mcp uninstall

run: build
	./eve-mcp

postgres:
	$(COMPOSE) up -d --wait postgres

down:
	$(COMPOSE) down

lint:
	$(GO) run ./evals lint

smoke:
	$(GO) run ./evals smoke

eval:
	$(GO) run ./evals all

gen:
	$(OAPI_CODEGEN) -config api/http.cfg.yaml api/http.yaml
