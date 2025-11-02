# DJAlgoRhythm Makefile
.PHONY: help build build-all test clean lint fmt vet staticcheck check check-env-example check-help-sync update-env-example run dev snapshot-release docker-run docker-compose-up docker-compose-down deps audit security lint-config goreleaser-check test-ci audit-sarif release

# Variables
BINARY_NAME := djalgorhythm
BINARY_PATH := bin/$(BINARY_NAME)
MAIN_PATH := ./cmd/djalgorhythm
DOCKER_IMAGE := enteee/djalgorhythm:latest
DOCKER_REGISTRY :=

# Detect current architecture for Docker platform-specific tags
# Converts: x86_64 -> amd64, aarch64/arm64 -> arm64
CURRENT_ARCH := $(shell uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')

# Default target
help: ## Show this help message
	@echo "DJAlgoRhythm - Live Chat → Spotify DJ"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development targets
build: ## Build binary for current platform (fast, local dev)
	@echo "Building $(BINARY_NAME) for current platform..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser build --snapshot --clean --single-target; \
		mkdir -p bin; \
		cp dist/djalgorhythm_*/djalgorhythm bin/djalgorhythm; \
	else \
		echo "❌ goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
		echo "   Or run: devenv shell"; \
		exit 1; \
	fi
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
snapshot-release: ## Build snapshot Docker images with GoReleaser (multi-platform)
	@echo "Building snapshot release with GoReleaser..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser release --snapshot --clean; \
		echo ""; \
		echo "✅ Docker images built:"; \
		docker images | grep -E "REPOSITORY|djalgorhythm" || true; \
		echo ""; \
		echo "Tagging current architecture ($(CURRENT_ARCH)) as latest..."; \
		docker tag $(DOCKER_IMAGE)-$(CURRENT_ARCH) $(DOCKER_IMAGE) 2>/dev/null || \
			echo "⚠️  Failed to tag $(DOCKER_IMAGE)-$(CURRENT_ARCH) as latest (image may not exist for this architecture)"; \
	else \
		echo "goreleaser not found. Install: go install github.com/goreleaser/goreleaser@latest"; \
		exit 1; \
	fi

docker-run: snapshot-release ## Build and run Docker container
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

# GoReleaser targets
build-all: ## Build binaries for all platforms (no Docker images)
	@echo "Building for all platforms..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser build --snapshot --clean; \
	else \
		echo "goreleaser not found. Install: go install github.com/goreleaser/goreleaser@latest"; \
		exit 1; \
	fi

goreleaser-check: ## Validate GoReleaser configuration
	@echo "Checking GoReleaser configuration..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser check; \
	else \
		echo "goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
	fi

# CI/CD targets
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

release: ## Build and push release with GoReleaser (tags required)
	@echo "Building release with GoReleaser..."
	@if command -v goreleaser > /dev/null; then \
		goreleaser release --clean; \
	else \
		echo "goreleaser not found. Install with: go install github.com/goreleaser/goreleaser@latest"; \
		exit 1; \
	fi
