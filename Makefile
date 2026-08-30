.PHONY: build install uninstall run lint gen

GO ?= go
OAPI_CODEGEN ?= $(GO) tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen

build:
	$(GO) build -o eve-mcp ./cmd/eve-mcp

install: build
	./eve-mcp install

uninstall:
	./eve-mcp uninstall

run: build
	./eve-mcp

lint:
	python3 evals/run.py lint

gen:
	$(OAPI_CODEGEN) -config api/http.cfg.yaml api/http.yaml
