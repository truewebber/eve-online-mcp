# ============================================================================
# Helm: package, install, clean
# ============================================================================

HELM_CHART_PATH := ./helm/eve-mcp
HELM_VALUES := $(HELM_CHART_PATH)/values.yaml
HELM_VALUES_LOCAL ?= $(HELM_CHART_PATH)/values.local.yaml
HELM_VALUES_EXAMPLE := $(HELM_CHART_PATH)/values.local.yaml.example
RELEASE_NAME := eve-mcp
PACKAGE_VERSION := 0.1.0
PACKAGE_DESTINATION := .
HELM_NAMESPACE ?= apps
APP_VERSION := $(shell git rev-parse HEAD)

.PHONY: helm-require-local helm-package helm-install helm-upgrade helm-clean helm-lint

helm-require-local: ## Fail unless the gitignored overlay exists
	@test -f "$(HELM_VALUES_LOCAL)" || { \
		echo "copy $(HELM_VALUES_EXAMPLE) to $(HELM_VALUES_LOCAL) and fill the instance fields"; \
		exit 1; \
	}

helm-package: ## helm package at AppVersion=$(git rev-parse HEAD)
	@{ \
	CHART_NAME=$$(helm show chart $(HELM_CHART_PATH) | grep '^name:' | awk -F ': ' '{print $$2}'); \
	echo "$$CHART_NAME, Version: $(PACKAGE_VERSION), AppVersion: $(APP_VERSION)"; \
	helm package $(HELM_CHART_PATH) \
		--version $(PACKAGE_VERSION) \
		--app-version $(APP_VERSION) \
		--destination $(PACKAGE_DESTINATION); \
	}

helm-install: helm-require-local helm-package ## helm upgrade --install with values + overlay
	@{ \
	CHART_NAME=$$(helm show chart $(HELM_CHART_PATH) | grep '^name:' | awk -F ': ' '{print $$2}'); \
	CHART="$(PACKAGE_DESTINATION)/$$CHART_NAME-$(PACKAGE_VERSION).tgz"; \
	echo "Package: $$CHART"; \
	helm upgrade --install $(RELEASE_NAME) $$CHART \
		-f $(HELM_VALUES) \
		-f $(HELM_VALUES_LOCAL) \
		--namespace $(HELM_NAMESPACE) \
		--rollback-on-failure \
		--wait; \
	}

helm-upgrade: helm-install ## Package, install, then drop the tgz
	@$(MAKE) helm-clean

helm-clean: ## Remove packaged chart tgz
	@rm -f $(PACKAGE_DESTINATION)/*-$(PACKAGE_VERSION).tgz

helm-lint: ## helm lint against committed values.yaml
	helm lint $(HELM_CHART_PATH)
