// Package internal provides the core webhook management implementation for go-hookd.
//
// This file defines all domain models and data structures used throughout the package.
// All models include JSON and database tags for serialization and persistence.
package hookd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/itsatony/go-cuserr"
)

// Subscription represents a webhook subscription configuration.
//
// A subscription defines where webhooks should be delivered, which event types
// to subscribe to, retry behavior, and custom headers/metadata.
type Subscription struct {
	CreatedAt   time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at" db:"updated_at"`
	RetryPolicy *RetryPolicy      `json:"retry_policy" db:"retry_policy"`
	Headers     map[string]string `json:"headers,omitempty" db:"headers"`
	Metadata    map[string]any    `json:"metadata,omitempty" db:"metadata"`
	// Filters enables metadata-based event filtering.
	// Only events whose metadata contains ALL filter key-values will be delivered.
	// Uses simple equality matching: filter["status"]="failed" matches metadata["status"]="failed".
	Filters    map[string]string `json:"filters,omitempty" db:"filters"`
	ID         string            `json:"id" db:"id"`
	TenantID   string            `json:"tenant_id" db:"tenant_id"`
	URL        string            `json:"url" db:"url"`
	Secret     string            `json:"-" db:"secret"`
	Status     string            `json:"status" db:"status"`
	EventTypes []string          `json:"event_types" db:"event_types"`
}

// Delivery represents a webhook delivery instance.
//
// A delivery is created when an event matching a subscription occurs,
// or as an inline delivery (without a pre-created subscription).
// It tracks the delivery lifecycle, retry attempts, and completion status.
type Delivery struct {
	CreatedAt   time.Time      `json:"created_at" db:"created_at"`
	Payload     map[string]any `json:"payload" db:"payload"`
	NextRetryAt *time.Time     `json:"next_retry_at,omitempty" db:"next_retry_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty" db:"completed_at"`
	ID          string         `json:"id" db:"id"`
	// SubscriptionID is the subscription this delivery belongs to (empty for inline deliveries)
	SubscriptionID string `json:"subscription_id,omitempty" db:"subscription_id"`
	TenantID       string `json:"tenant_id" db:"tenant_id"`
	EventType      string `json:"event_type" db:"event_type"`
	Status         string `json:"status" db:"status"`
	AttemptCount   int    `json:"attempt_count" db:"attempt_count"`
	MaxAttempts    int    `json:"max_attempts" db:"max_attempts"`
	// URL is the direct endpoint for inline deliveries (empty for subscription-based)
	URL string `json:"url,omitempty" db:"url"`
	// Secret is the HMAC key for inline deliveries (empty for subscription-based)
	Secret string `json:"-" db:"secret"`
	// IdempotencyKey is the key the queuer supplied (QueueDeliveryRequest /
	// QueueInlineDeliveryRequest), empty if none. Since v0.11.0 it is stored and
	// sent on every attempt as X-Webhook-Idempotency-Key, and carried on
	// delivery events. ⚠ It is visible to the receiver and to every EventBus
	// subscriber: never put sensitive data in it.
	IdempotencyKey string `json:"idempotency_key,omitempty" db:"idempotency_key"`
	// AttemptBudget is the retry budget the delivery was queued with (0 = not
	// recorded: MaxAttempts is the budget). RequeueDeadLetter renews exactly
	// this many attempts, and backoff restarts within each budget (v0.11.1).
	AttemptBudget int `json:"attempt_budget,omitempty" db:"attempt_budget"`
}

// attemptBudgetOrMax is the budget to record for a new delivery.
func (d *Delivery) attemptBudgetOrMax() int {
	if d.AttemptBudget > 0 {
		return d.AttemptBudget
	}
	return d.MaxAttempts
}

// attemptInBudget is the 1-based attempt number within the current budget —
// what the backoff schedule is computed from, so a redriven delivery starts a
// fresh schedule instead of at MaxBackoff.
func (d *Delivery) attemptInBudget() int {
	budget := d.attemptBudgetOrMax()
	n := d.AttemptCount - (d.MaxAttempts - budget)
	if n < 1 {
		return 1
	}
	return n
}

// DeliveryAttempt represents a single delivery attempt.
//
// Each time a delivery is attempted, a DeliveryAttempt record is created
// with the HTTP response details, duration, and any errors.
type DeliveryAttempt struct {
	AttemptedAt     time.Time         `json:"attempted_at" db:"attempted_at"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty" db:"response_headers"`
	ID              string            `json:"id" db:"id"`
	DeliveryID      string            `json:"delivery_id" db:"delivery_id"`
	ResponseBody    string            `json:"response_body,omitempty" db:"response_body"`
	Error           string            `json:"error,omitempty" db:"error"`
	AttemptNumber   int               `json:"attempt_number" db:"attempt_number"`
	StatusCode      int               `json:"status_code,omitempty" db:"status_code"`
	DurationMs      int64             `json:"duration_ms" db:"duration_ms"`
}

// RetryPolicy defines retry behavior for failed deliveries.
//
// Uses exponential backoff with configurable multiplier and maximum backoff.
type RetryPolicy struct {
	// MaxAttempts is the maximum number of delivery attempts
	MaxAttempts int `json:"max_attempts"`

	// InitialBackoff is the initial delay before the first retry
	InitialBackoff time.Duration `json:"initial_backoff"`

	// MaxBackoff is the maximum delay between retries
	MaxBackoff time.Duration `json:"max_backoff"`

	// BackoffFactor is the exponential multiplier (typically 2.0)
	BackoffFactor float64 `json:"backoff_factor"`
}

// CircuitBreakerState represents the state of a circuit breaker for an endpoint.
//
// Circuit breakers prevent cascading failures by temporarily blocking requests
// to failing endpoints.
type CircuitBreakerState struct {
	LastFailure  time.Time `json:"last_failure,omitempty" db:"last_failure"`
	OpenedAt     time.Time `json:"opened_at,omitempty" db:"opened_at"`
	NextRetryAt  time.Time `json:"next_retry_at,omitempty" db:"next_retry_at"`
	Endpoint     string    `json:"endpoint" db:"endpoint"`
	State        string    `json:"state" db:"state"`
	FailureCount int       `json:"failure_count" db:"failure_count"`
	SuccessCount int       `json:"success_count" db:"success_count"`
}

// CreateSubscriptionRequest is the request to create a new subscription.
type CreateSubscriptionRequest struct {
	RetryPolicy *RetryPolicy      `json:"retry_policy,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
	// Filters enables metadata-based event filtering.
	// Only events whose metadata contains ALL filter key-values will be delivered.
	Filters        map[string]string `json:"filters,omitempty"`
	TenantID       string            `json:"tenant_id"`
	URL            string            `json:"url"`
	Secret         string            `json:"secret"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	EventTypes     []string          `json:"event_types"`
}

// Validate implements the Validator interface for CreateSubscriptionRequest.
// A URL is judged under the STRICT egress policy; the Manager validates
// with its own policy (see WithAllowPrivateDestinations).
func (r *CreateSubscriptionRequest) Validate() error {
	return r.validate(strictEgressPolicy)
}

// validate is Validate under the given egress policy.
func (r *CreateSubscriptionRequest) validate(policy egressPolicy) error {
	// Validate tenant ID
	if r.TenantID == "" {
		return cuserr.NewValidationError("tenant_id", ErrMsgMissingTenantID)
	}
	if len(r.TenantID) > MaxTenantIDLength {
		return cuserr.NewValidationError("tenant_id", "tenant_id exceeds maximum length")
	}

	// Validate URL
	if r.URL == "" {
		return cuserr.NewValidationError("url", ErrMsgMissingURL)
	}
	if err := validateURLWithPolicy(r.URL, policy); err != nil {
		return err
	}

	// Validate secret
	if r.Secret == "" {
		return cuserr.NewValidationError("secret", ErrMsgMissingWebhookSecret)
	}
	if len(r.Secret) > MaxSecretLength {
		return cuserr.NewValidationError("secret", "secret exceeds maximum length")
	}
	if len(r.Secret) < MinPasswordLength {
		return cuserr.NewValidationError("secret", "secret must be at least 8 characters")
	}

	// Validate event types
	if err := validateEventTypes(r.EventTypes); err != nil {
		return err
	}

	// Validate retry policy (if provided)
	if r.RetryPolicy != nil {
		if err := r.RetryPolicy.Validate(); err != nil {
			return err
		}
	}

	// Validate headers
	if len(r.Headers) > MaxHeadersPerSubscription {
		return cuserr.NewValidationError("headers", ErrMsgTooManyHeaders)
	}

	// Validate metadata size
	if r.Metadata != nil {
		metadataJSON, err := json.Marshal(r.Metadata)
		if err != nil {
			return cuserr.NewValidationError("metadata", "invalid metadata format")
		}
		if len(metadataJSON) > MaxMetadataSize {
			return cuserr.NewValidationError("metadata", ErrMsgMetadataTooLarge)
		}
	}

	return nil
}

// UpdateSubscriptionRequest is the request to update a subscription.
//
// All fields are optional pointers. Only non-nil fields will be updated.
type UpdateSubscriptionRequest struct {
	// URL updates the webhook endpoint URL
	URL *string `json:"url,omitempty"`

	// EventTypes updates the event types list
	EventTypes *[]string `json:"event_types,omitempty"`

	// Status updates the subscription status
	Status *string `json:"status,omitempty"`

	// RetryPolicy updates the retry behavior
	RetryPolicy *RetryPolicy `json:"retry_policy,omitempty"`

	// Headers replaces the custom headers
	Headers *map[string]string `json:"headers,omitempty"`

	// Metadata replaces the metadata
	Metadata *map[string]any `json:"metadata,omitempty"`

	// Filters replaces the metadata filters
	Filters *map[string]string `json:"filters,omitempty"`
}

// Validate implements the Validator interface for UpdateSubscriptionRequest.
// A URL is judged under the STRICT egress policy; the Manager validates
// with its own policy (see WithAllowPrivateDestinations).
func (r *UpdateSubscriptionRequest) Validate() error {
	return r.validate(strictEgressPolicy)
}

// validate is Validate under the given egress policy.
func (r *UpdateSubscriptionRequest) validate(policy egressPolicy) error {
	// Validate URL (if provided)
	if r.URL != nil {
		if *r.URL == "" {
			return cuserr.NewValidationError("url", ErrMsgMissingURL)
		}
		if err := validateURLWithPolicy(*r.URL, policy); err != nil {
			return err
		}
	}

	// Validate event types (if provided)
	if r.EventTypes != nil {
		if err := validateEventTypes(*r.EventTypes); err != nil {
			return err
		}
	}

	// Validate status (if provided)
	if r.Status != nil {
		switch *r.Status {
		case SubscriptionStatusActive, SubscriptionStatusPaused, SubscriptionStatusDisabled:
			// Valid
		default:
			return cuserr.NewValidationError("status", ErrMsgInvalidStatus)
		}
	}

	// Validate retry policy (if provided)
	if r.RetryPolicy != nil {
		if err := r.RetryPolicy.Validate(); err != nil {
			return err
		}
	}

	// Validate headers (if provided)
	if r.Headers != nil && len(*r.Headers) > MaxHeadersPerSubscription {
		return cuserr.NewValidationError("headers", ErrMsgTooManyHeaders)
	}

	// Validate metadata size (if provided)
	if r.Metadata != nil {
		metadataJSON, err := json.Marshal(*r.Metadata)
		if err != nil {
			return cuserr.NewValidationError("metadata", "invalid metadata format")
		}
		if len(metadataJSON) > MaxMetadataSize {
			return cuserr.NewValidationError("metadata", ErrMsgMetadataTooLarge)
		}
	}

	return nil
}

// QueueDeliveryRequest is the request to queue a new delivery.
type QueueDeliveryRequest struct {
	// SubscriptionID identifies the subscription to deliver to
	SubscriptionID string `json:"subscription_id"`

	// EventType is the type of event being delivered
	EventType string `json:"event_type"`

	// Payload is the event data to deliver
	Payload map[string]any `json:"payload"`

	// Metadata contains additional event context used for filter matching.
	// If the subscription has filters, all filter key-values must match
	// the corresponding metadata values for delivery to proceed.
	Metadata map[string]any `json:"metadata,omitempty"`

	// IdempotencyKey ensures this delivery is processed exactly once (optional)
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// Validate implements the Validator interface for QueueDeliveryRequest.
func (r *QueueDeliveryRequest) Validate() error {
	// Validate subscription ID
	if r.SubscriptionID == "" {
		return cuserr.NewValidationError("subscription_id", ErrMsgMissingSubscriptionID)
	}

	// Validate event type
	if r.EventType == "" {
		return cuserr.NewValidationError("event_type", ErrMsgMissingEventType)
	}
	if len(r.EventType) > MaxEventTypeLength {
		return cuserr.NewValidationError("event_type", ErrMsgEventTypeTooLong)
	}

	// Validate payload
	if r.Payload == nil {
		return cuserr.NewValidationError("payload", ErrMsgMissingPayload)
	}
	payloadJSON, err := json.Marshal(r.Payload)
	if err != nil {
		return cuserr.NewValidationError("payload", "invalid payload format")
	}
	if len(payloadJSON) > MaxPayloadSize {
		return cuserr.NewValidationError("payload", ErrMsgPayloadTooLarge)
	}

	return validateIdempotencyKey(r.IdempotencyKey)
}

// QueueInlineDeliveryRequest is the request to queue an inline delivery.
//
// Inline deliveries don't require a pre-created subscription. They're useful
// for one-off callbacks like job completion webhooks.
type QueueInlineDeliveryRequest struct {
	// URL is the webhook endpoint to deliver to (required)
	URL string `json:"url"`

	// Secret is the HMAC signing key for this delivery (optional, recommended)
	Secret string `json:"secret,omitempty"`

	// EventType is the type of event being delivered (required)
	EventType string `json:"event_type"`

	// Payload is the event data to deliver (required)
	Payload map[string]any `json:"payload"`

	// TenantID is the tenant identifier for this delivery (required)
	TenantID string `json:"tenant_id"`

	// MaxRetries is the maximum number of retry attempts (optional, defaults to config)
	MaxRetries int `json:"max_retries,omitempty"`

	// IdempotencyKey ensures this delivery is processed exactly once (optional)
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// Validate implements the Validator interface for QueueInlineDeliveryRequest.
// A URL is judged under the STRICT egress policy; the Manager validates
// with its own policy (see WithAllowPrivateDestinations).
func (r *QueueInlineDeliveryRequest) Validate() error {
	return r.validate(strictEgressPolicy)
}

// validate is Validate under the given egress policy.
func (r *QueueInlineDeliveryRequest) validate(policy egressPolicy) error {
	// Validate URL
	if r.URL == "" {
		return cuserr.NewValidationError("url", ErrMsgMissingURL)
	}
	if err := validateURLWithPolicy(r.URL, policy); err != nil {
		return err
	}

	// Validate tenant ID
	if r.TenantID == "" {
		return cuserr.NewValidationError("tenant_id", ErrMsgMissingTenantID)
	}
	if len(r.TenantID) > MaxTenantIDLength {
		return cuserr.NewValidationError("tenant_id", "tenant_id exceeds maximum length")
	}

	// Validate event type
	if r.EventType == "" {
		return cuserr.NewValidationError("event_type", ErrMsgMissingEventType)
	}
	if len(r.EventType) > MaxEventTypeLength {
		return cuserr.NewValidationError("event_type", ErrMsgEventTypeTooLong)
	}

	// Validate payload
	if r.Payload == nil {
		return cuserr.NewValidationError("payload", ErrMsgMissingPayload)
	}
	payloadJSON, err := json.Marshal(r.Payload)
	if err != nil {
		return cuserr.NewValidationError("payload", "invalid payload format")
	}
	if len(payloadJSON) > MaxPayloadSize {
		return cuserr.NewValidationError("payload", ErrMsgPayloadTooLarge)
	}

	// Validate max retries (if provided)
	if r.MaxRetries < 0 {
		return cuserr.NewValidationError("max_retries", "max_retries cannot be negative")
	}

	return validateIdempotencyKey(r.IdempotencyKey)
}

// SubscriptionFilter defines filtering criteria for listing subscriptions.
type SubscriptionFilter struct {
	// TenantID filters by tenant (optional)
	TenantID string `json:"tenant_id,omitempty"`

	// Status filters by status (optional)
	Status string `json:"status,omitempty"`

	// EventTypes filters by event types (optional, matches any)
	EventTypes []string `json:"event_types,omitempty"`

	// Limit is the maximum number of results (required)
	Limit int `json:"limit"`

	// Offset is the number of results to skip (optional)
	Offset int `json:"offset"`

	// AllTenants opts into a CROSS-TENANT listing when TenantID is empty.
	//
	// ⛔ AN EMPTY TenantID ALONE IS FAIL-CLOSED (ErrMsgTenantScopeRequired): it
	// means "refuse", never "every tenant". This field is the ONLY way to scan
	// across tenants, and it exists so that intent is explicit and greppable at
	// the call site rather than implied by an omitted field. A per-tenant caller
	// must leave it false; only an operator/system path (a global dispatch bridge
	// or a cross-tenant dead-letter sweeper) sets it true, on a route it has
	// already authorised for that.
	AllTenants bool `json:"all_tenants,omitempty"`
}

// requireTenantScope fails closed when the filter would select every tenant
// without AllTenants set. It is the one predicate every List path (manager,
// postgres, tx, mock) shares, so a filter naming no tenant can never silently
// widen to the whole table.
func (f *SubscriptionFilter) requireTenantScope() error {
	if f.TenantID == "" && !f.AllTenants {
		return cuserr.NewValidationError("tenant_id", ErrMsgTenantScopeRequired)
	}
	return nil
}

// DeliveryFilter defines filtering criteria for listing deliveries.
type DeliveryFilter struct {
	// CreatedBefore filters deliveries created before this time (optional).
	// Useful for cleanup operations to find old deliveries.
	CreatedBefore *time.Time `json:"created_before,omitempty"`

	// CreatedAfter filters deliveries created after this time (optional).
	// Useful for finding recent deliveries.
	CreatedAfter *time.Time `json:"created_after,omitempty"`

	SubscriptionID *string `json:"subscription_id,omitempty"`
	Status         *string `json:"status,omitempty"`
	EventType      *string `json:"event_type,omitempty"`
	TenantID       string  `json:"tenant_id,omitempty"`
	Limit          int     `json:"limit"`
	Offset         int     `json:"offset"`

	// AllTenants opts into a CROSS-TENANT listing when TenantID is empty.
	//
	// ⛔ AN EMPTY TenantID ALONE IS FAIL-CLOSED (ErrMsgTenantScopeRequired): it
	// means "refuse", never "every tenant". Before v0.10.0 an empty TenantID here
	// silently dropped the WHERE clause and returned EVERY tenant's deliveries,
	// payloads included — a caller with an org-less identity could read the whole
	// table. This field is now the only way to scan across tenants: a per-tenant
	// caller leaves it false; only an operator/system path (a cross-tenant
	// dead-letter sweeper, say) sets it true, on a route it has already authorised.
	AllTenants bool `json:"all_tenants,omitempty"`
}

// requireTenantScope fails closed when the filter would select every tenant
// without AllTenants set. It is the one predicate every List path shares.
func (f *DeliveryFilter) requireTenantScope() error {
	if f.TenantID == "" && !f.AllTenants {
		return cuserr.NewValidationError("tenant_id", ErrMsgTenantScopeRequired)
	}
	return nil
}

// DeliveryEvent is published to the event bus for delivery lifecycle events.
type DeliveryEvent struct {
	Timestamp      time.Time      `json:"timestamp"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	DeliveryID     string         `json:"delivery_id"`
	SubscriptionID string         `json:"subscription_id"`
	TenantID       string         `json:"tenant_id"`
	EventType      string         `json:"event_type"`
	Status         string         `json:"status"`
	// IdempotencyKey is the queuer's key (v0.11.0), so a consumer that queues
	// from its own outbox can map an outcome back to its own row. ⚠ It reaches
	// every EventBus subscriber (and the receiver, as a header): never put
	// sensitive data in it.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// AuditEvent is published to the event bus for audit trail.
type AuditEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Type       string         `json:"type"`
	ResourceID string         `json:"resource_id"`
	TenantID   string         `json:"tenant_id"`
}

// CircuitBreakerEvent is published to the event bus for circuit breaker state changes.
type CircuitBreakerEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Endpoint  string    `json:"endpoint"`
	State     string    `json:"state"`
}

// TestResult represents the result of testing a subscription's webhook endpoint.
//
// This is returned by TestSubscription() and provides information about
// the endpoint's connectivity and response behavior.
type TestResult struct {
	// Success indicates if the test ping was successful (2xx response)
	Success bool `json:"success"`

	// StatusCode is the HTTP status code returned by the endpoint
	StatusCode int `json:"status_code"`

	// ResponseTime is how long the request took
	ResponseTime time.Duration `json:"response_time"`

	// ResponseBody is the response body (truncated if too long)
	ResponseBody string `json:"response_body,omitempty"`

	// Error contains any error message if the test failed
	Error string `json:"error,omitempty"`
}

// =============================================================================
// MAINTENANCE TYPES
// =============================================================================

// CleanupFilter specifies criteria for cleaning up deliveries.
//
// This filter is used by cleanup operations to select which deliveries
// to count or delete. All non-nil fields are combined with AND logic.
type CleanupFilter struct {
	// CreatedBefore filters deliveries created before this time.
	// This is typically the main filter for cleanup operations (e.g., "older than 30 days").
	CreatedBefore *time.Time `json:"created_before,omitempty"`

	// CreatedAfter filters deliveries created after this time (optional).
	CreatedAfter *time.Time `json:"created_after,omitempty"`

	// Status filters by delivery status.
	// Use DeliveryStatusSuccess, DeliveryStatusFailed, DeliveryStatusDeadLetter, etc.
	Status *string `json:"status,omitempty"`

	// TenantID scopes the cleanup to one tenant.
	//
	// ⛔ Since v0.11.0 an empty TenantID is REFUSED (ErrMsgTenantScopeRequired)
	// unless AllTenants is set: a destructive sweep across every tenant must say
	// so, exactly like the v0.10.0 List paths.
	TenantID string `json:"tenant_id,omitempty"`

	// SubscriptionID filters by subscription (optional).
	SubscriptionID *string `json:"subscription_id,omitempty"`

	// EventType filters by event type (optional).
	EventType *string `json:"event_type,omitempty"`

	// AllTenants is the explicit opt-in to count or delete across ALL tenants
	// when TenantID is empty (a fleet-wide retention sweep). It is not itself a
	// filter constraint: Validate still requires at least one real constraint.
	// When TenantID is set, TenantID wins and AllTenants has no effect.
	AllTenants bool `json:"all_tenants,omitempty"`
}

// requireTenantScope fails closed when a cleanup filter would count or delete
// across every tenant without AllTenants set. It is the same rule the List
// paths apply (DeliveryFilter.requireTenantScope), shared by every
// Count/DeleteDeliveriesByFilter implementation (postgres, tx, mock) and by
// Manager.CleanupDeliveries via Validate. A nil filter is refused too.
func (f *CleanupFilter) requireTenantScope() error {
	if f == nil {
		return cuserr.NewValidationError("filter", ErrMsgCleanupFilterRequired)
	}
	if f.TenantID == "" && !f.AllTenants {
		return cuserr.NewValidationError("tenant_id", ErrMsgTenantScopeRequired)
	}
	return nil
}

// Validate validates the CleanupFilter.
func (f *CleanupFilter) Validate() error {
	if err := f.requireTenantScope(); err != nil {
		return err
	}

	// At least one constraint must be provided to prevent accidental mass deletion
	if f.CreatedBefore == nil && f.CreatedAfter == nil && f.Status == nil &&
		f.TenantID == "" && f.SubscriptionID == nil && f.EventType == nil {
		return cuserr.NewValidationError("filter", "at least one filter constraint is required")
	}

	// Validate time range if both are provided
	if f.CreatedBefore != nil && f.CreatedAfter != nil {
		if !f.CreatedBefore.After(*f.CreatedAfter) {
			return cuserr.NewValidationError("filter", "created_before must be after created_after")
		}
	}

	return nil
}

// CleanupResult contains the result of a cleanup operation.
type CleanupResult struct {
	// DeliveriesDeleted is the number of delivery records deleted.
	DeliveriesDeleted int64 `json:"deliveries_deleted"`

	// AttemptsDeleted is the number of delivery attempt records deleted.
	// This is typically higher than DeliveriesDeleted due to cascade deletion.
	AttemptsDeleted int64 `json:"attempts_deleted"`

	// Duration is how long the cleanup operation took.
	Duration time.Duration `json:"duration"`

	// DryRun indicates if this was a dry-run (count-only) operation.
	DryRun bool `json:"dry_run"`
}

// MaintenanceStats provides statistics about data that could be cleaned up.
type MaintenanceStats struct {
	// TotalDeliveries is the total count of deliveries in the system.
	TotalDeliveries int64 `json:"total_deliveries"`

	// DeliveriesByStatus breaks down deliveries by status.
	DeliveriesByStatus map[string]int64 `json:"deliveries_by_status"`

	// OldestDeliveryAt is the timestamp of the oldest delivery.
	OldestDeliveryAt *time.Time `json:"oldest_delivery_at,omitempty"`

	// NewestDeliveryAt is the timestamp of the newest delivery.
	NewestDeliveryAt *time.Time `json:"newest_delivery_at,omitempty"`

	// TotalDeliveryAttempts is the total count of delivery attempts.
	TotalDeliveryAttempts int64 `json:"total_delivery_attempts"`

	// IdempotencyKeys is the count of stored idempotency keys.
	IdempotencyKeys int64 `json:"idempotency_keys"`

	// ExpiredIdempotencyKeys is the count of expired idempotency keys ready for cleanup.
	ExpiredIdempotencyKeys int64 `json:"expired_idempotency_keys"`

	// AsOf is the timestamp when these stats were collected.
	AsOf time.Time `json:"as_of"`
}

// IdempotencyCleanupResult contains the result of idempotency key cleanup.
type IdempotencyCleanupResult struct {
	// KeysDeleted is the number of expired idempotency keys deleted.
	KeysDeleted int64 `json:"keys_deleted"`

	// Duration is how long the cleanup operation took.
	Duration time.Duration `json:"duration"`
}

// Validate validates a RetryPolicy.
func (p *RetryPolicy) Validate() error {
	if p.MaxAttempts < 0 {
		return cuserr.NewValidationError("max_attempts", ErrMsgInvalidMaxRetries)
	}
	if p.InitialBackoff < 0 {
		return cuserr.NewValidationError("initial_backoff", "initial_backoff must be non-negative")
	}
	if p.MaxBackoff < p.InitialBackoff {
		return cuserr.NewValidationError("max_backoff", ErrMsgInvalidBackoff)
	}
	if p.BackoffFactor < 1.0 {
		return cuserr.NewValidationError("backoff_factor", "backoff_factor must be at least 1.0")
	}
	return nil
}

// validateURL validates a URL string under the STRICT egress policy: length,
// absolute http(s), a host, and no IP-literal host in a refused range. The
// Manager applies its own policy (which WithAllowPrivateDestinations widens)
// through validateURLWithPolicy; see hookd.egress.go.
func validateURL(urlStr string) error {
	return validateURLWithPolicy(urlStr, strictEgressPolicy)
}

// validateEventTypes validates an event types array.
// Supports wildcard patterns: "*" (all events) and "prefix.*" (prefix match).
func validateEventTypes(eventTypes []string) error {
	if len(eventTypes) == 0 {
		return cuserr.NewValidationError("event_types", ErrMsgMissingEventTypes)
	}

	if len(eventTypes) > MaxEventTypesPerSubscription {
		return cuserr.NewValidationError("event_types", ErrMsgTooManyEventTypes)
	}

	for i, et := range eventTypes {
		// Allow universal wildcard "*"
		if et == WildcardAll {
			continue
		}

		// Check for prefix wildcard pattern (e.g., "order.*")
		if strings.HasSuffix(et, WildcardSuffix) {
			prefix := strings.TrimSuffix(et, WildcardSuffix)
			// Prefix must have at least 1 character
			if len(prefix) < 1 {
				return cuserr.NewValidationError("event_types",
					fmt.Sprintf("event type at index %d: wildcard pattern must have a prefix (e.g., 'order.*')", i))
			}
			if len(prefix) > MaxEventTypeLength-2 { // -2 for ".*"
				return cuserr.NewValidationError("event_types",
					fmt.Sprintf("event type at index %d: %s", i, ErrMsgEventTypeTooLong))
			}
			continue
		}

		// Regular event type validation
		if len(et) < MinEventTypeLength {
			return cuserr.NewValidationError("event_types",
				fmt.Sprintf("event type at index %d is too short", i))
		}
		if len(et) > MaxEventTypeLength {
			return cuserr.NewValidationError("event_types",
				fmt.Sprintf("event type at index %d: %s", i, ErrMsgEventTypeTooLong))
		}
		if strings.TrimSpace(et) != et {
			return cuserr.NewValidationError("event_types",
				fmt.Sprintf("event type at index %d has leading/trailing whitespace", i))
		}
	}

	return nil
}

// validateIdempotencyKey refuses a key that cannot be sent as the
// X-Webhook-Idempotency-Key header (every attempt would fail) or stored
// (VARCHAR(255)): at most MaxIdempotencyKeyLength bytes of printable ASCII
// (space allowed, control characters and non-ASCII refused).
func validateIdempotencyKey(key string) error {
	if len(key) > MaxIdempotencyKeyLength {
		return cuserr.NewValidationError("idempotency_key", ErrMsgInvalidIdempotencyKey)
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return cuserr.NewValidationError("idempotency_key", ErrMsgInvalidIdempotencyKey)
		}
	}
	return nil
}
