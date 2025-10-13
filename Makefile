# WhatDj v2 Makefile
.PHONY: help build test clean lint fmt vet check install run dev docker-build docker-run docker-compose-up docker-compose-down deps audit security

# Variables
BINARY_NAME := whatdj
BINARY_PATH := bin/$(BINARY_NAME)
MAIN_PATH := ./cmd/whatdj
GO_VERSION := 1.24
DOCKER_IMAGE := whatdj:latest
DOCKER_REGISTRY :=
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go build flags
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)
BUILD_FLAGS := -ldflags "$(LDFLAGS)" -trimpath

# Default target
help: ## Show this help message
	@echo "WhatDj v2 - Live WhatsApp → Spotify DJ"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development targets
build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	go build $(BUILD_FLAGS) -o $(BINARY_PATH) $(MAIN_PATH)
	@echo "Built: $(BINARY_PATH)"

build-all: ## Build for all platforms
	@echo "Building for all platforms..."
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o bin/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	GOOS=linux GOARCH=arm64 go build $(BUILD_FLAGS) -o bin/$(BINARY_NAME)-linux-arm64 $(MAIN_PATH)
	GOOS=darwin GOARCH=amd64 go build $(BUILD_FLAGS) -o bin/$(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=arm64 go build $(BUILD_FLAGS) -o bin/$(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)
	GOOS=windows GOARCH=amd64 go build $(BUILD_FLAGS) -o bin/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "Built binaries for all platforms in bin/"

run: build ## Build and run the application
	@echo "Running $(BINARY_NAME)..."
	./$(BINARY_PATH)

dev: ## Run with live reload (requires air)
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "air not found. Install with: go install github.com/cosmtrek/air@latest"; \
		echo "Falling back to regular run..."; \
		$(MAKE) run; \
	fi

install: ## Install the binary to $GOPATH/bin
	@echo "Installing $(BINARY_NAME)..."
	go install $(BUILD_FLAGS) $(MAIN_PATH)

# Testing targets
test: ## Run tests
	@echo "Running tests..."
	go test -race -v ./...

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-integration: ## Run integration tests
	@echo "Running integration tests..."
	go test -tags=integration -race -v ./...

benchmark: ## Run benchmarks
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

# Code quality targets
fmt: ## Format code
	@echo "Formatting code..."
	go fmt ./...
	gofmt -s -w .

vet: ## Run go vet
	@echo "Running go vet..."
	go vet ./...

lint: ## Run golangci-lint
	@echo "Running golangci-lint..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

check: fmt vet lint test ## Run all code quality checks

# Security targets
security: ## Run security checks
	@echo "Running security checks..."
	@if command -v gosec > /dev/null; then \
		gosec ./...; \
	else \
		echo "gosec not found. Install with: go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"; \
	fi

audit: ## Audit dependencies for vulnerabilities
	@echo "Auditing dependencies..."
	@if command -v govulncheck > /dev/null; then \
		govulncheck ./...; \
	else \
		echo "govulncheck not found. Install with: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
	fi

# Dependency management
deps: ## Download and tidy dependencies
	@echo "Managing dependencies..."
	go mod download
	go mod tidy
	go mod verify

deps-update: ## Update all dependencies
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

deps-graph: ## Show dependency graph
	@echo "Generating dependency graph..."
	@if command -v go-mod-graph-chart > /dev/null; then \
		go mod graph | go-mod-graph-chart; \
	else \
		go mod graph; \
	fi

# Docker targets
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE) .
	@echo "Built: $(DOCKER_IMAGE)"

docker-run: docker-build ## Build and run Docker container
	@echo "Running Docker container..."
	docker run --rm -it \
		--env-file .env \
		-p 8080:8080 \
		--name whatdj \
		$(DOCKER_IMAGE)

docker-compose-up: ## Start services with docker-compose
	@echo "Starting services with docker-compose..."
	docker-compose up -d

docker-compose-down: ## Stop services with docker-compose
	@echo "Stopping services with docker-compose..."
	docker-compose down

docker-push: docker-build ## Build and push Docker image
	@if [ -z "$(DOCKER_REGISTRY)" ]; then \
		echo "DOCKER_REGISTRY not set. Use: make docker-push DOCKER_REGISTRY=your-registry.com"; \
		exit 1; \
	fi
	docker tag $(DOCKER_IMAGE) $(DOCKER_REGISTRY)/$(DOCKER_IMAGE)
	docker push $(DOCKER_REGISTRY)/$(DOCKER_IMAGE)

# Cleanup targets
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	rm -f *.log
	go clean -cache
	go clean -testcache
	go clean -modcache

clean-docker: ## Clean Docker artifacts
	@echo "Cleaning Docker artifacts..."
	docker system prune -f
	docker image prune -f

# Documentation targets
docs: ## Generate documentation
	@echo "Generating documentation..."
	@if command -v godoc > /dev/null; then \
		echo "Starting godoc server at http://localhost:6060"; \
		godoc -http=:6060; \
	else \
		echo "godoc not found. Install with: go install golang.org/x/tools/cmd/godoc@latest"; \
	fi

# Release targets
release-dry: ## Dry run release (requires goreleaser)
	@echo "Dry run release..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser release --snapshot --rm-dist; \
	else \
		echo "goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
	fi

release: ## Create release (requires goreleaser)
	@echo "Creating release..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser release --rm-dist; \
	else \
		echo "goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
	fi

# Development environment targets
dev-setup: ## Set up development environment
	@echo "Setting up development environment..."
	@if command -v nix > /dev/null; then \
		echo "Nix detected. Run: nix develop --impure"; \
	else \
		echo "Installing development tools..."; \
		go install github.com/cosmtrek/air@latest; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
		go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest; \
		go install golang.org/x/tools/cmd/godoc@latest; \
	fi

dev-status: ## Show development environment status
	@echo "Development Environment Status:"
	@echo "==============================="
	@echo "Go version: $(shell go version 2>/dev/null || echo 'not found')"
	@echo "Air: $(shell air -v 2>/dev/null || echo 'not found')"
	@echo "golangci-lint: $(shell golangci-lint version 2>/dev/null || echo 'not found')"
	@echo "gosec: $(shell gosec -version 2>/dev/null || echo 'not found')"
	@echo "govulncheck: $(shell govulncheck -version 2>/dev/null || echo 'not found')"
	@echo "Docker: $(shell docker --version 2>/dev/null || echo 'not found')"
	@echo "Docker Compose: $(shell docker-compose --version 2>/dev/null || echo 'not found')"

# CI/CD helpers
ci-deps: ## Install CI dependencies
	@echo "Installing CI dependencies..."
	go mod download
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

ci-test: ## Run CI tests
	@echo "Running CI tests..."
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

ci-build: ## Build for CI
	@echo "Building for CI..."
	CGO_ENABLED=0 GOOS=linux go build $(BUILD_FLAGS) -o $(BINARY_PATH) $(MAIN_PATH)

# Info targets
version: ## Show version information
	@echo "WhatDj v2"
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Built: $(BUILD_TIME)"
	@echo "Go: $(shell go version)"

info: version dev-status ## Show all information