# Sumpter Makefile
# High-performance Go-based XML streaming engine
#
# Quick Start Commands:
#   make help        - Show all available commands
#   make check-all   - Run all quality checks (same as pre-commit hook)
#   make pre-commit  - Run comprehensive pre-commit checks with tests
#   make build       - Build the binary
#   make test        - Run all tests with coverage
#   make dev         - Set up development environment
#
# Quality Assurance Workflow:
#   1. make check-all    - Fast quality checks (formatting, linting, vetting)
#   2. make test         - Run tests with coverage
#   3. make precommit    - Pre-commit validation (dynamic, lifecycle-aware)
#   4. make prepush      - Pre-push validation (80% production threshold)
#   5. make build        - Build the final binary
#   6. make install      - Install binary to PATH
#
# Coverage Strategy:
#   • Precommit:  Dynamic via LIFECYCLE_PHASE + config/coverage-thresholds.yaml
#                 (alpha 50%, beta 70%, production 80%).
#   • Prepush:    80% threshold, comprehensive validation

# Variables
BINARY_NAME := sumpter
VERSION := $(shell cat VERSION)
BUILD_DIR := dist
COVERAGE_DIR := coverage
TEMP_DIR := tmp
CMD_DIR := cmd/sumpter
INSTALL_DIR ?= $(HOME)/.local/bin

# Go related variables
GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod
GOFMT := $(GOCMD) fmt
GOVET := $(GOCMD) vet

# Build flags with version injection
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -ldflags "-X 'github.com/fulmenhq/sumpter/cmd/sumpter/commands.Version=$(VERSION)' -X 'github.com/fulmenhq/sumpter/cmd/sumpter/commands.BuildTime=$(BUILD_TIME)' -X 'github.com/fulmenhq/sumpter/cmd/sumpter/commands.GitCommit=$(GIT_COMMIT)'"
BUILD_FLAGS := $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)

# Test flags
# Note: -race flag removed from default (too slow). Use test-race for race detection.
TEST_FLAGS := -v -coverprofile=$(COVERAGE_DIR)/coverage.out
TEST_FLAGS_RACE := -v -race -coverprofile=$(COVERAGE_DIR)/coverage.out
BENCH_FLAGS := -bench=. -benchmem
PRE_PUSH_COVERAGE := 80

# Color output
RED := \033[0;31m
GREEN := \033[0;32m
YELLOW := \033[1;33m
BLUE := \033[0;34m
MAGENTA := \033[0;35m
CYAN := \033[0;36m
WHITE := \033[1;37m
NC := \033[0m # No Color

.PHONY: help
help: ## Show this help message
	@printf "%b\n" "$(CYAN)Sumpter - XML Streaming Engine$(NC)" "$(YELLOW)High-performance Go-based XML processing$(NC)" "" "$(WHITE)Available commands:$(NC)"
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@printf "%b\n" "" "$(WHITE)Quick workflow:$(NC)" "  $(CYAN)make check-all$(NC)     - Fast quality checks" "  $(CYAN)make test$(NC)          - Run tests with coverage" "  $(CYAN)make test-coverage$(NC) - Detailed coverage analysis" "  $(CYAN)make coverage-check-dynamic$(NC) - Dynamic coverage check" "  $(CYAN)make precommit$(NC)     - Pre-commit validation" "  $(CYAN)make build$(NC)         - Build the binary" "  $(CYAN)make dev$(NC)           - Setup development environment"

# Development setup
.PHONY: dev
dev: ## Set up development environment
	@echo "$(BLUE)Setting up Sumpter development environment...$(NC)"
	$(GOMOD) download
	$(GOMOD) tidy
	@mkdir -p $(BUILD_DIR) $(COVERAGE_DIR) $(TEMP_DIR)
	@echo "$(GREEN)✅ Development environment ready!$(NC)"

# Quality checks
.PHONY: check-all
check-all: fmt-strict vet lint safety-check schema-validate ## Run all quality checks (fast)
	@echo "$(GREEN)✅ All quality checks passed!$(NC)"

# Schema validation
.PHONY: schema-validate
schema-validate: ## Validate JSON schemas using goneat
	@echo "$(BLUE)Validating JSON schemas...$(NC)"
	@goneat validate --include schemas/ --exclude "schemas/extract/v0.1.0/file-signature-schema.yaml" --format json --fail-on high
	@echo "$(GREEN)✅ Schema validation passed!$(NC)"

# Code formatting
.PHONY: fmt
fmt: ## Format Go code only
	@echo "$(BLUE)Formatting Go code...$(NC)"
	$(GOFMT) ./...

.PHONY: fmt-strict
fmt-strict: ## Strictly check Go code formatting, fails if issues found
	@echo "$(BLUE)Checking code formatting...$(NC)"
	@if find . -name "*.go" -not -path "./vendor/*" -not -path "./.plans/*" -not -path "./.cache/*" | xargs gofmt -l | grep .; then \
		echo "$(RED)❌ Formatting issues found. Run 'make fmt' to fix.$(NC)"; \
		exit 1; \
	fi
	@echo "$(GREEN)✅ Code formatting check passed$(NC)"

# Document formatting (YAML, JSON, Markdown)
.PHONY: fmt-docs
fmt-docs: ## Format all documentation files (YAML, JSON, Markdown)
	@echo "$(BLUE)Formatting documentation files...$(NC)"
	@$(MAKE) fmt-yaml fmt-json fmt-markdown fmt-whitespace
	@echo "$(GREEN)✅ Documentation formatting completed$(NC)"

.PHONY: fmt-yaml
fmt-yaml: ## Format YAML files using yamlfmt
	@echo "  📄 Formatting YAML files..."
	@if command -v yamlfmt >/dev/null 2>&1; then \
		find . -name "*.yml" -o -name "*.yaml" | grep -v vendor/ | grep -v node_modules/ | while read file; do \
			echo "    Formatting: $$file"; \
			yamlfmt "$$file"; \
		done; \
	else \
		echo "  ⚠️ yamlfmt not found, install with: make install-dev-tools"; \
	fi

.PHONY: fmt-json
fmt-json: ## Format JSON files using yq
	@echo "  📄 Formatting JSON files..."
	@if command -v yq >/dev/null 2>&1; then \
		find . -name "*.json" | grep -v vendor/ | grep -v node_modules/ | while read file; do \
			echo "    Formatting: $$file"; \
			yq eval '.' "$$file" --output-format=json --indent=2 > "$$file.tmp" && mv "$$file.tmp" "$$file" || rm -f "$$file.tmp"; \
		done; \
	else \
		echo "  ⚠️ yq not found, install with: make install-dev-tools"; \
	fi

.PHONY: fmt-markdown
fmt-markdown: ## Format Markdown files (trailing whitespace cleanup)
	@echo "  📋 Formatting Markdown files..."
	@find . -name "*.md" | grep -v vendor/ | grep -v node_modules/ | grep -v .cache/ | while read file; do \
		echo "    Cleaning: $$file"; \
		sed -i.bak 's/[[:space:]]*$$//' "$$file" && rm -f "$$file.bak"; \
	done

.PHONY: fmt-whitespace
fmt-whitespace: ## Fix end-of-file and trailing whitespace issues
	@echo "  ✂️ Fixing whitespace and end-of-file issues..."
	@find . -type f \( -name "*.go" -o -name "*.md" -o -name "*.yml" -o -name "*.yaml" -o -name "*.json" -o -name "*.txt" -o -name "*.sh" -o -name "Makefile" -o -name "Dockerfile*" \) \
		| grep -v vendor/ | grep -v node_modules/ | grep -v .git/ | grep -v dist/ | grep -v bin/ | grep -v .cache/ \
		| while read file; do \
			sed -i.bak 's/[[:space:]]*$$//' "$$file" && rm -f "$$file.bak"; \
			if [ -s "$$file" ]; then \
				if [ "$$(tail -c1 "$$file" | wc -l)" -eq 0 ]; then \
					echo "" >> "$$file"; \
				fi; \
			fi; \
		done

# Unified formatting target
.PHONY: fmt-all
fmt-all: fmt fmt-docs ## Format all code and documentation files
	@echo "$(GREEN)✅ All formatting completed$(NC)"

# Linting and vetting
.PHONY: vet
vet: ## Run go vet
	@echo "$(BLUE)Running go vet...$(NC)"
	$(GOVET) ./...

.PHONY: lint
lint: ## Run golangci-lint (install if needed)
	@echo "$(BLUE)Running linter...$(NC)"
	@if ! command -v golangci-lint >/dev/null 2>&1; then echo "$(YELLOW)Installing golangci-lint...$(NC)"; curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$((go env GOPATH))/bin; fi
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else $$(go env GOPATH)/bin/golangci-lint run; fi

# Testing
.PHONY: test
test: ## Run all tests with coverage
	@echo "$(BLUE)Running tests with coverage...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) $(TEST_FLAGS) ./...
	@echo "$(GREEN)Tests completed. Coverage: $(COVERAGE_DIR)/coverage.out$(NC)"
	@echo "$(CYAN)Run 'make test-coverage' for detailed HTML report$(NC)"

.PHONY: test-short
test-short: ## Run tests without network dependencies
	@echo "$(BLUE)Running short tests...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -short $(TEST_FLAGS) ./...

.PHONY: test-verbose
test-verbose: ## Run tests with verbose output
	@echo "$(BLUE)Running verbose tests...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -v $(TEST_FLAGS) ./...

.PHONY: test-parallel
test-parallel: ## Run tests with parallel execution and race detection
	@echo "$(BLUE)Running parallel tests with race detection...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -parallel=4 $(TEST_FLAGS_RACE) ./...

.PHONY: test-coverage
test-coverage: ## Run tests with detailed coverage analysis
	@echo "$(BLUE)Running tests with detailed coverage analysis...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) $(TEST_FLAGS) ./...
	@echo "$(CYAN)Coverage Summary:$(NC)"
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out | tail -1
	@echo "$(GREEN)Coverage data saved to: $(COVERAGE_DIR)/coverage.out$(NC)"
	@echo "$(CYAN)To generate HTML report: go tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html$(NC)"

.PHONY: test-race
test-race: ## Run tests with race detector (slower but thorough)
	@echo "$(BLUE)Running tests with race detection...$(NC)"
	@echo "$(YELLOW)⚠️  Race detection is slower - use for final validation$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) $(TEST_FLAGS_RACE) ./...
	@echo "$(GREEN)✅ Race detection tests passed!$(NC)"

.PHONY: test-coverage-report
test-coverage-report: test ## Generate coverage report and show summary
	@echo "$(BLUE)Coverage Summary:$(NC)"
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out | grep -E "(total|coverage)"
	@echo ""
	@echo "$(CYAN)Generate HTML with: make coverage-html$(NC)"

.PHONY: coverage-html
coverage-html: ## Generate HTML coverage report (requires prior test run)
	@echo "$(BLUE)Generating HTML coverage report...$(NC)"
	@if [ ! -f "$(COVERAGE_DIR)/coverage.out" ]; then \
		echo "$(RED)❌ No coverage data found. Run 'make test' first.$(NC)"; \
		exit 1; \
	fi
	@$(GOCMD) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@echo "$(GREEN)✅ HTML report: $(COVERAGE_DIR)/coverage.html$(NC)"
	@echo "$(CYAN)Open with: open $(COVERAGE_DIR)/coverage.html$(NC)"

.PHONY: test-commands
test-commands: ## Run tests for command packages only
	@echo "$(BLUE)Running command package tests...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) $(TEST_FLAGS) ./cmd/sumpter/commands/...

.PHONY: test-cleanup
test-cleanup: ## Clean test artifacts and temporary files
	@echo "$(BLUE)Cleaning test artifacts...$(NC)"
	@$(GOCMD) clean -testcache
	@rm -rf $(COVERAGE_DIR) $(TEMP_DIR)
	@find . -name "*.test" -type f -delete 2>/dev/null || true
	@find . -name "*.out" -type f -delete 2>/dev/null || true
	@echo "$(GREEN)Test artifacts cleaned$(NC)"

.PHONY: test-integration
test-integration: build ## Run integration tests (requires external dependencies)
	@echo "$(BLUE)Running integration tests...$(NC)"
	@mkdir -p tmp/integration-tests
	$(GOTEST) -v -race -timeout=300s ./tests/integration/...

.PHONY: test-integration-short
test-integration-short: build ## Run integration tests (short mode - no external dependencies)
	@echo "$(BLUE)Running integration tests (short mode - no external dependencies)...$(NC)"
	@mkdir -p tmp/integration-tests
	$(GOTEST) -short -v -race -timeout=120s ./tests/integration/...

.PHONY: test-seekablezstd
test-seekablezstd: ## Run seekable-zstd integration tests (requires CGO)
	@echo "$(BLUE)Running seekable-zstd integration tests...$(NC)"
	@echo "$(YELLOW)⚠️  Requires CGO_ENABLED=1 and seekable-zstd library$(NC)"
	CGO_ENABLED=1 $(GOTEST) -v -tags seekablezstd ./internal/index/store/...

.PHONY: benchmark
benchmark: ## Run benchmarks
	@echo "$(BLUE)Running benchmarks...$(NC)"
	$(GOTEST) $(BENCH_FLAGS) ./...

.PHONY: coverage-check
coverage-check: test ## Check coverage threshold
	@echo "$(BLUE)Checking coverage threshold ($(PRE_PUSH_COVERAGE)%)...$(NC)"
	@COVERAGE=$$($(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	if [ "$${COVERAGE%.*}" -lt "$(PRE_PUSH_COVERAGE)" ]; then \
		echo "$(RED)Coverage $${COVERAGE}% is below threshold $(PRE_PUSH_COVERAGE)%$(NC)"; \
		exit 1; \
	else \
		echo "$(GREEN)Coverage $${COVERAGE}% meets threshold $(PRE_PUSH_COVERAGE)%$(NC)"; \
	fi

.PHONY: coverage-check-dynamic
coverage-check-dynamic: test ## Check coverage threshold based on lifecycle phase
	@echo "$(BLUE)Checking dynamic coverage threshold based on lifecycle phase...$(NC)"
	@if [ -f "config/coverage-thresholds.yaml" ]; then \
		./scripts/validate-coverage-threshold.sh || exit 1; \
	else \
		@echo "$(YELLOW)⚠️ No coverage config found, using default 50% threshold$(NC)"; \
		$(MAKE) coverage-check PRE_PUSH_COVERAGE=50; \
	fi
	@echo "$(GREEN)✅ Dynamic coverage validation passed!$(NC)"

# Asset embedding
.PHONY: embed-assets
embed-assets: ## Embed assets into binary (docs, schemas, examples)
	@echo "$(BLUE)Embedding assets...$(NC)"
	@./scripts/embed-assets.sh
	@echo "$(GREEN)✅ Assets embedded successfully$(NC)"

.PHONY: verify-embeds
verify-embeds: ## Verify embedded assets match SSOT
	@echo "$(BLUE)Verifying embedded assets...$(NC)"
	@./scripts/verify-embeds.sh
	@echo "$(GREEN)✅ Embedded assets verified$(NC)"

# Building
.PHONY: build
build: embed-assets clean ## Build the binary
	@echo "$(BLUE)Building $(BINARY_NAME) v$(VERSION)...$(NC)"
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(BUILD_FLAGS) ./$(CMD_DIR)
	@printf "%b\n" "$(GREEN)✅ Build completed: $(BUILD_DIR)/$(BINARY_NAME)$(NC)" "" "$(WHITE)Usage examples:$(NC)" "  $(CYAN)./$(BUILD_DIR)/$(BINARY_NAME) --help$(NC)" "  $(CYAN)./$(BUILD_DIR)/$(BINARY_NAME) version$(NC)" "  $(CYAN)./$(BUILD_DIR)/$(BINARY_NAME) envinfo$(NC)" "  $(CYAN)./$(BUILD_DIR)/$(BINARY_NAME) docs list$(NC)"

.PHONY: build-race
build-race: ## Build with race detector
	@echo "$(BLUE)Building with race detector...$(NC)"
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -race $(BUILD_FLAGS) ./$(CMD_DIR)

# Cross-platform building
.PHONY: build-all
build-all: ## Build for all platforms
	@echo "$(BLUE)Building for all platforms...$(NC)"
	@mkdir -p $(BUILD_DIR)

	@echo "Building for Linux x64..."
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)

	@echo "Building for Linux ARM64..."
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./$(CMD_DIR)

	@echo "Building for macOS x64..."
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./$(CMD_DIR)

	@echo "Building for macOS ARM64..."
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)

	@echo "Building for Windows x64..."
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./$(CMD_DIR)

	@echo "$(GREEN)✅ Multi-platform build completed:$(NC)"
	@ls -la $(BUILD_DIR)/$(BINARY_NAME)-*

.PHONY: install
install: build ## Install binary to $(INSTALL_DIR) (default ~/.local/bin; override with INSTALL_DIR=...)
	@echo "$(BLUE)Installing $(BINARY_NAME) to $(INSTALL_DIR)...$(NC)"
	@mkdir -p $(INSTALL_DIR)
	cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/
	@echo "$(GREEN)✅ Installed to $(INSTALL_DIR)/$(BINARY_NAME)$(NC)"

# Security scanning
.PHONY: gosec
gosec: ## Run gosec security scanner
	@echo "$(BLUE)Running gosec security scanner...$(NC)"
	@if command -v gosec >/dev/null; then gosec ./...; else echo "$(YELLOW)gosec not found. Install with: make install-dev-tools$(NC)"; echo "$(RED)Security scanning failed - gosec required$(NC)"; exit 1; fi

.PHONY: govulncheck
govulncheck: ## Check for known vulnerabilities
	@echo "$(BLUE)Checking for known vulnerabilities...$(NC)"
	@if command -v govulncheck >/dev/null; then govulncheck ./...; else echo "$(YELLOW)govulncheck not found. Install with: make install-dev-tools$(NC)"; echo "$(RED)Vulnerability check failed - govulncheck required$(NC)"; exit 1; fi

.PHONY: security-scan
security-scan: gosec govulncheck ## Run all security scans
	@echo "$(GREEN)✅ Security scanning completed!$(NC)"

# Dependency management
.PHONY: deps-licenses
deps-licenses: ## Check license compliance (offline)
	@echo "$(BLUE)Checking license compliance...$(NC)"
	@if command -v goneat >/dev/null; then \
		goneat dependencies --licenses --fail-on high; \
	else \
		echo "$(RED)goneat not found. Install from: https://github.com/fulmenhq/goneat$(NC)"; \
		exit 1; \
	fi
	@echo "$(GREEN)✅ License compliance check passed!$(NC)"

.PHONY: deps-cooling
deps-cooling: ## Check package cooling policy (requires network)
	@echo "$(BLUE)Checking package cooling policy...$(NC)"
	@echo "$(YELLOW)⚠️  This check requires network access to package registries$(NC)"
	@if command -v goneat >/dev/null; then \
		goneat dependencies --cooling --fail-on high; \
	else \
		echo "$(RED)goneat not found. Install from: https://github.com/fulmenhq/goneat$(NC)"; \
		exit 1; \
	fi
	@echo "$(GREEN)✅ Package cooling policy check passed!$(NC)"

.PHONY: deps-sbom
deps-sbom: ## Generate SBOM (Software Bill of Materials)
	@echo "$(BLUE)Generating SBOM...$(NC)"
	@if command -v goneat >/dev/null; then \
		goneat dependencies --sbom; \
	else \
		echo "$(RED)goneat not found. Install from: https://github.com/fulmenhq/goneat$(NC)"; \
		exit 1; \
	fi
	@echo "$(GREEN)✅ SBOM generated successfully!$(NC)"

.PHONY: deps-check
deps-check: deps-licenses ## Check dependencies (fast, offline - licenses only)
	@echo "$(GREEN)✅ Dependency checks passed!$(NC)"

.PHONY: deps-check-full
deps-check-full: deps-licenses deps-cooling ## Full dependency check (includes network-based cooling)
	@echo "$(GREEN)✅ Full dependency validation passed!$(NC)"

.PHONY: deps-assess
deps-assess: ## Run comprehensive dependency assessment via goneat assess
	@echo "$(BLUE)Running comprehensive dependency assessment...$(NC)"
	@if command -v goneat >/dev/null; then \
		goneat assess --categories dependencies --fail-on high; \
	else \
		echo "$(RED)goneat not found. Install from: https://github.com/fulmenhq/goneat$(NC)"; \
		exit 1; \
	fi
	@echo "$(GREEN)✅ Dependency assessment passed!$(NC)"

# Module management
.PHONY: mod-tidy
mod-tidy: ## Clean up go.mod and go.sum
	@echo "$(BLUE)Tidying Go modules...$(NC)"
	$(GOMOD) tidy

# Cleanup
.PHONY: clean
clean: ## Clean build artifacts
	@echo "$(BLUE)Cleaning build artifacts...$(NC)"
	@rm -rf $(BUILD_DIR)
	@rm -rf $(COVERAGE_DIR)
	@rm -rf $(TEMP_DIR)
	$(GOCLEAN)

.PHONY: clean-all
clean-all: clean ## Clean everything including dependencies
	@echo "$(BLUE)Cleaning all artifacts and dependencies...$(NC)"
	@rm -rf sbom/
	$(GOMOD) clean

# Pre-commit and CI
.PHONY: precommit
precommit: check-all build test-cleanup test-short coverage-check-dynamic fmt-docs ## Run pre-commit validation
	@echo "$(GREEN)✅ Pre-commit checks passed!$(NC)"

.PHONY: prepush
prepush: check-all test-cleanup test-race coverage-check-dynamic security-scan deps-check-full ## Run pre-push validation with full dependency checks (includes race detection)
	@echo "$(GREEN)✅ Pre-push checks passed!$(NC)"

# Aliases for backward compatibility
.PHONY: pre-commit
pre-commit: precommit ## Deprecated: use 'precommit' instead

.PHONY: pre-push
pre-push: prepush ## Deprecated: use 'prepush' instead

.PHONY: ci
ci: check-all test coverage-check build ## Run CI pipeline
	@echo "$(GREEN)✅ CI pipeline completed successfully!$(NC)"

# Safety checks
.PHONY: safety-check
safety-check: ## Run safety checks for repo hygiene (caches, ignores)
	@echo "$(BLUE)Running safety checks...$(NC)"
	@if [ -f "scripts/safety/safety-check.sh" ]; then \
		./scripts/safety/safety-check.sh all; \
	else \
		echo "$(YELLOW)⚠️ Safety check script not found, creating basic checks$(NC)"; \
		./scripts/safety-check.sh all 2>/dev/null || echo "$(YELLOW)Basic safety checks completed$(NC)"; \
	fi
	@echo "$(GREEN)✅ Safety checks completed$(NC)"

# Development environment setup
.PHONY: check-go-env
check-go-env: ## Check Go environment and PATH configuration
	@echo "$(BLUE)🛡️ Sumpter - Go Environment Analysis$(NC)"
	@echo "$(BLUE)Go Installation:$(NC)"
	@go version 2>/dev/null || echo "$(RED)❌ Go not installed or not in PATH$(NC)"
	@echo "$(BLUE)GOPATH:$(NC) $$((go env GOPATH 2>/dev/null || echo 'Not set'))"
	@echo "$(BLUE)GOBIN:$(NC) $$((go env GOBIN 2>/dev/null || echo 'Using GOPATH/bin'))"
	@echo "$(BLUE)Go binary tools directory:$(NC) $$((go env GOPATH))/bin"
	@echo
	@echo "$(BLUE)PATH Analysis:$(NC)"
	@if echo "$$PATH" | grep -q "$$((go env GOPATH))/bin" 2>/dev/null; then \
		echo "$(GREEN)✅ GOPATH/bin is in PATH$(NC)"; \
	else \
		echo "$(RED)❌ GOPATH/bin NOT in PATH$(NC)"; \
		echo "$(YELLOW)  Tools installed via 'go install' will not be executable$(NC)"; \
		echo "$(YELLOW)  Run 'make fix-go-path' to add to ~/.bashrc$(NC)"; \
	fi
	@echo
	@echo "$(BLUE)Development Tools Status:$(NC)"
	@for tool in golangci-lint gosec govulncheck yamlfmt yq; do \
		if command -v $$tool >/dev/null 2>&1; then \
			echo "$(GREEN)✅ $$tool$(NC) - $$((command -v $$tool))"; \
		else \
			echo "$(RED)❌ $$tool$(NC) - not found in PATH"; \
		fi \
	done

.PHONY: fix-go-path
fix-go-path: ## Add GOPATH/bin to PATH in ~/.bashrc
	@echo "$(BLUE)🛡️ Sumpter - Fixing Go PATH Configuration$(NC)"
	@GOPATH_BIN="$$((go env GOPATH))/bin"; \
	if grep -q "$$GOPATH_BIN" ~/.bashrc 2>/dev/null; then \
		echo "$(GREEN)✅ GOPATH/bin already in ~/.bashrc$(NC)"; \
	else \
		echo "$(YELLOW)Adding GOPATH/bin to ~/.bashrc$(NC)"; \
		echo "" >> ~/.bashrc; \
		echo "# Go tools (added by make fix-go-path)" >> ~/.bashrc; \
		echo "export PATH=\"\$$PATH:\$$HOME/go/bin\"" >> ~/.bashrc; \
		echo "$(GREEN)✅ Added to ~/.bashrc$(NC)"; \
		echo "$(YELLOW)⚠️  Run 'source ~/.bashrc' or restart terminal to apply$(NC)"; \
	fi

.PHONY: install-dev-tools
install-dev-tools: ## Install development and security tools
	@echo "$(BLUE)🛡️ Sumpter - Installing Development Tools$(NC)"
	@GOPATH_BIN="$$((go env GOPATH))/bin"; \
	echo "$(BLUE)Installing to: $$GOPATH_BIN$(NC)"; \
	mkdir -p "$$GOPATH_BIN"
	@echo "$(BLUE)Installing Go security tools...$(NC)"
	@if ! command -v gosec >/dev/null; then \
		echo "Installing gosec..."; \
		go install github.com/securego/gosec/v2/cmd/gosec@latest; \
	else \
		echo "$(GREEN)✅ gosec already installed$(NC)"; \
	fi
	@if ! command -v govulncheck >/dev/null; then \
		echo "Installing govulncheck..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	else \
		echo "$(GREEN)✅ govulncheck already installed$(NC)"; \
	fi
	@if ! command -v golangci-lint >/dev/null; then \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$$((go env GOPATH))/bin"; \
	else \
		echo "$(GREEN)✅ golangci-lint already installed$(NC)"; \
	fi
	@echo "$(BLUE)Installing document formatting tools...$(NC)"
	@if ! command -v yamlfmt >/dev/null; then \
		echo "Installing yamlfmt..."; \
		go install github.com/google/yamlfmt/cmd/yamlfmt@latest; \
	else \
		echo "$(GREEN)✅ yamlfmt already installed$(NC)"; \
	fi
	@if ! command -v yq >/dev/null; then \
		echo "Installing yq..."; \
		go install github.com/mikefarah/yq/v4@latest; \
	else \
		echo "$(GREEN)✅ yq already installed$(NC)"; \
	fi
	@echo "$(GREEN)✅ Development tools installation completed$(NC)"
	@if ! echo "$$PATH" | grep -q "$$((go env GOPATH))/bin" 2>/dev/null; then \
		echo "$(YELLOW)⚠️  Tools installed but not in PATH$(NC)"; \
		echo "$(YELLOW)   Run 'make fix-go-path' then 'source ~/.bashrc'$(NC)"; \
	fi

.PHONY: setup-dev-env
setup-dev-env: check-go-env fix-go-path install-dev-tools ## Complete development environment setup
	@echo "$(GREEN)🛡️ Development environment setup completed!$(NC)"
	@echo "$(YELLOW)Next steps:$(NC)"
	@echo "  1. Run: source ~/.bashrc"
	@echo "  2. Run: make check-go-env  # to verify"
	@echo "  3. Run: make check-all     # to test tools"

# Version management
.PHONY: version
version: ## Show current version
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Git Commit: $(GIT_COMMIT)"

.PHONY: version-bump-patch
version-bump-patch: ## Bump patch version
	@echo "$(BLUE)Bumping patch version...$(NC)"
	@NEW_VERSION=$$(echo $(VERSION) | awk -F. '{$$3++; print $$1"."$$2"."$$3}'); \
	echo $$NEW_VERSION > VERSION; \
	echo "$(GREEN)Version bumped to $$NEW_VERSION$(NC)"

.PHONY: version-bump-minor
version-bump-minor: ## Bump minor version
	@echo "$(BLUE)Bumping minor version...$(NC)"
	@NEW_VERSION=$$(echo $(VERSION) | awk -F. '{$$2++; $$3=0; print $$1"."$$2"."$$3}'); \
	echo $$NEW_VERSION > VERSION; \
	echo "$(GREEN)Version bumped to $$NEW_VERSION$(NC)"

.PHONY: version-bump-major
version-bump-major: ## Bump major version
	@echo "$(BLUE)Bumping major version...$(NC)"
	@NEW_VERSION=$$(echo $(VERSION) | awk -F. '{$$1++; $$2=0; $$3=0; print $$1"."$$2"."$$3}'); \
	echo $$NEW_VERSION > VERSION; \
	echo "$(GREEN)Version bumped to $$NEW_VERSION$(NC)"

.PHONY: version-get
version-get: ## Get current version from VERSION file
	@cat VERSION

.PHONY: version-set
version-set: ## Set explicit version (usage: make version-set VERSION_NEW=1.2.3)
	@if [ -z "$(VERSION_NEW)" ]; then \
		echo "$(RED)Error: VERSION_NEW is required$(NC)"; \
		echo "$(YELLOW)Usage: make version-set VERSION_NEW=1.2.3$(NC)"; \
		exit 1; \
	fi
	@echo "$(BLUE)Setting version to $(VERSION_NEW)...$(NC)"
	@echo "$(VERSION_NEW)" > VERSION
	@echo "$(GREEN)Version set to $(VERSION_NEW)$(NC)"

.PHONY: all
all: build ## Build default target

# Default target
.DEFAULT_GOAL := help
