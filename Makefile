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
#   3. make pre-commit   - Pre-commit validation (dynamic, lifecycle-aware)
#   4. make pre-push     - Pre-push validation (80% production threshold)
#   5. make build        - Build the final binary
#   6. make install      - Install binary to PATH
#
# Coverage Strategy:
#   • Pre-commit: Dynamic via LIFECYCLE_PHASE + config/coverage-thresholds.yaml
#                 (alpha 50%, beta 70%, production 80%).
#   • Pre-push:   80% threshold, comprehensive validation

# Variables
BINARY_NAME := sumpter
VERSION := $(shell cat VERSION)
BUILD_DIR := dist
COVERAGE_DIR := coverage
TEMP_DIR := tmp
CMD_DIR := cmd/sumpter

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
TEST_FLAGS := -v -race -coverprofile=$(COVERAGE_DIR)/coverage.out
BENCH_FLAGS := -bench=. -benchmem
PRE_PUSH_COVERAGE := 80

# Color output disabled for cross-platform simplicity
define color_echo
printf '%s\n' "$(2)"
endef

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
	@echo "$(CYAN)Sumpter - XML Streaming Engine$(NC)"
	@echo "$(YELLOW)High-performance Go-based XML processing$(NC)"
	@echo ""
	@echo "$(WHITE)Available commands:$(NC)"
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo ""
	@echo "$(WHITE)Quick workflow:$(NC)"
	@echo "  $(CYAN)make check-all$(NC)     - Fast quality checks"
	@echo "  $(CYAN)make test$(NC)          - Run tests with coverage"
	@echo "  $(CYAN)make test-coverage$(NC) - Detailed coverage analysis"
	@echo "  $(CYAN)make coverage-check-dynamic$(NC) - Dynamic coverage check"
	@echo "  $(CYAN)make pre-commit$(NC)    - Pre-commit validation"
	@echo "  $(CYAN)make build$(NC)         - Build the binary"
	@echo "  $(CYAN)make dev$(NC)           - Setup development environment"

# Development setup
.PHONY: dev
dev: ## Set up development environment
	@echo "$(BLUE)Setting up Sumpter development environment...$(NC)"
	$(GOMOD) download
	$(GOMOD) tidy
	@mkdir -p $(BUILD_DIR)
	@mkdir -p $(COVERAGE_DIR)
	@mkdir -p $(TEMP_DIR)
	@echo "$(GREEN)✅ Development environment ready!$(NC)"

# Quality checks
.PHONY: check-all
check-all: fmt-strict vet lint safety-check schema-validate ## Run all quality checks (fast)
	$(call color_echo,$(GREEN),✅ All quality checks passed!)

# Schema validation
.PHONY: schema-validate
schema-validate: ## Validate JSON schemas using goneat
	$(call color_echo,$(BLUE),Validating JSON schemas...)
	@goneat validate --include schemas/ --format json --fail-on high --no-color
	$(call color_echo,$(GREEN),✅ Schema validation passed!)

# Code formatting
.PHONY: fmt
fmt: ## Format Go code only
	$(call color_echo,$(BLUE),Formatting Go code...)
	$(GOFMT) ./...

.PHONY: fmt-strict
fmt-strict: ## Strictly check Go code formatting, fails if issues found
	$(call color_echo,$(BLUE),Checking code formatting...)
	@if find . -name "*.go" -not -path "./vendor/*" -not -path "./.plans/*" | xargs gofmt -l | grep .; then \
		$(call color_echo,$(RED),❌ Formatting issues found. Run 'make fmt' to fix.); \
		exit 1; \
	fi
	$(call color_echo,$(GREEN),✅ Code formatting check passed)

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
	@find . -name "*.md" | grep -v vendor/ | grep -v node_modules/ | while read file; do \
		echo "    Cleaning: $$file"; \
		sed -i.bak 's/[[:space:]]*$$//' "$$file" && rm -f "$$file.bak"; \
	done

.PHONY: fmt-whitespace
fmt-whitespace: ## Fix end-of-file and trailing whitespace issues
	@echo "  ✂️ Fixing whitespace and end-of-file issues..."
	@find . -type f \( -name "*.go" -o -name "*.md" -o -name "*.yml" -o -name "*.yaml" -o -name "*.json" -o -name "*.txt" -o -name "*.sh" -o -name "Makefile" -o -name "Dockerfile*" \) \
		| grep -v vendor/ | grep -v node_modules/ | grep -v .git/ | grep -v dist/ | grep -v bin/ \
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
	$(call color_echo,$(GREEN),✅ All formatting completed)

# Linting and vetting
.PHONY: vet
vet: ## Run go vet
	$(call color_echo,$(BLUE),Running go vet...)
	$(GOVET) ./...

.PHONY: lint
lint: ## Run golangci-lint (install if needed)
	$(call color_echo,$(BLUE),Running linter...)
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		$(call color_echo,$(YELLOW),Installing golangci-lint...); \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$((go env GOPATH))/bin; \
	fi
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --color=never; \
	else \
		$$(go env GOPATH)/bin/golangci-lint run --color=never; \
	fi

# Testing
.PHONY: test
test: ## Run all tests with coverage
	@echo "$(BLUE)Running tests with coverage...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) $(TEST_FLAGS) ./...
	@$(GOCMD) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	$(call color_echo,$(GREEN),Coverage report generated: $(COVERAGE_DIR)/coverage.html)

.PHONY: test-short
test-short: ## Run tests without network dependencies
	$(call color_echo,$(BLUE),Running short tests...)
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -short $(TEST_FLAGS) ./...

.PHONY: test-verbose
test-verbose: ## Run tests with verbose output
	$(call color_echo,$(BLUE),Running verbose tests...)
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -v $(TEST_FLAGS) ./...

.PHONY: test-parallel
test-parallel: ## Run tests with parallel execution and race detection
	$(call color_echo,$(BLUE),Running parallel tests with race detection...)
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -parallel=4 -race $(TEST_FLAGS) ./...

.PHONY: test-coverage
test-coverage: ## Run tests with detailed coverage analysis
	$(call color_echo,$(BLUE),Running tests with detailed coverage analysis...)
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) $(TEST_FLAGS) ./...
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out | tail -1
	@$(GOCMD) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	$(call color_echo,$(GREEN),Coverage report: $(COVERAGE_DIR)/coverage.html)

.PHONY: test-coverage-report
test-coverage-report: test ## Generate coverage report and show summary
	$(call color_echo,$(BLUE),Coverage Summary:)
	@$(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out | grep -E "(total|coverage)"
	@echo ""
	$(call color_echo,$(GREEN),Detailed HTML report: $(COVERAGE_DIR)/coverage.html)

.PHONY: test-commands
test-commands: ## Run tests for command packages only
	$(call color_echo,$(BLUE),Running command package tests...)
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) $(TEST_FLAGS) ./cmd/sumpter/commands/...

.PHONY: test-cleanup
test-cleanup: ## Clean test artifacts and temporary files
	$(call color_echo,$(BLUE),Cleaning test artifacts...)
	@rm -rf $(COVERAGE_DIR) $(TEMP_DIR)
	@find . -name "*.test" -type f -delete 2>/dev/null || true
	@find . -name "*.out" -type f -delete 2>/dev/null || true
	$(call color_echo,$(GREEN),Test artifacts cleaned)

.PHONY: test-integration
test-integration: build ## Run integration tests (requires external dependencies)
	$(call color_echo,$(BLUE),Running integration tests...)
	@mkdir -p tmp/integration-tests
	$(GOTEST) -v -race -timeout=300s ./tests/integration/...

.PHONY: test-integration-short
test-integration-short: build ## Run integration tests (short mode - no external dependencies)
	$(call color_echo,$(BLUE),Running integration tests (short mode - no external dependencies)...)
	@mkdir -p tmp/integration-tests
	$(GOTEST) -short -v -race -timeout=120s ./tests/integration/...

.PHONY: benchmark
benchmark: ## Run benchmarks
	$(call color_echo,$(BLUE),Running benchmarks...)
	$(GOTEST) $(BENCH_FLAGS) ./...

.PHONY: coverage-check
coverage-check: test ## Check coverage threshold
	$(call color_echo,$(BLUE),Checking coverage threshold ($(PRE_PUSH_COVERAGE)%)...)
	@COVERAGE=$$($(GOCMD) tool cover -func=$(COVERAGE_DIR)/coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	if [ "$${COVERAGE%.*}" -lt "$(PRE_PUSH_COVERAGE)" ]; then \
		$(call color_echo,$(RED),Coverage $${COVERAGE}% is below threshold $(PRE_PUSH_COVERAGE)%); \
		exit 1; \
	else \
		$(call color_echo,$(GREEN),Coverage $${COVERAGE}% meets threshold $(PRE_PUSH_COVERAGE)%); \
	fi

.PHONY: coverage-check-dynamic
coverage-check-dynamic: test ## Check coverage threshold based on lifecycle phase
	$(call color_echo,$(BLUE),Checking dynamic coverage threshold based on lifecycle phase...)
	@if [ -f "config/coverage-thresholds.yaml" ]; then \
		./scripts/validate-coverage-threshold.sh || exit 1; \
	else \
		$(call color_echo,$(YELLOW),⚠️ No coverage config found, using default 50% threshold); \
		$(MAKE) coverage-check PRE_PUSH_COVERAGE=50; \
	fi
	$(call color_echo,$(GREEN),✅ Dynamic coverage validation passed!)

# Asset embedding
.PHONY: embed-assets
embed-assets: ## Embed assets into binary (docs, schemas, examples)
	$(call color_echo,$(BLUE),Embedding assets...)
	@./scripts/embed-assets.sh
	$(call color_echo,$(GREEN),✅ Assets embedded successfully)

.PHONY: verify-embeds
verify-embeds: ## Verify embedded assets match SSOT
	$(call color_echo,$(BLUE),Verifying embedded assets...)
	@./scripts/verify-embeds.sh
	$(call color_echo,$(GREEN),✅ Embedded assets verified)

# Building
.PHONY: build
build: embed-assets clean ## Build the binary
	$(call color_echo,$(BLUE),Building $(BINARY_NAME) v$(VERSION)...)
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(BUILD_FLAGS) ./$(CMD_DIR)
	$(call color_echo,$(GREEN),✅ Build completed: $(BUILD_DIR)/$(BINARY_NAME))
	@echo ""
	$(call color_echo,$(WHITE),Usage examples:)
	@echo "  $(CYAN)./$(BUILD_DIR)/$(BINARY_NAME) --help$(NC)"
	@echo "  $(CYAN)./$(BUILD_DIR)/$(BINARY_NAME) version$(NC)"
	@echo "  $(CYAN)./$(BUILD_DIR)/$(BINARY_NAME) envinfo$(NC)"
	@echo "  $(CYAN)./$(BUILD_DIR)/$(BINARY_NAME) docs list$(NC)"

.PHONY: build-race
build-race: ## Build with race detector
	$(call color_echo,$(BLUE),Building with race detector...)
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -race $(BUILD_FLAGS) ./$(CMD_DIR)

# Cross-platform building
.PHONY: build-all
build-all: ## Build for all platforms
	$(call color_echo,$(BLUE),Building for all platforms...)
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

	$(call color_echo,$(GREEN),✅ Multi-platform build completed:)
	@ls -la $(BUILD_DIR)/$(BINARY_NAME)-*

.PHONY: install
install: build ## Install binary to GOPATH/bin
	$(call color_echo,$(BLUE),Installing $(BINARY_NAME)...)
	cp $(BUILD_DIR)/$(BINARY_NAME) $$((go env GOPATH))/bin/
	$(call color_echo,$(GREEN),✅ Installed to $$((go env GOPATH))/bin/$(BINARY_NAME))

# Security scanning
.PHONY: gosec
gosec: ## Run gosec security scanner
	$(call color_echo,$(BLUE),Running gosec security scanner...)
	@if command -v gosec >/dev/null; then \
		gosec ./...; \
	else \
		$(call color_echo,$(YELLOW),gosec not found. Install with: make install-dev-tools); \
		$(call color_echo,$(RED),Security scanning failed - gosec required); \
		exit 1; \
	fi

.PHONY: govulncheck
govulncheck: ## Check for known vulnerabilities
	$(call color_echo,$(BLUE),Checking for known vulnerabilities...)
	@if command -v govulncheck >/dev/null; then \
		govulncheck ./...; \
	else \
		$(call color_echo,$(YELLOW),govulncheck not found. Install with: make install-dev-tools); \
		$(call color_echo,$(RED),Vulnerability check failed - govulncheck required); \
		exit 1; \
	fi

.PHONY: security-scan
security-scan: gosec govulncheck ## Run all security scans
	$(call color_echo,$(GREEN),✅ Security scanning completed!)

# Module management
.PHONY: mod-tidy
mod-tidy: ## Clean up go.mod and go.sum
	$(call color_echo,$(BLUE),Tidying Go modules...)
	$(GOMOD) tidy

# Cleanup
.PHONY: clean
clean: ## Clean build artifacts
	$(call color_echo,$(BLUE),Cleaning build artifacts...)
	@rm -rf $(BUILD_DIR)
	@rm -rf $(COVERAGE_DIR)
	@rm -rf $(TEMP_DIR)
	$(GOCLEAN)

.PHONY: clean-all
clean-all: clean ## Clean everything including dependencies
	$(call color_echo,$(BLUE),Cleaning all artifacts and dependencies...)
	$(GOMOD) clean

# Pre-commit and CI
.PHONY: pre-commit
pre-commit: check-all test-clean-servers test-short coverage-check-dynamic fmt-docs ## Run pre-commit validation
	$(call color_echo,$(GREEN),✅ Pre-commit checks passed!)

.PHONY: pre-push
pre-push: check-all test-clean-servers test coverage-check-dynamic test-integration-short security-scan ## Run pre-push validation
	$(call color_echo,$(GREEN),✅ Pre-push checks passed!)

.PHONY: ci
ci: check-all test coverage-check build ## Run CI pipeline
	$(call color_echo,$(GREEN),✅ CI pipeline completed successfully!)

# Safety checks
.PHONY: safety-check
safety-check: ## Run safety checks for repo hygiene (caches, ignores)
	$(call color_echo,$(BLUE),Running safety checks...)
	@if [ -f "scripts/safety/safety-check.sh" ]; then \
		./scripts/safety/safety-check.sh all; \
	else \
		$(call color_echo,$(YELLOW),⚠️ Safety check script not found, creating basic checks); \
		./scripts/safety-check.sh all 2>/dev/null || $(call color_echo,$(YELLOW),Basic safety checks completed); \
	fi
	$(call color_echo,$(GREEN),✅ Safety checks completed)

# Development environment setup
.PHONY: check-go-env
check-go-env: ## Check Go environment and PATH configuration
	$(call color_echo,$(BLUE),🛡️ Sumpter - Go Environment Analysis)
	$(call color_echo,$(BLUE),Go Installation:)
	@go version 2>/dev/null || $(call color_echo,$(RED),❌ Go not installed or not in PATH)
	@echo "$(BLUE)GOPATH:$(NC) $$((go env GOPATH 2>/dev/null || echo 'Not set'))"
	@echo "$(BLUE)GOBIN:$(NC) $$((go env GOBIN 2>/dev/null || echo 'Using GOPATH/bin'))"
	@echo "$(BLUE)Go binary tools directory:$(NC) $$((go env GOPATH))/bin"
	@echo
	$(call color_echo,$(BLUE),PATH Analysis:)
	@if echo "$$PATH" | grep -q "$$((go env GOPATH))/bin" 2>/dev/null; then \
		$(call color_echo,$(GREEN),✅ GOPATH/bin is in PATH); \
	else \
		$(call color_echo,$(RED),❌ GOPATH/bin NOT in PATH); \
		$(call color_echo,$(YELLOW),  Tools installed via 'go install' will not be executable); \
		$(call color_echo,$(YELLOW),  Run 'make fix-go-path' to add to ~/.bashrc); \
	fi
	@echo
	$(call color_echo,$(BLUE),Development Tools Status:)
	@for tool in golangci-lint gosec govulncheck yamlfmt yq; do \
		if command -v $$tool >/dev/null 2>&1; then \
			echo "$(GREEN)✅ $$tool$(NC) - $$((command -v $$tool))"; \
		else \
			$(call color_echo,$(RED),❌ $$tool - not found in PATH); \
		fi \
	done

.PHONY: fix-go-path
fix-go-path: ## Add GOPATH/bin to PATH in ~/.bashrc
	$(call color_echo,$(BLUE),🛡️ Sumpter - Fixing Go PATH Configuration)
	@GOPATH_BIN="$$((go env GOPATH))/bin"; \
	if grep -q "$$GOPATH_BIN" ~/.bashrc 2>/dev/null; then \
		$(call color_echo,$(GREEN),✅ GOPATH/bin already in ~/.bashrc); \
	else \
		$(call color_echo,$(YELLOW),Adding GOPATH/bin to ~/.bashrc); \
		echo "" >> ~/.bashrc; \
		echo "# Go tools (added by make fix-go-path)" >> ~/.bashrc; \
		echo "export PATH=\"\$$PATH:\$$HOME/go/bin\"" >> ~/.bashrc; \
		$(call color_echo,$(GREEN),✅ Added to ~/.bashrc); \
		$(call color_echo,$(YELLOW),⚠️  Run 'source ~/.bashrc' or restart terminal to apply); \
	fi

.PHONY: install-dev-tools
install-dev-tools: ## Install development and security tools
	$(call color_echo,$(BLUE),🛡️ Sumpter - Installing Development Tools)
	@GOPATH_BIN="$$((go env GOPATH))/bin"; \
	$(call color_echo,$(BLUE),Installing to: $$GOPATH_BIN); \
	mkdir -p "$$GOPATH_BIN"
	$(call color_echo,$(BLUE),Installing Go security tools...)
	@if ! command -v gosec >/dev/null; then \
		echo "Installing gosec..."; \
		go install github.com/securego/gosec/v2/cmd/gosec@latest; \
	else \
		$(call color_echo,$(GREEN),✅ gosec already installed); \
	fi
	@if ! command -v govulncheck >/dev/null; then \
		echo "Installing govulncheck..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	else \
		$(call color_echo,$(GREEN),✅ govulncheck already installed); \
	fi
	@if ! command -v golangci-lint >/dev/null; then \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$$((go env GOPATH))/bin"; \
	else \
		$(call color_echo,$(GREEN),✅ golangci-lint already installed); \
	fi
	$(call color_echo,$(BLUE),Installing document formatting tools...)
	@if ! command -v yamlfmt >/dev/null; then \
		echo "Installing yamlfmt..."; \
		go install github.com/google/yamlfmt/cmd/yamlfmt@latest; \
	else \
		$(call color_echo,$(GREEN),✅ yamlfmt already installed); \
	fi
	@if ! command -v yq >/dev/null; then \
		echo "Installing yq..."; \
		go install github.com/mikefarah/yq/v4@latest; \
	else \
		$(call color_echo,$(GREEN),✅ yq already installed); \
	fi
	$(call color_echo,$(GREEN),✅ Development tools installation completed)
	@if ! echo "$$PATH" | grep -q "$$((go env GOPATH))/bin" 2>/dev/null; then \
		$(call color_echo,$(YELLOW),⚠️  Tools installed but not in PATH); \
		$(call color_echo,$(YELLOW),   Run 'make fix-go-path' then 'source ~/.bashrc'); \
	fi

.PHONY: setup-dev-env
setup-dev-env: check-go-env fix-go-path install-dev-tools ## Complete development environment setup
	$(call color_echo,$(GREEN),🛡️ Development environment setup completed!)
	$(call color_echo,$(YELLOW),Next steps:)
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
	$(call color_echo,$(BLUE),Bumping patch version...)
	@NEW_VERSION=$$(echo $(VERSION) | awk -F. '{$$3++; print $$1"."$$2"."$$3}'); \
	echo $$NEW_VERSION > VERSION; \
	$(call color_echo,$(GREEN),Version bumped to $$NEW_VERSION)

.PHONY: version-bump-minor
version-bump-minor: ## Bump minor version
	$(call color_echo,$(BLUE),Bumping minor version...)
	@NEW_VERSION=$$(echo $(VERSION) | awk -F. '{$$2++; $$3=0; print $$1"."$$2"."$$3}'); \
	echo $$NEW_VERSION > VERSION; \
	$(call color_echo,$(GREEN),Version bumped to $$NEW_VERSION)

.PHONY: version-bump-major
version-bump-major: ## Bump major version
	$(call color_echo,$(BLUE),Bumping major version...)
	@NEW_VERSION=$$(echo $(VERSION) | awk -F. '{$$1++; $$2=0; $$3=0; print $$1"."$$2"."$$3}'); \
	echo $$NEW_VERSION > VERSION; \
	$(call color_echo,$(GREEN),Version bumped to $$NEW_VERSION)

.PHONY: version-get
version-get: ## Get current version from VERSION file
	@cat VERSION

.PHONY: version-set
version-set: ## Set explicit version (usage: make version-set VERSION_NEW=1.2.3)
	@if [ -z "$(VERSION_NEW)" ]; then \
		$(call color_echo,$(RED),Error: VERSION_NEW is required); \
		$(call color_echo,$(YELLOW),Usage: make version-set VERSION_NEW=1.2.3); \
		exit 1; \
	fi
	$(call color_echo,$(BLUE),Setting version to $(VERSION_NEW)...)
	@echo "$(VERSION_NEW)" > VERSION
	$(call color_echo,$(GREEN),Version set to $(VERSION_NEW))

# Default target
.DEFAULT_GOAL := help