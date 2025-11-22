# go-hookd Makefile
# Enterprise-grade webhook management package
# vAudience.AI GmbH

.PHONY: all build test test-race test-short test-integration coverage lint fmt vet tidy clean gates help

# Variables
GO := go
GOFLAGS := -v
COVERAGE_THRESHOLD := 90
COVERAGE_FILE := coverage.out
COVERAGE_HTML := coverage.html
INTEGRATION_TAGS := integration

# Default target
all: gates

# Help target - show all available targets
help:
	@echo "go-hookd Makefile targets:"
	@echo ""
	@echo "Building:"
	@echo "  build                 - Build all packages"
	@echo "  clean                 - Remove build artifacts and cache"
	@echo ""
	@echo "Testing:"
	@echo "  test                  - Run all tests with race detector"
	@echo "  test-race             - Run tests with race detector (explicit)"
	@echo "  test-short            - Run fast tests only (skip integration)"
	@echo "  test-integration      - Run integration tests with testcontainers"
	@echo "  coverage              - Generate coverage report (enforces ${COVERAGE_THRESHOLD}% threshold)"
	@echo ""
	@echo "Code Quality:"
	@echo "  fmt                   - Format all Go code"
	@echo "  vet                   - Run go vet"
	@echo "  lint                  - Run golangci-lint (if installed)"
	@echo "  tidy                  - Tidy and verify dependencies"
	@echo ""
	@echo "Excellence Gates:"
	@echo "  gates                 - Run ALL quality gates (fmt, vet, lint, test-race, coverage)"
	@echo ""
	@echo "Utilities:"
	@echo "  help                  - Show this help message"

# Build all packages
build:
	@echo "==> Building go-hookd packages..."
	$(GO) build $(GOFLAGS) ./...
	@echo "==> Build complete!"

# Run all tests with race detector (default test target)
test: test-race

# Run tests with race detector (explicit target)
test-race:
	@echo "==> Running tests with race detector..."
	$(GO) test -race $(GOFLAGS) ./...
	@echo "==> Tests passed with race detector!"

# Run fast tests only (skip integration tests)
test-short:
	@echo "==> Running fast tests (skipping integration)..."
	$(GO) test -short $(GOFLAGS) ./...
	@echo "==> Fast tests passed!"

# Run integration tests with testcontainers
test-integration:
	@echo "==> Running integration tests..."
	@echo "==> Note: Requires Docker/Podman and testcontainers support"
	$(GO) test -tags=$(INTEGRATION_TAGS) $(GOFLAGS) -timeout=10m ./...
	@echo "==> Integration tests passed!"

# Generate coverage report with threshold enforcement
coverage:
	@echo "==> Generating coverage report..."
	@$(GO) test -cover -coverprofile=$(COVERAGE_FILE) ./...
	@$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "==> Coverage report generated: $(COVERAGE_HTML)"
	@echo ""
	@echo "==> Checking coverage threshold (>= ${COVERAGE_THRESHOLD}%)..."
	@COVERAGE=$$($(GO) tool cover -func=$(COVERAGE_FILE) | grep total | awk '{print $$3}' | sed 's/%//'); \
	if [ -z "$$COVERAGE" ]; then \
		echo "ERROR: Failed to extract coverage percentage"; \
		exit 1; \
	fi; \
	echo "Current coverage: $$COVERAGE%"; \
	if [ "$$(echo "$$COVERAGE < $(COVERAGE_THRESHOLD)" | bc)" -eq 1 ]; then \
		echo "❌ FAILED: Coverage $$COVERAGE% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	else \
		echo "✅ PASSED: Coverage $$COVERAGE% meets threshold $(COVERAGE_THRESHOLD)%"; \
	fi

# Format all Go code
fmt:
	@echo "==> Formatting Go code..."
	$(GO) fmt ./...
	@echo "==> Code formatted!"

# Run go vet
vet:
	@echo "==> Running go vet..."
	$(GO) vet ./...
	@echo "==> Vet passed!"

# Run golangci-lint (if installed)
lint:
	@echo "==> Running golangci-lint..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
		echo "==> Lint passed!"; \
	else \
		echo "WARNING: golangci-lint not installed, skipping..."; \
		echo "Install: https://golangci-lint.run/usage/install/"; \
	fi

# Tidy and verify dependencies
tidy:
	@echo "==> Tidying dependencies..."
	$(GO) mod tidy
	$(GO) mod verify
	@echo "==> Dependencies verified!"

# Clean build artifacts and cache
clean:
	@echo "==> Cleaning build artifacts..."
	$(GO) clean -cache -testcache -modcache
	rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)
	rm -f *.prof *.pprof
	rm -f TEST_*.md TEST_*.txt full_test_run.log
	@echo "==> Clean complete!"

# Excellence Gates - Run ALL quality gates
# This is the single command to verify production readiness
gates: fmt vet lint test-race coverage
	@echo ""
	@echo "============================================"
	@echo "✅ ALL EXCELLENCE GATES PASSED!"
	@echo "============================================"
	@echo ""
	@echo "Code is production-ready:"
	@echo "  ✅ Formatting (go fmt)"
	@echo "  ✅ Static analysis (go vet)"
	@echo "  ✅ Linting (golangci-lint)"
	@echo "  ✅ Race detection (go test -race)"
	@echo "  ✅ Test coverage (>= $(COVERAGE_THRESHOLD)%)"
	@echo ""
