# go-hookd Examples

This directory contains example applications demonstrating different ways to use go-hookd in your projects.

## Available Examples

### 1. Basic Example (`basic/`)

A simple command-line application showing the core webhook management features.

**Features Demonstrated:**
- Manager initialization and lifecycle
- Creating webhook subscriptions
- Queueing webhook deliveries
- Subscription status management (pause/resume)
- Graceful shutdown handling

**Running the example:**

```bash
cd examples/basic
go run main.go
```

**What it does:**
1. Initializes a Manager with mock repository (no database required)
2. Creates a webhook subscription for multiple event types
3. Queues 3 test deliveries (user.created, user.updated events)
4. Demonstrates subscription pause/resume
5. Lists all subscriptions
6. Runs until interrupted with Ctrl+C

**Output:**
```
=== go-hookd Basic Example ===
Database: postgres://hookd:hookd@localhost:54321/hookd?sslmode=disable

Configuration:
  Workers: 5
  Poll Interval: 1000ms
  Max Retries: 3

Using mock repository (no database required)
✓ Manager started

Creating webhook subscription...
✓ Subscription created: sub_abc123
  URL: https://webhook.site/unique-id
  Event Types: [user.created user.updated user.deleted]

Queueing webhook deliveries...
✓ Delivery 1 queued: dlv_xyz789 (event: user.created)
✓ Delivery 2 queued: dlv_xyz790 (event: user.updated)
✓ Delivery 3 queued: dlv_xyz791 (event: user.created)
```

---

### 2. HTTP Server Example (`http-server/`)

A RESTful HTTP API server exposing all webhook management operations.

**Features Demonstrated:**
- REST API design for webhook management
- HTTP request/response handling
- Error responses with appropriate status codes
- Health check endpoint
- Logging middleware
- Graceful shutdown

**Running the example:**

```bash
cd examples/http-server
go run main.go
```

**Environment Variables:**
- `DATABASE_URL` - PostgreSQL connection string (default: mock repository)
- `PORT` - HTTP server port (default: 8080)

**API Endpoints:**

#### Health Check
```bash
GET /health
```

#### Subscriptions
```bash
# Create subscription
POST /api/v1/subscriptions
Content-Type: application/json

{
  "tenant_id": "tenant_123",
  "url": "https://example.com/webhook",
  "event_types": ["user.created", "user.updated"],
  "secret": "webhook_secret",
  "headers": {
    "X-Custom-Header": "value"
  }
}

# Get subscription
GET /api/v1/subscriptions/{id}

# Update subscription
PUT /api/v1/subscriptions/{id}
Content-Type: application/json

{
  "event_types": ["user.created", "user.updated", "user.deleted"],
  "status": "active"
}

# Delete subscription
DELETE /api/v1/subscriptions/{id}

# List subscriptions
GET /api/v1/subscriptions?tenant_id=tenant_123&status=active

# Pause subscription
POST /api/v1/subscriptions/{id}/pause

# Resume subscription
POST /api/v1/subscriptions/{id}/resume
```

#### Deliveries
```bash
# Queue delivery
POST /api/v1/deliveries
Content-Type: application/json

{
  "subscription_id": "sub_abc123",
  "event_type": "user.created",
  "payload": {
    "user_id": "123",
    "email": "user@example.com"
  },
  "idempotency_key": "evt_user_123_created"
}

# Get delivery
GET /api/v1/deliveries/{id}

# Get delivery attempts
GET /api/v1/deliveries/{id}/attempts

# Retry delivery
POST /api/v1/deliveries/{id}/retry
```

**Example cURL commands:**

```bash
# Health check
curl http://localhost:8080/health

# Create subscription
curl -X POST http://localhost:8080/api/v1/subscriptions \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "tenant_123",
    "url": "https://webhook.site/your-unique-id",
    "event_types": ["user.created"],
    "secret": "your_secret"
  }'

# Queue delivery
curl -X POST http://localhost:8080/api/v1/deliveries \
  -H "Content-Type: application/json" \
  -d '{
    "subscription_id": "sub_abc123",
    "event_type": "user.created",
    "payload": {"user_id": "123", "email": "test@example.com"}
  }'

# List subscriptions
curl http://localhost:8080/api/v1/subscriptions?tenant_id=tenant_123
```

---

### 3. Monitoring Example (`monitoring/`)

Demonstrates metrics collection and observability integration using the event bus system.

**Features Demonstrated:**
- Custom EventBus implementation
- Real-time metrics collection
- Aggregated statistics (success rate, response times)
- Periodic metrics reporting
- Event-driven monitoring
- Error tracking

**Running the example:**

```bash
cd examples/monitoring
go run main.go
```

**What it does:**
1. Creates a custom `ObservabilityEventBus` that collects metrics
2. Initializes Manager with the observability event bus
3. Creates test subscriptions
4. Queues 20 test deliveries
5. Prints comprehensive metrics every 10 seconds
6. Shows final metrics on shutdown (Ctrl+C)

**Metrics Collected:**
- **Deliveries**: queued, succeeded, failed, retried, dead letter, success rate
- **Circuit Breaker**: opened, half-open, closed events
- **Performance**: average response time, sample count
- **Subscriptions**: created, deleted, paused, resumed
- **Errors**: last N errors for debugging

**Sample Output:**
```
============================================================
WEBHOOK DELIVERY METRICS SUMMARY
============================================================

Deliveries:
  Queued:       20
  Succeeded:    15
  Failed:       5
  Retried:      3
  Dead Letter:  2
  Success Rate: 75.00%

Circuit Breaker:
  Opened:     1
  Half-Open:  1
  Closed:     1

Performance:
  Avg Response Time: 145ms
  Sample Count:      15

Subscriptions:
  Created: 2
  Deleted: 0
  Paused:  0
  Resumed: 0

Recent Errors:
  1. timeout: context deadline exceeded
  2. connection refused: dial tcp: connect: connection refused
============================================================
```

**Integration with Your Monitoring System:**

The `ObservabilityEventBus` can be extended to send metrics to:
- Prometheus (using client library)
- StatsD/DogStatsD
- CloudWatch/DataDog
- Custom time-series database
- Logging aggregation (ELK, Splunk)

**Example Prometheus Integration:**

```go
import "github.com/prometheus/client_golang/prometheus"

var (
    deliveriesQueued = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "hookd_deliveries_queued_total",
        Help: "Total number of queued deliveries",
    })
)

func (b *ObservabilityEventBus) Publish(topic string, data interface{}) {
    switch topic {
    case internal.EventTopicDeliveryQueued:
        b.metrics.RecordDeliveryQueued()
        deliveriesQueued.Inc()  // Send to Prometheus
    // ... other cases
    }
}
```

---

## Common Patterns

### Using with PostgreSQL

All examples use a mock repository by default for simplicity. To use with PostgreSQL:

1. Start PostgreSQL:
```bash
docker-compose up -d
```

2. Run migrations:
```bash
psql -h localhost -p 54321 -U hookd -d hookd -f ../../migrations/postgres/000001_create_tables.up.sql
```

3. Update the code to use PostgreSQL repository:
```go
// Replace:
repo := internal.NewMockRepository()

// With:
repo, err := internal.NewPostgresRepository(ctx, config)
if err != nil {
    log.Fatal(err)
}
defer repo.Close()
```

### Custom Event Bus

```go
type MyEventBus struct {
    // Your monitoring backend
}

func (b *MyEventBus) Publish(topic string, data interface{}) {
    // Send to your monitoring system
}

func (b *MyEventBus) Subscribe(topic string, handler func(interface{})) func() {
    // Register handler
}

// Use with Manager
manager, err := internal.NewManager(config, repo,
    internal.WithEventBus(&MyEventBus{}),
)
```

### Custom HTTP Client

```go
httpClient := &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 10,
    },
}

manager, err := internal.NewManager(config, repo,
    internal.WithHTTPClient(httpClient),
)
```

### Structured Logging

```go
import "go.uber.org/zap"

logger, _ := zap.NewProduction()
defer logger.Sync()

manager, err := internal.NewManager(config, repo,
    internal.WithLogger(logger),
)
```

---

## Next Steps

1. **Production Deployment**: See the main [README.md](../README.md) for production deployment guidelines
2. **API Reference**: Check [CONTRIBUTING.md](../CONTRIBUTING.md) for detailed API documentation
3. **Package Docs**: Run `godoc -http=:6060` and visit http://localhost:6060/pkg/github.com/itsatony/go-hookd/internal/

---

## Support

- GitHub Issues: https://github.com/itsatony/go-hookd/issues
- Documentation: See [../README.md](../README.md)
- Contributing: See [../CONTRIBUTING.md](../CONTRIBUTING.md)
