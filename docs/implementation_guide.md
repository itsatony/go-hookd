# go-hookd: Enterprise Webhook Management Package
## Implementation Guide v2.0

**Excellence. Always.**

**Repository**: `github.com/itsatony/go-hookd`  
**Package**: `github.com/itsatony/go-hookd`  
**Company**: vAudience.AI GmbH  
**Status**: Implementation Phase  
**Last Updated**: 2025-11-08

---

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [Project Overview](#project-overview)
3. [Core Architecture](#core-architecture)
4. [vAudience.AI Ecosystem Integration](#vaudienceai-ecosystem-integration)
5. [Package Structure](#package-structure)
6. [Development Standards](#development-standards)
7. [Implementation Phases](#implementation-phases)
8. [Domain Models & Interfaces](#domain-models--interfaces)
9. [Core Components Implementation](#core-components-implementation)
10. [Testing Strategy](#testing-strategy)
11. [CodeFinder MCP Integration](#codefinder-mcp-integration)
12. [Deployment & Operations](#deployment--operations)
13. [Excellence Gates](#excellence-gates)

---

## Executive Summary

go-hookd is a production-ready Go webhook management package designed to handle webhook subscriptions, delivery, retries, and observability at enterprise scale. The package focuses exclusively on webhook management logic with PostgreSQL storage, leaving HTTP handler implementation to consuming applications.

### Key Features

- **Reliable Delivery**: Asynchronous queue-based processing with exponential backoff and circuit breakers
- **PostgreSQL-Native**: No external message broker dependency, leveraging SKIP LOCKED for efficient queuing
- **Idempotency**: Built-in deduplication and delivery tracking
- **Observable**: First-class support for metrics, logging, and distributed tracing
- **Secure**: HMAC signatures, secret rotation, replay protection
- **Enterprise-Grade**: Dead letter queues, reconciliation jobs, audit trails

### Built on vAudience.AI Go Ecosystem

- **go-cuserr** (v0.3.0) - Protocol-agnostic error handling with 92.3% test coverage
- **go-version** (v1.0.0) - Multi-dimensional versioning with HTTP endpoints
- **go-pubbing** (v0.4.0) - High-performance internal event bus (6M+ ops/sec)

---

## Project Overview

### Core Philosophy

1. **Separation of Concerns**: Zero HTTP handler code - pure webhook management logic
2. **Repository Pattern**: Clean abstraction over PostgreSQL with interface-first design
3. **Idempotency by Default**: All operations designed for at-least-once delivery
4. **Observable**: First-class support for metrics, logging, and distributed tracing
5. **Configurable**: Flexible policies for retries, timeouts, and rate limiting
6. **Production-Ready**: Circuit breakers, dead letter queues, and graceful degradation built-in

### Non-Goals

- HTTP server/handler implementation (consumer responsibility)
- Message queue abstraction (focus on PostgreSQL-backed queue)
- Webhook reception/verification (focus on delivery)

### Unique Selling Points

#### 1. PostgreSQL-Native Queue
- Leverages PostgreSQL's SKIP LOCKED for efficient queue processing
- No external message broker dependency
- Transactional guarantees
- Built-in persistence and durability
- Simpler operational model

#### 2. Smart Retry Intelligence
- Response-code aware retry logic
- Exponential backoff with jitter (prevents thundering herd)
- Adaptive backoff based on endpoint behavior
- Maximum retry budget per delivery
- Time-bounded retry windows

#### 3. Circuit Breaker Integration
- Per-endpoint circuit breakers
- Automatic failure detection
- Half-open state testing
- Configurable failure thresholds
- Fast-fail during endpoint outages

#### 4. Enterprise-Grade Reliability
- Dead letter queue for manual intervention
- Reconciliation job support
- Audit trail for all deliveries
- Delivery status webhooks (meta-webhooks)
- Guaranteed at-least-once delivery

---

## Core Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      Application Layer                      │
│                    (HTTP Handlers - User Code)              │
│                   Optional: Subscribe to Events             │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       │ Uses
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                   Webhook Manager Package                   │
│                                                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │ Subscription │  │   Delivery   │  │ Observability│    │
│  │   Manager    │  │    Engine    │  │   Manager    │    │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘    │
│         │                  │                  │             │
│         │     ┌────────────┴────────────┐    │             │
│         │     │                         │    │             │
│  ┌──────▼─────▼─────┐     ┌────────────▼────▼────┐       │
│  │    Repository     │     │   Observability      │       │
│  │    Interface      │     │     Interface        │       │
│  └──────────┬────────┘     └──────────────────────┘       │
│             │                                               │
│  ┌──────────┴────────────────────────────────────────────┐ │
│  │              Internal Event Bus (go-pubbing)          │ │
│  │   Topics: delivery.*, circuit.*, metrics.*, audit.*   │ │
│  │   ┌───────────────────────────────────────────────────┐│ │
│  │   │  Internal Subscriptions (automatic):            ││ │
│  │   │  • delivery.success → Update metrics            ││ │
│  │   │  • delivery.failed → Circuit breaker check      ││ │
│  │   │  • circuit.opened → Pause deliveries            ││ │
│  │   │  • metrics.* → Prometheus collector             ││ │
│  │   └───────────────────────────────────────────────────┘│ │
│  │   ┌───────────────────────────────────────────────────┐│ │
│  │   │  Optional External Subscriptions:               ││ │
│  │   │  • Application can subscribe to any topic       ││ │
│  │   │  • Real-time dashboards, alerting, analytics    ││ │
│  │   └───────────────────────────────────────────────────┘│ │
│  └───────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                       │
                       │ Repository Implementation
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                      PostgreSQL Database                     │
│                                                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │ subscriptions│  │  deliveries  │  │idempotency_  │    │
│  │              │  │              │  │   store      │    │
│  └──────────────┘  └──────────────┘  └──────────────┘    │
│                                                             │
│  ┌──────────────┐  ┌──────────────┐                       │
│  │ delivery_    │  │   circuit_   │                       │
│  │  attempts    │  │   breaker_   │                       │
│  │              │  │    state     │                       │
│  └──────────────┘  └──────────────┘                       │
└─────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

#### Subscription Manager
- CRUD operations for webhook subscriptions
- Event type filtering and routing
- Subscription status management (active, paused, disabled)
- Versioning support for payload schemas

#### Delivery Engine
- Asynchronous, queue-based processing
- Worker pool management
- Delivery attempt execution
- Retry orchestration
- Circuit breaker coordination

#### Observability Manager
- Metrics collection and exposure
- Structured logging
- Distributed tracing integration
- Event publishing to internal bus

#### Repository Interface
- Clean abstraction over data persistence
- Transaction management
- Query optimization

#### Internal Event Bus (go-pubbing)
- Component coordination without tight coupling
- Real-time metrics collection
- Audit event streaming
- Optional external event subscriptions for dashboards

---

## vAudience.AI Ecosystem Integration

### Mandatory Dependencies

All three packages are **mandatory** and deeply integrated into go-hookd:

#### 1. go-cuserr - Error Management

**Package**: `github.com/itsatony/go-cuserr` v0.3.0

**Purpose**: Protocol-agnostic error handling with automatic mapping to HTTP status, gRPC codes, CLI exit codes, and syslog severity.

**Integration Points**:
- All error creation and handling
- Retry logic categorization
- HTTP/gRPC response mapping
- Structured error logging

**Usage Pattern**:
```go
// Create errors with full context
err := cuserr.NewExternalError("webhook-endpoint", "POST", httpErr,
    cuserr.WithLogger(logger),
    cuserr.WithContext(ctx),
    cuserr.WithMetadata("url", sub.URL),
    cuserr.WithMetadata("attempt", attemptCount),
    cuserr.WithMetadata("status_code", resp.StatusCode),
)

// Log when appropriate
err.Log(ctx)

// Intelligent retry decisions
func shouldRetry(err error) bool {
    if cuserr.IsErrorCategory(err, cuserr.ErrorCategoryExternal) {
        return true // Retry external service failures
    }
    if cuserr.IsErrorCategory(err, cuserr.ErrorCategoryValidation) {
        return false // Don't retry validation errors
    }
    return false
}
```

**Key Features**:
- Protocol-agnostic design (HTTP, gRPC, CLI, batch jobs)
- Automatic code mapping across protocols
- Thread-safe with 92.3% test coverage
- Injectable logging (works with zap, zerolog, slog)
- Zero dependencies (stdlib only)

#### 2. go-version - Version Management

**Package**: `github.com/itsatony/go-version` v1.0.0

**Purpose**: Multi-dimensional versioning with health endpoints and schema validation.

**Integration Points**:
- Schema version validation on startup
- Component version tracking
- Build metadata injection
- Health and version HTTP endpoints
- Structured logging with version context

**Usage Pattern**:
```go
// Initialize FIRST - validates before anything else starts
if err := version.Initialize(
    version.WithManifestPath("versions.yaml"),
    version.WithGitInfo(),
    version.WithBuildInfo(),
    version.WithValidators(
        version.NewSchemaValidator("postgres_main", "1"),
    ),
); err != nil {
    log.Fatalf("Version initialization failed: %v", err)
}

// Get version info
versionInfo := version.MustGet()

// Setup logger with version context
logger = logger.With(versionInfo.LogFields()...)

// Expose HTTP endpoints
mux.Handle("/version", version.Handler())
mux.Handle("/health", version.HealthHandler())
handler := version.Middleware(mux)
```

**Key Features**:
- Multi-dimensional versioning (project, schemas, components)
- Schema version validation
- Built-in `/version` and `/health` endpoints
- Version-aware structured logging
- Build-time metadata injection

#### 3. go-pubbing - Internal Event Bus

**Package**: `github.com/itsatony/go-pubbing` v0.4.0

**Purpose**: High-performance in-memory pub/sub system for component communication and real-time metrics.

**Integration Points**:
- Internal component communication
- Real-time metrics collection
- Circuit breaker events
- Audit event streaming
- Optional external subscriptions

**Usage Pattern**:
```go
// Create broker
broker, err := pubbing.New(
    pubbing.WithLogger(logger),
    pubbing.WithRetentionCount(10000),
    pubbing.WithRetentionAge(1*time.Hour),
)
if err != nil {
    return err
}
defer broker.Shutdown(5 * time.Second)

// Type-safe publishing
pubbing.PublishTyped(broker, "delivery.success", DeliveryEvent{
    DeliveryID:     delivery.ID,
    SubscriptionID: delivery.SubscriptionID,
    Status:         "success",
    Timestamp:      time.Now(),
})

// Type-safe subscription
pubbing.SubscribeTyped[DeliveryEvent](
    broker,
    ctx,
    "delivery.>", // Wildcard pattern
    func(event DeliveryEvent) error {
        metrics.RecordDeliverySuccess(event.SubscriptionID)
        return nil
    },
)
```

**Key Features**:
- Lock-free architecture, 6M+ ops/sec performance
- Hierarchical topic patterns with wildcards
- Type-safe generics API
- Message retention with count and age-based limits
- Context-aware with full cancellation support
- Optional external subscriptions

### Event Bus Topics

**Delivery Events**:
- `delivery.queued` - New delivery queued
- `delivery.started` - Delivery attempt started
- `delivery.success` - Delivery succeeded
- `delivery.failed` - Delivery failed (will retry)
- `delivery.dead_letter` - Delivery moved to DLQ

**Circuit Breaker Events**:
- `circuit.opened` - Circuit breaker opened
- `circuit.half_open` - Circuit breaker testing
- `circuit.closed` - Circuit breaker closed

**Metrics Events**:
- `metrics.delivery_attempt` - Each delivery attempt
- `metrics.retry_triggered` - Retry scheduled
- `metrics.queue_depth` - Queue size update

**Audit Events**:
- `audit.subscription_created` - New subscription
- `audit.subscription_updated` - Subscription modified
- `audit.subscription_deleted` - Subscription removed

---

## Package Structure

```
github.com/itsatony/go-hookd/
├── cmd/
│   └── example/
│       └── main.go                          # Example usage
├── internal/
│   ├── hookd.config.go                      # Configuration structures
│   ├── hookd.constants.go                   # Package-wide constants
│   ├── hookd.errors.go                      # Error definitions using go-cuserr
│   ├── hookd.models.go                      # Domain models
│   ├── hookd.interfaces.go                  # Core interfaces
│   ├── hookd.manager.go                     # Main manager implementation
│   ├── hookd.subscription.go                # Subscription operations
│   ├── hookd.delivery.go                    # Delivery engine
│   ├── hookd.retry.go                       # Retry logic
│   ├── hookd.circuit_breaker.go             # Circuit breaker implementation
│   ├── hookd.idempotency.go                 # Idempotency handling
│   ├── hookd.security.go                    # HMAC signature generation
│   ├── hookd.repository.interface.go        # Repository interface
│   ├── hookd.repository.postgres.go         # PostgreSQL implementation
│   ├── hookd.repository.mock.go             # Mock for testing
│   ├── hookd.observability.go               # Metrics and logging
│   └── hookd.events.go                      # Event definitions and publishing
├── migrations/
│   └── postgres/
│       ├── 000001_create_tables.up.sql
│       └── 000001_create_tables.down.sql
├── docs/
│   ├── architecture.md
│   ├── usage.md
│   └── operations.md
├── examples/
│   ├── basic/
│   │   └── main.go
│   └── advanced/
│       └── main.go
├── go.mod
├── go.sum
├── versions.yaml                            # Version manifest
├── implementation_guide.md                  # This document
├── CHANGELOG.md
└── README.md
```

### File Naming Convention

All internal files follow the pattern: `hookd.{type}.{module}.go`

Examples:
- `hookd.repository.postgres.go`
- `hookd.service.delivery.go`
- `hookd.models.subscription.go`

---

## Development Standards

### Core Principles (Non-Negotiable)

1. **Thread Safety by Default**: All code must handle concurrent access safely
2. **No Magic Strings**: EVERY string literal must be a constant
3. **Comprehensive Error Handling**: Use go-cuserr sentinel errors with rich context
4. **Complete Type Safety**: Full type hints throughout
5. **Production-Ready**: No stubs, mocks, or incomplete implementations unless explicitly requested
6. **DRY & SOLID**: Follow these principles religiously
7. **Test Everything**: Tests must validate actual functionality, not just coverage
8. **ID Generation**: ALWAYS use prefixed nanoIds (e.g., `sub_6ByTSYmGzT2c`), NEVER UUIDs or integer IDs

### Error Handling Standards

**ALWAYS use go-cuserr for ALL errors:**

```go
// Validation errors (400)
err := cuserr.NewValidationError("url", "invalid webhook URL format")

// Not found (404)
err := cuserr.NewNotFoundError("subscription", subscriptionID)

// External service failures (502)
err := cuserr.NewExternalError("webhook-endpoint", "POST", originalErr,
    cuserr.WithLogger(logger),
    cuserr.WithContext(ctx),
    cuserr.WithMetadata("url", url),
)

// Internal errors (500)
err := cuserr.NewInternalError("database", dbErr,
    cuserr.WithLogger(logger),
    cuserr.WithContext(ctx),
)

// Timeout (408)
err := cuserr.NewTimeoutError("webhook-delivery", ctx.Err(),
    cuserr.WithLogger(logger),
    cuserr.WithMetadata("timeout_ms", timeout.Milliseconds()),
)
```

### String Constants Pattern

**NO magic strings anywhere:**

```go
// hookd.constants.go
const (
    // Status constants
    SubscriptionStatusActive   = "active"
    SubscriptionStatusPaused   = "paused"
    SubscriptionStatusDisabled = "disabled"
    
    DeliveryStatusPending     = "pending"
    DeliveryStatusSuccess     = "success"
    DeliveryStatusFailed      = "failed"
    DeliveryStatusDeadLetter  = "dead_letter"
    
    // Event topic constants
    EventTopicDeliveryQueued      = "delivery.queued"
    EventTopicDeliveryStarted     = "delivery.started"
    EventTopicDeliverySuccess     = "delivery.success"
    EventTopicDeliveryFailed      = "delivery.failed"
    EventTopicDeliveryDeadLetter  = "delivery.dead_letter"
    
    EventTopicCircuitOpened   = "circuit.opened"
    EventTopicCircuitHalfOpen = "circuit.half_open"
    EventTopicCircuitClosed   = "circuit.closed"
    
    // Header constants
    HeaderSignature = "X-Webhook-Signature"
    HeaderTimestamp = "X-Webhook-Timestamp"
    HeaderDeliveryID = "X-Webhook-Delivery-ID"
    HeaderAttemptNumber = "X-Webhook-Attempt"
    
    // Default configuration values
    DefaultMaxRetries = 10
    DefaultInitialBackoffMs = 1000
    DefaultMaxBackoffMs = 3600000
    DefaultWorkerCount = 10
    DefaultQueuePollIntervalMs = 1000
)
```

### ID Generation Pattern

**ALWAYS use prefixed nanoIds:**

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

### Dependency Injection Pattern

```go
type Manager struct {
    config      *Config
    logger      *zap.Logger
    repo        Repository
    broker      *pubbing.Broker
    versionInfo *version.Info
    httpClient  *http.Client
    
    // Internal components
    deliveryEngine   *DeliveryEngine
    circuitBreakers  map[string]*CircuitBreaker
    idempotencyStore *IdempotencyStore
    
    // Synchronization
    mu sync.RWMutex
    
    // Lifecycle
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

func NewManager(
    config *Config,
    logger *zap.Logger,
    repo Repository,
    broker *pubbing.Broker,
) (*Manager, error) {
    // Validate inputs
    if config == nil {
        return nil, cuserr.NewValidationError("config", "config is required")
    }
    if logger == nil {
        return nil, cuserr.NewValidationError("logger", "logger is required")
    }
    if repo == nil {
        return nil, cuserr.NewValidationError("repo", "repository is required")
    }
    if broker == nil {
        return nil, cuserr.NewValidationError("broker", "broker is required")
    }
    
    ctx, cancel := context.WithCancel(context.Background())
    
    m := &Manager{
        config:          config,
        logger:          logger,
        repo:            repo,
        broker:          broker,
        versionInfo:     version.MustGet(),
        circuitBreakers: make(map[string]*CircuitBreaker),
        ctx:             ctx,
        cancel:          cancel,
    }
    
    // Initialize components
    if err := m.initialize(); err != nil {
        cancel()
        return nil, err
    }
    
    return m, nil
}
```

### Testing Standards

**MUST achieve 90%+ test coverage:**

```go
func TestManager_CreateSubscription(t *testing.T) {
    // Setup
    logger := zaptest.NewLogger(t)
    broker, err := pubbing.New()
    require.NoError(t, err)
    defer broker.Shutdown(time.Second)
    
    repo := NewMockRepository()
    config := &Config{
        WorkerCount: 5,
        MaxRetries:  10,
    }
    
    manager, err := NewManager(config, logger, repo, broker)
    require.NoError(t, err)
    defer manager.Shutdown(context.Background())
    
    // Test successful creation
    t.Run("Success", func(t *testing.T) {
        ctx := context.Background()
        req := &CreateSubscriptionRequest{
            TenantID:   "tenant_123",
            URL:        "https://example.com/webhook",
            EventTypes: []string{"user.created"},
        }
        
        sub, err := manager.CreateSubscription(ctx, req)
        assert.NoError(t, err)
        assert.NotEmpty(t, sub.ID)
        assert.True(t, strings.HasPrefix(sub.ID, PrefixSubscription))
        assert.Equal(t, req.URL, sub.URL)
    })
    
    // Test validation
    t.Run("InvalidURL", func(t *testing.T) {
        req := &CreateSubscriptionRequest{
            TenantID: "tenant_123",
            URL:      "not-a-url",
        }
        
        _, err := manager.CreateSubscription(context.Background(), req)
        assert.Error(t, err)
        assert.True(t, cuserr.IsErrorCategory(err, cuserr.ErrorCategoryValidation))
    })
    
    // Test idempotency
    t.Run("Idempotent", func(t *testing.T) {
        req := &CreateSubscriptionRequest{
            TenantID:      "tenant_123",
            URL:           "https://example.com/webhook",
            IdempotencyKey: "test-key-123",
        }
        
        sub1, err := manager.CreateSubscription(context.Background(), req)
        require.NoError(t, err)
        
        sub2, err := manager.CreateSubscription(context.Background(), req)
        require.NoError(t, err)
        
        assert.Equal(t, sub1.ID, sub2.ID)
    })
}
```

---

## Implementation Phases

### Phase 1: Foundation (Week 1)

**Goal**: Set up project structure, dependencies, and core interfaces

**Tasks**:
1. Initialize Go module with proper name
2. Add vAudience.AI dependencies (go-cuserr, go-version, go-pubbing)
3. Create versions.yaml manifest
4. Define all constants (NO magic strings)
5. Define core interfaces (Repository, Observability)
6. Create domain models
7. Set up error definitions using go-cuserr
8. Write README with basic usage

**Deliverables**:
- [ ] go.mod with all dependencies
- [ ] versions.yaml with schema version 1
- [ ] hookd.constants.go (all constants defined)
- [ ] hookd.interfaces.go (complete interfaces)
- [ ] hookd.models.go (all domain models)
- [ ] hookd.errors.go (error definitions)
- [ ] Basic README.md

**Validation**:
```bash
go mod verify
go build ./...
go test -race ./...
```

### Phase 2: Data Layer (Week 1-2)

**Goal**: Implement PostgreSQL repository with full transaction support

**Tasks**:
1. Create migration files
2. Implement Repository interface for PostgreSQL
3. Implement transaction support
4. Create mock repository for testing
5. Write comprehensive repository tests (90%+ coverage)
6. Add connection pooling configuration
7. Implement query optimization

**Deliverables**:
- [ ] migrations/postgres/*.sql files
- [ ] hookd.repository.interface.go
- [ ] hookd.repository.postgres.go
- [ ] hookd.repository.mock.go
- [ ] hookd.repository.postgres_test.go (90%+ coverage)

**Database Schema**:
```sql
-- subscriptions table
CREATE TABLE subscriptions (
    id TEXT PRIMARY KEY,                  -- sub_6ByTSYmGzT2c
    tenant_id TEXT NOT NULL,
    url TEXT NOT NULL,
    secret TEXT NOT NULL,
    event_types TEXT[] NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    retry_policy JSONB NOT NULL,
    headers JSONB,
    metadata JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- deliveries table
CREATE TABLE deliveries (
    id TEXT PRIMARY KEY,                  -- dlv_6ByTSYmGzT2c
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
    tenant_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER DEFAULT 0,
    max_attempts INTEGER NOT NULL,
    next_retry_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- More tables in migrations file...
```

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestRepository
```

### Phase 3: Core Manager (Week 2)

**Goal**: Implement main Manager with subscription management

**Tasks**:
1. Implement Manager constructor with validation
2. Implement CRUD operations for subscriptions
3. Add event publishing to go-pubbing
4. Implement graceful shutdown
5. Add comprehensive tests
6. Document public API

**Deliverables**:
- [ ] hookd.manager.go (main manager)
- [ ] hookd.subscription.go (subscription operations)
- [ ] hookd.manager_test.go (90%+ coverage)
- [ ] API documentation in godoc format

**Key Methods**:
```go
func NewManager(config *Config, logger *zap.Logger, repo Repository, broker *pubbing.Broker) (*Manager, error)
func (m *Manager) CreateSubscription(ctx context.Context, req *CreateSubscriptionRequest) (*Subscription, error)
func (m *Manager) GetSubscription(ctx context.Context, id string) (*Subscription, error)
func (m *Manager) UpdateSubscription(ctx context.Context, id string, req *UpdateSubscriptionRequest) (*Subscription, error)
func (m *Manager) DeleteSubscription(ctx context.Context, id string) error
func (m *Manager) ListSubscriptions(ctx context.Context, filter *SubscriptionFilter) ([]*Subscription, error)
func (m *Manager) Start(ctx context.Context) error
func (m *Manager) Shutdown(ctx context.Context) error
```

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestManager
```

### Phase 4: Delivery Engine (Week 2-3)

**Goal**: Implement asynchronous delivery with worker pools

**Tasks**:
1. Implement DeliveryEngine with worker pool
2. Add queue polling logic
3. Implement HTTP client with timeout
4. Add HMAC signature generation
5. Implement delivery execution
6. Add event publishing for all delivery states
7. Write comprehensive tests including concurrency tests

**Deliverables**:
- [ ] hookd.delivery.go (delivery engine)
- [ ] hookd.security.go (HMAC signatures)
- [ ] hookd.delivery_test.go (90%+ coverage)
- [ ] Concurrent execution tests

**Key Components**:
```go
type DeliveryEngine struct {
    manager    *Manager
    workerPool chan struct{}
    stopCh     chan struct{}
    wg         sync.WaitGroup
}

func (e *DeliveryEngine) Start(ctx context.Context) error
func (e *DeliveryEngine) Stop(ctx context.Context) error
func (e *DeliveryEngine) executeDelivery(ctx context.Context, delivery *Delivery) error
```

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestDelivery
go test -v -race -count=100 ./internal/... -run TestDelivery_Concurrent
```

### Phase 5: Retry & Circuit Breaker (Week 3)

**Goal**: Implement intelligent retry logic with circuit breakers

**Tasks**:
1. Implement exponential backoff with jitter
2. Create per-endpoint circuit breakers
3. Implement circuit breaker state machine
4. Add retry budget tracking
5. Implement adaptive backoff
6. Add comprehensive tests
7. Test failure scenarios

**Deliverables**:
- [ ] hookd.retry.go (retry logic)
- [ ] hookd.circuit_breaker.go (circuit breaker)
- [ ] hookd.retry_test.go (90%+ coverage)
- [ ] hookd.circuit_breaker_test.go (90%+ coverage)

**Retry Algorithm**:
```go
func calculateNextRetryDelay(attempt int, config *RetryConfig) time.Duration {
    // Exponential backoff: initialDelay * (2 ^ attempt)
    delay := config.InitialBackoff * time.Duration(math.Pow(2, float64(attempt)))
    
    // Cap at max backoff
    if delay > config.MaxBackoff {
        delay = config.MaxBackoff
    }
    
    // Add jitter (±25% randomization)
    jitter := time.Duration(float64(delay) * 0.25 * (2*rand.Float64() - 1))
    delay += jitter
    
    return delay
}
```

**Circuit Breaker States**:
- Closed: Normal operation
- Open: Fast-fail, no delivery attempts
- Half-Open: Testing with limited traffic

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestRetry
go test -v -race -cover ./internal/... -run TestCircuitBreaker
```

### Phase 6: Idempotency & Security (Week 3-4)

**Goal**: Implement idempotency guarantees and security features

**Tasks**:
1. Implement idempotency store
2. Add content-based deduplication
3. Implement HMAC signature generation
4. Add timestamp validation
5. Implement secret rotation support
6. Add replay protection
7. Write security tests

**Deliverables**:
- [ ] hookd.idempotency.go
- [ ] hookd.security.go (complete implementation)
- [ ] hookd.idempotency_test.go (90%+ coverage)
- [ ] hookd.security_test.go (90%+ coverage)

**HMAC Signature**:
```go
func generateSignature(payload []byte, secret string, timestamp int64) string {
    message := fmt.Sprintf("%d.%s", timestamp, payload)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(message))
    return hex.EncodeToString(mac.Sum(nil))
}
```

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestIdempotency
go test -v -race -cover ./internal/... -run TestSecurity
```

### Phase 7: Observability (Week 4)

**Goal**: Implement comprehensive metrics, logging, and tracing

**Tasks**:
1. Implement metrics collection
2. Add Prometheus-compatible exporters
3. Implement distributed tracing support
4. Add structured logging throughout
5. Set up internal event subscriptions
6. Create observability tests
7. Document metrics and events

**Deliverables**:
- [ ] hookd.observability.go
- [ ] hookd.events.go (event definitions)
- [ ] hookd.observability_test.go (90%+ coverage)
- [ ] docs/observability.md

**Metrics**:
```go
var (
    DeliveryAttemptsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hookd_delivery_attempts_total",
            Help: "Total number of delivery attempts",
        },
        []string{"status", "tenant_id"},
    )
    
    DeliveryDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "hookd_delivery_duration_seconds",
            Help:    "Delivery attempt duration",
            Buckets: prometheus.DefBuckets,
        },
        []string{"status"},
    )
    
    CircuitBreakerState = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "hookd_circuit_breaker_state",
            Help: "Circuit breaker state (0=closed, 1=half-open, 2=open)",
        },
        []string{"endpoint"},
    )
)
```

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestObservability
```

### Phase 8: Integration & Examples (Week 4)

**Goal**: Create comprehensive examples and integration tests

**Tasks**:
1. Create basic usage example
2. Create advanced usage example
3. Write integration tests with testcontainers
4. Add end-to-end workflow tests
5. Create example HTTP handlers
6. Document all examples
7. Add troubleshooting guide

**Deliverables**:
- [ ] examples/basic/main.go
- [ ] examples/advanced/main.go
- [ ] integration_test.go
- [ ] docs/usage.md
- [ ] docs/troubleshooting.md

**Example Usage**:
```go
package main

import (
    "context"
    "log"
    
    "github.com/itsatony/go-hookd/internal"
    "github.com/itsatony/go-cuserr/adapters"
    "github.com/itsatony/go-version"
    "github.com/itsatony/go-pubbing"
    "go.uber.org/zap"
)

func main() {
    // Initialize version FIRST
    if err := version.Initialize(
        version.WithManifestPath("versions.yaml"),
        version.WithGitInfo(),
    ); err != nil {
        log.Fatalf("Version init failed: %v", err)
    }
    
    // Setup logger with version
    zapLogger, _ := zap.NewProduction()
    versionInfo := version.MustGet()
    zapLogger = zapLogger.With(versionInfo.LogFields()...)
    logger := adapters.NewZapAdapter(zapLogger)
    
    // Create event broker
    broker, _ := pubbing.New()
    defer broker.Shutdown(5 * time.Second)
    
    // Setup repository
    repo, _ := internal.NewPostgresRepository(config.DatabaseURL, logger)
    defer repo.Close()
    
    // Create manager
    manager, _ := internal.NewManager(config, logger, repo, broker)
    defer manager.Shutdown(context.Background())
    
    // Start manager
    if err := manager.Start(context.Background()); err != nil {
        log.Fatalf("Failed to start: %v", err)
    }
    
    // Create subscription
    sub, err := manager.CreateSubscription(ctx, &internal.CreateSubscriptionRequest{
        TenantID:   "tenant_123",
        URL:        "https://example.com/webhook",
        EventTypes: []string{"user.created", "user.updated"},
        Secret:     "your-secret-key",
    })
    if err != nil {
        log.Fatalf("Failed to create subscription: %v", err)
    }
    
    // Queue delivery
    delivery, err := manager.QueueDelivery(ctx, &internal.QueueDeliveryRequest{
        SubscriptionID: sub.ID,
        EventType:      "user.created",
        Payload:        map[string]interface{}{"user_id": "usr_123"},
    })
    
    log.Printf("Delivery queued: %s", delivery.ID)
}
```

**Validation**:
```bash
go test -v -race ./integration/...
go run examples/basic/main.go
go run examples/advanced/main.go
```

### Phase 9: Documentation & Polish (Week 4)

**Goal**: Complete documentation and final polish

**Tasks**:
1. Write comprehensive README
2. Generate godoc documentation
3. Create architecture documentation
4. Write operations runbook
5. Add migration guide
6. Create CHANGELOG
7. Final code review and polish

**Deliverables**:
- [ ] README.md (complete)
- [ ] docs/architecture.md
- [ ] docs/operations.md
- [ ] docs/migration.md
- [ ] CHANGELOG.md
- [ ] All godoc comments complete

**README Structure**:
1. Overview and features
2. Installation
3. Quick start
4. Basic usage
5. Advanced usage
6. Configuration
7. Observability
8. Contributing
9. License

**Validation**:
```bash
go doc -all github.com/itsatony/go-hookd
godoc -http=:6060 # Manual review
```

---

## Domain Models & Interfaces

### Core Domain Models

```go
// hookd.models.go

// Subscription represents a webhook subscription
type Subscription struct {
    ID          string                 `json:"id"`
    TenantID    string                 `json:"tenant_id"`
    URL         string                 `json:"url"`
    Secret      string                 `json:"-"` // Never expose in JSON
    EventTypes  []string               `json:"event_types"`
    Status      string                 `json:"status"`
    RetryPolicy *RetryPolicy           `json:"retry_policy"`
    Headers     map[string]string      `json:"headers,omitempty"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
    CreatedAt   time.Time              `json:"created_at"`
    UpdatedAt   time.Time              `json:"updated_at"`
}

// Delivery represents a webhook delivery
type Delivery struct {
    ID             string                 `json:"id"`
    SubscriptionID string                 `json:"subscription_id"`
    TenantID       string                 `json:"tenant_id"`
    EventType      string                 `json:"event_type"`
    Payload        map[string]interface{} `json:"payload"`
    Status         string                 `json:"status"`
    AttemptCount   int                    `json:"attempt_count"`
    MaxAttempts    int                    `json:"max_attempts"`
    NextRetryAt    *time.Time             `json:"next_retry_at,omitempty"`
    CompletedAt    *time.Time             `json:"completed_at,omitempty"`
    CreatedAt      time.Time              `json:"created_at"`
}

// DeliveryAttempt represents a single delivery attempt
type DeliveryAttempt struct {
    ID               string    `json:"id"`
    DeliveryID       string    `json:"delivery_id"`
    AttemptNumber    int       `json:"attempt_number"`
    StatusCode       int       `json:"status_code,omitempty"`
    ResponseBody     string    `json:"response_body,omitempty"`
    ResponseHeaders  map[string]string `json:"response_headers,omitempty"`
    Error            string    `json:"error,omitempty"`
    DurationMs       int64     `json:"duration_ms"`
    AttemptedAt      time.Time `json:"attempted_at"`
}

// RetryPolicy defines retry behavior
type RetryPolicy struct {
    MaxAttempts    int           `json:"max_attempts"`
    InitialBackoff time.Duration `json:"initial_backoff"`
    MaxBackoff     time.Duration `json:"max_backoff"`
    BackoffFactor  float64       `json:"backoff_factor"`
}

// CircuitBreakerState represents circuit breaker state
type CircuitBreakerState struct {
    Endpoint       string    `json:"endpoint"`
    State          string    `json:"state"` // closed, half_open, open
    FailureCount   int       `json:"failure_count"`
    SuccessCount   int       `json:"success_count"`
    LastFailure    time.Time `json:"last_failure,omitempty"`
    OpenedAt       time.Time `json:"opened_at,omitempty"`
    NextRetryAt    time.Time `json:"next_retry_at,omitempty"`
}
```

### Repository Interface

```go
// hookd.repository.interface.go

type Repository interface {
    // Subscription operations
    CreateSubscription(ctx context.Context, sub *Subscription) error
    GetSubscription(ctx context.Context, id string) (*Subscription, error)
    GetSubscriptionByTenantAndURL(ctx context.Context, tenantID, url string) (*Subscription, error)
    UpdateSubscription(ctx context.Context, sub *Subscription) error
    DeleteSubscription(ctx context.Context, id string) error
    ListSubscriptions(ctx context.Context, filter *SubscriptionFilter) ([]*Subscription, error)
    
    // Delivery operations
    CreateDelivery(ctx context.Context, delivery *Delivery) error
    GetDelivery(ctx context.Context, id string) (*Delivery, error)
    UpdateDelivery(ctx context.Context, delivery *Delivery) error
    GetPendingDeliveries(ctx context.Context, limit int) ([]*Delivery, error)
    MoveToDeadLetter(ctx context.Context, deliveryID string, reason string) error
    
    // Delivery attempt operations
    CreateDeliveryAttempt(ctx context.Context, attempt *DeliveryAttempt) error
    GetDeliveryAttempts(ctx context.Context, deliveryID string) ([]*DeliveryAttempt, error)
    
    // Idempotency operations
    CheckIdempotency(ctx context.Context, eventID, subscriptionID string) (bool, error)
    StoreIdempotencyKey(ctx context.Context, eventID, subscriptionID string, expiresAt time.Time) error
    
    // Circuit breaker operations
    GetCircuitBreakerState(ctx context.Context, endpoint string) (*CircuitBreakerState, error)
    UpdateCircuitBreakerState(ctx context.Context, state *CircuitBreakerState) error
    
    // Transaction support
    BeginTx(ctx context.Context) (RepositoryTx, error)
    
    // Health and maintenance
    Ping(ctx context.Context) error
    Close() error
}

type RepositoryTx interface {
    Repository
    Commit() error
    Rollback() error
}
```

---

## Core Components Implementation

### Manager Implementation

```go
// hookd.manager.go

type Manager struct {
    config      *Config
    logger      *zap.Logger
    repo        Repository
    broker      *pubbing.Broker
    versionInfo *version.Info
    httpClient  *http.Client
    
    // Internal components
    deliveryEngine   *DeliveryEngine
    circuitBreakers  map[string]*CircuitBreaker
    idempotencyStore *IdempotencyStore
    
    // Synchronization
    mu sync.RWMutex
    
    // Lifecycle
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

func NewManager(
    config *Config,
    logger *zap.Logger,
    repo Repository,
    broker *pubbing.Broker,
) (*Manager, error) {
    // Input validation
    if config == nil {
        return nil, cuserr.NewValidationError("config", "config is required")
    }
    if logger == nil {
        return nil, cuserr.NewValidationError("logger", "logger is required")
    }
    if repo == nil {
        return nil, cuserr.NewValidationError("repo", "repository is required")
    }
    if broker == nil {
        return nil, cuserr.NewValidationError("broker", "broker is required")
    }
    
    // Validate configuration
    if err := config.Validate(); err != nil {
        return nil, cuserr.NewValidationError("config", "invalid configuration",
            cuserr.WithMetadata("validation_error", err.Error()),
        )
    }
    
    ctx, cancel := context.WithCancel(context.Background())
    
    // Create HTTP client with timeout
    httpClient := &http.Client{
        Timeout: config.DeliveryTimeout,
        Transport: &http.Transport{
            MaxIdleConns:        100,
            MaxIdleConnsPerHost: 10,
            IdleConnTimeout:     90 * time.Second,
        },
    }
    
    m := &Manager{
        config:          config,
        logger:          logger,
        repo:            repo,
        broker:          broker,
        versionInfo:     version.MustGet(),
        httpClient:      httpClient,
        circuitBreakers: make(map[string]*CircuitBreaker),
        ctx:             ctx,
        cancel:          cancel,
    }
    
    // Initialize delivery engine
    m.deliveryEngine = NewDeliveryEngine(m)
    
    // Initialize idempotency store
    m.idempotencyStore = NewIdempotencyStore(repo, logger)
    
    // Setup internal event subscriptions
    if err := m.setupEventSubscriptions(); err != nil {
        cancel()
        return nil, cuserr.NewInternalError("event_subscriptions", err,
            cuserr.WithLogger(logger),
        )
    }
    
    logger.Info("Manager initialized",
        zap.String("version", m.versionInfo.Project.Version),
        zap.Int("worker_count", config.WorkerCount),
    )
    
    return m, nil
}

func (m *Manager) Start(ctx context.Context) error {
    m.logger.Info("Starting webhook manager")
    
    // Start delivery engine
    if err := m.deliveryEngine.Start(ctx); err != nil {
        return cuserr.NewInternalError("delivery_engine", err,
            cuserr.WithLogger(m.logger),
        )
    }
    
    return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
    m.logger.Info("Shutting down webhook manager")
    
    // Cancel context
    m.cancel()
    
    // Stop delivery engine
    if err := m.deliveryEngine.Stop(ctx); err != nil {
        m.logger.Error("Error stopping delivery engine", zap.Error(err))
    }
    
    // Wait for all goroutines
    done := make(chan struct{})
    go func() {
        m.wg.Wait()
        close(done)
    }()
    
    select {
    case <-done:
        m.logger.Info("Graceful shutdown completed")
        return nil
    case <-ctx.Done():
        return cuserr.NewTimeoutError("shutdown", ctx.Err(),
            cuserr.WithLogger(m.logger),
        )
    }
}

func (m *Manager) setupEventSubscriptions() error {
    // Subscribe to delivery success events for metrics
    _, err := pubbing.SubscribeTyped[DeliveryEvent](
        m.broker,
        m.ctx,
        EventTopicDeliverySuccess,
        func(event DeliveryEvent) error {
            m.logger.Debug("Delivery succeeded",
                zap.String("delivery_id", event.DeliveryID),
                zap.String("subscription_id", event.SubscriptionID),
            )
            // Update metrics
            return nil
        },
    )
    if err != nil {
        return err
    }
    
    // Subscribe to delivery failure events for circuit breaker
    _, err = pubbing.SubscribeTyped[DeliveryEvent](
        m.broker,
        m.ctx,
        EventTopicDeliveryFailed,
        func(event DeliveryEvent) error {
            m.handleDeliveryFailure(event)
            return nil
        },
    )
    if err != nil {
        return err
    }
    
    return nil
}

func (m *Manager) CreateSubscription(
    ctx context.Context,
    req *CreateSubscriptionRequest,
) (*Subscription, error) {
    // Validate request
    if err := req.Validate(); err != nil {
        return nil, cuserr.NewValidationError("request", "invalid request",
            cuserr.WithLogger(m.logger),
            cuserr.WithContext(ctx),
            cuserr.WithMetadata("validation_error", err.Error()),
        )
    }
    
    // Generate ID
    id, err := generateSubscriptionID()
    if err != nil {
        return nil, cuserr.NewInternalError("id_generation", err,
            cuserr.WithLogger(m.logger),
            cuserr.WithContext(ctx),
        )
    }
    
    // Create subscription
    sub := &Subscription{
        ID:          id,
        TenantID:    req.TenantID,
        URL:         req.URL,
        Secret:      req.Secret,
        EventTypes:  req.EventTypes,
        Status:      SubscriptionStatusActive,
        RetryPolicy: req.RetryPolicy,
        Headers:     req.Headers,
        Metadata:    req.Metadata,
        CreatedAt:   time.Now(),
        UpdatedAt:   time.Now(),
    }
    
    // Store in database
    if err := m.repo.CreateSubscription(ctx, sub); err != nil {
        return nil, cuserr.NewInternalError("database", err,
            cuserr.WithLogger(m.logger),
            cuserr.WithContext(ctx),
            cuserr.WithMetadata("subscription_id", id),
        )
    }
    
    // Publish event
    pubbing.PublishTyped(m.broker, "audit.subscription_created", AuditEvent{
        Type:       "subscription_created",
        ResourceID: sub.ID,
        TenantID:   sub.TenantID,
        Timestamp:  time.Now(),
    })
    
    m.logger.Info("Subscription created",
        zap.String("subscription_id", sub.ID),
        zap.String("tenant_id", sub.TenantID),
        zap.String("url", sub.URL),
    )
    
    return sub, nil
}

func (m *Manager) QueueDelivery(
    ctx context.Context,
    req *QueueDeliveryRequest,
) (*Delivery, error) {
    // Validate request
    if err := req.Validate(); err != nil {
        return nil, cuserr.NewValidationError("request", "invalid request",
            cuserr.WithLogger(m.logger),
            cuserr.WithContext(ctx),
        )
    }
    
    // Get subscription
    sub, err := m.repo.GetSubscription(ctx, req.SubscriptionID)
    if err != nil {
        if cuserr.IsErrorCode(err, cuserr.CodeNotFound) {
            return nil, cuserr.NewNotFoundError("subscription", req.SubscriptionID,
                cuserr.WithLogger(m.logger),
                cuserr.WithContext(ctx),
            )
        }
        return nil, err
    }
    
    // Check subscription status
    if sub.Status != SubscriptionStatusActive {
        return nil, cuserr.NewValidationError("subscription", "subscription is not active",
            cuserr.WithLogger(m.logger),
            cuserr.WithContext(ctx),
            cuserr.WithMetadata("subscription_id", sub.ID),
            cuserr.WithMetadata("status", sub.Status),
        )
    }
    
    // Check idempotency
    if req.IdempotencyKey != "" {
        exists, err := m.idempotencyStore.Check(ctx, req.IdempotencyKey, sub.ID)
        if err != nil {
            return nil, err
        }
        if exists {
            // Return existing delivery
            // Implementation depends on requirements
        }
    }
    
    // Generate delivery ID
    deliveryID, err := generateDeliveryID()
    if err != nil {
        return nil, cuserr.NewInternalError("id_generation", err,
            cuserr.WithLogger(m.logger),
            cuserr.WithContext(ctx),
        )
    }
    
    // Create delivery
    delivery := &Delivery{
        ID:             deliveryID,
        SubscriptionID: sub.ID,
        TenantID:       sub.TenantID,
        EventType:      req.EventType,
        Payload:        req.Payload,
        Status:         DeliveryStatusPending,
        AttemptCount:   0,
        MaxAttempts:    sub.RetryPolicy.MaxAttempts,
        NextRetryAt:    timePtr(time.Now()),
        CreatedAt:      time.Now(),
    }
    
    // Store in database
    if err := m.repo.CreateDelivery(ctx, delivery); err != nil {
        return nil, cuserr.NewInternalError("database", err,
            cuserr.WithLogger(m.logger),
            cuserr.WithContext(ctx),
        )
    }
    
    // Store idempotency key if provided
    if req.IdempotencyKey != "" {
        expiresAt := time.Now().Add(24 * time.Hour)
        if err := m.idempotencyStore.Store(ctx, req.IdempotencyKey, sub.ID, expiresAt); err != nil {
            m.logger.Warn("Failed to store idempotency key", zap.Error(err))
        }
    }
    
    // Publish queued event
    pubbing.PublishTyped(m.broker, EventTopicDeliveryQueued, DeliveryEvent{
        DeliveryID:     delivery.ID,
        SubscriptionID: sub.ID,
        TenantID:       sub.TenantID,
        EventType:      req.EventType,
        Status:         DeliveryStatusPending,
        Timestamp:      time.Now(),
    })
    
    m.logger.Info("Delivery queued",
        zap.String("delivery_id", delivery.ID),
        zap.String("subscription_id", sub.ID),
        zap.String("event_type", req.EventType),
    )
    
    return delivery, nil
}
```

### Delivery Engine Implementation

```go
// hookd.delivery.go

type DeliveryEngine struct {
    manager    *Manager
    workerPool chan struct{}
    stopCh     chan struct{}
    wg         sync.WaitGroup
}

func NewDeliveryEngine(manager *Manager) *DeliveryEngine {
    return &DeliveryEngine{
        manager:    manager,
        workerPool: make(chan struct{}, manager.config.WorkerCount),
        stopCh:     make(chan struct{}),
    }
}

func (e *DeliveryEngine) Start(ctx context.Context) error {
    e.manager.logger.Info("Starting delivery engine",
        zap.Int("worker_count", e.manager.config.WorkerCount),
    )
    
    // Start queue poller
    e.wg.Add(1)
    go e.pollQueue(ctx)
    
    return nil
}

func (e *DeliveryEngine) Stop(ctx context.Context) error {
    e.manager.logger.Info("Stopping delivery engine")
    
    // Signal stop
    close(e.stopCh)
    
    // Wait for workers
    done := make(chan struct{})
    go func() {
        e.wg.Wait()
        close(done)
    }()
    
    select {
    case <-done:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

func (e *DeliveryEngine) pollQueue(ctx context.Context) {
    defer e.wg.Done()
    
    ticker := time.NewTicker(e.manager.config.QueuePollInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-e.stopCh:
            return
        case <-ticker.C:
            e.processPendingDeliveries(ctx)
        }
    }
}

func (e *DeliveryEngine) processPendingDeliveries(ctx context.Context) {
    // Get pending deliveries
    deliveries, err := e.manager.repo.GetPendingDeliveries(ctx, 100)
    if err != nil {
        e.manager.logger.Error("Failed to get pending deliveries", zap.Error(err))
        return
    }
    
    for _, delivery := range deliveries {
        // Check if we should process this delivery
        if delivery.NextRetryAt != nil && delivery.NextRetryAt.After(time.Now()) {
            continue
        }
        
        // Acquire worker slot
        select {
        case e.workerPool <- struct{}{}:
            e.wg.Add(1)
            go func(d *Delivery) {
                defer e.wg.Done()
                defer func() { <-e.workerPool }()
                
                if err := e.executeDelivery(ctx, d); err != nil {
                    e.manager.logger.Error("Delivery execution failed",
                        zap.String("delivery_id", d.ID),
                        zap.Error(err),
                    )
                }
            }(delivery)
        case <-ctx.Done():
            return
        case <-e.stopCh:
            return
        }
    }
}

func (e *DeliveryEngine) executeDelivery(ctx context.Context, delivery *Delivery) error {
    startTime := time.Now()
    
    // Get subscription
    sub, err := e.manager.repo.GetSubscription(ctx, delivery.SubscriptionID)
    if err != nil {
        return err
    }
    
    // Check circuit breaker
    cb := e.manager.getOrCreateCircuitBreaker(sub.URL)
    if !cb.AllowRequest() {
        e.manager.logger.Debug("Circuit breaker open, skipping delivery",
            zap.String("delivery_id", delivery.ID),
            zap.String("url", sub.URL),
        )
        return nil
    }
    
    // Publish started event
    pubbing.PublishTyped(e.manager.broker, EventTopicDeliveryStarted, DeliveryEvent{
        DeliveryID:     delivery.ID,
        SubscriptionID: sub.ID,
        TenantID:       sub.TenantID,
        Status:         "started",
        Timestamp:      time.Now(),
    })
    
    // Prepare payload
    payloadBytes, err := json.Marshal(delivery.Payload)
    if err != nil {
        return cuserr.NewInternalError("json_marshal", err,
            cuserr.WithLogger(e.manager.logger),
            cuserr.WithMetadata("delivery_id", delivery.ID),
        )
    }
    
    // Generate signature
    timestamp := time.Now().Unix()
    signature := generateSignature(payloadBytes, sub.Secret, timestamp)
    
    // Create HTTP request
    req, err := http.NewRequestWithContext(ctx, "POST", sub.URL, bytes.NewReader(payloadBytes))
    if err != nil {
        return cuserr.NewInternalError("http_request", err,
            cuserr.WithLogger(e.manager.logger),
        )
    }
    
    // Set headers
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set(HeaderSignature, signature)
    req.Header.Set(HeaderTimestamp, fmt.Sprintf("%d", timestamp))
    req.Header.Set(HeaderDeliveryID, delivery.ID)
    req.Header.Set(HeaderAttemptNumber, fmt.Sprintf("%d", delivery.AttemptCount+1))
    
    // Add custom headers
    for key, value := range sub.Headers {
        req.Header.Set(key, value)
    }
    
    // Execute request
    resp, err := e.manager.httpClient.Do(req)
    duration := time.Since(startTime)
    
    // Create delivery attempt
    attempt := &DeliveryAttempt{
        DeliveryID:    delivery.ID,
        AttemptNumber: delivery.AttemptCount + 1,
        DurationMs:    duration.Milliseconds(),
        AttemptedAt:   time.Now(),
    }
    
    if err != nil {
        // Network error
        attempt.Error = err.Error()
        e.handleDeliveryFailure(ctx, delivery, sub, attempt, err)
        cb.RecordFailure()
        return nil
    }
    defer resp.Body.Close()
    
    // Read response
    body, _ := io.ReadAll(resp.Body)
    attempt.StatusCode = resp.StatusCode
    attempt.ResponseBody = string(body)
    
    // Save attempt
    if err := e.manager.repo.CreateDeliveryAttempt(ctx, attempt); err != nil {
        e.manager.logger.Error("Failed to save delivery attempt", zap.Error(err))
    }
    
    // Check response status
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        // Success
        cb.RecordSuccess()
        return e.handleDeliverySuccess(ctx, delivery)
    }
    
    // Failure
    cb.RecordFailure()
    return e.handleDeliveryFailure(ctx, delivery, sub, attempt, 
        fmt.Errorf("HTTP %d: %s", resp.StatusCode, body))
}

func (e *DeliveryEngine) handleDeliverySuccess(ctx context.Context, delivery *Delivery) error {
    // Update delivery
    delivery.Status = DeliveryStatusSuccess
    delivery.CompletedAt = timePtr(time.Now())
    
    if err := e.manager.repo.UpdateDelivery(ctx, delivery); err != nil {
        return err
    }
    
    // Publish success event
    pubbing.PublishTyped(e.manager.broker, EventTopicDeliverySuccess, DeliveryEvent{
        DeliveryID:     delivery.ID,
        SubscriptionID: delivery.SubscriptionID,
        TenantID:       delivery.TenantID,
        Status:         DeliveryStatusSuccess,
        Timestamp:      time.Now(),
    })
    
    e.manager.logger.Info("Delivery succeeded",
        zap.String("delivery_id", delivery.ID),
    )
    
    return nil
}

func (e *DeliveryEngine) handleDeliveryFailure(
    ctx context.Context,
    delivery *Delivery,
    sub *Subscription,
    attempt *DeliveryAttempt,
    err error,
) error {
    delivery.AttemptCount++
    
    // Check if should retry
    if delivery.AttemptCount >= delivery.MaxAttempts {
        // Move to dead letter queue
        delivery.Status = DeliveryStatusDeadLetter
        delivery.CompletedAt = timePtr(time.Now())
        
        if err := e.manager.repo.MoveToDeadLetter(ctx, delivery.ID, err.Error()); err != nil {
            return err
        }
        
        // Publish dead letter event
        pubbing.PublishTyped(e.manager.broker, EventTopicDeliveryDeadLetter, DeliveryEvent{
            DeliveryID:     delivery.ID,
            SubscriptionID: delivery.SubscriptionID,
            TenantID:       delivery.TenantID,
            Status:         DeliveryStatusDeadLetter,
            Timestamp:      time.Now(),
        })
        
        e.manager.logger.Warn("Delivery moved to dead letter queue",
            zap.String("delivery_id", delivery.ID),
            zap.Int("attempts", delivery.AttemptCount),
        )
        
        return nil
    }
    
    // Schedule retry
    nextRetry := calculateNextRetryDelay(delivery.AttemptCount, sub.RetryPolicy)
    delivery.NextRetryAt = timePtr(time.Now().Add(nextRetry))
    delivery.Status = DeliveryStatusFailed
    
    if err := e.manager.repo.UpdateDelivery(ctx, delivery); err != nil {
        return err
    }
    
    // Publish failed event
    pubbing.PublishTyped(e.manager.broker, EventTopicDeliveryFailed, DeliveryEvent{
        DeliveryID:     delivery.ID,
        SubscriptionID: delivery.SubscriptionID,
        TenantID:       delivery.TenantID,
        Status:         DeliveryStatusFailed,
        Timestamp:      time.Now(),
        Metadata: map[string]interface{}{
            "attempt_count": delivery.AttemptCount,
            "next_retry":    delivery.NextRetryAt,
        },
    })
    
    e.manager.logger.Debug("Delivery failed, will retry",
        zap.String("delivery_id", delivery.ID),
        zap.Int("attempt", delivery.AttemptCount),
        zap.Time("next_retry", *delivery.NextRetryAt),
    )
    
    return nil
}
```

---

## Testing Strategy

### Test Coverage Requirements

**Mandatory**: 90%+ test coverage for all packages

### Test Structure

```
internal/
├── hookd.manager_test.go
├── hookd.subscription_test.go
├── hookd.delivery_test.go
├── hookd.retry_test.go
├── hookd.circuit_breaker_test.go
├── hookd.idempotency_test.go
├── hookd.security_test.go
├── hookd.repository.postgres_test.go
└── hookd.observability_test.go

integration/
├── integration_test.go
└── testcontainers_test.go
```

### Unit Testing Pattern

```go
func TestManager_CreateSubscription(t *testing.T) {
    // Setup
    logger := zaptest.NewLogger(t)
    broker, err := pubbing.New()
    require.NoError(t, err)
    defer broker.Shutdown(time.Second)
    
    repo := NewMockRepository()
    config := &Config{
        WorkerCount: 5,
        MaxRetries:  10,
    }
    
    manager, err := NewManager(config, logger, repo, broker)
    require.NoError(t, err)
    defer manager.Shutdown(context.Background())
    
    tests := []struct {
        name    string
        request *CreateSubscriptionRequest
        wantErr bool
        errType error
    }{
        {
            name: "Success",
            request: &CreateSubscriptionRequest{
                TenantID:   "tenant_123",
                URL:        "https://example.com/webhook",
                EventTypes: []string{"user.created"},
                Secret:     "secret",
            },
            wantErr: false,
        },
        {
            name: "InvalidURL",
            request: &CreateSubscriptionRequest{
                TenantID: "tenant_123",
                URL:      "not-a-url",
            },
            wantErr: true,
            errType: cuserr.ErrInvalidInput,
        },
        {
            name: "MissingTenantID",
            request: &CreateSubscriptionRequest{
                URL: "https://example.com/webhook",
            },
            wantErr: true,
            errType: cuserr.ErrInvalidInput,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctx := context.Background()
            
            sub, err := manager.CreateSubscription(ctx, tt.request)
            
            if tt.wantErr {
                assert.Error(t, err)
                assert.True(t, errors.Is(err, tt.errType))
                return
            }
            
            assert.NoError(t, err)
            assert.NotEmpty(t, sub.ID)
            assert.True(t, strings.HasPrefix(sub.ID, PrefixSubscription))
            assert.Equal(t, tt.request.URL, sub.URL)
        })
    }
}
```

### Integration Testing Pattern

```go
func TestIntegration_EndToEnd(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }
    
    // Start PostgreSQL container
    ctx := context.Background()
    postgres, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: testcontainers.ContainerRequest{
            Image:        "postgres:15",
            ExposedPorts: []string{"5432/tcp"},
            Env: map[string]string{
                "POSTGRES_PASSWORD": "test",
                "POSTGRES_DB":       "hookd_test",
            },
            WaitingFor: wait.ForListeningPort("5432/tcp"),
        },
        Started: true,
    })
    require.NoError(t, err)
    defer postgres.Terminate(ctx)
    
    // Get connection string
    host, _ := postgres.Host(ctx)
    port, _ := postgres.MappedPort(ctx, "5432")
    connStr := fmt.Sprintf("postgres://postgres:test@%s:%s/hookd_test?sslmode=disable",
        host, port.Port())
    
    // Run migrations
    // ...
    
    // Create manager
    logger := zaptest.NewLogger(t)
    broker, _ := pubbing.New()
    defer broker.Shutdown(time.Second)
    
    repo, _ := NewPostgresRepository(connStr, logger)
    defer repo.Close()
    
    config := &Config{
        WorkerCount:        5,
        QueuePollInterval:  100 * time.Millisecond,
        DeliveryTimeout:    5 * time.Second,
    }
    
    manager, err := NewManager(config, logger, repo, broker)
    require.NoError(t, err)
    defer manager.Shutdown(ctx)
    
    // Start manager
    require.NoError(t, manager.Start(ctx))
    
    // Test end-to-end workflow
    t.Run("CreateAndDeliver", func(t *testing.T) {
        // Create test HTTP server
        received := make(chan map[string]interface{}, 1)
        server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            var payload map[string]interface{}
            json.NewDecoder(r.Body).Decode(&payload)
            received <- payload
            w.WriteHeader(http.StatusOK)
        }))
        defer server.Close()
        
        // Create subscription
        sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
            TenantID:   "tenant_test",
            URL:        server.URL,
            EventTypes: []string{"test.event"},
            Secret:     "test-secret",
        })
        require.NoError(t, err)
        
        // Queue delivery
        testPayload := map[string]interface{}{
            "message": "test",
            "value":   123,
        }
        
        delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
            SubscriptionID: sub.ID,
            EventType:      "test.event",
            Payload:        testPayload,
        })
        require.NoError(t, err)
        
        // Wait for delivery
        select {
        case payload := <-received:
            assert.Equal(t, "test", payload["message"])
            assert.Equal(t, float64(123), payload["value"])
        case <-time.After(5 * time.Second):
            t.Fatal("Delivery timeout")
        }
        
        // Verify delivery status
        time.Sleep(100 * time.Millisecond) // Allow for async updates
        updated, err := manager.GetDelivery(ctx, delivery.ID)
        require.NoError(t, err)
        assert.Equal(t, DeliveryStatusSuccess, updated.Status)
    })
}
```

### Concurrency Testing

```go
func TestDeliveryEngine_Concurrent(t *testing.T) {
    // Setup
    logger := zaptest.NewLogger(t)
    broker, _ := pubbing.New()
    defer broker.Shutdown(time.Second)
    
    repo := NewMockRepository()
    config := &Config{
        WorkerCount: 10,
    }
    
    manager, _ := NewManager(config, logger, repo, broker)
    defer manager.Shutdown(context.Background())
    
    // Create multiple deliveries concurrently
    var wg sync.WaitGroup
    deliveries := make(chan *Delivery, 100)
    
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            
            delivery, err := manager.QueueDelivery(context.Background(), &QueueDeliveryRequest{
                SubscriptionID: "sub_test",
                EventType:      "test.event",
                Payload:        map[string]interface{}{"index": idx},
            })
            
            if err == nil {
                deliveries <- delivery
            }
        }(i)
    }
    
    wg.Wait()
    close(deliveries)
    
    // Verify all deliveries created
    count := 0
    for range deliveries {
        count++
    }
    assert.Equal(t, 100, count)
}
```

### Test Running Commands

```bash
# Run all tests with race detector
go test -v -race -cover ./...

# Run only unit tests
go test -v -short ./...

# Run integration tests
go test -v -run Integration ./...

# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run specific test
go test -v -run TestManager_CreateSubscription ./internal/

# Run with verbose output
go test -v -race ./... 2>&1 | tee test.log

# Benchmark tests
go test -bench=. -benchmem ./...
```

---

## CodeFinder MCP Integration

### Using CodeFinder for Development

CodeFinder is a proprietary MCP (Model Context Protocol) tool that provides powerful code search and analysis capabilities. Claude Code agents should use CodeFinder extensively during implementation.

### Key CodeFinder Commands

#### 1. Project Overview
```python
# Get repository overview
aggregate(metric="count", scope=["internal/"])

# Find entry points
query(q="NewManager|main", t="fn", m="f", ctx="std")
```

#### 2. Symbol Discovery
```python
# Find all subscription-related functions
query(q="subscription", t="fn", m="f", ctx="min", lim=20)

# Find specific interface
query(q="Repository", t="interface", m="x", ctx="full")

# Find all error definitions
query(q="Err*", t="var", m="f", ctx="std")
```

#### 3. Dependency Analysis
```python
# Trace dependencies from Manager
traverse(start=["Manager"], typ="dep", dir="down", depth=3)

# Find all callers of CreateSubscription
traverse(start=["CreateSubscription"], typ="dep", dir="up", depth=2)
```

#### 4. Code Quality Checks
```python
# Check complexity
aggregate(metric="complexity", scope=["internal/"])

# Check documentation coverage
aggregate(metric="doc_coverage", scope=["internal/"])

# Find patterns
aggregate(metric="patterns", pattern="TODO|FIXME", scope=["internal/"])
```

#### 5. Multi-File Operations
```python
# Get overview of all manager files
batch_files(
    files=["internal/hookd.manager.go", "internal/hookd.delivery.go"],
    op="overview"
)

# List all symbols in repository files
batch_files(
    files=["internal/hookd.repository.interface.go", "internal/hookd.repository.postgres.go"],
    op="list",
    ctx="std"
)
```

### Development Workflow with CodeFinder

#### Phase 1: Understanding Existing Code
```python
# 1. Get project structure
aggregate(metric="count", scope=["internal/"])

# 2. Find main components
query(q="Manager|Engine|Repository", t="struct", m="f", ctx="std")

# 3. Understand dependencies
traverse(start=["Manager"], typ="dep", dir="down", depth=2)
```

#### Phase 2: Implementation
```python
# 1. Find similar implementations
query(q="Create*", t="fn", m="f", ctx="std", lim=10)

# 2. Check for existing patterns
query(q="generate*ID", t="fn", m="f", ctx="std")

# 3. Find interface implementations
traverse(start=["Repository"], typ="hier", dir="down", depth=1)
```

#### Phase 3: Testing
```python
# 1. Find existing tests
query(q="Test*", t="fn", f="**/*_test.go", m="f", ctx="min")

# 2. Check test coverage patterns
batch_files(files=[...test files...], op="list", t="fn")
```

#### Phase 4: Refactoring
```python
# 1. Find all usages
traverse(start=["FunctionName"], typ="dep", dir="up", depth=3)

# 2. Impact analysis
diff(base="main", target="feature-branch", scope="symbols")
```

### CodeFinder Best Practices

1. **Start with minimal context**: Use `ctx="min"` by default, expand to `ctx="full"` only when needed
2. **Use exact matching when possible**: `m="x"` is faster than `m="f"`
3. **Batch operations**: Use `batch_files()` for multiple files instead of multiple `query()` calls
4. **Progressive disclosure**: Get summaries first, then details
5. **Use symbol IDs**: Chain operations using symbol IDs for efficiency

### Example: Finding and Understanding Manager Implementation

```python
# Step 1: Find Manager struct
result = query(q="Manager", t="struct", m="x", ctx="full")
manager_id = result["r"][0]["id"]

# Step 2: Find all Manager methods
methods = query(q="*", t="mth", m="x", ctx="std", rel=True)
# Filter for Manager methods in results

# Step 3: Understand dependencies
deps = traverse(start=[manager_id], typ="dep", dir="down", depth=2)

# Step 4: Check implementation completeness
# Get all interface methods
interface_methods = query(q="*", t="mth", f="internal/hookd.interfaces.go")

# Compare with Manager methods to ensure all implemented
```

---

## Deployment & Operations

### Version Management

**versions.yaml** (mandatory):
```yaml
project:
  name: "go-hookd"
  version: "1.0.0"
  description: "Enterprise webhook management package"

schemas:
  postgres_main:
    version: "1"
    description: "Main PostgreSQL schema"

components:
  manager:
    version: "1.0.0"
  delivery_engine:
    version: "1.0.0"
  circuit_breaker:
    version: "1.0.0"

dependencies:
  go_cuserr:
    version: "0.3.0"
    required: true
  go_version:
    version: "1.0.0"
    required: true
  go_pubbing:
    version: "0.4.0"
    required: true
```

### Build Configuration

**Makefile**:
```makefile
.PHONY: build test coverage lint docker

VERSION := $(shell git describe --tags --always --dirty)
COMMIT := $(shell git rev-parse HEAD)
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS := -X 'github.com/itsatony/go-version.GitCommit=$(COMMIT)' \
           -X 'github.com/itsatony/go-version.GitTag=$(VERSION)' \
           -X 'github.com/itsatony/go-version.BuildTime=$(BUILD_TIME)'

build:
	go build -ldflags "$(LDFLAGS)" -o bin/hookd cmd/example/main.go

test:
	go test -v -race -cover ./...

test-short:
	go test -v -short ./...

test-integration:
	go test -v -run Integration ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run

docker:
	docker build -t vaudience/go-hookd:$(VERSION) .

clean:
	rm -rf bin/ coverage.out coverage.html
```

### Docker Deployment

**Dockerfile**:
```dockerfile
# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with version info
ARG VERSION
ARG COMMIT
ARG BUILD_TIME
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X 'github.com/itsatony/go-version.GitCommit=${COMMIT}' \
              -X 'github.com/itsatony/go-version.GitTag=${VERSION}' \
              -X 'github.com/itsatony/go-version.BuildTime=${BUILD_TIME}'" \
    -o hookd cmd/example/main.go

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary and version manifest
COPY --from=builder /app/hookd .
COPY --from=builder /app/versions.yaml .

# Expose ports
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["./hookd"]
```

### Kubernetes Deployment

**deployment.yaml**:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: go-hookd
  labels:
    app: go-hookd
spec:
  replicas: 3
  selector:
    matchLabels:
      app: go-hookd
  template:
    metadata:
      labels:
        app: go-hookd
    spec:
      containers:
      - name: go-hookd
        image: vaudience/go-hookd:v1.0.0
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: url
        - name: WORKER_COUNT
          value: "10"
        - name: LOG_LEVEL
          value: "info"
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /version
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: go-hookd
spec:
  selector:
    app: go-hookd
  ports:
  - port: 80
    targetPort: 8080
  type: ClusterIP
```

### Monitoring & Observability

#### Prometheus Metrics

**Key Metrics**:
- `hookd_delivery_attempts_total` - Total delivery attempts
- `hookd_delivery_duration_seconds` - Delivery duration histogram
- `hookd_circuit_breaker_state` - Circuit breaker state gauge
- `hookd_queue_depth` - Current queue depth
- `hookd_worker_pool_utilization` - Worker pool utilization

#### Grafana Dashboard

Create dashboards for:
1. Delivery success/failure rates
2. Delivery latency (p50, p95, p99)
3. Circuit breaker states
4. Queue depth over time
5. Worker pool utilization
6. Error rates by type

#### Logging

All logs include:
- Version information (from go-version)
- Request/operation IDs
- Tenant IDs for multi-tenancy
- Structured fields for parsing

**Example Log Entry**:
```json
{
  "level": "info",
  "ts": "2025-11-08T10:30:00Z",
  "caller": "internal/hookd.manager.go:123",
  "msg": "Subscription created",
  "version": "1.0.0",
  "git_commit": "a1b2c3d",
  "subscription_id": "sub_6ByTSYmGzT2c",
  "tenant_id": "tenant_123",
  "url": "https://example.com/webhook"
}
```

---

## Excellence Gates

### Gate 1: Foundation ✓

**Requirements**:
- [ ] All dependencies installed correctly
- [ ] versions.yaml created and validated
- [ ] All constants defined (NO magic strings)
- [ ] Core interfaces complete
- [ ] Domain models defined
- [ ] Error definitions using go-cuserr
- [ ] README with basic documentation

**Validation**:
```bash
go mod verify
go build ./...
go test -race ./...
```

### Gate 2: Data Layer ✓

**Requirements**:
- [ ] Migration files created
- [ ] Repository interface complete
- [ ] PostgreSQL implementation finished
- [ ] Mock repository for testing
- [ ] 90%+ test coverage on repository
- [ ] All queries optimized with indexes
- [ ] Transaction support working

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestRepository
# Coverage must be >= 90%
```

### Gate 3: Core Manager ✓

**Requirements**:
- [ ] Manager constructor with validation
- [ ] All subscription CRUD operations
- [ ] Event publishing to go-pubbing
- [ ] Graceful shutdown implemented
- [ ] 90%+ test coverage
- [ ] godoc documentation complete

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestManager
go doc -all github.com/itsatony/go-hookd
```

### Gate 4: Delivery Engine ✓

**Requirements**:
- [ ] Worker pool implementation
- [ ] Queue polling logic
- [ ] HTTP client with timeout
- [ ] HMAC signature generation
- [ ] Delivery execution complete
- [ ] Event publishing for all states
- [ ] 90%+ test coverage
- [ ] Concurrency tests passing

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestDelivery
go test -v -race -count=100 ./internal/... -run TestDelivery_Concurrent
```

### Gate 5: Retry & Circuit Breaker ✓

**Requirements**:
- [ ] Exponential backoff with jitter
- [ ] Circuit breaker implementation
- [ ] State machine correct
- [ ] Retry budget tracking
- [ ] Adaptive backoff working
- [ ] 90%+ test coverage
- [ ] Failure scenario tests passing

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestRetry
go test -v -race -cover ./internal/... -run TestCircuitBreaker
```

### Gate 6: Security & Idempotency ✓

**Requirements**:
- [ ] Idempotency store complete
- [ ] Content-based deduplication
- [ ] HMAC signature working
- [ ] Timestamp validation
- [ ] Secret rotation support
- [ ] Replay protection
- [ ] 90%+ test coverage
- [ ] Security tests passing

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestIdempotency
go test -v -race -cover ./internal/... -run TestSecurity
```

### Gate 7: Observability ✓

**Requirements**:
- [ ] Metrics collection working
- [ ] Prometheus exporters functional
- [ ] Distributed tracing support
- [ ] Structured logging throughout
- [ ] Internal event subscriptions
- [ ] 90%+ test coverage
- [ ] Metrics documentation complete

**Validation**:
```bash
go test -v -race -cover ./internal/... -run TestObservability
# Manual verification of metrics endpoint
```

### Gate 8: Integration ✓

**Requirements**:
- [ ] Basic example working
- [ ] Advanced example working
- [ ] Integration tests passing
- [ ] End-to-end tests passing
- [ ] Example HTTP handlers
- [ ] Documentation complete
- [ ] Troubleshooting guide

**Validation**:
```bash
go test -v -race ./integration/...
go run examples/basic/main.go
go run examples/advanced/main.go
```

### Gate 9: Production Ready ✓

**Requirements**:
- [ ] README complete
- [ ] All godoc documentation
- [ ] Architecture docs
- [ ] Operations runbook
- [ ] Migration guide
- [ ] CHANGELOG current
- [ ] Docker build working
- [ ] Kubernetes manifests tested
- [ ] Overall 90%+ coverage

**Final Validation**:
```bash
# Build
make build

# Test
make test
make test-integration
make coverage

# Lint
make lint

# Docker
make docker
docker run vaudience/go-hookd:latest --version

# Coverage check
go test -cover ./... | grep -E 'coverage: [0-9]+\.[0-9]+%' | \
    awk '{if ($2 < 90.0) exit 1}'
```

---

## Summary

This implementation guide provides a complete roadmap for building the go-hookd enterprise webhook management package. Key aspects:

### Core Principles
- **Excellence. Always.** - Every component built to production standards
- **Thread-safe by default** - All code handles concurrency
- **No magic strings** - Everything is a constant
- **go-cuserr everywhere** - Consistent error handling
- **90%+ test coverage** - Comprehensive testing

### Integration
- **go-cuserr** - Protocol-agnostic error handling
- **go-version** - Multi-dimensional versioning
- **go-pubbing** - High-performance event bus

### Implementation Approach
- 9 clear phases with specific deliverables
- Excellence Gates to ensure quality
- CodeFinder MCP integration for efficient development
- Comprehensive testing at every phase

### Production Readiness
- Docker and Kubernetes deployment
- Prometheus metrics and Grafana dashboards
- Comprehensive documentation
- Operations runbook

---

**Excellence. Always.**

*vAudience.AI GmbH - go-hookd Implementation Guide v2.0*  
*Powered by: go-cuserr • go-version • go-pubbing*
