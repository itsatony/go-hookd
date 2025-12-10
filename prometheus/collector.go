// Package prometheus provides optional Prometheus metrics integration for go-hookd.
//
// This package implements the EventBus interface from go-hookd, allowing users
// to collect and export webhook delivery metrics to Prometheus.
//
// Usage:
//
//	import (
//	    "github.com/itsatony/go-hookd"
//	    hookdprom "github.com/itsatony/go-hookd/prometheus"
//	    "github.com/prometheus/client_golang/prometheus"
//	)
//
//	func main() {
//	    // Create Prometheus registry (or use default)
//	    registry := prometheus.NewRegistry()
//
//	    // Create metrics collector
//	    collector := hookdprom.NewCollector(registry)
//
//	    // Create go-hookd manager with Prometheus metrics
//	    config := hookd.NewConfig(dbURL)
//	    manager, err := hookd.NewManager(config, repo,
//	        hookd.WithEventBus(collector),
//	    )
//	}
//
// Exported Metrics:
//
//	hookd_deliveries_total          - Counter of total deliveries by status and event_type
//	hookd_delivery_attempts_total   - Counter of delivery attempts by status_code
//	hookd_delivery_duration_seconds - Histogram of delivery duration
//	hookd_circuit_breaker_state     - Gauge of circuit breaker states per endpoint
//	hookd_queue_depth               - Gauge of pending deliveries in queue
//	hookd_dead_letter_queue_size    - Gauge of deliveries in dead letter queue
package prometheus

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Metric names and labels
const (
	namespace = "hookd"

	// Label names
	labelStatus       = "status"
	labelEventType    = "event_type"
	labelEndpoint     = "endpoint"
	labelStatusCode   = "status_code"
	labelTenantID     = "tenant_id"
	labelCircuitState = "state"
	labelSubscription = "subscription_id"
	labelDeliveryType = "delivery_type" // "subscription" or "inline"
)

// Collector implements the hookd EventBus interface and collects Prometheus metrics.
// It subscribes to hookd events and updates appropriate Prometheus metrics.
type Collector struct {
	registry *prometheus.Registry

	// Metrics
	deliveriesTotal       *prometheus.CounterVec
	deliveryAttemptsTotal *prometheus.CounterVec
	deliveryDuration      *prometheus.HistogramVec
	circuitBreakerState   *prometheus.GaugeVec
	queueDepth            prometheus.Gauge
	deadLetterQueueSize   prometheus.Gauge

	// Event subscriptions
	mu          sync.RWMutex
	subscribers map[string][]func(interface{})
}

// CollectorOption configures the Collector.
type CollectorOption func(*Collector)

// NewCollector creates a new Prometheus metrics collector for go-hookd.
// If registry is nil, the default registry is used.
func NewCollector(registry *prometheus.Registry, opts ...CollectorOption) *Collector {
	if registry == nil {
		registry = prometheus.DefaultRegisterer.(*prometheus.Registry)
	}

	c := &Collector{
		registry:    registry,
		subscribers: make(map[string][]func(interface{})),
	}

	// Apply options
	for _, opt := range opts {
		opt(c)
	}

	// Initialize metrics
	c.initMetrics()

	// Register metrics
	c.registerMetrics()

	return c
}

// initMetrics creates all Prometheus metrics.
func (c *Collector) initMetrics() {
	c.deliveriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "deliveries_total",
			Help:      "Total number of webhook deliveries by status and event type",
		},
		[]string{labelStatus, labelEventType, labelDeliveryType},
	)

	c.deliveryAttemptsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "delivery_attempts_total",
			Help:      "Total number of delivery attempts by HTTP status code",
		},
		[]string{labelStatusCode, labelEventType},
	)

	c.deliveryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "delivery_duration_seconds",
			Help:      "Duration of webhook deliveries in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{labelStatus, labelEventType},
	)

	c.circuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_state",
			Help:      "Current state of circuit breakers (0=closed, 1=half-open, 2=open)",
		},
		[]string{labelEndpoint},
	)

	c.queueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "queue_depth",
			Help:      "Number of pending deliveries in the queue",
		},
	)

	c.deadLetterQueueSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "dead_letter_queue_size",
			Help:      "Number of deliveries in the dead letter queue",
		},
	)
}

// registerMetrics registers all metrics with the Prometheus registry.
func (c *Collector) registerMetrics() {
	c.registry.MustRegister(
		c.deliveriesTotal,
		c.deliveryAttemptsTotal,
		c.deliveryDuration,
		c.circuitBreakerState,
		c.queueDepth,
		c.deadLetterQueueSize,
	)
}

// Publish implements the hookd.EventBus interface.
// It receives events from go-hookd and updates Prometheus metrics accordingly.
func (c *Collector) Publish(topic string, data interface{}) {
	// Update metrics based on event type
	c.handleEvent(topic, data)

	// Forward to any registered subscribers
	c.mu.RLock()
	handlers := c.subscribers[topic]
	c.mu.RUnlock()

	for _, handler := range handlers {
		handler(data)
	}
}

// Subscribe implements the hookd.EventBus interface.
// Returns an unsubscribe function.
func (c *Collector) Subscribe(topic string, handler func(interface{})) func() {
	c.mu.Lock()
	c.subscribers[topic] = append(c.subscribers[topic], handler)
	c.mu.Unlock()

	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		handlers := c.subscribers[topic]
		for i, h := range handlers {
			// Compare function pointers (this is a simplification)
			if &h == &handler {
				c.subscribers[topic] = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
	}
}

// handleEvent processes events and updates appropriate metrics.
func (c *Collector) handleEvent(topic string, data interface{}) {
	switch topic {
	case "delivery.queued":
		c.handleDeliveryQueued(data)
	case "delivery.success":
		c.handleDeliverySuccess(data)
	case "delivery.failed":
		c.handleDeliveryFailed(data)
	case "delivery.dead_letter":
		c.handleDeliveryDeadLetter(data)
	case "delivery.started":
		c.handleDeliveryStarted(data)
	case "circuit.opened":
		c.handleCircuitOpened(data)
	case "circuit.closed":
		c.handleCircuitClosed(data)
	case "circuit.half_open":
		c.handleCircuitHalfOpen(data)
	case "metrics.retry_triggered":
		// No special handling needed, delivery.failed already increments
	}
}

// DeliveryEvent represents the data structure for delivery events.
// This mirrors the structure from go-hookd but avoids circular imports.
type DeliveryEvent struct {
	DeliveryID     string
	SubscriptionID string
	TenantID       string
	EventType      string
	Status         string
	AttemptCount   int
	DurationMs     int64
	StatusCode     int
	URL            string // For inline deliveries
}

// CircuitEvent represents the data structure for circuit breaker events.
type CircuitEvent struct {
	Endpoint string
	State    string
}

// handleDeliveryQueued handles delivery.queued events.
func (c *Collector) handleDeliveryQueued(data interface{}) {
	c.queueDepth.Inc()

	if evt, ok := data.(DeliveryEvent); ok {
		deliveryType := "subscription"
		if evt.SubscriptionID == "" {
			deliveryType = "inline"
		}
		c.deliveriesTotal.WithLabelValues("queued", evt.EventType, deliveryType).Inc()
	} else if evt, ok := data.(map[string]interface{}); ok {
		eventType, _ := evt["event_type"].(string)
		subscriptionID, _ := evt["subscription_id"].(string)
		deliveryType := "subscription"
		if subscriptionID == "" {
			deliveryType = "inline"
		}
		c.deliveriesTotal.WithLabelValues("queued", eventType, deliveryType).Inc()
	}
}

// handleDeliveryStarted handles delivery.started events.
func (c *Collector) handleDeliveryStarted(data interface{}) {
	c.queueDepth.Dec()
}

// handleDeliverySuccess handles delivery.success events.
func (c *Collector) handleDeliverySuccess(data interface{}) {
	if evt, ok := data.(DeliveryEvent); ok {
		deliveryType := "subscription"
		if evt.SubscriptionID == "" {
			deliveryType = "inline"
		}
		c.deliveriesTotal.WithLabelValues("success", evt.EventType, deliveryType).Inc()
		c.deliveryAttemptsTotal.WithLabelValues(formatStatusCode(evt.StatusCode), evt.EventType).Inc()
		c.deliveryDuration.WithLabelValues("success", evt.EventType).Observe(float64(evt.DurationMs) / 1000.0)
	} else if evt, ok := data.(map[string]interface{}); ok {
		eventType, _ := evt["event_type"].(string)
		statusCode, _ := evt["status_code"].(int)
		durationMs, _ := evt["duration_ms"].(int64)
		subscriptionID, _ := evt["subscription_id"].(string)
		deliveryType := "subscription"
		if subscriptionID == "" {
			deliveryType = "inline"
		}
		c.deliveriesTotal.WithLabelValues("success", eventType, deliveryType).Inc()
		c.deliveryAttemptsTotal.WithLabelValues(formatStatusCode(statusCode), eventType).Inc()
		c.deliveryDuration.WithLabelValues("success", eventType).Observe(float64(durationMs) / 1000.0)
	}
}

// handleDeliveryFailed handles delivery.failed events.
func (c *Collector) handleDeliveryFailed(data interface{}) {
	if evt, ok := data.(DeliveryEvent); ok {
		deliveryType := "subscription"
		if evt.SubscriptionID == "" {
			deliveryType = "inline"
		}
		c.deliveriesTotal.WithLabelValues("failed", evt.EventType, deliveryType).Inc()
		c.deliveryAttemptsTotal.WithLabelValues(formatStatusCode(evt.StatusCode), evt.EventType).Inc()
		c.deliveryDuration.WithLabelValues("failed", evt.EventType).Observe(float64(evt.DurationMs) / 1000.0)
	} else if evt, ok := data.(map[string]interface{}); ok {
		eventType, _ := evt["event_type"].(string)
		statusCode, _ := evt["status_code"].(int)
		durationMs, _ := evt["duration_ms"].(int64)
		subscriptionID, _ := evt["subscription_id"].(string)
		deliveryType := "subscription"
		if subscriptionID == "" {
			deliveryType = "inline"
		}
		c.deliveriesTotal.WithLabelValues("failed", eventType, deliveryType).Inc()
		c.deliveryAttemptsTotal.WithLabelValues(formatStatusCode(statusCode), eventType).Inc()
		c.deliveryDuration.WithLabelValues("failed", eventType).Observe(float64(durationMs) / 1000.0)
	}
}

// handleDeliveryDeadLetter handles delivery.dead_letter events.
func (c *Collector) handleDeliveryDeadLetter(data interface{}) {
	c.deadLetterQueueSize.Inc()

	if evt, ok := data.(DeliveryEvent); ok {
		deliveryType := "subscription"
		if evt.SubscriptionID == "" {
			deliveryType = "inline"
		}
		c.deliveriesTotal.WithLabelValues("dead_letter", evt.EventType, deliveryType).Inc()
	} else if evt, ok := data.(map[string]interface{}); ok {
		eventType, _ := evt["event_type"].(string)
		subscriptionID, _ := evt["subscription_id"].(string)
		deliveryType := "subscription"
		if subscriptionID == "" {
			deliveryType = "inline"
		}
		c.deliveriesTotal.WithLabelValues("dead_letter", eventType, deliveryType).Inc()
	}
}

// handleCircuitOpened handles circuit.opened events.
func (c *Collector) handleCircuitOpened(data interface{}) {
	if evt, ok := data.(CircuitEvent); ok {
		c.circuitBreakerState.WithLabelValues(evt.Endpoint).Set(2) // 2 = open
	} else if evt, ok := data.(map[string]interface{}); ok {
		endpoint, _ := evt["endpoint"].(string)
		c.circuitBreakerState.WithLabelValues(endpoint).Set(2)
	}
}

// handleCircuitClosed handles circuit.closed events.
func (c *Collector) handleCircuitClosed(data interface{}) {
	if evt, ok := data.(CircuitEvent); ok {
		c.circuitBreakerState.WithLabelValues(evt.Endpoint).Set(0) // 0 = closed
	} else if evt, ok := data.(map[string]interface{}); ok {
		endpoint, _ := evt["endpoint"].(string)
		c.circuitBreakerState.WithLabelValues(endpoint).Set(0)
	}
}

// handleCircuitHalfOpen handles circuit.half_open events.
func (c *Collector) handleCircuitHalfOpen(data interface{}) {
	if evt, ok := data.(CircuitEvent); ok {
		c.circuitBreakerState.WithLabelValues(evt.Endpoint).Set(1) // 1 = half-open
	} else if evt, ok := data.(map[string]interface{}); ok {
		endpoint, _ := evt["endpoint"].(string)
		c.circuitBreakerState.WithLabelValues(endpoint).Set(1)
	}
}

// DecrementDeadLetterQueue decrements the dead letter queue size counter.
// Call this when retrying or purging dead letter deliveries.
func (c *Collector) DecrementDeadLetterQueue(count float64) {
	c.deadLetterQueueSize.Sub(count)
}

// SetQueueDepth sets the queue depth to an absolute value.
// Useful for periodic reconciliation with actual database count.
func (c *Collector) SetQueueDepth(count float64) {
	c.queueDepth.Set(count)
}

// SetDeadLetterQueueSize sets the dead letter queue size to an absolute value.
// Useful for periodic reconciliation with actual database count.
func (c *Collector) SetDeadLetterQueueSize(count float64) {
	c.deadLetterQueueSize.Set(count)
}

// formatStatusCode converts an integer status code to a string label.
func formatStatusCode(code int) string {
	if code == 0 {
		return "error"
	}
	// Group by category
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "other"
	}
}
