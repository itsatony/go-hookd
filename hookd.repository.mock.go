// Package internal provides the core webhook management implementation for go-hookd.
//
// This file implements an in-memory mock Repository for testing and development.
// All data is stored in memory and is thread-safe using sync.RWMutex.
package hookd

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/itsatony/go-cuserr"
)

// MockRepository implements the Repository interface using in-memory storage.
//
// Thread Safety: MockRepository is safe for concurrent use by multiple goroutines.
// All operations are protected by a sync.RWMutex.
//
// Note: This implementation is intended for testing and development only.
// Data is not persisted and will be lost when the process exits.
type MockRepository struct {
	schemaConfig                  *SchemaConfig // Optional: for API consistency with PostgresRepository
	injectError                   error
	lockedDeliveries              map[string]bool
	deliveries                    map[string]*Delivery
	deliveryAttempts              map[string][]*DeliveryAttempt
	idempotencyKeys               map[string]idempotencyEntry
	circuitBreakerState           map[string]*CircuitBreakerState
	subscriptions                 map[string]*Subscription
	mu                            sync.RWMutex
	injectErrorOnCreate           bool
	injectErrorOnUpdate           bool
	injectErrorOnClose            bool
	injectErrorOnIdempotencyCheck bool
	injectErrorOnCreateDelivery   bool
	injectErrorOnStoreIdempotency bool
}

// idempotencyEntry stores idempotency key data.
type idempotencyEntry struct {
	ExpiresAt      time.Time
	CreatedAt      time.Time
	Key            string
	SubscriptionID string
}

// NewMockRepository creates a new in-memory mock repository.
// The prefix parameter is optional; if empty, "mock" is used as the default prefix.
// This parameter exists for API consistency with PostgresRepository.
func NewMockRepository(prefix ...string) *MockRepository {
	// Use default prefix if not provided
	p := "mock"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}

	// Create schema config (ignore errors for mock since prefix is controlled)
	schemaConfig, _ := NewSchemaConfig(p)

	return &MockRepository{
		schemaConfig:        schemaConfig,
		subscriptions:       make(map[string]*Subscription),
		deliveries:          make(map[string]*Delivery),
		deliveryAttempts:    make(map[string][]*DeliveryAttempt),
		idempotencyKeys:     make(map[string]idempotencyEntry),
		circuitBreakerState: make(map[string]*CircuitBreakerState),
		lockedDeliveries:    make(map[string]bool),
	}
}

// SchemaConfig returns the schema configuration for this repository.
// This method provides API consistency with PostgresRepository.
func (r *MockRepository) SchemaConfig() *SchemaConfig {
	return r.schemaConfig
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// copySubscription creates a deep copy of a subscription to prevent external modifications.
func copySubscription(sub *Subscription) *Subscription {
	if sub == nil {
		return nil
	}

	copied := &Subscription{
		ID:         sub.ID,
		TenantID:   sub.TenantID,
		URL:        sub.URL,
		Secret:     sub.Secret,
		EventTypes: make([]string, len(sub.EventTypes)),
		Status:     sub.Status,
		CreatedAt:  sub.CreatedAt,
		UpdatedAt:  sub.UpdatedAt,
	}

	copy(copied.EventTypes, sub.EventTypes)

	if sub.RetryPolicy != nil {
		copied.RetryPolicy = &RetryPolicy{
			MaxAttempts:    sub.RetryPolicy.MaxAttempts,
			InitialBackoff: sub.RetryPolicy.InitialBackoff,
			MaxBackoff:     sub.RetryPolicy.MaxBackoff,
			BackoffFactor:  sub.RetryPolicy.BackoffFactor,
		}
	}

	if sub.Headers != nil {
		copied.Headers = make(map[string]string)
		for k, v := range sub.Headers {
			copied.Headers[k] = v
		}
	}

	if sub.Metadata != nil {
		copied.Metadata = make(map[string]any)
		for k, v := range sub.Metadata {
			copied.Metadata[k] = v
		}
	}

	if sub.Filters != nil {
		copied.Filters = make(map[string]string)
		for k, v := range sub.Filters {
			copied.Filters[k] = v
		}
	}

	return copied
}

// copyDelivery creates a deep copy of a delivery to prevent external modifications.
func copyDelivery(dlv *Delivery) *Delivery {
	if dlv == nil {
		return nil
	}

	copied := &Delivery{
		ID:             dlv.ID,
		SubscriptionID: dlv.SubscriptionID,
		TenantID:       dlv.TenantID,
		EventType:      dlv.EventType,
		Status:         dlv.Status,
		AttemptCount:   dlv.AttemptCount,
		MaxAttempts:    dlv.MaxAttempts,
		CreatedAt:      dlv.CreatedAt,
		// Inline delivery fields
		URL:            dlv.URL,
		Secret:         dlv.Secret,
		IdempotencyKey: dlv.IdempotencyKey,
	}

	if dlv.Payload != nil {
		copied.Payload = make(map[string]any)
		for k, v := range dlv.Payload {
			copied.Payload[k] = v
		}
	}

	if dlv.NextRetryAt != nil {
		t := *dlv.NextRetryAt
		copied.NextRetryAt = &t
	}

	if dlv.CompletedAt != nil {
		t := *dlv.CompletedAt
		copied.CompletedAt = &t
	}

	return copied
}

// copyDeliveryAttempt creates a deep copy of a delivery attempt.
func copyDeliveryAttempt(att *DeliveryAttempt) *DeliveryAttempt {
	if att == nil {
		return nil
	}

	copied := &DeliveryAttempt{
		ID:            att.ID,
		DeliveryID:    att.DeliveryID,
		AttemptNumber: att.AttemptNumber,
		StatusCode:    att.StatusCode,
		ResponseBody:  att.ResponseBody,
		Error:         att.Error,
		DurationMs:    att.DurationMs,
		AttemptedAt:   att.AttemptedAt,
	}

	if att.ResponseHeaders != nil {
		copied.ResponseHeaders = make(map[string]string)
		for k, v := range att.ResponseHeaders {
			copied.ResponseHeaders[k] = v
		}
	}

	return copied
}

// copyCircuitBreakerState creates a deep copy of circuit breaker state.
func copyCircuitBreakerState(state *CircuitBreakerState) *CircuitBreakerState {
	if state == nil {
		return nil
	}

	return &CircuitBreakerState{
		Endpoint:     state.Endpoint,
		State:        state.State,
		FailureCount: state.FailureCount,
		SuccessCount: state.SuccessCount,
		LastFailure:  state.LastFailure,
		OpenedAt:     state.OpenedAt,
		NextRetryAt:  state.NextRetryAt,
	}
}

// matchesEventTypes checks if any of the provided event types match the subscription's event types.
func matchesEventTypes(subEventTypes []string, filterEventTypes []string) bool {
	for _, filter := range filterEventTypes {
		for _, subType := range subEventTypes {
			if subType == filter {
				return true
			}
		}
	}
	return false
}

// =============================================================================
// SUBSCRIPTION OPERATIONS
// =============================================================================

// CreateSubscription creates a new subscription in memory.
func (r *MockRepository) CreateSubscription(ctx context.Context, sub *Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Error injection for testing
	if r.injectErrorOnCreate {
		return cuserr.NewInternalError("mock_repository", nil, cuserr.WithMetadata("injected", "true"))
	}

	// Check for duplicate tenant_id + url
	for _, existing := range r.subscriptions {
		if existing.TenantID == sub.TenantID && existing.URL == sub.URL {
			return ErrDuplicateSubscription
		}
	}

	r.subscriptions[sub.ID] = copySubscription(sub)
	return nil
}

// GetSubscription retrieves a subscription by its ID.
func (r *MockRepository) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sub, exists := r.subscriptions[id]
	if !exists {
		return nil, ErrSubscriptionNotFound
	}

	return copySubscription(sub), nil
}

// GetSubscriptionByTenantAndURL retrieves a subscription by tenant ID and URL.
func (r *MockRepository) GetSubscriptionByTenantAndURL(ctx context.Context, tenantID, url string) (*Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, sub := range r.subscriptions {
		if sub.TenantID == tenantID && sub.URL == url {
			return copySubscription(sub), nil
		}
	}

	return nil, ErrSubscriptionNotFound
}

// UpdateSubscription updates an existing subscription.
func (r *MockRepository) UpdateSubscription(ctx context.Context, sub *Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.subscriptions[sub.ID]; !exists {
		return ErrSubscriptionNotFound
	}

	// Update timestamp
	sub.UpdatedAt = time.Now()
	r.subscriptions[sub.ID] = copySubscription(sub)
	return nil
}

// DeleteSubscription deletes a subscription and cascades to related deliveries.
func (r *MockRepository) DeleteSubscription(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.subscriptions[id]; !exists {
		return ErrSubscriptionNotFound
	}

	delete(r.subscriptions, id)

	// Cascade delete: remove all deliveries for this subscription
	for deliveryID, delivery := range r.deliveries {
		if delivery.SubscriptionID == id {
			delete(r.deliveries, deliveryID)
			delete(r.deliveryAttempts, deliveryID)
		}
	}

	// Cascade delete: remove idempotency keys for this subscription
	for key, entry := range r.idempotencyKeys {
		if entry.SubscriptionID == id {
			delete(r.idempotencyKeys, key)
		}
	}

	return nil
}

// ListSubscriptions retrieves subscriptions matching the given filter.
func (r *MockRepository) ListSubscriptions(ctx context.Context, filter *SubscriptionFilter) ([]*Subscription, error) {
	// Mirror the real repositories' fail-closed tenant scope (v0.10.0).
	if err := filter.requireTenantScope(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := []*Subscription{}

	for _, sub := range r.subscriptions {
		// Apply filters
		if filter.TenantID != "" && sub.TenantID != filter.TenantID {
			continue
		}

		if filter.Status != "" && sub.Status != filter.Status {
			continue
		}

		if len(filter.EventTypes) > 0 && !matchesEventTypes(sub.EventTypes, filter.EventTypes) {
			continue
		}

		results = append(results, copySubscription(sub))
	}

	// Sort by created_at descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	// Apply offset
	if filter.Offset > 0 {
		if filter.Offset >= len(results) {
			return []*Subscription{}, nil
		}
		results = results[filter.Offset:]
	}

	// Apply limit
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

// =============================================================================
// DELIVERY OPERATIONS
// =============================================================================

// CreateDelivery creates a new delivery in memory.
func (r *MockRepository) CreateDelivery(ctx context.Context, delivery *Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Error injection for testing
	if r.injectErrorOnCreateDelivery {
		return cuserr.NewInternalError("mock_repository", nil, cuserr.WithMetadata("injected", "true"))
	}

	r.deliveries[delivery.ID] = copyDelivery(delivery)
	return nil
}

// GetDelivery retrieves a delivery by its ID.
func (r *MockRepository) GetDelivery(ctx context.Context, id string) (*Delivery, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	delivery, exists := r.deliveries[id]
	if !exists {
		return nil, ErrDeliveryNotFound
	}

	return copyDelivery(delivery), nil
}

// UpdateDelivery updates an existing delivery.
func (r *MockRepository) UpdateDelivery(ctx context.Context, delivery *Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.deliveries[delivery.ID]; !exists {
		return ErrDeliveryNotFound
	}

	r.deliveries[delivery.ID] = copyDelivery(delivery)

	// Unlock delivery after update (simulates transaction commit releasing row lock)
	delete(r.lockedDeliveries, delivery.ID)

	return nil
}

// DeleteDelivery permanently deletes a delivery and its attempts.
func (r *MockRepository) DeleteDelivery(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.deliveries[id]; !exists {
		return ErrDeliveryNotFound
	}

	// Delete the delivery
	delete(r.deliveries, id)

	// Delete associated attempts
	delete(r.deliveryAttempts, id)

	// Clean up any locks
	delete(r.lockedDeliveries, id)

	return nil
}

// UnlockDelivery unlocks a delivery (for testing purposes).
// This is not part of the Repository interface but useful for testing.
func (r *MockRepository) UnlockDelivery(deliveryID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.lockedDeliveries, deliveryID)
}

// UnlockAllDeliveries unlocks all deliveries (for testing purposes).
func (r *MockRepository) UnlockAllDeliveries() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lockedDeliveries = make(map[string]bool)
}

// ListDeliveries retrieves deliveries matching the given filter.
func (r *MockRepository) ListDeliveries(ctx context.Context, filter *DeliveryFilter) ([]*Delivery, error) {
	// Mirror the real repositories' fail-closed tenant scope (v0.10.0).
	if err := filter.requireTenantScope(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Error injection for testing
	if r.injectError != nil {
		return nil, r.injectError
	}

	results := []*Delivery{}

	// Collect matching deliveries
	for _, delivery := range r.deliveries {
		// Apply filters
		if filter.TenantID != "" && delivery.TenantID != filter.TenantID {
			continue
		}

		if filter.SubscriptionID != nil && *filter.SubscriptionID != "" &&
			delivery.SubscriptionID != *filter.SubscriptionID {
			continue
		}

		if filter.Status != nil && *filter.Status != "" &&
			delivery.Status != *filter.Status {
			continue
		}

		if filter.EventType != nil && *filter.EventType != "" &&
			delivery.EventType != *filter.EventType {
			continue
		}

		results = append(results, copyDelivery(delivery))
	}

	// Sort by created_at descending (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	// Apply offset
	if filter.Offset > 0 && filter.Offset < len(results) {
		results = results[filter.Offset:]
	} else if filter.Offset >= len(results) {
		results = []*Delivery{}
	}

	// Apply limit
	if filter.Limit > 0 && filter.Limit < len(results) {
		results = results[:filter.Limit]
	}

	return results, nil
}

// MoveToDeadLetter moves a delivery to the dead letter queue.
func (r *MockRepository) MoveToDeadLetter(ctx context.Context, deliveryID string, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delivery, exists := r.deliveries[deliveryID]
	if !exists {
		return ErrDeliveryNotFound
	}

	delivery.Status = DeliveryStatusDeadLetter
	now := time.Now()
	delivery.CompletedAt = &now

	// Unlock if locked
	delete(r.lockedDeliveries, deliveryID)

	return nil
}

// =============================================================================
// DELIVERY ATTEMPT OPERATIONS
// =============================================================================

// CreateDeliveryAttempt records a delivery attempt in memory.
func (r *MockRepository) CreateDeliveryAttempt(ctx context.Context, attempt *DeliveryAttempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate attempt number
	if attempts, exists := r.deliveryAttempts[attempt.DeliveryID]; exists {
		for _, existing := range attempts {
			if existing.AttemptNumber == attempt.AttemptNumber {
				return cuserr.NewConflictError("delivery_attempt", "attempt_number", fmt.Sprintf("attempt %d already exists for delivery %s", attempt.AttemptNumber, attempt.DeliveryID))
			}
		}
	}

	r.deliveryAttempts[attempt.DeliveryID] = append(
		r.deliveryAttempts[attempt.DeliveryID],
		copyDeliveryAttempt(attempt),
	)

	// Update delivery's attempt count (mirrors PostgreSQL trigger behavior)
	if delivery, exists := r.deliveries[attempt.DeliveryID]; exists {
		delivery.AttemptCount = len(r.deliveryAttempts[attempt.DeliveryID])
	}

	return nil
}

// GetDeliveryAttempts retrieves all attempts for a delivery.
func (r *MockRepository) GetDeliveryAttempts(ctx context.Context, deliveryID string) ([]*DeliveryAttempt, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Error injection for testing
	if r.injectError != nil {
		return nil, r.injectError
	}

	attempts := r.deliveryAttempts[deliveryID]
	if attempts == nil {
		return []*DeliveryAttempt{}, nil
	}

	// Copy and sort by attempt number
	results := make([]*DeliveryAttempt, len(attempts))
	for i, att := range attempts {
		results[i] = copyDeliveryAttempt(att)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].AttemptNumber < results[j].AttemptNumber
	})

	return results, nil
}

// =============================================================================
// IDEMPOTENCY OPERATIONS
// =============================================================================

// CheckIdempotency checks if an idempotency key exists and is not expired.
func (r *MockRepository) CheckIdempotency(ctx context.Context, key string, subscriptionID string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Error injection for testing
	if r.injectErrorOnIdempotencyCheck {
		return false, cuserr.NewInternalError("mock_repository", nil, cuserr.WithMetadata("injected", "true"))
	}

	entryKey := fmt.Sprintf("%s:%s", key, subscriptionID)
	entry, exists := r.idempotencyKeys[entryKey]

	if !exists {
		return false, nil
	}

	// Check if expired
	if entry.ExpiresAt.Before(time.Now()) {
		return false, nil
	}

	return true, nil
}

// StoreIdempotencyKey stores an idempotency key with an expiration time.
func (r *MockRepository) StoreIdempotencyKey(ctx context.Context, key string, subscriptionID string, expiresAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Error injection for testing
	if r.injectErrorOnStoreIdempotency {
		return cuserr.NewInternalError("mock_repository", nil, cuserr.WithMetadata("injected", "true"))
	}

	entryKey := fmt.Sprintf("%s:%s", key, subscriptionID)

	// Check if key already exists (mimics database unique constraint)
	if existing, exists := r.idempotencyKeys[entryKey]; exists {
		// Check if not expired
		if time.Now().Before(existing.ExpiresAt) {
			return cuserr.NewConflictError(
				"idempotency_key",
				key,
				fmt.Sprintf("duplicate idempotency key for subscription %s", subscriptionID),
			)
		}
	}

	r.idempotencyKeys[entryKey] = idempotencyEntry{
		Key:            key,
		SubscriptionID: subscriptionID,
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now(),
	}

	return nil
}

// =============================================================================
// CIRCUIT BREAKER OPERATIONS
// =============================================================================

// GetCircuitBreakerState retrieves the circuit breaker state for an endpoint.
func (r *MockRepository) GetCircuitBreakerState(ctx context.Context, endpoint string) (*CircuitBreakerState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, exists := r.circuitBreakerState[endpoint]
	if !exists {
		// Return default closed state
		return &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateClosed,
			FailureCount: 0,
			SuccessCount: 0,
		}, nil
	}

	return copyCircuitBreakerState(state), nil
}

// UpdateCircuitBreakerState updates the circuit breaker state for an endpoint.
func (r *MockRepository) UpdateCircuitBreakerState(ctx context.Context, state *CircuitBreakerState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Error injection for testing
	if r.injectErrorOnUpdate {
		return cuserr.NewInternalError("mock_repository", nil, cuserr.WithMetadata("injected", "true"))
	}

	r.circuitBreakerState[state.Endpoint] = copyCircuitBreakerState(state)
	return nil
}

// =============================================================================
// TRANSACTION SUPPORT
// =============================================================================

// BeginTx begins a new transaction and returns a MockRepositoryTx.
// The transaction uses a copy-on-write strategy to isolate changes.
func (r *MockRepository) BeginTx(ctx context.Context) (RepositoryTx, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Create a transaction with copied data
	tx := &MockRepositoryTx{
		parent: r,

		subscriptions:       make(map[string]*Subscription),
		deliveries:          make(map[string]*Delivery),
		deliveryAttempts:    make(map[string][]*DeliveryAttempt),
		idempotencyKeys:     make(map[string]idempotencyEntry),
		circuitBreakerState: make(map[string]*CircuitBreakerState),
		lockedDeliveries:    make(map[string]bool),

		committed:  false,
		rolledBack: false,
	}

	// Copy all data
	for k, v := range r.subscriptions {
		tx.subscriptions[k] = copySubscription(v)
	}
	for k, v := range r.deliveries {
		tx.deliveries[k] = copyDelivery(v)
	}
	for k, v := range r.deliveryAttempts {
		copiedAttempts := make([]*DeliveryAttempt, len(v))
		for i, att := range v {
			copiedAttempts[i] = copyDeliveryAttempt(att)
		}
		tx.deliveryAttempts[k] = copiedAttempts
	}
	for k, v := range r.idempotencyKeys {
		tx.idempotencyKeys[k] = v
	}
	for k, v := range r.circuitBreakerState {
		tx.circuitBreakerState[k] = copyCircuitBreakerState(v)
	}
	for k, v := range r.lockedDeliveries {
		tx.lockedDeliveries[k] = v
	}

	return tx, nil
}

// =============================================================================
// HEALTH AND MAINTENANCE
// =============================================================================

// Ping checks if the repository is accessible (always returns nil for mock).
func (r *MockRepository) Ping(ctx context.Context) error {
	return nil
}

// Close closes the repository (no-op for mock).
func (r *MockRepository) Close() error {
	// Error injection for testing
	if r.injectErrorOnClose {
		return cuserr.NewInternalError("mock_repository", nil, cuserr.WithMetadata("operation", "close"))
	}
	return nil
}

// =============================================================================
// MAINTENANCE OPERATIONS
// =============================================================================

// CountDeliveriesByFilter counts deliveries matching the cleanup filter.
func (r *MockRepository) CountDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error) {
	// ⛔ Fail closed (v0.11.0): an empty TenantID drops the tenant clause below,
	// so it is only allowed on the explicit AllTenants opt-in — and, as in
	// Manager.CleanupDeliveries, at least one real constraint is required, so
	// AllTenants alone can never mean "the whole table".
	if err := filter.Validate(); err != nil {
		return 0, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	var count int64
	for _, delivery := range r.deliveries {
		if matchesCleanupFilter(delivery, filter) {
			count++
		}
	}
	return count, nil
}

// DeleteDeliveriesByFilter deletes deliveries matching the cleanup filter.
func (r *MockRepository) DeleteDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error) {
	// ⛔ Fail closed (v0.11.0): an empty TenantID drops the tenant clause below,
	// so it is only allowed on the explicit AllTenants opt-in — and, as in
	// Manager.CleanupDeliveries, at least one real constraint is required, so
	// AllTenants alone can never mean "the whole table".
	if err := filter.Validate(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var deleted int64
	toDelete := []string{}

	for id, delivery := range r.deliveries {
		if matchesCleanupFilter(delivery, filter) {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		delete(r.deliveries, id)
		delete(r.deliveryAttempts, id)
		deleted++
	}

	return deleted, nil
}

// GetMaintenanceStats retrieves comprehensive statistics for maintenance planning.
func (r *MockRepository) GetMaintenanceStats(ctx context.Context) (*MaintenanceStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := &MaintenanceStats{
		DeliveriesByStatus: make(map[string]int64),
		AsOf:               time.Now(),
	}

	var oldest, newest *time.Time

	for _, delivery := range r.deliveries {
		stats.TotalDeliveries++
		stats.DeliveriesByStatus[delivery.Status]++

		if oldest == nil || delivery.CreatedAt.Before(*oldest) {
			t := delivery.CreatedAt
			oldest = &t
		}
		if newest == nil || delivery.CreatedAt.After(*newest) {
			t := delivery.CreatedAt
			newest = &t
		}
	}

	stats.OldestDeliveryAt = oldest
	stats.NewestDeliveryAt = newest

	for _, attempts := range r.deliveryAttempts {
		stats.TotalDeliveryAttempts += int64(len(attempts))
	}

	now := time.Now()
	for _, entry := range r.idempotencyKeys {
		stats.IdempotencyKeys++
		if entry.ExpiresAt.Before(now) {
			stats.ExpiredIdempotencyKeys++
		}
	}

	return stats, nil
}

// CountExpiredIdempotencyKeys counts idempotency keys that have expired.
func (r *MockRepository) CountExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var count int64
	now := time.Now()
	for _, entry := range r.idempotencyKeys {
		if entry.ExpiresAt.Before(now) {
			count++
		}
	}
	return count, nil
}

// CleanupExpiredIdempotencyKeys deletes all expired idempotency keys.
func (r *MockRepository) CleanupExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var deleted int64
	now := time.Now()
	toDelete := []string{}

	for key, entry := range r.idempotencyKeys {
		if entry.ExpiresAt.Before(now) {
			toDelete = append(toDelete, key)
		}
	}

	for _, key := range toDelete {
		delete(r.idempotencyKeys, key)
		deleted++
	}

	return deleted, nil
}

// matchesCleanupFilter checks if a delivery matches the given cleanup filter.
func matchesCleanupFilter(delivery *Delivery, filter *CleanupFilter) bool {
	if filter.CreatedBefore != nil && !delivery.CreatedAt.Before(*filter.CreatedBefore) {
		return false
	}
	if filter.CreatedAfter != nil && !delivery.CreatedAt.After(*filter.CreatedAfter) {
		return false
	}
	if filter.Status != nil && *filter.Status != "" && delivery.Status != *filter.Status {
		return false
	}
	if filter.TenantID != "" && delivery.TenantID != filter.TenantID {
		return false
	}
	if filter.SubscriptionID != nil && *filter.SubscriptionID != "" && delivery.SubscriptionID != *filter.SubscriptionID {
		return false
	}
	if filter.EventType != nil && *filter.EventType != "" && delivery.EventType != *filter.EventType {
		return false
	}
	return true
}

// =============================================================================
// MOCK TRANSACTION IMPLEMENTATION
// =============================================================================

// MockRepositoryTx implements RepositoryTx using copy-on-write semantics.
type MockRepositoryTx struct {
	parent              *MockRepository
	subscriptions       map[string]*Subscription
	deliveries          map[string]*Delivery
	deliveryAttempts    map[string][]*DeliveryAttempt
	idempotencyKeys     map[string]idempotencyEntry
	circuitBreakerState map[string]*CircuitBreakerState
	lockedDeliveries    map[string]bool
	mu                  sync.RWMutex
	committed           bool
	rolledBack          bool
}

// Commit commits the transaction by copying changes back to the parent repository.
func (tx *MockRepositoryTx) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.committed {
		return nil // Already committed
	}
	if tx.rolledBack {
		return cuserr.NewValidationError("transaction", "transaction already rolled back")
	}

	// Lock parent and copy changes back
	tx.parent.mu.Lock()
	defer tx.parent.mu.Unlock()

	tx.parent.subscriptions = make(map[string]*Subscription)
	for k, v := range tx.subscriptions {
		tx.parent.subscriptions[k] = copySubscription(v)
	}

	tx.parent.deliveries = make(map[string]*Delivery)
	for k, v := range tx.deliveries {
		tx.parent.deliveries[k] = copyDelivery(v)
	}

	tx.parent.deliveryAttempts = make(map[string][]*DeliveryAttempt)
	for k, v := range tx.deliveryAttempts {
		copiedAttempts := make([]*DeliveryAttempt, len(v))
		for i, att := range v {
			copiedAttempts[i] = copyDeliveryAttempt(att)
		}
		tx.parent.deliveryAttempts[k] = copiedAttempts
	}

	tx.parent.idempotencyKeys = make(map[string]idempotencyEntry)
	for k, v := range tx.idempotencyKeys {
		tx.parent.idempotencyKeys[k] = v
	}

	tx.parent.circuitBreakerState = make(map[string]*CircuitBreakerState)
	for k, v := range tx.circuitBreakerState {
		tx.parent.circuitBreakerState[k] = copyCircuitBreakerState(v)
	}

	tx.parent.lockedDeliveries = make(map[string]bool)
	for k, v := range tx.lockedDeliveries {
		tx.parent.lockedDeliveries[k] = v
	}

	tx.committed = true
	return nil
}

// Rollback rolls back the transaction (discards all changes).
func (tx *MockRepositoryTx) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.rolledBack {
		return nil // Already rolled back
	}
	if tx.committed {
		return nil // Already committed, rollback is a no-op
	}

	tx.rolledBack = true
	return nil
}

// All Repository methods below delegate to the transaction's internal state

// CreateSubscription creates a subscription within the transaction.
func (tx *MockRepositoryTx) CreateSubscription(ctx context.Context, sub *Subscription) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	for _, existing := range tx.subscriptions {
		if existing.TenantID == sub.TenantID && existing.URL == sub.URL {
			return ErrDuplicateSubscription
		}
	}

	tx.subscriptions[sub.ID] = copySubscription(sub)
	return nil
}

// GetSubscription retrieves a subscription within the transaction.
func (tx *MockRepositoryTx) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	sub, exists := tx.subscriptions[id]
	if !exists {
		return nil, ErrSubscriptionNotFound
	}

	return copySubscription(sub), nil
}

// GetSubscriptionByTenantAndURL retrieves a subscription by tenant and URL within the transaction.
func (tx *MockRepositoryTx) GetSubscriptionByTenantAndURL(ctx context.Context, tenantID, url string) (*Subscription, error) {
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	for _, sub := range tx.subscriptions {
		if sub.TenantID == tenantID && sub.URL == url {
			return copySubscription(sub), nil
		}
	}

	return nil, ErrSubscriptionNotFound
}

// UpdateSubscription updates a subscription within the transaction.
func (tx *MockRepositoryTx) UpdateSubscription(ctx context.Context, sub *Subscription) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if _, exists := tx.subscriptions[sub.ID]; !exists {
		return ErrSubscriptionNotFound
	}

	sub.UpdatedAt = time.Now()
	tx.subscriptions[sub.ID] = copySubscription(sub)
	return nil
}

// DeleteSubscription deletes a subscription within the transaction.
func (tx *MockRepositoryTx) DeleteSubscription(ctx context.Context, id string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if _, exists := tx.subscriptions[id]; !exists {
		return ErrSubscriptionNotFound
	}

	delete(tx.subscriptions, id)

	// Cascade delete
	for deliveryID, delivery := range tx.deliveries {
		if delivery.SubscriptionID == id {
			delete(tx.deliveries, deliveryID)
			delete(tx.deliveryAttempts, deliveryID)
		}
	}

	for key, entry := range tx.idempotencyKeys {
		if entry.SubscriptionID == id {
			delete(tx.idempotencyKeys, key)
		}
	}

	return nil
}

// ListSubscriptions lists subscriptions within the transaction.
func (tx *MockRepositoryTx) ListSubscriptions(ctx context.Context, filter *SubscriptionFilter) ([]*Subscription, error) {
	// Mirror the real repositories' fail-closed tenant scope (v0.10.0).
	if err := filter.requireTenantScope(); err != nil {
		return nil, err
	}
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	results := []*Subscription{}

	for _, sub := range tx.subscriptions {
		if filter.TenantID != "" && sub.TenantID != filter.TenantID {
			continue
		}
		if filter.Status != "" && sub.Status != filter.Status {
			continue
		}
		if len(filter.EventTypes) > 0 && !matchesEventTypes(sub.EventTypes, filter.EventTypes) {
			continue
		}
		results = append(results, copySubscription(sub))
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	if filter.Offset > 0 {
		if filter.Offset >= len(results) {
			return []*Subscription{}, nil
		}
		results = results[filter.Offset:]
	}

	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

// CreateDelivery creates a delivery within the transaction.
func (tx *MockRepositoryTx) CreateDelivery(ctx context.Context, delivery *Delivery) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	tx.deliveries[delivery.ID] = copyDelivery(delivery)
	return nil
}

// GetDelivery retrieves a delivery within the transaction.
func (tx *MockRepositoryTx) GetDelivery(ctx context.Context, id string) (*Delivery, error) {
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	delivery, exists := tx.deliveries[id]
	if !exists {
		return nil, ErrDeliveryNotFound
	}

	return copyDelivery(delivery), nil
}

// UpdateDelivery updates a delivery within the transaction.
func (tx *MockRepositoryTx) UpdateDelivery(ctx context.Context, delivery *Delivery) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if _, exists := tx.deliveries[delivery.ID]; !exists {
		return ErrDeliveryNotFound
	}

	tx.deliveries[delivery.ID] = copyDelivery(delivery)

	// Unlock delivery after update (simulates transaction commit releasing row lock)
	delete(tx.lockedDeliveries, delivery.ID)

	return nil
}

// ListDeliveries retrieves deliveries matching the given filter within the transaction.
func (tx *MockRepositoryTx) ListDeliveries(ctx context.Context, filter *DeliveryFilter) ([]*Delivery, error) {
	// Mirror the real repositories' fail-closed tenant scope (v0.10.0).
	if err := filter.requireTenantScope(); err != nil {
		return nil, err
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()

	results := []*Delivery{}

	// Collect matching deliveries
	for _, delivery := range tx.deliveries {
		// Apply filters
		if filter.TenantID != "" && delivery.TenantID != filter.TenantID {
			continue
		}

		if filter.SubscriptionID != nil && *filter.SubscriptionID != "" &&
			delivery.SubscriptionID != *filter.SubscriptionID {
			continue
		}

		if filter.Status != nil && *filter.Status != "" &&
			delivery.Status != *filter.Status {
			continue
		}

		if filter.EventType != nil && *filter.EventType != "" &&
			delivery.EventType != *filter.EventType {
			continue
		}

		results = append(results, copyDelivery(delivery))
	}

	// Sort by created_at descending (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	// Apply offset
	if filter.Offset > 0 && filter.Offset < len(results) {
		results = results[filter.Offset:]
	} else if filter.Offset >= len(results) {
		results = []*Delivery{}
	}

	// Apply limit
	if filter.Limit > 0 && filter.Limit < len(results) {
		results = results[:filter.Limit]
	}

	return results, nil
}

// MoveToDeadLetter moves a delivery to dead letter within the transaction.
func (tx *MockRepositoryTx) MoveToDeadLetter(ctx context.Context, deliveryID string, reason string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	delivery, exists := tx.deliveries[deliveryID]
	if !exists {
		return ErrDeliveryNotFound
	}

	delivery.Status = DeliveryStatusDeadLetter
	now := time.Now()
	delivery.CompletedAt = &now
	delete(tx.lockedDeliveries, deliveryID)
	return nil
}

// DeleteDelivery permanently deletes a delivery and its attempts within the transaction.
func (tx *MockRepositoryTx) DeleteDelivery(ctx context.Context, id string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if _, exists := tx.deliveries[id]; !exists {
		return ErrDeliveryNotFound
	}

	// Delete the delivery
	delete(tx.deliveries, id)

	// Delete associated attempts
	delete(tx.deliveryAttempts, id)

	// Clean up any locks
	delete(tx.lockedDeliveries, id)

	return nil
}

// CreateDeliveryAttempt creates a delivery attempt within the transaction.
func (tx *MockRepositoryTx) CreateDeliveryAttempt(ctx context.Context, attempt *DeliveryAttempt) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if attempts, exists := tx.deliveryAttempts[attempt.DeliveryID]; exists {
		for _, existing := range attempts {
			if existing.AttemptNumber == attempt.AttemptNumber {
				return cuserr.NewConflictError("delivery_attempt", "attempt_number", fmt.Sprintf("attempt %d already exists for delivery %s", attempt.AttemptNumber, attempt.DeliveryID))
			}
		}
	}

	tx.deliveryAttempts[attempt.DeliveryID] = append(
		tx.deliveryAttempts[attempt.DeliveryID],
		copyDeliveryAttempt(attempt),
	)
	return nil
}

// GetDeliveryAttempts retrieves delivery attempts within the transaction.
func (tx *MockRepositoryTx) GetDeliveryAttempts(ctx context.Context, deliveryID string) ([]*DeliveryAttempt, error) {
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	attempts := tx.deliveryAttempts[deliveryID]
	if attempts == nil {
		return []*DeliveryAttempt{}, nil
	}

	results := make([]*DeliveryAttempt, len(attempts))
	for i, att := range attempts {
		results[i] = copyDeliveryAttempt(att)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].AttemptNumber < results[j].AttemptNumber
	})

	return results, nil
}

// CheckIdempotency checks idempotency within the transaction.
func (tx *MockRepositoryTx) CheckIdempotency(ctx context.Context, key string, subscriptionID string) (bool, error) {
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	entryKey := fmt.Sprintf("%s:%s", key, subscriptionID)
	entry, exists := tx.idempotencyKeys[entryKey]
	if !exists {
		return false, nil
	}
	if entry.ExpiresAt.Before(time.Now()) {
		return false, nil
	}
	return true, nil
}

// StoreIdempotencyKey stores an idempotency key within the transaction.
func (tx *MockRepositoryTx) StoreIdempotencyKey(ctx context.Context, key string, subscriptionID string, expiresAt time.Time) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	entryKey := fmt.Sprintf("%s:%s", key, subscriptionID)

	// Check if key already exists (mimics database unique constraint)
	if existing, exists := tx.idempotencyKeys[entryKey]; exists {
		// Check if not expired
		if time.Now().Before(existing.ExpiresAt) {
			return cuserr.NewConflictError(
				"idempotency_key",
				key,
				fmt.Sprintf("duplicate idempotency key for subscription %s", subscriptionID),
			)
		}
	}

	tx.idempotencyKeys[entryKey] = idempotencyEntry{
		Key:            key,
		SubscriptionID: subscriptionID,
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now(),
	}
	return nil
}

// GetCircuitBreakerState retrieves circuit breaker state within the transaction.
func (tx *MockRepositoryTx) GetCircuitBreakerState(ctx context.Context, endpoint string) (*CircuitBreakerState, error) {
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	state, exists := tx.circuitBreakerState[endpoint]
	if !exists {
		return &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateClosed,
			FailureCount: 0,
			SuccessCount: 0,
		}, nil
	}
	return copyCircuitBreakerState(state), nil
}

// UpdateCircuitBreakerState updates circuit breaker state within the transaction.
func (tx *MockRepositoryTx) UpdateCircuitBreakerState(ctx context.Context, state *CircuitBreakerState) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	tx.circuitBreakerState[state.Endpoint] = copyCircuitBreakerState(state)
	return nil
}

// BeginTx is not supported within a transaction (nested transactions).
func (tx *MockRepositoryTx) BeginTx(ctx context.Context) (RepositoryTx, error) {
	return nil, cuserr.NewValidationError("transaction", "nested transactions are not supported")
}

// Ping checks database health (always returns nil for mock).
func (tx *MockRepositoryTx) Ping(ctx context.Context) error {
	return nil
}

// Close is a no-op for transactions.
func (tx *MockRepositoryTx) Close() error {
	return nil
}

// CountDeliveriesByFilter counts deliveries matching the cleanup filter within the transaction.
func (tx *MockRepositoryTx) CountDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error) {
	// ⛔ Fail closed (v0.11.0): an empty TenantID drops the tenant clause below,
	// so it is only allowed on the explicit AllTenants opt-in — and, as in
	// Manager.CleanupDeliveries, at least one real constraint is required, so
	// AllTenants alone can never mean "the whole table".
	if err := filter.Validate(); err != nil {
		return 0, err
	}
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	var count int64
	for _, delivery := range tx.deliveries {
		if matchesCleanupFilter(delivery, filter) {
			count++
		}
	}
	return count, nil
}

// DeleteDeliveriesByFilter deletes deliveries matching the cleanup filter within the transaction.
func (tx *MockRepositoryTx) DeleteDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error) {
	// ⛔ Fail closed (v0.11.0): an empty TenantID drops the tenant clause below,
	// so it is only allowed on the explicit AllTenants opt-in — and, as in
	// Manager.CleanupDeliveries, at least one real constraint is required, so
	// AllTenants alone can never mean "the whole table".
	if err := filter.Validate(); err != nil {
		return 0, err
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()

	var deleted int64
	toDelete := []string{}

	for id, delivery := range tx.deliveries {
		if matchesCleanupFilter(delivery, filter) {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		delete(tx.deliveries, id)
		delete(tx.deliveryAttempts, id)
		deleted++
	}

	return deleted, nil
}

// GetMaintenanceStats retrieves comprehensive statistics for maintenance planning within the transaction.
func (tx *MockRepositoryTx) GetMaintenanceStats(ctx context.Context) (*MaintenanceStats, error) {
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	stats := &MaintenanceStats{
		DeliveriesByStatus: make(map[string]int64),
		AsOf:               time.Now(),
	}

	var oldest, newest *time.Time

	for _, delivery := range tx.deliveries {
		stats.TotalDeliveries++
		stats.DeliveriesByStatus[delivery.Status]++

		if oldest == nil || delivery.CreatedAt.Before(*oldest) {
			t := delivery.CreatedAt
			oldest = &t
		}
		if newest == nil || delivery.CreatedAt.After(*newest) {
			t := delivery.CreatedAt
			newest = &t
		}
	}

	stats.OldestDeliveryAt = oldest
	stats.NewestDeliveryAt = newest

	for _, attempts := range tx.deliveryAttempts {
		stats.TotalDeliveryAttempts += int64(len(attempts))
	}

	now := time.Now()
	for _, entry := range tx.idempotencyKeys {
		stats.IdempotencyKeys++
		if entry.ExpiresAt.Before(now) {
			stats.ExpiredIdempotencyKeys++
		}
	}

	return stats, nil
}

// CountExpiredIdempotencyKeys counts idempotency keys that have expired within the transaction.
func (tx *MockRepositoryTx) CountExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	tx.mu.RLock()
	defer tx.mu.RUnlock()

	var count int64
	now := time.Now()
	for _, entry := range tx.idempotencyKeys {
		if entry.ExpiresAt.Before(now) {
			count++
		}
	}
	return count, nil
}

// CleanupExpiredIdempotencyKeys deletes all expired idempotency keys within the transaction.
func (tx *MockRepositoryTx) CleanupExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	var deleted int64
	now := time.Now()
	toDelete := []string{}

	for key, entry := range tx.idempotencyKeys {
		if entry.ExpiresAt.Before(now) {
			toDelete = append(toDelete, key)
		}
	}

	for _, key := range toDelete {
		delete(tx.idempotencyKeys, key)
		deleted++
	}

	return deleted, nil
}
