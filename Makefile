.PHONY: build install uninstall run lint

build:
	go build -o eve-mcp ./cmd/eve-mcp

install: build
	./eve-mcp install

uninstall:
	./eve-mcp uninstall

run: build
	./eve-mcp

lint:
	python3 evals/run.py lint
