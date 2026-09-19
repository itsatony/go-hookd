# go-hookd

A webhook delivery management library for Go applications. Handles webhook subscriptions, reliable delivery with retries, circuit breakers, and idempotency.

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Test Coverage](https://img.shields.io/badge/coverage-60%25-yellow.svg)](https://github.com/itsatony/go-hookd)

> **Status**: Production Ready (v0.7.2)
> Core functionality is implemented, tested, and production-ready. The API is stable with comprehensive test coverage.
>
> **v0.7.x**: The worker pool now backs off while the delivery queue is empty, so its
> idle database cost is proportional to traffic rather than to `WorkerCount`. Tune with
> `Config.QueueIdleMaxInterval` / `QueueIdleBackoffFactor`, and call `Manager.Notify()`
> after committing a delivery to keep latency independent of the ceiling.
> See [Idle polling and Notify](#idle-polling-and-notify).

> **v0.6.0 Breaking Change**: Database tables now use configurable prefixes: `{prefix}_hookd_{table}` (e.g., `myapp_hookd_subscriptions`). The prefix is **required** via `WithTablePrefix("myapp")`. Schema is managed via `SchemaManager.EnsureSchema(ctx)` instead of migrations.

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
- **Wildcard Event Types**: Pattern matching for event subscriptions (`*`, `job.*`)
- **Metadata Filtering**: Filter deliveries based on event metadata fields
- **Inline Deliveries**: One-off webhooks without pre-created subscriptions
- **Dead Letter Queue Management**: List, retry, and purge failed deliveries
- **Multi-Service Isolation**: Configurable table prefixes for database sharing
- **Schema Manager**: Idempotent schema setup without incremental migrations
- **Prometheus Metrics**: Optional `prometheus/` subpackage for metrics collection
- **Signature Verification Package**: `verify/` subpackage with replay protection

### Current Limitations

- **PostgreSQL Only**: No support for other databases yet
- **No Built-in API**: You implement HTTP handlers for your use case
- **No Admin UI**: Library-only, bring your own dashboard
- **Single-Region Circuit Breaker**: Per-manager state (not distributed)

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
	// Optional prefix for API consistency with PostgresRepository
	repo := hookd.NewMockRepository("test")

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
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/itsatony/go-hookd"
	_ "github.com/lib/pq" // PostgreSQL driver
)

func main() {
	// PostgreSQL connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable required")
	}

	// Choose a unique prefix for your service
	prefix := os.Getenv("HOOKD_PREFIX")
	if prefix == "" {
		prefix = "myservice" // Default prefix
	}

	// Step 1: Create schema config with your service prefix
	schemaConfig, err := hookd.NewSchemaConfig(prefix)
	if err != nil {
		log.Fatalf("Invalid prefix: %v", err)
	}

	// Step 2: Ensure database schema exists
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	schemaMgr := hookd.NewSchemaManager(db, schemaConfig)
	ctx := context.Background()
	if err := schemaMgr.EnsureSchema(ctx); err != nil {
		log.Fatalf("Failed to setup schema: %v", err)
	}

	// Step 3: Create repository with the same prefix
	repo, err := hookd.NewPostgresRepository(dbURL, hookd.WithTablePrefix(prefix))
	if err != nil {
		log.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Step 4: Configure and create manager
	config := hookd.NewConfig(dbURL)
	config.WorkerCount = 10
	config.QueuePollInterval = 1000
	config.DefaultMaxRetries = 3

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

## Multi-Service Deployments

Multiple services can share the same PostgreSQL database using different table prefixes. Each service uses its own prefix to ensure complete data isolation:

```go
// Service A: "orders" service
schemaConfigA, _ := hookd.NewSchemaConfig("orders")
schemaMgr := hookd.NewSchemaManager(db, schemaConfigA)
schemaMgr.EnsureSchema(ctx) // Creates: orders_hookd_subscriptions, etc.
repoA, _ := hookd.NewPostgresRepository(dbURL, hookd.WithTablePrefix("orders"))

// Service B: "payments" service (same database, different prefix)
schemaConfigB, _ := hookd.NewSchemaConfig("payments")
hookd.NewSchemaManager(db, schemaConfigB).EnsureSchema(ctx)
repoB, _ := hookd.NewPostgresRepository(dbURL, hookd.WithTablePrefix("payments"))
```

**Key Points:**
- **Prefix Required**: `NewPostgresRepository()` requires `WithTablePrefix()` - there's no default
- **Validation**: Prefix must be lowercase alphanumeric + underscore, max 32 characters
- **Idempotent**: `EnsureSchema()` is safe to call multiple times (creates only if missing)
- **Schema Version**: On version mismatch, schema is dropped and recreated (no data migration)

## Configuration

### Config Options Explained

```go
config := hookd.NewConfig(databaseURL)

// Worker Configuration
config.WorkerCount = 10                    // Number of concurrent delivery workers
config.QueuePollInterval = 1000            // How often to check for pending deliveries (ms)
config.MaxBatchSize = 100                  // Max deliveries to fetch per poll

// Idle Polling (see "Idle polling and Notify")
config.QueueIdleMaxInterval = 30000        // Poll interval ceiling while the queue is empty (ms)
config.QueueIdleBackoffFactor = 2.0        // Widen the interval by this much per empty poll (1.0 = off)

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

### Idle polling and Notify

The pool's steady-state query rate against the deliveries table is
`WorkerCount / QueuePollInterval`, and it is paid whether or not anything is ever
enqueued. At the defaults that is **10 queries per second, forever** — which is what a
production deployment of this library was measured doing against a queue that had been
empty for its entire life, accounting for 81 % of all statements on that database.

Since v0.7.0 a worker widens its own poll interval after each poll that finds nothing,
by `QueueIdleBackoffFactor`, up to `QueueIdleMaxInterval`. At the defaults an idle pool
settles at `WorkerCount / 30s` — a 30x reduction — and the interval snaps back to
`QueuePollInterval` the instant a poll returns work, so **backoff never slows a queue
that has work in it**. Wake-ups carry ±20 % jitter, because every worker is started in
the same loop and would otherwise poll in lockstep.

The cost would be first-delivery latency — a delivery enqueued into an idle pool waiting
up to `QueueIdleMaxInterval` to be noticed — so **every Manager entry point that leaves a
delivery pending wakes the pool itself**: `QueueDelivery`, `QueueInlineDelivery`,
`QueueDeliveries` and `RetryDelivery`. Consumers using the Manager API need no change and
lose no latency.

`Manager.Notify()` is exported for the one case the library cannot see: a caller that
writes to the deliveries table through its own repository handle or through
`RepositoryTx`, outside the Manager API. It never blocks and is safe before `Start` and
after `Stop`.

```go
if err := tx.CreateDelivery(ctx, delivery); err != nil {
    return err
}
// after the transaction commits:
manager.Notify() // poll now instead of on the next scheduled tick
```

To opt out entirely, set `QueueIdleMaxInterval` equal to `QueuePollInterval`, or
`QueueIdleBackoffFactor` to `1.0`.

⚠ Both fields default when left at their zero value, so a `Config` built as a struct
literal by code that predates them keeps working — an upgrade does not become a boot
failure. An explicitly set out-of-range value is still refused.

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

### Inline Deliveries (Without Subscriptions)

For one-off webhooks without creating a subscription:

```go
// Queue an inline delivery (no subscription required)
delivery, err := manager.QueueInlineDelivery(ctx, &hookd.QueueInlineDeliveryRequest{
	URL:            "https://example.com/callback",
	Secret:         "webhook_secret",        // For HMAC signature
	TenantID:       "tenant_123",
	EventType:      "job.completed",
	Payload: map[string]any{
		"job_id": "job_456",
		"status": "success",
	},
	MaxRetries:     3,                       // Optional, uses defaults if not specified
	IdempotencyKey: "job_456_completed",     // Optional, prevents duplicates
})
```

### Dead Letter Queue Management

Manage failed deliveries that exceeded retry limits:

```go
// List dead letter deliveries
deadLetters, err := manager.ListDeadLetters(ctx, &hookd.DeadLetterFilter{
	TenantID:  "tenant_123",
	EventType: "order.created",
	Limit:     100,
})

// Retry a single dead letter delivery
retried, err := manager.RetryDeadLetter(ctx, deliveryID)

// Bulk retry dead letters
count, err := manager.RetryDeadLetters(ctx, &hookd.DeadLetterFilter{
	TenantID:  "tenant_123",
	EventType: "order.created",
})
fmt.Printf("Retried %d dead letter deliveries\n", count)

// Purge old dead letters
purged, err := manager.PurgeDeadLetters(ctx, &hookd.PurgeFilter{
	TenantID:  "tenant_123",
	OlderThan: time.Now().AddDate(0, 0, -30), // 30 days old
})
fmt.Printf("Purged %d dead letter deliveries\n", purged)
```

### Wildcard Event Types

Subscribe to multiple event types with patterns:

```go
sub, err := manager.CreateSubscription(ctx, &hookd.CreateSubscriptionRequest{
	TenantID:   "tenant_123",
	URL:        "https://api.example.com/webhook",
	EventTypes: []string{
		"user.*",           // Matches user.created, user.updated, user.deleted
		"order.completed",  // Exact match
		"*",                // Matches everything (use carefully!)
	},
	Secret: "your_secret",
})
```

### Metadata Filtering

Filter deliveries based on event metadata:

```go
// Create subscription with metadata filters
sub, err := manager.CreateSubscription(ctx, &hookd.CreateSubscriptionRequest{
	TenantID:   "tenant_123",
	URL:        "https://api.example.com/webhook",
	EventTypes: []string{"job.*"},
	Secret:     "your_secret",
	Filters: map[string]string{
		"corpus_id": "corpus_abc",   // Only receive events for this corpus
		"status":    "failed",       // Only receive failed job events
	},
})

// Queue delivery with metadata
delivery, err := manager.QueueDelivery(ctx, &hookd.QueueDeliveryRequest{
	SubscriptionID: subID,
	EventType:      "job.completed",
	Payload:        payload,
	Metadata: map[string]any{       // Must match subscription filters
		"corpus_id": "corpus_abc",
		"status":    "failed",
	},
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

### Prometheus Metrics Integration

Use the optional `prometheus/` subpackage for production metrics:

```go
import (
	"github.com/itsatony/go-hookd"
	hookdprom "github.com/itsatony/go-hookd/prometheus"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	// Create Prometheus registry
	registry := prometheus.NewRegistry()

	// Create metrics collector (implements EventBus interface)
	collector := hookdprom.NewCollector(registry)

	// Create manager with Prometheus metrics
	config := hookd.NewConfig(dbURL)
	manager, err := hookd.NewManager(config, repo,
		hookd.WithEventBus(collector),
	)
}
```

**Exported Metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `hookd_deliveries_total` | Counter | Total deliveries by status, event_type, delivery_type |
| `hookd_delivery_attempts_total` | Counter | Delivery attempts by status_code, event_type |
| `hookd_delivery_duration_seconds` | Histogram | Delivery duration by status, event_type |
| `hookd_circuit_breaker_state` | Gauge | Circuit breaker state per endpoint (0=closed, 1=half-open, 2=open) |
| `hookd_queue_depth` | Gauge | Number of pending deliveries |
| `hookd_dead_letter_queue_size` | Gauge | Number of dead letter deliveries |

## Webhook Signature Verification

go-hookd signs all webhook payloads with HMAC-SHA256. Recipients should verify signatures.

### Using the verify Package (Recommended)

```go
import "github.com/itsatony/go-hookd/verify"

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	valid, err := verify.Signature(verify.SignatureParams{
		Secret:    "your_webhook_secret",
		Signature: r.Header.Get("X-Webhook-Signature"),
		Timestamp: r.Header.Get("X-Webhook-Timestamp"),
		Payload:   body,
		MaxAge:    5 * time.Minute, // Reject signatures older than 5 minutes
	})

	if err != nil || !valid {
		http.Error(w, "Invalid signature", http.StatusUnauthorized)
		return
	}

	// Process webhook...
	w.WriteHeader(http.StatusOK)
}
```

### Manual Verification

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

### Using SchemaManager

go-hookd uses SchemaManager to create and manage database schemas. Each service uses a unique prefix for complete data isolation:

```go
import (
	"database/sql"
	"github.com/itsatony/go-hookd"
)

// Step 1: Create schema config with your service prefix
schemaConfig, err := hookd.NewSchemaConfig("myservice")
if err != nil {
	log.Fatalf("Invalid prefix: %v", err)
}

// Step 2: Create schema manager and ensure schema exists
db, _ := sql.Open("postgres", dbURL)
schemaMgr := hookd.NewSchemaManager(db, schemaConfig)

if err := schemaMgr.EnsureSchema(ctx); err != nil {
	log.Fatalf("Schema setup failed: %v", err)
}
// Creates: myservice_hookd_subscriptions, myservice_hookd_deliveries, etc.

// Check schema info
info, _ := schemaMgr.GetSchemaInfo(ctx)
fmt.Printf("Schema exists: %v, Version: %s\n", info.Exists, info.Version)

// Drop schema (for testing/cleanup)
err = schemaMgr.DropSchema(ctx)
```

### Table Naming Convention

All tables use configurable prefixes: `{prefix}_hookd_{table}`

**Note:** The prefix is **required** - there's no default. Prefixes must be lowercase alphanumeric + underscore, max 32 characters.

### Example Schema Structure

```sql
-- Example with prefix "myservice"
CREATE TABLE myservice_hookd_subscriptions (
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
CREATE TABLE hookd_deliveries (
    id VARCHAR(50) PRIMARY KEY,
    subscription_id VARCHAR(50) REFERENCES hookd_subscriptions(id),
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
CREATE TABLE hookd_delivery_attempts (
    id VARCHAR(50) PRIMARY KEY,
    delivery_id VARCHAR(50) REFERENCES hookd_deliveries(id),
    attempt_number INTEGER NOT NULL,
    response_status_code INTEGER,
    response_body TEXT,
    response_duration INTEGER,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Circuit Breaker State
CREATE TABLE hookd_circuit_breaker_state (
    endpoint VARCHAR(500) PRIMARY KEY,
    state VARCHAR(20) NOT NULL DEFAULT 'closed',
    failure_count INTEGER NOT NULL DEFAULT 0,
    last_failure_at TIMESTAMP,
    opened_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Idempotency Store
CREATE TABLE hookd_idempotency_store (
    idempotency_key VARCHAR(200) NOT NULL,
    subscription_id VARCHAR(50) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (idempotency_key, subscription_id)
);

-- Indexes for performance (all prefixed with idx_hookd_)
CREATE INDEX idx_hookd_deliveries_status ON hookd_deliveries(status);
CREATE INDEX idx_hookd_deliveries_pending ON hookd_deliveries(next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_hookd_deliveries_subscription ON hookd_deliveries(subscription_id);
CREATE INDEX idx_hookd_deliveries_tenant ON hookd_deliveries(tenant_id);
CREATE INDEX idx_hookd_idempotency_expires ON hookd_idempotency_store(expires_at);
```

**Note:** This schema is a guideline. See `migrations/postgres/000001_baseline.up.sql` for the complete schema with all indexes and constraints.

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
2. **No HTTP Server**: Library only, bring your own API
3. **Single-Region**: No built-in multi-region support
4. **No Webhooks Registry**: No centralized webhook discovery

### Known Issues

- Integration tests require Docker/PostgreSQL setup
- Circuit breaker state is per-manager (not distributed)
- No automatic cleanup of old delivery attempts
- Event bus handlers should be non-blocking

## Contributing

Contributions welcome! This project follows:
- Conventional commits
- 100% test coverage on new code
- go-cuserr for error handling
- No magic strings (use constants)

See [CONTRIBUTING.md](CONTRIBUTING.md) for full guidelines.

## Project Status

**Current Version**: v0.7.2 (Production Ready)

**What's Implemented:**
- ✓ Core subscription and delivery management
- ✓ Retry logic with exponential backoff
- ✓ Circuit breaker per endpoint
- ✓ Idempotency with keys
- ✓ Event bus for observability
- ✓ Signature verification with `verify/` package
- ✓ Graceful shutdown
- ✓ Wildcard event type subscriptions
- ✓ Metadata filtering for deliveries
- ✓ Inline deliveries (without subscriptions)
- ✓ Dead letter queue management API
- ✓ PostgreSQL migration helper
- ✓ Prometheus metrics (`prometheus/` package)
- ✓ Subscription testing endpoint

**What's Coming:**
- Distributed circuit breaker (Redis)
- Admin UI dashboard
- Delivery replay functionality
- OpenTelemetry tracing integration

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
- [prometheus/client_golang](https://github.com/prometheus/client_golang) - Metrics (optional)

---

**Documentation:**
- [Implementation Guide](docs/implementation_guide.md) - Architecture and design
- [Code Rules](docs/code_rules.md) - Development standards

**Need Help?** Check the `examples/` directory or open an issue.
