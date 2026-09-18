// Package hookd provides a production-ready webhook management system with
// reliable delivery, circuit breakers, and comprehensive retry logic.
//
// # Overview
//
// go-hookd handles the complexity of webhook delivery at scale, including:
//
//   - Reliable delivery with automatic retries and exponential backoff
//   - Circuit breakers for per-endpoint failure detection and recovery
//   - Idempotency keys to prevent duplicate deliveries
//   - Multi-tenancy support with tenant-isolated subscriptions
//   - HMAC-SHA256 payload signing for secure webhook verification
//   - PostgreSQL backend with SKIP LOCKED for concurrent processing
//   - Event-driven architecture with pluggable event bus
//
// # Quick Start
//
// Create a webhook manager and start processing deliveries:
//
//	config := hookd.NewConfig("postgres://localhost:5432/hookd")
//	config.WorkerCount = 10
//
//	repo, err := hookd.NewPostgresRepository(context.Background(), config)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	manager, err := hookd.NewManager(config, repo)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	ctx := context.Background()
//	if err := manager.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer manager.Stop()
//
// # Creating Subscriptions
//
// Subscriptions define which webhooks to deliver and where:
//
//	sub, err := manager.CreateSubscription(ctx, &hookd.CreateSubscriptionRequest{
//	    TenantID:   "tenant_123",
//	    URL:        "https://example.com/webhook",
//	    EventTypes: []string{"user.created", "user.updated"},
//	    Secret:     "webhook_secret",
//	    RetryPolicy: &hookd.RetryPolicy{
//	        MaxAttempts:    5,
//	        InitialBackoff: 2 * time.Second,
//	        MaxBackoff:     2 * time.Minute,
//	        BackoffFactor:  2.0,
//	    },
//	})
//
// # Queueing Deliveries
//
// Queue webhook deliveries for asynchronous processing:
//
//	delivery, err := manager.QueueDelivery(ctx, &hookd.QueueDeliveryRequest{
//	    SubscriptionID: sub.ID,
//	    EventType:      "user.created",
//	    Payload: map[string]any{
//	        "user_id": "123",
//	        "email":   "user@example.com",
//	    },
//	    IdempotencyKey: "evt_user_123_created",
//	})
//
// # Configuration
//
// The Manager can be configured for various deployment scenarios:
//
//	config := hookd.NewConfig("postgres://localhost:5432/hookd")
//
//	// Worker pool configuration
//	config.WorkerCount = 10              // Number of concurrent workers
//	config.QueuePollInterval = 1000      // Queue polling interval (ms)
//	config.MaxBatchSize = 100            // Deliveries per poll
//
//	// Idle polling: while the queue is empty a worker widens its own poll interval
//	// up to the ceiling, so the pool's idle database cost is proportional to traffic
//	// rather than to WorkerCount. Call Manager.Notify after committing a delivery to
//	// keep latency independent of the ceiling.
//	config.QueueIdleMaxInterval = 30000  // Idle poll ceiling (ms); == QueuePollInterval disables
//	config.QueueIdleBackoffFactor = 2.0  // Widening per empty poll; 1.0 disables
//
//	// Retry configuration
//	config.MaxRetries = 3                // Maximum retry attempts
//	config.InitialBackoffMs = 1000       // Initial backoff (ms)
//	config.MaxBackoffMs = 60000          // Maximum backoff (ms)
//	config.BackoffFactor = 2.0           // Exponential factor
//
//	// Circuit breaker configuration
//	config.CircuitBreakerThreshold = 5   // Failures to open circuit
//	config.CircuitBreakerTimeoutMs = 60000   // Time before retry (ms)
//
// # Circuit Breaker
//
// The circuit breaker protects downstream services by detecting failures
// and temporarily stopping delivery attempts:
//
//   - CLOSED: Normal operation, deliveries processed
//   - OPEN: Too many failures, deliveries skipped
//   - HALF-OPEN: Testing if endpoint recovered
//
// Circuit state transitions automatically based on success/failure rates.
//
// # Event System
//
// Subscribe to internal events for monitoring and observability:
//
//	type MyEventBus struct {
//	    subscribers map[string][]func(any)
//	}
//
//	func (b *MyEventBus) Publish(topic string, data any) {
//	    // Send to metrics, logging, alerting systems
//	    for _, handler := range b.subscribers[topic] {
//	        go handler(data)
//	    }
//	}
//
//	// Use custom event bus
//	manager, err := hookd.NewManager(config, repo,
//	    hookd.WithEventBus(&MyEventBus{}),
//	)
//
// Available event topics:
//
//   - delivery.queued, delivery.started, delivery.success, delivery.failed
//   - circuit.opened, circuit.half_open, circuit.closed
//   - metrics.delivery_attempt, metrics.retry_triggered
//   - audit.subscription_created, audit.subscription_updated, audit.subscription_deleted
//
// # Error Handling
//
// All errors are strongly typed using go-cuserr:
//
//	delivery, err := manager.QueueDelivery(ctx, req)
//	if err != nil {
//	    switch e := err.(type) {
//	    case *ValidationError:
//	        // Handle validation error
//	    case *NotFoundError:
//	        // Handle not found error
//	    case *ConflictError:
//	        // Handle conflict (e.g., duplicate idempotency key)
//	    default:
//	        // Handle other errors
//	    }
//	}
//
// # Thread Safety
//
// All Manager methods are thread-safe and can be called concurrently
// from multiple goroutines. The worker pool uses semaphore-based
// concurrency control to limit simultaneous deliveries.
//
// # Graceful Shutdown
//
// The Manager supports graceful shutdown via context cancellation:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	manager.Start(ctx)
//
//	// On shutdown signal
//	cancel()
//	manager.Stop() // Waits for workers to finish
//
// # Performance
//
// Typical benchmarks on modern hardware (16 cores):
//
//   - QueueDelivery: ~35,000 ns/op, 4,200 B/op
//   - GetSubscription: ~10,000 ns/op, 2,000 B/op
//   - ID Generation: ~700 ns/op, 192 B/op
//
// # Testing
//
// Use the mock repository for testing without a database:
//
//	repo := hookd.NewMockRepository()
//	manager, _ := hookd.NewManager(config, repo)
//
//	// Test your webhook logic without database
//
// # Architecture
//
// The package follows clean architecture principles:
//
//   - Manager: Orchestration and business logic
//   - Repository: Data persistence abstraction
//   - Models: Domain entities and value objects
//   - Config: Configuration and validation
//   - Errors: Type-safe error handling
//
// # Security
//
// Webhooks are signed using HMAC-SHA256:
//
//	X-Webhook-Signature: sha256=<hex_encoded_hmac>
//
// Receivers should verify signatures before processing:
//
//	mac := hmac.New(sha256.New, []byte(secret))
//	mac.Write(payload)
//	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
//	valid := hmac.Equal([]byte(signature), []byte(expected))
//
// # Production Deployment
//
// For production use:
//
//   - Run multiple instances for high availability
//   - Use connection pooling (pool size = workers × 2)
//   - Monitor circuit breaker events for endpoint health
//   - Set appropriate retry policies per subscription
//   - Use idempotency keys for at-least-once delivery
//   - Configure worker count based on CPU cores
//
// # Examples
//
// See the examples/ directory for complete working examples:
//
//   - examples/basic/ - Simple webhook delivery
//   - examples/http-server/ - REST API for webhook management
//   - examples/monitoring/ - Metrics and observability
package hookd
