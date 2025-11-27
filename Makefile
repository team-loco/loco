# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=loco
BINARY_UNIX=$(BINARY_NAME)_unix

include .env

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

all: test build

build: ## Build the application
	$(GOBUILD) -o bin/$(BINARY_NAME) -v

vet:
	@$(GOCMD) vet ./...
test: clean ## Run tests
	$(GOTEST) -v -coverprofile=c.out

test-cov: test ## Run tests with HTML coverage
	@go tool cover -o coverage.html -html=c.out; sed -i '' 's/black/whitesmoke/g' coverage.html; open coverage.html

clean: ## Clean up the project directory and tidy modules
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

# Reload CLI with air
reload-cli:
	@echo "Starting CLI with live reload..."
	@(air \
		--build.cmd "mkdir -p ./bin; go build -o ./bin/loco .; chmod +x ./bin/loco" \
		--build.bin "./bin/loco" \
		--build.exclude_dir "bin,api,archive,assets,dashboards,docs,kube,terraform,web")

gen:
	buf generate
	cd api && sqlc generate

ui:
	@echo "Starting UI..."
	@cd web && npm run dev

lint: clean
	@(golangci-lint run)

help: ## show help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make <command>\ncommands:\033[36m\033[0m\n"} /^[$$()% a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

MAKEFLAGS += --always-make
