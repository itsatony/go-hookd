#!/bin/bash
# Full coverage check including integration tests

set -e

echo "🧪 Running full test suite with coverage..."
echo ""

# Check if Docker is running (for integration tests)
if ! docker ps >/dev/null 2>&1 && ! podman ps >/dev/null 2>&1; then
    echo "⚠️  Warning: Docker/Podman not available - integration tests will be skipped"
    echo "   Coverage will be lower without integration tests"
    echo ""
fi

# Run with integration tag
echo "Running all tests (unit + E2E + integration)..."
if [ -n "$DOCKER_HOST" ]; then
    go test ./internal/... -tags=integration -coverprofile=coverage.out -covermode=atomic -timeout=5m
else
    # Try with podman socket
    export DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock
    go test ./internal/... -tags=integration -coverprofile=coverage.out -covermode=atomic -timeout=5m ||  \
    go test ./internal/... -tags=integration -coverprofile=coverage.out -covermode=atomic -timeout=5m
fi

echo ""
echo "📊 Coverage Report:"
go tool cover -func=coverage.out | tail -1

echo ""
COVERAGE=$(go tool cover -func=coverage.out | tail -1 | awk '{print $NF}' | sed 's/%//')
TARGET=90.0

if (( $(echo "$COVERAGE >= $TARGET" | bc -l) )); then
    echo "✅ Coverage target met: $COVERAGE% >= $TARGET%"
    exit 0
else
    echo "⚠️  Coverage below target: $COVERAGE% < $TARGET%"
    echo "   Gap: $(echo "$TARGET - $COVERAGE" | bc)%"
    exit 1
fi
