SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
COMPOSE ?= docker compose
OAPI_CODEGEN ?= $(GO) tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
GOLANGCI_LINT ?= $(GO) tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint
GOOSE ?= $(GO) tool github.com/pressly/goose/v3/cmd/goose
DATABASE_URL ?= postgres://eve:eve@127.0.0.1:5432/eve_mcp?sslmode=disable
SQL_DIR := sql
BINARY := eve-mcp
CMD_PATH := ./cmd/eve-mcp

.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  %-25s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

include .make/go.mk
include .make/postgres.mk
include .make/helm.mk
