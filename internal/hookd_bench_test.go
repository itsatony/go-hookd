package internal

import (
	"context"
	"testing"
	"time"
)

// =============================================================================
// SUBSCRIPTION BENCHMARKS
// =============================================================================

func BenchmarkCreateSubscription(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := &CreateSubscriptionRequest{
			TenantID:   "tenant_bench",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		}
		manager.CreateSubscription(ctx, req)
	}
}

func BenchmarkGetSubscription(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create a subscription
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.GetSubscription(ctx, sub.ID)
	}
}

func BenchmarkUpdateSubscription(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create a subscription
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	newURL := "https://new-url.com/webhook"
	req := &UpdateSubscriptionRequest{
		URL: &newURL,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.UpdateSubscription(ctx, sub.ID, req)
	}
}

func BenchmarkListSubscriptions(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create 10 subscriptions
	for i := 0; i < 10; i++ {
		manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_bench",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})
	}

	filter := &SubscriptionFilter{
		TenantID: "tenant_bench",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.ListSubscriptions(ctx, filter)
	}
}

// =============================================================================
// DELIVERY BENCHMARKS
// =============================================================================

func BenchmarkQueueDelivery(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create a subscription
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
		})
	}
}

func BenchmarkQueueDeliveryWithIdempotency(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create a subscription
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			IdempotencyKey: "bench_key",
		})
	}
}

func BenchmarkGetDelivery(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create subscription and delivery
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	delivery, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.event",
		Payload:        map[string]interface{}{"test": "data"},
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.GetDelivery(ctx, delivery.ID)
	}
}

func BenchmarkGetDeliveryAttempts(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create subscription and delivery
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	delivery, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.event",
		Payload:        map[string]interface{}{"test": "data"},
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.GetDeliveryAttempts(ctx, delivery.ID)
	}
}

// =============================================================================
// MANAGER BENCHMARKS
// =============================================================================

func BenchmarkManagerInitialization(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewManager(config, repo)
	}
}

func BenchmarkManagerStartStop(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	config.WorkerCount = 2
	repo := NewMockRepository()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager, _ := NewManager(config, repo)
		ctx := context.Background()
		manager.Start(ctx)
		manager.Stop()
	}
}

// =============================================================================
// UTILITY BENCHMARKS
// =============================================================================

func BenchmarkGenerateSubscriptionID(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateSubscriptionID()
	}
}

func BenchmarkGenerateDeliveryID(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateDeliveryID()
	}
}

func BenchmarkCalculateBackoff(b *testing.B) {
	policy := DefaultRetryPolicy()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CalculateBackoff(3, policy)
	}
}

// =============================================================================
// VALIDATION BENCHMARKS
// =============================================================================

func BenchmarkSubscriptionRequestValidation(b *testing.B) {
	req := &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.Validate()
	}
}

func BenchmarkDeliveryRequestValidation(b *testing.B) {
	req := &QueueDeliveryRequest{
		SubscriptionID: "sub_test123",
		EventType:      "test.event",
		Payload:        map[string]interface{}{"test": "data"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.Validate()
	}
}

func BenchmarkRetryPolicyValidation(b *testing.B) {
	policy := &RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     60 * time.Second,
		BackoffFactor:  2.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		policy.Validate()
	}
}

// =============================================================================
// CONCURRENT OPERATIONS BENCHMARKS
// =============================================================================

func BenchmarkConcurrentQueueDelivery(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create a subscription
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			manager.QueueDelivery(ctx, &QueueDeliveryRequest{
				SubscriptionID: sub.ID,
				EventType:      "test.event",
				Payload:        map[string]interface{}{"test": "data"},
				IdempotencyKey: string(rune(i)),
			})
			i++
		}
	})
}

func BenchmarkConcurrentSubscriptionRead(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup: Create a subscription
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			manager.GetSubscription(ctx, sub.ID)
		}
	})
}

// =============================================================================
// MEMORY ALLOCATION BENCHMARKS
// =============================================================================

func BenchmarkMemoryAllocation_CreateSubscription(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_bench",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})
	}
}

func BenchmarkMemoryAllocation_QueueDelivery(b *testing.B) {
	config := NewConfig("postgres://localhost/test")
	repo := NewMockRepository()
	manager, _ := NewManager(config, repo)
	ctx := context.Background()

	// Setup
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bench",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"test.event"},
		Secret:     "test_secret",
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
		})
	}
}
