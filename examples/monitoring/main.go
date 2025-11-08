package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/itsatony/go-hookd/internal"
)

// =============================================================================
// METRICS COLLECTOR
// =============================================================================

// MetricsCollector collects and aggregates webhook delivery metrics
type MetricsCollector struct {
	mu sync.RWMutex

	// Delivery metrics
	deliveriesQueued     int64
	deliveriesSucceeded  int64
	deliveriesFailed     int64
	deliveriesRetried    int64
	deliveriesDeadLetter int64

	// Circuit breaker metrics
	circuitsOpened   int64
	circuitsHalfOpen int64
	circuitsClosed   int64

	// Response time tracking
	responseTimes   []time.Duration
	avgResponseTime time.Duration

	// Subscription metrics
	subscriptionsCreated int64
	subscriptionsDeleted int64
	subscriptionsPaused  int64
	subscriptionsResumed int64

	// Error tracking
	lastErrors []string
	maxErrors  int
}

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		responseTimes: make([]time.Duration, 0, 100),
		lastErrors:    make([]string, 0, 10),
		maxErrors:     10,
	}
}

// RecordDeliveryQueued increments the queued deliveries counter
func (m *MetricsCollector) RecordDeliveryQueued() {
	atomic.AddInt64(&m.deliveriesQueued, 1)
}

// RecordDeliverySuccess increments the succeeded deliveries counter
func (m *MetricsCollector) RecordDeliverySuccess(duration time.Duration) {
	atomic.AddInt64(&m.deliveriesSucceeded, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	m.responseTimes = append(m.responseTimes, duration)
	if len(m.responseTimes) > 100 {
		m.responseTimes = m.responseTimes[1:]
	}

	// Calculate average
	var total time.Duration
	for _, d := range m.responseTimes {
		total += d
	}
	if len(m.responseTimes) > 0 {
		m.avgResponseTime = total / time.Duration(len(m.responseTimes))
	}
}

// RecordDeliveryFailure increments the failed deliveries counter
func (m *MetricsCollector) RecordDeliveryFailure(err string) {
	atomic.AddInt64(&m.deliveriesFailed, 1)
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastErrors = append(m.lastErrors, err)
	if len(m.lastErrors) > m.maxErrors {
		m.lastErrors = m.lastErrors[1:]
	}
}

// RecordDeliveryRetry increments the retried deliveries counter
func (m *MetricsCollector) RecordDeliveryRetry() {
	atomic.AddInt64(&m.deliveriesRetried, 1)
}

// RecordDeliveryDeadLetter increments the dead letter counter
func (m *MetricsCollector) RecordDeliveryDeadLetter() {
	atomic.AddInt64(&m.deliveriesDeadLetter, 1)
}

// RecordCircuitOpened increments the circuits opened counter
func (m *MetricsCollector) RecordCircuitOpened() {
	atomic.AddInt64(&m.circuitsOpened, 1)
}

// RecordCircuitHalfOpen increments the circuits half-open counter
func (m *MetricsCollector) RecordCircuitHalfOpen() {
	atomic.AddInt64(&m.circuitsHalfOpen, 1)
}

// RecordCircuitClosed increments the circuits closed counter
func (m *MetricsCollector) RecordCircuitClosed() {
	atomic.AddInt64(&m.circuitsClosed, 1)
}

// RecordSubscriptionCreated increments the subscriptions created counter
func (m *MetricsCollector) RecordSubscriptionCreated() {
	atomic.AddInt64(&m.subscriptionsCreated, 1)
}

// RecordSubscriptionDeleted increments the subscriptions deleted counter
func (m *MetricsCollector) RecordSubscriptionDeleted() {
	atomic.AddInt64(&m.subscriptionsDeleted, 1)
}

// RecordSubscriptionPaused increments the subscriptions paused counter
func (m *MetricsCollector) RecordSubscriptionPaused() {
	atomic.AddInt64(&m.subscriptionsPaused, 1)
}

// RecordSubscriptionResumed increments the subscriptions resumed counter
func (m *MetricsCollector) RecordSubscriptionResumed() {
	atomic.AddInt64(&m.subscriptionsResumed, 1)
}

// GetMetrics returns a snapshot of current metrics
func (m *MetricsCollector) GetMetrics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]interface{}{
		"deliveries": map[string]interface{}{
			"queued":       atomic.LoadInt64(&m.deliveriesQueued),
			"succeeded":    atomic.LoadInt64(&m.deliveriesSucceeded),
			"failed":       atomic.LoadInt64(&m.deliveriesFailed),
			"retried":      atomic.LoadInt64(&m.deliveriesRetried),
			"dead_letter":  atomic.LoadInt64(&m.deliveriesDeadLetter),
			"success_rate": m.calculateSuccessRate(),
		},
		"circuit_breaker": map[string]interface{}{
			"opened":    atomic.LoadInt64(&m.circuitsOpened),
			"half_open": atomic.LoadInt64(&m.circuitsHalfOpen),
			"closed":    atomic.LoadInt64(&m.circuitsClosed),
		},
		"performance": map[string]interface{}{
			"avg_response_time_ms": m.avgResponseTime.Milliseconds(),
			"sample_count":         len(m.responseTimes),
		},
		"subscriptions": map[string]interface{}{
			"created": atomic.LoadInt64(&m.subscriptionsCreated),
			"deleted": atomic.LoadInt64(&m.subscriptionsDeleted),
			"paused":  atomic.LoadInt64(&m.subscriptionsPaused),
			"resumed": atomic.LoadInt64(&m.subscriptionsResumed),
		},
		"errors": map[string]interface{}{
			"last_errors": m.lastErrors,
		},
	}
}

func (m *MetricsCollector) calculateSuccessRate() float64 {
	succeeded := atomic.LoadInt64(&m.deliveriesSucceeded)
	failed := atomic.LoadInt64(&m.deliveriesFailed)
	total := succeeded + failed

	if total == 0 {
		return 0
	}

	return float64(succeeded) / float64(total) * 100
}

// PrintSummary prints a formatted metrics summary
func (m *MetricsCollector) PrintSummary() {
	metrics := m.GetMetrics()

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("WEBHOOK DELIVERY METRICS SUMMARY")
	fmt.Println(strings.Repeat("=", 60))

	// Deliveries
	deliveries := metrics["deliveries"].(map[string]interface{})
	fmt.Println("\nDeliveries:")
	fmt.Printf("  Queued:       %d\n", deliveries["queued"])
	fmt.Printf("  Succeeded:    %d\n", deliveries["succeeded"])
	fmt.Printf("  Failed:       %d\n", deliveries["failed"])
	fmt.Printf("  Retried:      %d\n", deliveries["retried"])
	fmt.Printf("  Dead Letter:  %d\n", deliveries["dead_letter"])
	fmt.Printf("  Success Rate: %.2f%%\n", deliveries["success_rate"])

	// Circuit Breaker
	circuit := metrics["circuit_breaker"].(map[string]interface{})
	fmt.Println("\nCircuit Breaker:")
	fmt.Printf("  Opened:     %d\n", circuit["opened"])
	fmt.Printf("  Half-Open:  %d\n", circuit["half_open"])
	fmt.Printf("  Closed:     %d\n", circuit["closed"])

	// Performance
	perf := metrics["performance"].(map[string]interface{})
	fmt.Println("\nPerformance:")
	fmt.Printf("  Avg Response Time: %dms\n", perf["avg_response_time_ms"])
	fmt.Printf("  Sample Count:      %d\n", perf["sample_count"])

	// Subscriptions
	subs := metrics["subscriptions"].(map[string]interface{})
	fmt.Println("\nSubscriptions:")
	fmt.Printf("  Created: %d\n", subs["created"])
	fmt.Printf("  Deleted: %d\n", subs["deleted"])
	fmt.Printf("  Paused:  %d\n", subs["paused"])
	fmt.Printf("  Resumed: %d\n", subs["resumed"])

	// Recent Errors
	errors := metrics["errors"].(map[string]interface{})
	lastErrors := errors["last_errors"].([]string)
	if len(lastErrors) > 0 {
		fmt.Println("\nRecent Errors:")
		for i, err := range lastErrors {
			fmt.Printf("  %d. %s\n", i+1, err)
		}
	}

	fmt.Println(strings.Repeat("=", 60) + "\n")
}

// =============================================================================
// OBSERVABILITY EVENT BUS
// =============================================================================

// ObservabilityEventBus implements internal.EventBus with metrics collection
type ObservabilityEventBus struct {
	metrics     *MetricsCollector
	subscribers map[string][]func(interface{})
	mu          sync.RWMutex
}

func NewObservabilityEventBus(metrics *MetricsCollector) *ObservabilityEventBus {
	return &ObservabilityEventBus{
		metrics:     metrics,
		subscribers: make(map[string][]func(interface{})),
	}
}

func (b *ObservabilityEventBus) Publish(topic string, data interface{}) {
	// Record metrics based on event topic
	switch topic {
	case internal.EventTopicDeliveryQueued:
		b.metrics.RecordDeliveryQueued()
	case internal.EventTopicDeliverySuccess:
		if evt, ok := data.(map[string]interface{}); ok {
			if duration, ok := evt["duration"].(time.Duration); ok {
				b.metrics.RecordDeliverySuccess(duration)
			}
		}
	case internal.EventTopicDeliveryFailed:
		if evt, ok := data.(map[string]interface{}); ok {
			if err, ok := evt["error"].(string); ok {
				b.metrics.RecordDeliveryFailure(err)
			}
		}
	case internal.EventTopicMetricsRetryTriggered:
		b.metrics.RecordDeliveryRetry()
	case internal.EventTopicDeliveryDeadLetter:
		b.metrics.RecordDeliveryDeadLetter()
	case internal.EventTopicCircuitOpened:
		b.metrics.RecordCircuitOpened()
	case internal.EventTopicCircuitHalfOpen:
		b.metrics.RecordCircuitHalfOpen()
	case internal.EventTopicCircuitClosed:
		b.metrics.RecordCircuitClosed()
	case internal.EventTopicAuditSubscriptionCreated:
		b.metrics.RecordSubscriptionCreated()
	case internal.EventTopicAuditSubscriptionDeleted:
		b.metrics.RecordSubscriptionDeleted()
	}

	// Notify subscribers
	b.mu.RLock()
	handlers := b.subscribers[topic]
	b.mu.RUnlock()

	for _, handler := range handlers {
		go handler(data)
	}
}

func (b *ObservabilityEventBus) Subscribe(topic string, handler func(interface{})) func() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers[topic] = append(b.subscribers[topic], handler)

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		handlers := b.subscribers[topic]
		for i, h := range handlers {
			if &h == &handler {
				b.subscribers[topic] = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
	}
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// StartMetricsReporter periodically prints metrics summary
func StartMetricsReporter(metrics *MetricsCollector, interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			metrics.PrintSummary()
		case <-stop:
			return
		}
	}
}

// =============================================================================
// MAIN
// =============================================================================

func main() {
	// Database URL from environment or default
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://hookd:hookd@localhost:54321/hookd?sslmode=disable"
	}

	fmt.Println("=== go-hookd Monitoring Example ===")
	fmt.Printf("Database: %s\n\n", dbURL)

	// Create metrics collector
	metrics := NewMetricsCollector()

	// Create observability event bus
	eventBus := NewObservabilityEventBus(metrics)

	// Subscribe to delivery events for logging
	eventBus.Subscribe(internal.EventTopicDeliveryQueued, func(data interface{}) {
		log.Printf("[EVENT] Delivery queued: %+v", data)
	})

	eventBus.Subscribe(internal.EventTopicDeliverySuccess, func(data interface{}) {
		log.Printf("[EVENT] Delivery succeeded: %+v", data)
	})

	eventBus.Subscribe(internal.EventTopicDeliveryFailed, func(data interface{}) {
		log.Printf("[EVENT] Delivery failed: %+v", data)
	})

	eventBus.Subscribe(internal.EventTopicCircuitOpened, func(data interface{}) {
		log.Printf("[EVENT] Circuit opened: %+v", data)
	})

	// Create configuration
	config := internal.NewConfig(dbURL)
	config.WorkerCount = 5
	config.QueuePollInterval = 1000
	config.DefaultMaxRetries = 3
	config.DefaultInitialBackoffMs = 1000
	config.DefaultMaxBackoffMs = 30000
	config.DefaultBackoffFactor = 2.0

	fmt.Println("Using mock repository (no database required)")
	repo := internal.NewMockRepository()

	// Initialize manager with observability event bus
	manager, err := internal.NewManager(config, repo, internal.WithEventBus(eventBus))
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	// Start manager in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		log.Fatalf("Failed to start manager: %v", err)
	}
	fmt.Println("✓ Manager started with observability")
	fmt.Println()

	// Start metrics reporter
	stopMetrics := make(chan struct{})
	go StartMetricsReporter(metrics, 10*time.Second, stopMetrics)

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nShutting down gracefully...")
		close(stopMetrics)
		cancel()
		if err := manager.Stop(); err != nil {
			log.Printf("Error stopping manager: %v", err)
		}

		// Print final metrics
		fmt.Println("\n=== FINAL METRICS ===")
		metrics.PrintSummary()
		os.Exit(0)
	}()

	// Create test subscriptions and deliveries
	fmt.Println("Creating test subscriptions...")
	sub1, _ := manager.CreateSubscription(ctx, &internal.CreateSubscriptionRequest{
		TenantID:   "tenant_metrics",
		URL:        "https://webhook.site/test-endpoint-1",
		EventTypes: []string{"user.created", "user.updated"},
		Secret:     "test_secret_1",
	})
	fmt.Printf("✓ Subscription created: %s\n", sub1.ID)

	sub2, _ := manager.CreateSubscription(ctx, &internal.CreateSubscriptionRequest{
		TenantID:   "tenant_metrics",
		URL:        "https://webhook.site/test-endpoint-2",
		EventTypes: []string{"order.created"},
		Secret:     "test_secret_2",
	})
	fmt.Printf("✓ Subscription created: %s\n\n", sub2.ID)

	// Queue test deliveries
	fmt.Println("Queueing test deliveries...")
	for i := 0; i < 20; i++ {
		subID := sub1.ID
		eventType := "user.created"
		if i%2 == 0 {
			subID = sub2.ID
			eventType = "order.created"
		}

		delivery, err := manager.QueueDelivery(ctx, &internal.QueueDeliveryRequest{
			SubscriptionID: subID,
			EventType:      eventType,
			Payload: map[string]interface{}{
				"id":        fmt.Sprintf("evt_%d", i+1),
				"timestamp": time.Now().Format(time.RFC3339),
			},
			IdempotencyKey: fmt.Sprintf("metrics_test_%d", i+1),
		})
		if err != nil {
			log.Printf("Failed to queue delivery %d: %v", i+1, err)
			continue
		}
		fmt.Printf("✓ Delivery %d queued: %s\n", i+1, delivery.ID)

		// Stagger deliveries
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Println("\n✓ All test deliveries queued")
	fmt.Println("\nMetrics will be printed every 10 seconds.")
	fmt.Println("Press Ctrl+C to see final metrics and exit.")

	// Keep running
	select {}
}
