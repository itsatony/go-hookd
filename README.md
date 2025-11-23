# go-hookd

A webhook delivery management library for Go applications. Handles webhook subscriptions, reliable delivery with retries, circuit breakers, and idempotency.

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Test Coverage](https://img.shields.io/badge/coverage-62.1%25-yellow.svg)](https://github.com/itsatony/go-hookd)

> **Status**: Pre-MVP Development
> This library is under active development. Core functionality is implemented and tested, but breaking changes may occur before v1.0.

## What is go-hookd?

go-hookd is a Go library that manages webhook subscriptions and deliveries. It provides the infrastructure to:
- Store webhook subscriptions with configuration
- Queue and deliver webhook HTTP requests
- Retry failed deliveries with exponential backoff
- Track delivery attempts and responses
- Prevent duplicate deliveries via idempotency keys
- Circuit-break failing endpoints automatically

**What go-hookd is NOT:**
- Not a standalone service (it's a library you integrate)
- Not an HTTP server (you provide your own API endpoints)
- Not a message queue replacement (it uses PostgreSQL for persistence)

## Features

### Implemented and Tested

- **Subscription Management**: Create, read, update, delete webhook subscriptions with filtering
- **Delivery Queue**: Queue webhook deliveries with validation and status tracking
- **Worker Pool**: Configurable concurrent workers for processing deliveries
- **Retry Logic**: Exponential backoff with configurable attempts and timing
- **Circuit Breaker**: Per-endpoint failure detection (closed → half-open → open states)
- **Idempotency**: Duplicate prevention using idempotency keys with TTL
- **Status Tracking**: Detailed delivery attempts with HTTP responses
- **Event System**: Internal event bus for observability (delivery, circuit breaker, metrics events)
- **Graceful Shutdown**: Context-based cancellation with in-flight request completion
- **Signature Verification**: HMAC-SHA256 payload signing for webhook authenticity

### Current Limitations

- **No Migrations Included**: You need to create database schema based on repository interface
- **PostgreSQL Only**: No support for other databases yet
- **No Built-in API**: You implement HTTP handlers for your use case
- **Limited Observability**: Event bus works, but metrics/tracing require integration
- **No Admin UI**: Library-only, bring your own dashboard

## Installation

```bash
go get github.com/itsatony/go-hookd
```

**Dependencies:**
- Go 1.24+
- PostgreSQL 13+ (for production use)
- Or use `MockRepository` for testing/development

## Quick Start

### Basic Example (with Mock Repository)

Perfect for testing and development without a database:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/itsatony/go-hookd"
)

func main() {
	// Configure (PostgreSQL connection not required for mock)
	config := hookd.NewConfig("")
	config.WorkerCount = 2
	config.QueuePollInterval = 500 // milliseconds

	// Use mock repository (in-memory, no database needed)
	repo := hookd.NewMockRepository()

	// Create manager
	manager, err := hookd.NewManager(config, repo)
	if err != nil {
		log.Fatal(err)
	}

	// Start workers
	ctx := context.Background()
	if err := manager.Start(ctx); err != nil {
		log.Fatal(err)
	}
	defer manager.Stop()

	// Create webhook subscription
	sub, err := manager.CreateSubscription(ctx, &hookd.CreateSubscriptionRequest{
		TenantID:   "tenant_001",
		URL:        "https://webhook.site/unique-id",
		EventTypes: []string{"user.created"},
		Secret:     "your_webhook_secret",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Created subscription: %s\n", sub.ID)

	// Queue a delivery
	delivery, err := manager.QueueDelivery(ctx, &hookd.QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "user.created",
		Payload: map[string]any{
			"user_id": "12345",
			"email":   "user@example.com",
		},
		IdempotencyKey: "user_12345_created_at_" + time.Now().Format("20060102"),
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Queued delivery: %s (status: %s)\n", delivery.ID, delivery.Status)

	// Wait a moment for delivery processing
	time.Sleep(3 * time.Second)

	// Check delivery status
	updated, err := manager.GetDelivery(ctx, delivery.ID)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Final delivery status: %s\n", updated.Status)
	if updated.Status == hookd.DeliveryStatusSuccess {
		fmt.Println("✓ Webhook delivered successfully!")
	}
}
```

### Production Example (with PostgreSQL)

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/itsatony/go-hookd"
)

func main() {
	// PostgreSQL connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable required")
	}

	// Configure
	config := hookd.NewConfig(dbURL)
	config.WorkerCount = 10
	config.QueuePollInterval = 1000
	config.DefaultMaxRetries = 3

	// Create repository
	repo, err := hookd.NewPostgresRepository(context.Background(), config)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Create manager
	manager, err := hookd.NewManager(config, repo)
	if err != nil {
		log.Fatal(err)
	}

	// Start processing
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		log.Fatal(err)
	}

	log.Println("go-hookd manager started")

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down gracefully...")
	cancel()
	manager.Stop()
}
```

**See `examples/` directory for:**
- `basic/` - Simple usage with mock repository
- `http-server/` - REST API integration example
- `monitoring/` - Event bus integration for metrics
- `helpers/` - Common utility functions

## Configuration

### Config Options Explained

```go
config := hookd.NewConfig(databaseURL)

// Worker Configuration
config.WorkerCount = 10                    // Number of concurrent delivery workers
config.QueuePollInterval = 1000            // How often to check for pending deliveries (ms)
config.MaxBatchSize = 100                  // Max deliveries to fetch per poll

// Retry Configuration
config.DefaultMaxRetries = 3               // Default retry attempts (overridable per subscription)
config.DefaultInitialBackoffMs = 1000      // First retry after 1s
config.DefaultMaxBackoffMs = 60000         // Max retry delay of 60s
config.DefaultBackoffFactor = 2.0          // Double the delay each retry

// Circuit Breaker Configuration
config.CircuitBreakerThreshold = 5         // Open circuit after 5 consecutive failures
config.CircuitBreakerTimeoutMs = 60000     // Keep circuit open for 60s
config.CircuitBreakerHalfOpenRequests = 3  // Need 3 successes to close circuit

// HTTP Configuration
config.DeliveryTimeoutMs = 30000           // Timeout for webhook HTTP requests (30s)

// Idempotency Configuration
config.IdempotencyTTLHours = 24            // How long to remember idempotency keys

// Shutdown Configuration
config.ShutdownTimeoutMs = 30000           // Max time to wait for graceful shutdown
```

### Understanding Retry Logic

When a delivery fails:
1. **Check if retryable**: 5xx status codes and network errors → retry, 4xx → don't retry
2. **Check attempts**: If attempts < MaxRetries → schedule retry
3. **Calculate backoff**: `delay = min(InitialBackoff * (Factor ^ attempt), MaxBackoff)`
4. **Schedule**: Delivery moves to `pending` status with `next_attempt_at` timestamp
5. **Retry**: Worker picks it up when `next_attempt_at` is reached

### Understanding Circuit Breaker

Per-endpoint state machine:
- **Closed** (normal): Requests go through normally
- **Open** (failing): After `Threshold` consecutive failures, all requests fast-fail
- **Half-Open** (testing): After `TimeoutMs`, allow `HalfOpenRequests` to test recovery
- **Closed** (recovered): If half-open requests succeed, resume normal operation

## API Reference

### Subscription Management

```go
// Create subscription
sub, err := manager.CreateSubscription(ctx, &hookd.CreateSubscriptionRequest{
	TenantID:   "tenant_123",             // Multi-tenancy identifier
	URL:        "https://api.example.com/webhook",
	EventTypes: []string{"user.created", "user.updated"},
	Secret:     "webhook_signing_secret",  // For HMAC signature
	RetryPolicy: &hookd.RetryPolicy{       // Optional: override defaults
		MaxAttempts:    5,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     5 * time.Minute,
		BackoffFactor:  2.0,
	},
	Headers: map[string]string{            // Optional: custom headers
		"X-API-Key": "your-api-key",
	},
	Metadata: map[string]any{              // Optional: arbitrary data
		"customer_id": "cust_123",
		"environment": "production",
	},
})

// Get subscription by ID
sub, err := manager.GetSubscription(ctx, subscriptionID)

// Update subscription (all fields optional via pointers)
updated, err := manager.UpdateSubscription(ctx, subscriptionID, &hookd.UpdateSubscriptionRequest{
	EventTypes: &[]string{"user.created", "user.updated", "user.deleted"},
	Status:     hookd.StringPtr(hookd.SubscriptionStatusPaused),
	Headers:    &map[string]string{"X-API-Key": "new-key"},
})

// List subscriptions with filters
subs, err := manager.ListSubscriptions(ctx, &hookd.SubscriptionFilter{
	TenantID:   "tenant_123",             // Filter by tenant
	Status:     hookd.SubscriptionStatusActive,  // Only active subscriptions
	EventTypes: []string{"user.created"}, // Subscriptions for this event
	Limit:      100,                      // Pagination
	Offset:     0,
})

// Lifecycle management
paused, err := manager.PauseSubscription(ctx, subscriptionID)   // Pause delivery
resumed, err := manager.ResumeSubscription(ctx, subscriptionID) // Resume delivery
disabled, err := manager.DisableSubscription(ctx, subscriptionID) // Soft delete

// Hard delete (removes from database)
err := manager.DeleteSubscription(ctx, subscriptionID)
```

### Delivery Management

```go
// Queue a delivery
delivery, err := manager.QueueDelivery(ctx, &hookd.QueueDeliveryRequest{
	SubscriptionID: subscriptionID,
	EventType:      "user.created",
	Payload: map[string]any{
		"user_id":    "12345",
		"email":      "user@example.com",
		"created_at": time.Now(),
	},
	IdempotencyKey: "user_12345_created", // Optional: prevent duplicates
})

// Check delivery status
delivery, err := manager.GetDelivery(ctx, deliveryID)
fmt.Printf("Status: %s, Attempts: %d\n", delivery.Status, delivery.AttemptCount)

// Get all delivery attempts
attempts, err := manager.GetDeliveryAttempts(ctx, deliveryID)
for _, attempt := range attempts {
	fmt.Printf("Attempt %d: %d %s (duration: %dms)\n",
		attempt.AttemptNumber,
		attempt.ResponseStatusCode,
		attempt.ResponseBody,
		attempt.ResponseDuration,
	)
}

// Manually retry a failed delivery
retried, err := manager.RetryDelivery(ctx, deliveryID)

// List deliveries with filters
deliveries, err := manager.ListDeliveries(ctx, &hookd.DeliveryFilter{
	SubscriptionID: subscriptionID,       // Deliveries for specific subscription
	Status:         hookd.DeliveryStatusFailed,  // Only failed deliveries
	EventType:      "user.created",       // Specific event type
	Limit:          50,
	Offset:         0,
})
```

### Delivery Statuses

| Status | Description | Next Action |
|--------|-------------|-------------|
| `pending` | Queued, waiting for worker | Worker will pick up |
| `processing` | Currently being delivered | Wait for completion |
| `success` | Delivered successfully (2xx response) | Done |
| `failed` | Delivery failed, will retry | Automatic retry scheduled |
| `dead_letter` | Failed after max retries | Manual intervention needed |

## Event System for Observability

go-hookd publishes internal events that you can subscribe to for monitoring:

```go
type EventBus interface {
	Publish(topic string, data any)
	Subscribe(topic string, handler func(any)) func()
}

// Implement your event bus
type MyEventBus struct {
	handlers map[string][]func(any)
	mu       sync.RWMutex
}

func (b *MyEventBus) Publish(topic string, data any) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, handler := range b.handlers[topic] {
		go handler(data) // Handle async to not block delivery
	}
}

func (b *MyEventBus) Subscribe(topic string, handler func(any)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.handlers[topic] = append(b.handlers[topic], handler)

	return func() { /* unsubscribe logic */ }
}

// Use with manager
eventBus := &MyEventBus{handlers: make(map[string][]func(any))}
manager, err := hookd.NewManager(config, repo,
	hookd.WithEventBus(eventBus),
)

// Subscribe to events
eventBus.Subscribe("delivery.success", func(data any) {
	// Increment success counter, log, send to metrics system, etc.
	log.Printf("Delivery succeeded: %+v", data)
})

eventBus.Subscribe("delivery.failed", func(data any) {
	// Increment failure counter, alert on-call, etc.
	log.Printf("Delivery failed: %+v", data)
})

eventBus.Subscribe("circuit.opened", func(data any) {
	// Alert: endpoint is failing
	log.Printf("Circuit breaker opened: %+v", data)
})
```

### Available Event Topics

**Delivery Events:**
- `delivery.queued` - New delivery queued
- `delivery.started` - Worker started processing
- `delivery.success` - Delivered successfully (2xx)
- `delivery.failed` - Delivery failed (will retry or dead letter)
- `delivery.dead_letter` - Moved to dead letter queue

**Circuit Breaker Events:**
- `circuit.opened` - Circuit opened due to failures
- `circuit.half_open` - Testing recovery
- `circuit.closed` - Circuit closed, normal operation

**Metrics Events:**
- `metrics.delivery_attempt` - Delivery attempt completed (success or failure)
- `metrics.retry_triggered` - Retry scheduled
- `metrics.queue_depth` - Periodic queue depth measurement

**Audit Events:**
- `audit.subscription_created`
- `audit.subscription_updated`
- `audit.subscription_deleted`

## Webhook Signature Verification

go-hookd signs all webhook payloads with HMAC-SHA256. Recipients should verify signatures:

### Receiver Implementation

```go
import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
)

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	// Extract signature header
	signature := r.Header.Get("X-Webhook-Signature")
	timestamp := r.Header.Get("X-Webhook-Timestamp")
	deliveryID := r.Header.Get("X-Webhook-Delivery-ID")

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Cannot read body", http.StatusBadRequest)
		return
	}

	// Verify signature
	secret := "your_webhook_secret" // From subscription
	if !verifySignature(timestamp, body, signature, secret) {
		http.Error(w, "Invalid signature", http.StatusUnauthorized)
		return
	}

	// Process webhook
	// ... your business logic ...

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"received"}`))
}

func verifySignature(timestamp string, payload []byte, signature, secret string) bool {
	// Construct signed message: timestamp.payload
	message := timestamp + "." + string(payload)

	// Calculate HMAC-SHA256
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	// Compare signatures (constant-time comparison)
	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}
```

### Signature Headers

go-hookd includes these headers in webhook requests:

| Header | Description | Example |
|--------|-------------|---------|
| `X-Webhook-Signature` | HMAC-SHA256 hex signature | `a3f2...` |
| `X-Webhook-Timestamp` | Unix timestamp (string) | `1635789456` |
| `X-Webhook-Delivery-ID` | Unique delivery ID | `dlv_abc123` |

**Signature Format:**
```
HMAC-SHA256(secret, timestamp + "." + payload)
```

## Database Schema

### Required Tables

go-hookd requires these PostgreSQL tables. You need to create them:

```sql
-- Subscriptions
CREATE TABLE subscriptions (
    id VARCHAR(50) PRIMARY KEY,
    tenant_id VARCHAR(100) NOT NULL,
    url TEXT NOT NULL,
    event_types TEXT[] NOT NULL,
    secret TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    retry_policy JSONB,
    headers JSONB,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Deliveries
CREATE TABLE deliveries (
    id VARCHAR(50) PRIMARY KEY,
    subscription_id VARCHAR(50) REFERENCES subscriptions(id),
    tenant_id VARCHAR(100) NOT NULL,
    event_type VARCHAR(100) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP,
    idempotency_key VARCHAR(200),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Delivery Attempts
CREATE TABLE delivery_attempts (
    id VARCHAR(50) PRIMARY KEY,
    delivery_id VARCHAR(50) REFERENCES deliveries(id),
    attempt_number INTEGER NOT NULL,
    response_status_code INTEGER,
    response_body TEXT,
    response_duration INTEGER,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Circuit Breaker State
CREATE TABLE circuit_breaker_state (
    endpoint VARCHAR(500) PRIMARY KEY,
    state VARCHAR(20) NOT NULL DEFAULT 'closed',
    failure_count INTEGER NOT NULL DEFAULT 0,
    last_failure_at TIMESTAMP,
    opened_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Idempotency Keys
CREATE TABLE idempotency_keys (
    key VARCHAR(200) PRIMARY KEY,
    delivery_id VARCHAR(50),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL
);

-- Indexes for performance
CREATE INDEX idx_deliveries_status ON deliveries(status);
CREATE INDEX idx_deliveries_next_attempt ON deliveries(next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_deliveries_subscription ON deliveries(subscription_id);
CREATE INDEX idx_deliveries_tenant ON deliveries(tenant_id);
CREATE INDEX idx_idempotency_expires ON idempotency_keys(expires_at);
```

**Note:** This schema is a guideline. Adapt it to your database conventions.

## Testing

```bash
# Run all unit tests
go test . -v

# Run with race detector
go test -race .

# Run with coverage
go test -cover .

# Generate coverage report
go test -coverprofile=coverage.out .
go tool cover -html=coverage.out
```

## Performance Considerations

### Scaling Guidelines

**Worker Count:**
- Start with 1-2 workers per CPU core
- Monitor queue depth - if growing, increase workers
- Too many workers can overwhelm database connection pool

**Batch Size:**
- Default: 100 deliveries per poll
- Higher = more efficient polling, but longer processing cycles
- Lower = faster reaction time, but more database queries

**PostgreSQL Connection Pool:**
- Set connection pool size ≥ WorkerCount × 2
- Each worker needs connections for: fetch delivery, update status, insert attempts

**Queue Poll Interval:**
- Default: 1000ms (1 second)
- Lower = faster delivery start, but more database load
- Higher = less database load, but slower to pick up new deliveries

### Monitoring Recommendations

Essential metrics to track via event bus:
1. Queue depth (pending deliveries)
2. Delivery success/failure rates
3. Average delivery duration
4. Circuit breaker state changes
5. Worker utilization
6. Dead letter queue size

## Limitations and Known Issues

### Current Limitations

1. **Database**: PostgreSQL only (no MySQL, SQLite, etc.)
2. **No Migrations**: You must create schema manually
3. **No HTTP Server**: Library only, bring your own API
4. **Single-Region**: No built-in multi-region support
5. **No Webhooks Registry**: No centralized webhook discovery
6. **Limited Filtering**: Basic subscription filtering only

### Known Issues

- Integration tests require Docker/PostgreSQL setup
- Circuit breaker state is per-manager (not distributed)
- No automatic cleanup of old delivery attempts
- Event bus is synchronous (can block delivery if slow)

## Contributing

Contributions welcome! This project follows:
- Conventional commits
- 100% test coverage on new code
- go-cuserr for error handling
- No magic strings (use constants)

See [CONTRIBUTING.md](CONTRIBUTING.md) for full guidelines.

## Project Status

**Current Version**: Pre-1.0 (under development)

**What's Working:**
- ✓ Core subscription and delivery management
- ✓ Retry logic with exponential backoff
- ✓ Circuit breaker per endpoint
- ✓ Idempotency with keys
- ✓ Event bus for observability
- ✓ Signature verification
- ✓ Graceful shutdown

**What's Coming:**
- Database migrations
- Distributed circuit breaker (Redis)
- Prometheus metrics integration
- Dead letter queue management UI
- Delivery replay functionality
- Webhook testing tools

## License

MIT License - see [LICENSE](LICENSE) for details.

## Credits

Created by [@itsatony](https://github.com/itsatony)

**Core Dependencies:**
- [go-cuserr](https://github.com/itsatony/go-cuserr) - Structured error handling
- [go-version](https://github.com/itsatony/go-version) - Version management
- [zap](https://github.com/uber-go/zap) - Structured logging
- [nanoid](https://github.com/matoous/go-nanoid) - Unique ID generation
- [lib/pq](https://github.com/lib/pq) - PostgreSQL driver

---

**Documentation:**
- [Implementation Guide](docs/implementation_guide.md) - Architecture and design
- [Code Rules](docs/code_rules.md) - Development standards
- [Production Readiness](PRODUCTION_READINESS_ASSESSMENT.md) - Quality assessment

**Need Help?** Check the `examples/` directory or open an issue.
