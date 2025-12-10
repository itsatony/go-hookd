package prometheus

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCollector(t *testing.T) {
	t.Run("creates collector with custom registry", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		require.NotNil(t, collector)
		assert.NotNil(t, collector.deliveriesTotal)
		assert.NotNil(t, collector.deliveryAttemptsTotal)
		assert.NotNil(t, collector.deliveryDuration)
		assert.NotNil(t, collector.circuitBreakerState)
		assert.NotNil(t, collector.queueDepth)
		assert.NotNil(t, collector.deadLetterQueueSize)
	})

	t.Run("creates collector with nil registry uses default", func(t *testing.T) {
		// This test uses the default registry, which may have metrics from other tests
		// Just verify it doesn't panic
		collector := NewCollector(nil)
		require.NotNil(t, collector)
	})
}

func TestCollector_Publish(t *testing.T) {
	t.Run("handles delivery.queued event", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		// Publish event
		collector.Publish("delivery.queued", DeliveryEvent{
			DeliveryID:     "dlv_test",
			SubscriptionID: "sub_test",
			EventType:      "order.created",
		})

		// Verify metrics
		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_queue_depth" {
				found = true
				assert.Equal(t, float64(1), m.GetMetric()[0].GetGauge().GetValue())
			}
		}
		assert.True(t, found, "Should have queue_depth metric")
	})

	t.Run("handles delivery.success event", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		collector.Publish("delivery.success", DeliveryEvent{
			DeliveryID:     "dlv_test",
			SubscriptionID: "sub_test",
			EventType:      "order.created",
			StatusCode:     200,
			DurationMs:     150,
		})

		metrics, err := registry.Gather()
		require.NoError(t, err)

		var foundDeliveries, foundAttempts, foundDuration bool
		for _, m := range metrics {
			switch m.GetName() {
			case "hookd_deliveries_total":
				foundDeliveries = true
				assert.Greater(t, len(m.GetMetric()), 0)
			case "hookd_delivery_attempts_total":
				foundAttempts = true
			case "hookd_delivery_duration_seconds":
				foundDuration = true
			}
		}
		assert.True(t, foundDeliveries, "Should have deliveries_total metric")
		assert.True(t, foundAttempts, "Should have delivery_attempts_total metric")
		assert.True(t, foundDuration, "Should have delivery_duration_seconds metric")
	})

	t.Run("handles delivery.failed event", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		collector.Publish("delivery.failed", DeliveryEvent{
			DeliveryID:     "dlv_test",
			SubscriptionID: "sub_test",
			EventType:      "order.created",
			StatusCode:     500,
			DurationMs:     100,
		})

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_deliveries_total" {
				found = true
			}
		}
		assert.True(t, found, "Should have deliveries_total metric")
	})

	t.Run("handles delivery.dead_letter event", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		collector.Publish("delivery.dead_letter", DeliveryEvent{
			DeliveryID:     "dlv_test",
			SubscriptionID: "sub_test",
			EventType:      "order.created",
		})

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_dead_letter_queue_size" {
				found = true
				assert.Equal(t, float64(1), m.GetMetric()[0].GetGauge().GetValue())
			}
		}
		assert.True(t, found, "Should have dead_letter_queue_size metric")
	})

	t.Run("handles circuit.opened event", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		collector.Publish("circuit.opened", CircuitEvent{
			Endpoint: "https://example.com/webhook",
			State:    "open",
		})

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_circuit_breaker_state" {
				found = true
				assert.Equal(t, float64(2), m.GetMetric()[0].GetGauge().GetValue())
			}
		}
		assert.True(t, found, "Should have circuit_breaker_state metric")
	})

	t.Run("handles circuit.closed event", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		// First open
		collector.Publish("circuit.opened", CircuitEvent{
			Endpoint: "https://example.com/webhook",
			State:    "open",
		})

		// Then close
		collector.Publish("circuit.closed", CircuitEvent{
			Endpoint: "https://example.com/webhook",
			State:    "closed",
		})

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_circuit_breaker_state" {
				found = true
				assert.Equal(t, float64(0), m.GetMetric()[0].GetGauge().GetValue())
			}
		}
		assert.True(t, found, "Should have circuit_breaker_state metric")
	})

	t.Run("handles circuit.half_open event", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		collector.Publish("circuit.half_open", CircuitEvent{
			Endpoint: "https://example.com/webhook",
			State:    "half_open",
		})

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_circuit_breaker_state" {
				found = true
				assert.Equal(t, float64(1), m.GetMetric()[0].GetGauge().GetValue())
			}
		}
		assert.True(t, found, "Should have circuit_breaker_state metric")
	})

	t.Run("handles map[string]interface{} event data", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		// Simulate how go-hookd publishes events internally
		collector.Publish("delivery.queued", map[string]interface{}{
			"delivery_id":     "dlv_test",
			"subscription_id": "sub_test",
			"event_type":      "order.created",
		})

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_queue_depth" {
				found = true
			}
		}
		assert.True(t, found, "Should handle map data format")
	})

	t.Run("handles inline delivery events", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		// Inline delivery has empty SubscriptionID
		collector.Publish("delivery.queued", DeliveryEvent{
			DeliveryID: "dlv_test",
			EventType:  "job.completed",
			URL:        "https://example.com/callback",
		})

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_deliveries_total" {
				found = true
				// Should have delivery_type="inline" label
				for _, metric := range m.GetMetric() {
					for _, label := range metric.GetLabel() {
						if label.GetName() == "delivery_type" {
							assert.Equal(t, "inline", label.GetValue())
						}
					}
				}
			}
		}
		assert.True(t, found, "Should track inline deliveries")
	})
}

func TestCollector_Subscribe(t *testing.T) {
	t.Run("forwards events to subscribers", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		received := false
		unsubscribe := collector.Subscribe("delivery.success", func(data interface{}) {
			received = true
		})

		collector.Publish("delivery.success", DeliveryEvent{
			DeliveryID: "dlv_test",
			EventType:  "test.event",
		})

		assert.True(t, received, "Subscriber should receive event")

		// Test unsubscribe
		unsubscribe()
	})

	t.Run("supports multiple subscribers", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		count := 0
		collector.Subscribe("test.topic", func(data interface{}) {
			count++
		})
		collector.Subscribe("test.topic", func(data interface{}) {
			count++
		})

		collector.Publish("test.topic", "data")

		assert.Equal(t, 2, count, "Both subscribers should receive event")
	})
}

func TestCollector_ManualUpdates(t *testing.T) {
	t.Run("SetQueueDepth updates gauge", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		collector.SetQueueDepth(42)

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_queue_depth" {
				found = true
				assert.Equal(t, float64(42), m.GetMetric()[0].GetGauge().GetValue())
			}
		}
		assert.True(t, found)
	})

	t.Run("SetDeadLetterQueueSize updates gauge", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		collector.SetDeadLetterQueueSize(15)

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_dead_letter_queue_size" {
				found = true
				assert.Equal(t, float64(15), m.GetMetric()[0].GetGauge().GetValue())
			}
		}
		assert.True(t, found)
	})

	t.Run("DecrementDeadLetterQueue decreases counter", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		collector := NewCollector(registry)

		// First add some
		collector.SetDeadLetterQueueSize(10)

		// Then remove
		collector.DecrementDeadLetterQueue(3)

		metrics, err := registry.Gather()
		require.NoError(t, err)

		found := false
		for _, m := range metrics {
			if m.GetName() == "hookd_dead_letter_queue_size" {
				found = true
				assert.Equal(t, float64(7), m.GetMetric()[0].GetGauge().GetValue())
			}
		}
		assert.True(t, found)
	})
}

func TestFormatStatusCode(t *testing.T) {
	tests := []struct {
		code     int
		expected string
	}{
		{0, "error"},
		{200, "2xx"},
		{201, "2xx"},
		{301, "3xx"},
		{400, "4xx"},
		{404, "4xx"},
		{500, "5xx"},
		{503, "5xx"},
		{600, "other"},
		{-1, "other"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatStatusCode(tt.code)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCollector_DeliveryStarted(t *testing.T) {
	registry := prometheus.NewRegistry()
	collector := NewCollector(registry)

	// Queue 3 deliveries
	for i := 0; i < 3; i++ {
		collector.Publish("delivery.queued", DeliveryEvent{
			DeliveryID: "dlv_test",
			EventType:  "test.event",
		})
	}

	// Start 2 deliveries
	collector.Publish("delivery.started", nil)
	collector.Publish("delivery.started", nil)

	metrics, err := registry.Gather()
	require.NoError(t, err)

	found := false
	for _, m := range metrics {
		if m.GetName() == "hookd_queue_depth" {
			found = true
			assert.Equal(t, float64(1), m.GetMetric()[0].GetGauge().GetValue())
		}
	}
	assert.True(t, found, "Queue depth should be 1 (3 queued - 2 started)")
}
