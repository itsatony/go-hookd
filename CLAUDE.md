# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

---

## Project Overview

**go-hookd** is an enterprise-grade Go package for webhook management. This is a **NEW project** being built from scratch following vAudience.AI's "Excellence. Always." philosophy.

**Key Characteristics**:
- **Pure library package** - NO HTTP handlers (consumer responsibility)
- **PostgreSQL-native** - Queue-based delivery using SKIP LOCKED (no external message broker)
- **vAI ecosystem** - Built on go-cuserr, go-version, and go-pubbing
- **90%+ test coverage** - Non-negotiable quality standard
- **Production-ready from day one** - No stubs, mocks, or TODOs

**Package Path**: `github.com/itsatony/go-hookd`

---

## Critical Documentation

Read these files IN FULL before starting any work:

1. **`docs/implementation_guide.md`** (2773 lines) - THE master plan
   - Complete architecture & component design
   - All domain models & interfaces defined
   - 9 implementation phases with specific deliverables
   - Excellence gates with validation criteria

2. **`docs/code_rules.md`** (2140 lines) - vAudience.AI development standards
   - Core principles: Thread safety, no magic strings, go-cuserr everywhere
   - Standard library usage patterns (go-cuserr, go-pubbing, go-version, zap)
   - Makefile patterns, testing requirements, error handling

3. **`docs/codefinder_mcp_guide.md`** - CodeFinder MCP tools reference
   - Use CodeFinder MCP tools extensively during implementation
   - 50+ examples of efficient code navigation and analysis

---

## Core Architecture

```
┌─────────────────────────────────────────────┐
│         Application Layer (User Code)       │
│           HTTP Handlers, REST APIs          │
└──────────────────┬──────────────────────────┘
                   │ Uses Package API
                   ▼
┌─────────────────────────────────────────────┐
│          go-hookd Package                   │
│                                             │
│  ┌─────────────────────────────────────┐   │
│  │  Manager (subscription CRUD)        │   │
│  │  DeliveryEngine (worker pool)       │   │
│  │  CircuitBreaker (per-endpoint)      │   │
│  │  IdempotencyStore (deduplication)   │   │
│  └─────────────────────────────────────┘   │
│                                             │
│  ┌─────────────────────────────────────┐   │
│  │  Internal Event Bus (go-pubbing)    │   │
│  │  Topics: delivery.*, circuit.*,     │   │
│  │          metrics.*, audit.*         │   │
│  └─────────────────────────────────────┘   │
└──────────────────┬──────────────────────────┘
                   │ Repository Interface
                   ▼
┌─────────────────────────────────────────────┐
│          PostgreSQL Database                │
│  subscriptions, deliveries, attempts,       │
│  circuit_breaker_state, idempotency_store   │
└─────────────────────────────────────────────┘
```

**Separation of Concerns**:
- Package provides webhook management logic ONLY
- Consumers provide HTTP handlers, routing, auth
- PostgreSQL-backed queue (no Kafka, RabbitMQ, etc.)

---

## Mandatory Dependencies

All three are **non-negotiable** and deeply integrated:

### 1. go-cuserr (v0.3.0) - Error Management
**ALL errors must use go-cuserr**:
```go
// External service failures (webhook endpoints)
err := cuserr.NewExternalError("webhook-endpoint", "POST", httpErr,
    cuserr.WithLogger(logger),
    cuserr.WithContext(ctx),
    cuserr.WithMetadata("url", url),
    cuserr.WithMetadata("status_code", statusCode),
)

// Validation errors
err := cuserr.NewValidationError("url", "invalid webhook URL format")

// Not found
err := cuserr.NewNotFoundError("subscription", subscriptionID)

// Internal errors
err := cuserr.NewInternalError("database", dbErr,
    cuserr.WithLogger(logger),
    cuserr.WithContext(ctx),
)
```

**NEVER**: Return `fmt.Errorf()` or stdlib errors directly

### 2. go-version (v1.0.0) - Version Management
**Initialize FIRST in main.go**:
```go
// MUST be first - validates schema compatibility before startup
if err := version.Initialize(
    version.WithManifestPath("versions.yaml"),
    version.WithGitInfo(),
    version.WithBuildInfo(),
    version.WithValidators(
        version.NewSchemaValidator("postgres_main", "1"),
    ),
); err != nil {
    log.Fatalf("Version validation failed: %v", err)
}

// Include in all loggers
versionInfo := version.MustGet()
logger = logger.With(versionInfo.LogFields()...)
```

**versions.yaml** is the single source of truth for all version info.

### 3. EventBus Pattern (Optional Observability)
**Flexible event abstraction** for observability and monitoring:
```go
// EventBus interface (defined in hookd.manager.go)
type EventBus interface {
    Publish(topic string, data interface{})
    Subscribe(topic string, handler func(interface{})) func()
}

// Zero-cost default: noOpEventBus (does nothing)
manager, err := NewManager(config, repo) // No events

// Custom implementation example (see examples/monitoring)
type CustomEventBus struct {
    subscribers map[string][]func(interface{})
    mu sync.RWMutex
}

manager, err := NewManager(config, repo,
    WithEventBus(&CustomEventBus{}),
)
```

**Design Decision**:
- **NOT using go-pubbing directly** - The EventBus interface provides flexibility
- Default is no-op (zero cost for users who don't need events)
- Users can implement EventBus with ANY event system (go-pubbing, NATS, Kafka, logs, metrics)
- Examples show custom implementations for monitoring and metrics

**Event Topics**:
- `delivery.queued`, `delivery.success`, `delivery.failed`, `delivery.dead_letter`
- `circuit.opened`, `circuit.half_open`, `circuit.closed`
- `metrics.*`, `audit.*`

**Note**: go-pubbing v0.5.2 is a dev dependency for potential future use, but the EventBus abstraction is intentionally decoupled.

---

## Development Standards (Non-Negotiable)

### 1. No Magic Strings
**ALL strings must be constants** in `hookd.constants.go`:
```go
const (
    // Status
    SubscriptionStatusActive   = "active"
    DeliveryStatusPending      = "pending"

    // Event topics
    EventTopicDeliverySuccess  = "delivery.success"
    EventTopicCircuitOpened    = "circuit.opened"

    // Headers
    HeaderSignature = "X-Webhook-Signature"
    HeaderDeliveryID = "X-Webhook-Delivery-ID"

    // Configuration
    DefaultMaxRetries = 10
    DefaultWorkerCount = 10
)
```

### 2. Prefixed NanoIDs (NEVER UUIDs)
```go
import "github.com/matoous/go-nanoid/v2"

const (
    PrefixSubscription = "sub"
    PrefixDelivery     = "dlv"
    PrefixAttempt      = "att"
)

func generateSubscriptionID() (string, error) {
    id, err := gonanoid.New()
    if err != nil {
        return "", err
    }
    return fmt.Sprintf("%s_%s", PrefixSubscription, id), nil
}
```

### 3. Thread Safety by Default
- All public methods must be thread-safe
- Use sync.RWMutex for shared state
- Test with `-race` flag (required to pass)

### 4. Dependency Injection Pattern
```go
type Manager struct {
    config      *Config
    logger      *zap.Logger
    repo        Repository      // Interface, not concrete type
    broker      *pubbing.Broker
    versionInfo *version.Info

    mu sync.RWMutex
    ctx context.Context
    cancel context.CancelFunc
}

func NewManager(
    config *Config,
    logger *zap.Logger,
    repo Repository,
    broker *pubbing.Broker,
) (*Manager, error) {
    // Validate all inputs
    if config == nil {
        return nil, cuserr.NewValidationError("config", "config is required")
    }
    // ...construct and initialize
}
```

### 5. Testing Requirements
- **90%+ coverage** on all packages (validated in CI)
- Every public function must have tests
- Test both success and all error paths
- Race detector must pass: `go test -race ./...`

```bash
# Run tests
make test           # Standard tests with race detector
make test-race      # Explicit race detection
make coverage       # Generate coverage report (must be ≥90%)
make test-integration  # Integration tests with testcontainers
```

---

## File Structure & Naming

All package files follow: `hookd.{type}.{module}.go`

Package files are located at the repository root (following Go conventions):

```
github.com/itsatony/go-hookd/
├── hookd.config.go              # Configuration structures
├── hookd.constants.go           # ALL constants (no magic strings)
├── hookd.errors.go              # Error definitions using go-cuserr
├── hookd.models.go              # Domain models (Subscription, Delivery, etc.)
├── hookd.interfaces.go          # Core interfaces (Repository, etc.)
├── hookd.manager.go             # Main Manager implementation
├── hookd.subscription.go        # Subscription operations
├── hookd.delivery.go            # Delivery engine (planned)
├── hookd.retry.go               # Retry logic with exponential backoff (planned)
├── hookd.circuit_breaker.go     # Circuit breaker implementation (planned)
├── hookd.idempotency.go         # Idempotency handling (planned)
├── hookd.security.go            # HMAC signature generation (planned)
├── hookd.repository.interface.go
├── hookd.repository.postgres.go
├── hookd.repository.mock.go
├── hookd.observability.go       # Metrics, logging, tracing (planned)
├── hookd.events.go              # Event definitions
├── examples/                    # Example applications
├── migrations/                  # PostgreSQL migrations
└── testutil/                    # Test utilities
```

---

## Key Commands

### Build & Test
```bash
# Build with version injection
make build

# Run all tests with race detector
make test

# Run tests with coverage report
make coverage

# Run only fast tests (skip integration)
make test-short

# Run integration tests (requires Docker)
make test-integration

# Lint code
make lint

# Run all excellence gates (format, vet, lint, test-race, coverage)
make gates
```

### Development
```bash
# Run example
go run cmd/example/main.go

# Format code
make fmt

# Tidy dependencies
make tidy

# Clean build artifacts
make clean
```

### Database Migrations
```bash
# Run migrations
make migrations-up

# Rollback last migration
make migrations-down

# Create new migration
make migrations-create NAME=add_webhook_metadata
```

---

## Implementation Workflow

### Phase-by-Phase Approach
Follow `docs/implementation_guide.md` phases sequentially:

**Phase 1: Foundation** (Week 1)
- Set up go.mod, versions.yaml
- Define all constants (hookd.constants.go)
- Define core interfaces
- Define domain models
- Define errors using go-cuserr

**Phase 2: Data Layer** (Week 1-2)
- Create PostgreSQL migrations
- Implement Repository interface
- Create mock repository
- Achieve 90%+ test coverage

**Phase 3: Core Manager** (Week 2)
- Implement Manager with subscription CRUD
- Add event publishing to go-pubbing
- Graceful shutdown support

**Phase 4: Delivery Engine** (Week 2-3)
- Worker pool implementation
- Queue polling & delivery execution
- HMAC signature generation

**Phase 5: Retry & Circuit Breaker** (Week 3)
- Exponential backoff with jitter
- Per-endpoint circuit breakers
- Retry budget tracking

**Phase 6: Idempotency & Security** (Week 3-4)
- Idempotency store
- Content-based deduplication
- Replay protection

**Phase 7: Observability** (Week 4)
- Prometheus metrics
- Distributed tracing
- Structured logging

**Phase 8: Integration & Examples** (Week 4)
- Example applications
- Integration tests with testcontainers

**Phase 9: Documentation & Polish** (Week 4)
- Complete README
- Architecture docs
- Operations runbook

### Excellence Gates
Each phase has specific validation criteria. See `docs/implementation_guide.md` Excellence Gates section.

---

## Using CodeFinder MCP Tools

**Use CodeFinder extensively** during implementation for efficient code navigation:

### Quick Commands
```python
# Project overview
aggregate(metric="count", scope=["."])

# Find specific symbol
query(q="Manager", t="struct", m="x", ctx="full")

# Find all functions matching pattern
query(q="Create*", t="fn", m="f", ctx="std", lim=20)

# Trace dependencies
traverse(start=["Manager"], typ="dep", dir="down", depth=2)

# Find implementation of interface
query(q="Repository", t="interface", m="x", ctx="full", rel=True)

# Check test coverage
query(q="Test*", t="fn", f="**/*_test.go", m="f")
```

See `docs/codefinder_mcp_guide.md` for 50+ detailed examples.

---

## Common Patterns

### Error Creation
```go
// ALWAYS use go-cuserr, NEVER fmt.Errorf
err := cuserr.NewExternalError("webhook-endpoint", "POST", httpErr,
    cuserr.WithLogger(logger),
    cuserr.WithContext(ctx),
    cuserr.WithMetadata("delivery_id", deliveryID),
    cuserr.WithMetadata("url", url),
)
```

### Structured Logging
```go
// Include context in all logs
logger.Info("delivery succeeded",
    zap.String("delivery_id", delivery.ID),
    zap.String("subscription_id", delivery.SubscriptionID),
    zap.String("tenant_id", delivery.TenantID),
    zap.Int("attempt_count", delivery.AttemptCount),
)
```

### Event Publishing
```go
// Type-safe event publishing
pubbing.PublishTyped(broker, EventTopicDeliverySuccess, DeliveryEvent{
    DeliveryID:     delivery.ID,
    SubscriptionID: delivery.SubscriptionID,
    TenantID:       delivery.TenantID,
    Status:         DeliveryStatusSuccess,
    Timestamp:      time.Now(),
})
```

---

## What NOT to Do

❌ **Don't** create HTTP handlers (package responsibility)
❌ **Don't** use magic strings (ALL must be constants)
❌ **Don't** use UUIDs (use prefixed nanoIDs)
❌ **Don't** use stdlib errors (use go-cuserr)
❌ **Don't** skip tests or accept <90% coverage
❌ **Don't** commit without running `make gates`
❌ **Don't** ignore thread safety
❌ **Don't** create incomplete implementations (production-ready only)

---

## Key Architectural Decisions

1. **PostgreSQL-native queue** - Use `FOR UPDATE SKIP LOCKED`, no external broker
2. **Interface-first design** - Repository interface, not concrete implementations
3. **EventBus abstraction** - Flexible interface, NOT hardcoded to go-pubbing
4. **Per-endpoint circuit breakers** - Prevent cascading failures
5. **Exponential backoff with jitter** - Prevent thundering herd
6. **Idempotency by default** - Content-based and key-based deduplication
7. **Zero HTTP dependency** - Pure webhook management logic

---

## Testing Strategy

### Unit Tests
```go
func TestManager_CreateSubscription(t *testing.T) {
    logger := zaptest.NewLogger(t)
    broker, err := pubbing.New()
    require.NoError(t, err)
    defer broker.Shutdown(time.Second)

    repo := NewMockRepository()
    config := &Config{WorkerCount: 5, MaxRetries: 10}

    manager, err := NewManager(config, logger, repo, broker)
    require.NoError(t, err)
    defer manager.Shutdown(context.Background())

    // Test cases...
}
```

### Integration Tests
```go
func TestIntegration_EndToEnd(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    // Use testcontainers for PostgreSQL
    // Full end-to-end workflow test
}
```

### Race Detection
```bash
# MUST pass
go test -race ./...
go test -race -count=100 . -run TestDelivery_Concurrent
```

---

## Observability

### Metrics (Prometheus-compatible)
- `hookd_delivery_attempts_total` - Counter with status label
- `hookd_delivery_duration_seconds` - Histogram
- `hookd_circuit_breaker_state` - Gauge (0=closed, 1=half-open, 2=open)
- `hookd_queue_depth` - Gauge
- `hookd_worker_pool_utilization` - Gauge

### Logging (Structured with Zap)
All logs include:
- Version information (from go-version)
- Request/operation IDs (from context)
- Tenant IDs (multi-tenancy support)
- Structured fields (not string concatenation)

### Tracing
Support for distributed tracing via OpenTelemetry (future enhancement).

---

## Version Management

**versions.yaml** is the single source of truth:
```yaml
project:
  name: "go-hookd"
  version: "0.3.0"

schemas:
  postgres_main: "1"

components:
  manager: "0.3.0"
  delivery_engine: "0.3.0"
  circuit_breaker: "0.2.0"
  idempotency_store: "0.2.0"
  dead_letter_queue: "0.1.0"

dependencies:
  go_cuserr: "0.3.0"
  go_version: "1.0.0"
  go_pubbing: "0.5.2"
```

Update on every release, schema change, or component version bump.

---

## Quick Reference

### Core Interfaces
- `Repository` - Data persistence abstraction
- `Manager` - Main package API
- `DeliveryEngine` - Worker pool & delivery execution
- `CircuitBreaker` - Per-endpoint failure detection

### Domain Models
- `Subscription` - Webhook subscription configuration
- `Delivery` - Individual webhook delivery
- `DeliveryAttempt` - Single delivery attempt record
- `RetryPolicy` - Retry behavior configuration

### Key Constants Files
- `hookd.constants.go` - ALL string literals
- `hookd.errors.go` - Error definitions (using go-cuserr)

---

## Getting Help

1. **Read the docs first**: `docs/implementation_guide.md` has everything
2. **Use CodeFinder MCP**: Efficient code navigation and analysis
3. **Follow the phases**: Don't skip ahead, each builds on previous
4. **Check excellence gates**: Validation criteria for each phase

---

## Philosophy: "Excellence. Always."

This is not a prototype. This is not an MVP. Every line of code, every test, every piece of documentation reflects production-ready quality from day one. We don't ship TODOs, we don't skip tests, and we don't accept compromises on quality.

**If it's worth building, it's worth building right.**

---

*vAudience.AI GmbH - go-hookd*
*Powered by: go-cuserr • go-version • go-pubbing*
