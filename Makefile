# DJAlgoRhythm Makefile
.PHONY: help build test clean lint fmt vet staticcheck check check-env-example check-help-sync update-env-example install run dev docker-build docker-run docker-compose-up docker-compose-down deps audit security lint-config goreleaser-snapshot goreleaser-check test-ci audit-sarif snapshot-release release docker-tag-latest

# Variables
BINARY_NAME := djalgorhythm
BINARY_PATH := bin/$(BINARY_NAME)
MAIN_PATH := ./cmd/djalgorhythm
DOCKER_IMAGE := djalgorhythm:latest
DOCKER_REGISTRY :=
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go build flags
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)
BUILD_FLAGS := -ldflags "$(LDFLAGS)" -trimpath

# Default target
help: ## Show this help message
	@echo "DJAlgoRhythm - Live Chat → Spotify DJ"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development targets
build: ## Build the binary for local development
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	go build $(BUILD_FLAGS) -o $(BINARY_PATH) $(MAIN_PATH)
	@echo "Built: $(BINARY_PATH)"

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
	find . -name "*.go" -not -path "./.devenv/*" -not -path "./vendor/*" -exec gofmt -s -w {} \;

vet: ## Run go vet
	@echo "Running go vet..."
	go vet ./...

lint: ## Run golangci-lint
	@echo "Running golangci-lint..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run --max-same-issues 0 --max-issues-per-linter 0; \
	else \
		echo "golangci-lint not found. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

lint-config: ## Verify golangci-lint configuration
	@echo "Verifying golangci-lint configuration..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint config verify; \
	else \
		echo "golangci-lint not found. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

staticcheck: ## Run staticcheck
	@echo "Running staticcheck..."
	@if command -v staticcheck > /dev/null; then \
		staticcheck ./...; \
	else \
		echo "staticcheck not found. Install with: go install honnef.co/go/tools/cmd/staticcheck@latest"; \
	fi

check-env-example: ## Verify .env.example is up-to-date with current configuration
	@echo "Checking if .env.example is up-to-date..."
	@if [ ! -f .env.example ]; then \
		echo "❌ .env.example not found"; \
		exit 1; \
	fi
	@cp .env.example .env.example.backup
	@go run $(MAIN_PATH) --generate-env-example > /dev/null 2>&1 || (echo "❌ Failed to generate .env.example"; mv .env.example.backup .env.example; exit 1)
	@if ! diff -q .env.example.backup .env.example > /dev/null 2>&1; then \
		echo "❌ .env.example is out of sync with current configuration"; \
		echo ""; \
		echo "Run 'make update-env-example' to fix this, or manually run:"; \
		echo "  go run $(MAIN_PATH) --generate-env-example"; \
		echo ""; \
		echo "Differences found:"; \
		diff .env.example.backup .env.example || true; \
		mv .env.example.backup .env.example; \
		exit 1; \
	fi
	@rm -f .env.example.backup
	@echo "✅ .env.example is up-to-date"

update-env-example: ## Update .env.example with current configuration
	@echo "Updating .env.example..."
	@go run $(MAIN_PATH) --generate-env-example
	@echo "✅ .env.example updated"

check-help-sync: build ## Verify CLI help output matches README.md
	@echo "Checking if --help output matches README.md CLI Flags section..."
	@./$(BINARY_PATH) --help 2>&1 | sed -n '/^Flags:/,$$p' > /tmp/help-output.txt
	@sed -n '/^### CLI Flags/,/^```$$/p' README.md | sed -n '/^Flags:/,/^```$$/p' | sed '$$d' > /tmp/readme-flags.txt
	@if ! diff -u /tmp/readme-flags.txt /tmp/help-output.txt > /dev/null 2>&1; then \
		echo "❌ CLI flags in README.md are out of sync with --help output"; \
		echo ""; \
		echo "Differences found:"; \
		diff -u /tmp/readme-flags.txt /tmp/help-output.txt || true; \
		echo ""; \
		echo "To fix: Update the CLI Flags section in README.md to match the output of:"; \
		echo "  ./$(BINARY_PATH) --help"; \
		rm -f /tmp/help-output.txt /tmp/readme-flags.txt; \
		exit 1; \
	fi
	@rm -f /tmp/help-output.txt /tmp/readme-flags.txt
	@echo "✅ CLI flags documentation is in sync"

pre-commit: ## Run all pre-commit hooks (devenv git-hooks)
	@echo "Running pre-commit hooks..."
	@if command -v pre-commit > /dev/null; then \
		pre-commit run --all-files; \
	else \
		echo "pre-commit not found. Run 'devenv shell' to enter the development environment with pre-commit hooks."; \
		exit 1; \
	fi

check: fmt vet lint-config lint staticcheck security check-env-example check-help-sync pre-commit test build goreleaser-check ## Run all code quality checks, security scans, and build

# Security targets
security: ## Run security checks
	@echo "Running security checks..."
	@if command -v gosec > /dev/null; then \
		gosec -exclude-dir=.devenv -exclude-dir=vendor ./...; \
	else \
		echo "gosec not found. Install with: go install github.com/securego/gosec/v2/cmd/gosec@latest"; \
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
docker-build: build ## Build Docker image (auto-detects buildx and CI cache)
	@echo "Building Docker image..."
	@if command -v docker > /dev/null 2>&1; then \
		echo "Setting up platform-specific binary directory..."; \
		mkdir -p bin/linux/amd64; \
		cp $(BINARY_PATH) bin/linux/amd64/djalgorhythm; \
		if docker buildx version > /dev/null 2>&1; then \
			echo "Using buildx with caching..."; \
			CACHE_FROM=""; \
			CACHE_TO=""; \
			if [ -n "$$GITHUB_ACTIONS" ]; then \
				CACHE_FROM="--cache-from=type=gha"; \
				CACHE_TO="--cache-to=type=gha,mode=max"; \
			fi; \
			docker buildx build \
				--platform linux/amd64 \
				--load \
				-t $(DOCKER_IMAGE) \
				$$CACHE_FROM \
				$$CACHE_TO \
				--label=org.opencontainers.image.created=$$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
				--label=org.opencontainers.image.title=$(BINARY_NAME) \
				--label=org.opencontainers.image.revision=$$(git rev-parse HEAD 2>/dev/null || echo "unknown") \
				--label=org.opencontainers.image.version=$(VERSION) \
				--label=org.opencontainers.image.source=https://github.com/Enteee/DJAlgoRhythm \
				--build-context . \
				.; \
		else \
			echo "Using standard docker build..."; \
			docker build -t $(DOCKER_IMAGE) .; \
		fi; \
		rm -rf bin/linux; \
		echo "Built: $(DOCKER_IMAGE)"; \
	else \
		echo "Docker not found. Skipping build."; \
		exit 1; \
	fi

docker-run: docker-build ## Build and run Docker container
	@echo "Running Docker container..."
	docker run --rm -it \
		--env-file .env \
		-p 8080:8080 \
		--name djalgorhythm \
		$(DOCKER_IMAGE)

docker-compose-up: ## Start services with docker-compose
	@echo "Starting services with docker-compose..."
	docker-compose up -d

docker-compose-down: ## Stop services with docker-compose
	@echo "Stopping services with docker-compose..."
	docker-compose down

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

# GoReleaser targets (for local testing only - CI handles actual releases)
goreleaser-snapshot: ## Build snapshot release locally without publishing
	@echo "Building snapshot release..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser build --snapshot --clean; \
	else \
		echo "goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
	fi

goreleaser-check: ## Validate GoReleaser configuration
	@echo "Checking GoReleaser configuration..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser check; \
	else \
		echo "goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
	fi

# Development environment targets
dev-setup: ## Set up development environment
	@echo "Setting up development environment..."
	@if command -v devenv > /dev/null; then \
		echo "devenv detected. Run: devenv shell"; \
	elif command -v nix > /dev/null; then \
		echo "Nix detected. Install devenv: https://devenv.sh/getting-started/"; \
	else \
		echo "Installing development tools..."; \
		go install github.com/cosmtrek/air@latest; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
		go install honnef.co/go/tools/cmd/staticcheck@latest; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
		go install github.com/securego/gosec/v2/cmd/gosec@latest; \
		go install golang.org/x/tools/cmd/godoc@latest; \
	fi

dev-status: ## Show development environment status
	@echo "Development Environment Status:"
	@echo "==============================="
	@echo "Go version: $(shell go version 2>/dev/null || echo 'not found')"
	@echo "Air: $(shell air -v 2>/dev/null || echo 'not found')"
	@echo "golangci-lint: $(shell golangci-lint version 2>/dev/null || echo 'not found')"
	@echo "staticcheck: $(shell staticcheck -version 2>/dev/null || echo 'not found')"
	@echo "gosec: $(shell gosec -version 2>/dev/null || echo 'not found')"
	@echo "govulncheck: $(shell govulncheck -version 2>/dev/null || echo 'not found')"
	@echo "Docker: $(shell docker --version 2>/dev/null || echo 'not found')"
	@echo "Docker Compose: $(shell docker-compose --version 2>/dev/null || echo 'not found')"

# CI/CD targets
ci-deps: ## Install CI dependencies
	@echo "Installing CI dependencies..."
	go mod download
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install honnef.co/go/tools/cmd/staticcheck@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

test-ci: ## Run tests with coverage for CI (includes go mod verify)
	@echo "Running CI tests..."
	go mod verify
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

audit-sarif: ## Run govulncheck with SARIF output and check for vulnerabilities
	@echo "Running govulncheck with SARIF output..."
	@if command -v govulncheck > /dev/null; then \
		govulncheck -format sarif ./... > govulncheck.sarif || true; \
		if command -v jq > /dev/null; then \
			jq '.runs[]?.tool.driver.rules[]?.properties.tags |= unique' govulncheck.sarif > govulncheck-clean.sarif; \
			mv govulncheck-clean.sarif govulncheck.sarif; \
		else \
			echo "jq not found. Skipping SARIF cleanup."; \
		fi; \
		if [ -f govulncheck.sarif ]; then \
			VULN_COUNT=$$(jq '.runs[0].results | length' govulncheck.sarif 2>/dev/null || echo "0"); \
			if [ "$$VULN_COUNT" -gt 0 ]; then \
				echo ""; \
				echo "⚠️  Security Vulnerabilities Detected"; \
				echo ""; \
				echo "govulncheck found $$VULN_COUNT potential vulnerabilities in dependencies."; \
				echo ""; \
				echo "📋 Next Steps:"; \
				echo "  - Review findings in govulncheck.sarif"; \
				echo "  - Update vulnerable dependencies if fixes are available"; \
				echo "  - Assess whether vulnerabilities affect your deployment"; \
				echo ""; \
				if [ -n "$$GITHUB_STEP_SUMMARY" ]; then \
					echo "## ⚠️ Security Vulnerabilities Detected" >> $$GITHUB_STEP_SUMMARY; \
					echo "" >> $$GITHUB_STEP_SUMMARY; \
					echo "govulncheck found $$VULN_COUNT potential vulnerabilities in dependencies." >> $$GITHUB_STEP_SUMMARY; \
					echo "" >> $$GITHUB_STEP_SUMMARY; \
					echo "📋 **Next Steps:**" >> $$GITHUB_STEP_SUMMARY; \
					echo "- Review findings in the [Security tab](https://github.com/$$GITHUB_REPOSITORY/security/code-scanning)" >> $$GITHUB_STEP_SUMMARY; \
					echo "- Update vulnerable dependencies if fixes are available" >> $$GITHUB_STEP_SUMMARY; \
					echo "- Assess whether vulnerabilities affect your deployment" >> $$GITHUB_STEP_SUMMARY; \
				fi; \
			else \
				echo "✅ No vulnerabilities found."; \
			fi; \
		fi; \
	else \
		echo "govulncheck not found. Install with: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
		exit 1; \
	fi

snapshot-release: ## Build and push snapshot release with GoReleaser
	@echo "Building snapshot release with GoReleaser..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser release --snapshot --clean; \
	else \
		echo "goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
		exit 1; \
	fi

release: ## Build and push release with GoReleaser
	@echo "Building release with GoReleaser..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser release --clean; \
	else \
		echo "goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
		exit 1; \
	fi

docker-tag-latest: ## Tag snapshot Docker images as latest
	@echo "Tagging snapshot images as latest..."
	@SNAPSHOT_TAG=$$(git describe --tags --always --dirty 2>/dev/null || echo "0.0.0")-$$(git rev-parse --short HEAD); \
	echo "Snapshot tag: $$SNAPSHOT_TAG"; \
	docker buildx imagetools create \
		-t enteee/djalgorhythm:latest \
		-t ghcr.io/enteee/djalgorhythm:latest \
		enteee/djalgorhythm:$$SNAPSHOT_TAG || true

ci-build: ## Build for CI
	@echo "Building for CI..."
	CGO_ENABLED=0 GOOS=linux go build $(BUILD_FLAGS) -o $(BINARY_PATH) $(MAIN_PATH)

# Info targets
version: ## Show version information
	@echo "DJAlgoRhythm"
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Built: $(BUILD_TIME)"
	@echo "Go: $(shell go version)"

info: version dev-status ## Show all information
