package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/itsatony/go-hookd"
)

// =============================================================================
// FIXTURE BUILDER - Fluent API for Test Data Creation
// =============================================================================

// ScenarioBuilder provides a fluent API for creating complex test scenarios
type ScenarioBuilder struct {
	t    *testing.T
	repo hookd.Repository

	// Created entities
	subscription *hookd.Subscription
	deliveries   []*hookd.Delivery
	attempts     []*hookd.DeliveryAttempt

	// Configuration
	tenantID   string
	url        string
	eventTypes []string
	secret     string
	headers    map[string]string
	metadata   map[string]interface{}
}

// NewScenario creates a new scenario builder
func NewScenario(t *testing.T, repo hookd.Repository) *ScenarioBuilder {
	return &ScenarioBuilder{
		t:          t,
		repo:       repo,
		tenantID:   "test_tenant",
		url:        "https://example.com/webhook",
		eventTypes: []string{"test.event"},
		secret:     "test_secret",
	}
}

// WithTenantID sets the tenant ID
func (b *ScenarioBuilder) WithTenantID(tenantID string) *ScenarioBuilder {
	b.tenantID = tenantID
	return b
}

// WithURL sets the webhook URL
func (b *ScenarioBuilder) WithURL(url string) *ScenarioBuilder {
	b.url = url
	return b
}

// WithEventTypes sets the event types
func (b *ScenarioBuilder) WithEventTypes(eventTypes ...string) *ScenarioBuilder {
	b.eventTypes = eventTypes
	return b
}

// WithSecret sets the webhook secret
func (b *ScenarioBuilder) WithSecret(secret string) *ScenarioBuilder {
	b.secret = secret
	return b
}

// WithHeaders sets custom headers
func (b *ScenarioBuilder) WithHeaders(headers map[string]string) *ScenarioBuilder {
	b.headers = headers
	return b
}

// WithMetadata sets metadata
func (b *ScenarioBuilder) WithMetadata(metadata map[string]interface{}) *ScenarioBuilder {
	b.metadata = metadata
	return b
}

// WithSubscription creates a subscription and returns the builder
func (b *ScenarioBuilder) WithSubscription(tenantID, url string) *ScenarioBuilder {
	b.tenantID = tenantID
	b.url = url
	return b
}

// WithDelivery adds a delivery to the scenario
func (b *ScenarioBuilder) WithDelivery(eventType string, payload map[string]interface{}) *ScenarioBuilder {
	if b.subscription == nil {
		b.t.Fatal("Must create subscription before adding delivery")
	}

	deliveryID, err := hookd.GenerateDeliveryID()
	if err != nil {
		b.t.Fatalf("Failed to generate delivery ID: %v", err)
	}

	delivery := &hookd.Delivery{
		ID:             deliveryID,
		SubscriptionID: b.subscription.ID,
		TenantID:       b.subscription.TenantID,
		EventType:      eventType,
		Payload:        payload,
		Status:         hookd.DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    3,
		CreatedAt:      time.Now(),
	}

	b.deliveries = append(b.deliveries, delivery)
	return b
}

// WithFailedAttempts adds failed delivery attempts to the last delivery
func (b *ScenarioBuilder) WithFailedAttempts(count int) *ScenarioBuilder {
	if len(b.deliveries) == 0 {
		b.t.Fatal("Must add delivery before adding attempts")
	}

	delivery := b.deliveries[len(b.deliveries)-1]

	for i := 0; i < count; i++ {
		attemptID, err := hookd.GenerateAttemptID()
		if err != nil {
			b.t.Fatalf("Failed to generate attempt ID: %v", err)
		}

		attempt := &hookd.DeliveryAttempt{
			ID:            attemptID,
			DeliveryID:    delivery.ID,
			AttemptNumber: i + 1,
			StatusCode:    500,
			Error:         fmt.Sprintf("Test error %d", i+1),
			DurationMs:    100,
			AttemptedAt:   time.Now().Add(-time.Duration(count-i) * time.Minute),
		}
		b.attempts = append(b.attempts, attempt)
	}

	delivery.AttemptCount = count
	delivery.Status = hookd.DeliveryStatusFailed

	return b
}

// WithSuccessfulAttempt adds a successful delivery attempt to the last delivery
func (b *ScenarioBuilder) WithSuccessfulAttempt() *ScenarioBuilder {
	if len(b.deliveries) == 0 {
		b.t.Fatal("Must add delivery before adding attempts")
	}

	delivery := b.deliveries[len(b.deliveries)-1]

	attemptID, err := hookd.GenerateAttemptID()
	if err != nil {
		b.t.Fatalf("Failed to generate attempt ID: %v", err)
	}

	attemptTime := time.Now()
	attempt := &hookd.DeliveryAttempt{
		ID:            attemptID,
		DeliveryID:    delivery.ID,
		AttemptNumber: delivery.AttemptCount + 1,
		StatusCode:    200,
		ResponseBody:  `{"status": "ok"}`,
		DurationMs:    50,
		AttemptedAt:   attemptTime,
	}
	b.attempts = append(b.attempts, attempt)

	delivery.AttemptCount++
	delivery.Status = hookd.DeliveryStatusSuccess
	delivery.CompletedAt = &attemptTime

	return b
}

// Build creates all entities in the repository and returns the scenario
func (b *ScenarioBuilder) Build() *Scenario {
	ctx := context.Background()

	// Create subscription
	subID, err := hookd.GenerateSubscriptionID()
	if err != nil {
		b.t.Fatalf("Failed to generate subscription ID: %v", err)
	}

	sub := &hookd.Subscription{
		ID:         subID,
		TenantID:   b.tenantID,
		URL:        b.url,
		EventTypes: b.eventTypes,
		Secret:     b.secret,
		Headers:    b.headers,
		Metadata:   b.metadata,
		Status:     hookd.SubscriptionStatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := b.repo.CreateSubscription(ctx, sub); err != nil {
		b.t.Fatalf("Failed to create subscription: %v", err)
	}
	b.subscription = sub

	// Create deliveries
	for _, delivery := range b.deliveries {
		delivery.SubscriptionID = sub.ID
		if err := b.repo.CreateDelivery(ctx, delivery); err != nil {
			b.t.Fatalf("Failed to create delivery: %v", err)
		}
	}

	// Create attempts
	for _, attempt := range b.attempts {
		if err := b.repo.CreateDeliveryAttempt(ctx, attempt); err != nil {
			b.t.Fatalf("Failed to create delivery attempt: %v", err)
		}
	}

	return &Scenario{
		Subscription: b.subscription,
		Deliveries:   b.deliveries,
		Attempts:     b.attempts,
	}
}

// Scenario represents a complete test scenario with all created entities
type Scenario struct {
	Subscription *hookd.Subscription
	Deliveries   []*hookd.Delivery
	Attempts     []*hookd.DeliveryAttempt
}

// =============================================================================
// PRE-CONFIGURED SCENARIOS
// =============================================================================

// StandardRetryScenario creates a scenario with a failed delivery that needs retry
func StandardRetryScenario(t *testing.T, repo hookd.Repository) *Scenario {
	return NewScenario(t, repo).
		WithSubscription("tenant_retry", "https://example.com/retry").
		WithDelivery("order.created", map[string]interface{}{
			"order_id": "order_123",
			"amount":   99.99,
		}).
		WithFailedAttempts(2).
		Build()
}

// CircuitBreakerScenario creates a scenario with multiple failed deliveries
// that should trigger a circuit breaker
func CircuitBreakerScenario(t *testing.T, repo hookd.Repository) *Scenario {
	builder := NewScenario(t, repo).
		WithSubscription("tenant_cb", "https://example.com/flaky")

	// Add 5 failed deliveries to trigger circuit breaker
	for i := 0; i < 5; i++ {
		builder.WithDelivery("user.created", map[string]interface{}{
			"user_id": fmt.Sprintf("user_%d", i),
		}).WithFailedAttempts(3)
	}

	return builder.Build()
}

// DeadLetterQueueScenario creates a delivery that exhausted all retries
func DeadLetterQueueScenario(t *testing.T, repo hookd.Repository) *Scenario {
	return NewScenario(t, repo).
		WithSubscription("tenant_dlq", "https://example.com/dead").
		WithDelivery("payment.failed", map[string]interface{}{
			"payment_id": "pay_123",
		}).
		WithFailedAttempts(5).
		Build()
}

// SuccessfulDeliveryScenario creates a completed successful delivery
func SuccessfulDeliveryScenario(t *testing.T, repo hookd.Repository) *Scenario {
	return NewScenario(t, repo).
		WithSubscription("tenant_success", "https://example.com/success").
		WithDelivery("user.updated", map[string]interface{}{
			"user_id": "user_456",
		}).
		WithSuccessfulAttempt().
		Build()
}

// MultiTenantScenario creates subscriptions and deliveries for multiple tenants
func MultiTenantScenario(t *testing.T, repo hookd.Repository, tenantCount int) []*Scenario {
	scenarios := make([]*Scenario, tenantCount)

	for i := 0; i < tenantCount; i++ {
		scenarios[i] = NewScenario(t, repo).
			WithTenantID(fmt.Sprintf("tenant_%d", i)).
			WithURL(fmt.Sprintf("https://example.com/tenant%d", i)).
			WithDelivery("test.event", map[string]interface{}{
				"tenant": i,
				"data":   "test",
			}).
			Build()
	}

	return scenarios
}

// =============================================================================
// SUBSCRIPTION BUILDER
// =============================================================================

// SubscriptionBuilder provides a fluent API for creating subscriptions
type SubscriptionBuilder struct {
	t    *testing.T
	repo hookd.Repository

	tenantID    string
	url         string
	eventTypes  []string
	secret      string
	status      string
	headers     map[string]string
	metadata    map[string]interface{}
	retryPolicy *hookd.RetryPolicy
}

// NewSubscription creates a new subscription builder with defaults
func NewSubscription(t *testing.T, repo hookd.Repository) *SubscriptionBuilder {
	return &SubscriptionBuilder{
		t:          t,
		repo:       repo,
		tenantID:   "test_tenant",
		url:        "https://example.com/webhook",
		eventTypes: []string{"test.event"},
		secret:     "test_secret",
		status:     hookd.SubscriptionStatusActive,
	}
}

// WithTenantID sets the tenant ID
func (b *SubscriptionBuilder) WithTenantID(tenantID string) *SubscriptionBuilder {
	b.tenantID = tenantID
	return b
}

// WithURL sets the webhook URL
func (b *SubscriptionBuilder) WithURL(url string) *SubscriptionBuilder {
	b.url = url
	return b
}

// WithEventTypes sets the event types
func (b *SubscriptionBuilder) WithEventTypes(eventTypes ...string) *SubscriptionBuilder {
	b.eventTypes = eventTypes
	return b
}

// WithSecret sets the secret
func (b *SubscriptionBuilder) WithSecret(secret string) *SubscriptionBuilder {
	b.secret = secret
	return b
}

// WithStatus sets the status
func (b *SubscriptionBuilder) WithStatus(status string) *SubscriptionBuilder {
	b.status = status
	return b
}

// WithHeaders sets custom headers
func (b *SubscriptionBuilder) WithHeaders(headers map[string]string) *SubscriptionBuilder {
	b.headers = headers
	return b
}

// WithMetadata sets metadata
func (b *SubscriptionBuilder) WithMetadata(metadata map[string]interface{}) *SubscriptionBuilder {
	b.metadata = metadata
	return b
}

// WithRetryPolicy sets a custom retry policy
func (b *SubscriptionBuilder) WithRetryPolicy(policy *hookd.RetryPolicy) *SubscriptionBuilder {
	b.retryPolicy = policy
	return b
}

// Create creates the subscription in the repository
func (b *SubscriptionBuilder) Create() *hookd.Subscription {
	subID, err := hookd.GenerateSubscriptionID()
	if err != nil {
		b.t.Fatalf("Failed to generate subscription ID: %v", err)
	}

	sub := &hookd.Subscription{
		ID:          subID,
		TenantID:    b.tenantID,
		URL:         b.url,
		EventTypes:  b.eventTypes,
		Secret:      b.secret,
		Status:      b.status,
		Headers:     b.headers,
		Metadata:    b.metadata,
		RetryPolicy: b.retryPolicy,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	ctx := context.Background()
	if err := b.repo.CreateSubscription(ctx, sub); err != nil {
		b.t.Fatalf("Failed to create subscription: %v", err)
	}

	return sub
}

// =============================================================================
// DELIVERY BUILDER
// =============================================================================

// DeliveryBuilder provides a fluent API for creating deliveries
type DeliveryBuilder struct {
	t    *testing.T
	repo hookd.Repository

	subscriptionID string
	tenantID       string
	eventType      string
	payload        map[string]interface{}
	status         string
	attemptCount   int
	maxAttempts    int
	idempotencyKey string
}

// NewDelivery creates a new delivery builder
func NewDelivery(t *testing.T, repo hookd.Repository, subscriptionID string) *DeliveryBuilder {
	return &DeliveryBuilder{
		t:              t,
		repo:           repo,
		subscriptionID: subscriptionID,
		tenantID:       "test_tenant",
		eventType:      "test.event",
		payload:        map[string]interface{}{"test": "data"},
		status:         hookd.DeliveryStatusPending,
		maxAttempts:    3,
	}
}

// WithEventType sets the event type
func (b *DeliveryBuilder) WithEventType(eventType string) *DeliveryBuilder {
	b.eventType = eventType
	return b
}

// WithPayload sets the payload
func (b *DeliveryBuilder) WithPayload(payload map[string]interface{}) *DeliveryBuilder {
	b.payload = payload
	return b
}

// WithStatus sets the status
func (b *DeliveryBuilder) WithStatus(status string) *DeliveryBuilder {
	b.status = status
	return b
}

// WithAttemptCount sets the attempt count
func (b *DeliveryBuilder) WithAttemptCount(count int) *DeliveryBuilder {
	b.attemptCount = count
	return b
}

// WithMaxAttempts sets the max attempts
func (b *DeliveryBuilder) WithMaxAttempts(max int) *DeliveryBuilder {
	b.maxAttempts = max
	return b
}

// WithIdempotencyKey sets the idempotency key
func (b *DeliveryBuilder) WithIdempotencyKey(key string) *DeliveryBuilder {
	b.idempotencyKey = key
	return b
}

// Create creates the delivery in the repository
func (b *DeliveryBuilder) Create() *hookd.Delivery {
	deliveryID, err := hookd.GenerateDeliveryID()
	if err != nil {
		b.t.Fatalf("Failed to generate delivery ID: %v", err)
	}

	delivery := &hookd.Delivery{
		ID:             deliveryID,
		SubscriptionID: b.subscriptionID,
		TenantID:       b.tenantID,
		EventType:      b.eventType,
		Payload:        b.payload,
		Status:         b.status,
		AttemptCount:   b.attemptCount,
		MaxAttempts:    b.maxAttempts,
		CreatedAt:      time.Now(),
	}

	ctx := context.Background()
	if err := b.repo.CreateDelivery(ctx, delivery); err != nil {
		b.t.Fatalf("Failed to create delivery: %v", err)
	}

	return delivery
}
