GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
BINARY_NAME=loco
BINARY_UNIX=$(BINARY_NAME)_unix

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

all: test build

build: ## Build the application
	$(GOBUILD) -o bin/$(BINARY_NAME) -v

vet: ## Run go vet against code.
	go vet ./...

fmt: ## Run go fmt against code.
	go fmt ./...

test: clean ## Run tests
	$(GOTEST) -v -coverprofile=c.out

test-cov: test ## Run tests with HTML coverage
	@go tool cover -o coverage.html -html=c.out; sed -i '' 's/black/whitesmoke/g' coverage.html; open coverage.html

clean: ## Clean up build artifacts and tidy modules
	@$(GOCLEAN)
	@rm -f $(BINARY_NAME)
	@rm -f $(BINARY_UNIX)
	@rm -rf tmp
	@rm -f coverage.html
	@rm -f c.out
	@$(GOCMD) mod tidy

build-linux: clean ## Build the application for Linux
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) -v

reload-api:
	@echo "Starting API with live reload..."
	@lsof -ti:8000 | xargs -r kill -15 2>/dev/null || true
	@(air \
		--build.cmd "cd api && go build -o ./bin/loco-api ." \
		--build.bin "./api/bin/loco-api" \
		--build.exclude_dir "bin,archive,assets,cmd,docs,internal,terraform,web")

reload-cli:
	@echo "Starting CLI with live reload..."
	@(air \
		--build.cmd "mkdir -p ./bin; go build -o ./bin/loco .; chmod +x ./bin/loco" \
		--build.bin "./bin/loco" \
		--build.exclude_dir "bin,api,archive,assets,dashboards,docs,kube,terraform,web")

reload-agent:
	@echo "Starting Agent with live reload..."
	@(air \
		--build.cmd "cd agent && go build -o ./bin/loco-agent ." \
		--build.bin "./agent/bin/loco-agent" \
		--build.exclude_dir "bin,web")

reload-obs-proxy:
	@echo "Starting Observability Proxy with live reload..."
	@(air \
		--build.cmd "cd observability-proxy && go build -o ./bin/loco-obs-proxy ." \
		--build.bin "./observability-proxy/bin/loco-obs-proxy" \
		--build.exclude_dir "bin,web")

gen:
	buf generate
	cd api && sqlc generate

tilt: ## Start full local dev environment (infra + services) via Tilt — recommended
	tilt up

dev: fmt vet ## Start local services only (requires infrastructure already running via tilt or manually)
	@echo "Starting local services (API, Agent, Obs Proxy, UI)..."
	@(trap 'kill $(jobs -p) 2>/dev/null' EXIT; \
		$(MAKE) reload-api & \
		$(MAKE) reload-agent & \
		$(MAKE) reload-obs-proxy & \
		(cd web && npm run dev) & \
		wait || exit 1)

helm-repos: ## Add/update helm repositories
	helm repo add jetstack https://charts.jetstack.io
	helm repo add cilium https://helm.cilium.io
	helm repo add altinity https://helm.altinity.com
	helm repo add grafana https://grafana.github.io/helm-charts
	helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
	helm repo update

helm-deps: ## Build helm chart dependencies
	helm dependency build ./charts/loco-networking
	helm dependency build ./charts/loco-obs
	helm dependency build ./charts/loco-core
	helm dependency build ./charts/loco-controller

controller-gen: ## Generate controller manifests and code
	cd controller && make manifests && make generate

helm-u-all: helm-deps ## Sync all releases (local environment)
	helmfile -e local sync

helm-u-net: helm-deps ## Sync networking release only
	helmfile -e local sync loco-networking

helm-u-core: helm-deps ## Sync core releases (cert-manager, gateway, loco-core)
	helmfile -e local sync cert-manager envoy-gateway loco-core

helm-u-obs: helm-deps ## Sync observability release only
	helmfile -e local sync loco-obs

helm-uninstall-all: ## Uninstall all releases
	helmfile destroy

helm-fix-clickhouse: ## Remove clickhouse finalizer if stuck
	kubectl -n observability patch clickhouseinstallations.clickhouse.altinity.com/clickhouse -p '{"metadata":{"finalizers":[]}}' --type=merge

upgrade-rpc: ## Upgrade protobuf/RPC toolchain
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
	npm install -g @connectrpc/protoc-gen-connect-query @bufbuild/protoc-gen-es

lint: clean
	@(golangci-lint run)

e2e: ## Run end-to-end tests (full setup + teardown)
	./e2e/run.sh

e2e-no-teardown: ## Run e2e tests, keep infra running for debugging
	./e2e/run.sh --no-teardown

e2e-teardown: ## Tear down e2e infrastructure
	./e2e/run.sh --teardown-only

go-list-updates: ## Show available updates for all Go modules
	@find . -name "go.mod" -not -path "*/vendor/*" | sort | while read modfile; do \
		dir=$$(dirname $$modfile); \
		echo "\n=== $$dir ==="; \
		(cd $$dir && go list -m -u all 2>/dev/null | grep '\['); \
	done

go-update-all: ## Update all dependencies in each Go module, one at a time
	@find . -name "go.mod" -not -path "*/vendor/*" | sort | while read modfile; do \
		dir=$$(dirname $$modfile); \
		echo "\n=== Updating $$dir ==="; \
		(cd $$dir && go get -u ./... && go mod tidy); \
	done

help: ## show help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make <command>\ncommands:\033[36m\033[0m\n"} /^[$$()% a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

MAKEFLAGS += --always-make
