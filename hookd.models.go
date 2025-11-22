// Package internal provides the core webhook management implementation for go-hookd.
//
// This file defines all domain models and data structures used throughout the package.
// All models include JSON and database tags for serialization and persistence.
package hookd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/itsatony/go-cuserr"
)

// Subscription represents a webhook subscription configuration.
//
// A subscription defines where webhooks should be delivered, which event types
// to subscribe to, retry behavior, and custom headers/metadata.
type Subscription struct {
	// ID is the unique subscription identifier (format: sub_<nanoID>)
	ID string `json:"id" db:"id"`

	// TenantID identifies the tenant owning this subscription
	TenantID string `json:"tenant_id" db:"tenant_id"`

	// URL is the webhook endpoint URL (must be http or https)
	URL string `json:"url" db:"url"`

	// Secret is used for HMAC signature generation (never exposed in JSON)
	Secret string `json:"-" db:"secret"`

	// EventTypes is the list of event types this subscription receives
	EventTypes []string `json:"event_types" db:"event_types"`

	// Status is the current subscription status (active, paused, disabled)
	Status string `json:"status" db:"status"`

	// RetryPolicy defines the retry behavior for failed deliveries
	RetryPolicy *RetryPolicy `json:"retry_policy" db:"retry_policy"`

	// Headers are custom HTTP headers to include in webhook requests
	Headers map[string]string `json:"headers,omitempty" db:"headers"`

	// Metadata contains arbitrary key-value pairs for application use
	Metadata map[string]interface{} `json:"metadata,omitempty" db:"metadata"`

	// CreatedAt is the timestamp when the subscription was created
	CreatedAt time.Time `json:"created_at" db:"created_at"`

	// UpdatedAt is the timestamp when the subscription was last updated
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Delivery represents a webhook delivery instance.
//
// A delivery is created when an event matching a subscription occurs.
// It tracks the delivery lifecycle, retry attempts, and completion status.
type Delivery struct {
	// ID is the unique delivery identifier (format: dlv_<nanoID>)
	ID string `json:"id" db:"id"`

	// SubscriptionID references the subscription for this delivery
	SubscriptionID string `json:"subscription_id" db:"subscription_id"`

	// TenantID identifies the tenant (copied from subscription for filtering)
	TenantID string `json:"tenant_id" db:"tenant_id"`

	// EventType is the type of event being delivered
	EventType string `json:"event_type" db:"event_type"`

	// Payload is the event data to be delivered as JSON
	Payload map[string]interface{} `json:"payload" db:"payload"`

	// Status is the current delivery status (pending, success, failed, dead_letter)
	Status string `json:"status" db:"status"`

	// AttemptCount is the number of delivery attempts made
	AttemptCount int `json:"attempt_count" db:"attempt_count"`

	// MaxAttempts is the maximum number of attempts allowed (from RetryPolicy)
	MaxAttempts int `json:"max_attempts" db:"max_attempts"`

	// NextRetryAt is the timestamp for the next retry attempt (nil if completed)
	NextRetryAt *time.Time `json:"next_retry_at,omitempty" db:"next_retry_at"`

	// CompletedAt is the timestamp when the delivery completed (success or dead letter)
	CompletedAt *time.Time `json:"completed_at,omitempty" db:"completed_at"`

	// CreatedAt is the timestamp when the delivery was created
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// DeliveryAttempt represents a single delivery attempt.
//
// Each time a delivery is attempted, a DeliveryAttempt record is created
// with the HTTP response details, duration, and any errors.
type DeliveryAttempt struct {
	// ID is the unique attempt identifier (format: att_<nanoID>)
	ID string `json:"id" db:"id"`

	// DeliveryID references the parent delivery
	DeliveryID string `json:"delivery_id" db:"delivery_id"`

	// AttemptNumber is the 1-based attempt number
	AttemptNumber int `json:"attempt_number" db:"attempt_number"`

	// StatusCode is the HTTP status code received (0 if network error)
	StatusCode int `json:"status_code,omitempty" db:"status_code"`

	// ResponseBody is the HTTP response body (truncated if too large)
	ResponseBody string `json:"response_body,omitempty" db:"response_body"`

	// ResponseHeaders are the HTTP response headers received
	ResponseHeaders map[string]string `json:"response_headers,omitempty" db:"response_headers"`

	// Error is the error message if the attempt failed
	Error string `json:"error,omitempty" db:"error"`

	// DurationMs is the attempt duration in milliseconds
	DurationMs int64 `json:"duration_ms" db:"duration_ms"`

	// AttemptedAt is the timestamp when this attempt was made
	AttemptedAt time.Time `json:"attempted_at" db:"attempted_at"`
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
	// Endpoint is the URL being monitored
	Endpoint string `json:"endpoint" db:"endpoint"`

	// State is the current circuit state (closed, half_open, open)
	State string `json:"state" db:"state"`

	// FailureCount is the consecutive failure count
	FailureCount int `json:"failure_count" db:"failure_count"`

	// SuccessCount is the consecutive success count (used in half-open)
	SuccessCount int `json:"success_count" db:"success_count"`

	// LastFailure is the timestamp of the most recent failure
	LastFailure time.Time `json:"last_failure,omitempty" db:"last_failure"`

	// OpenedAt is the timestamp when the circuit opened
	OpenedAt time.Time `json:"opened_at,omitempty" db:"opened_at"`

	// NextRetryAt is the timestamp when half-open testing should begin
	NextRetryAt time.Time `json:"next_retry_at,omitempty" db:"next_retry_at"`
}

// CreateSubscriptionRequest is the request to create a new subscription.
type CreateSubscriptionRequest struct {
	// TenantID identifies the tenant creating the subscription
	TenantID string `json:"tenant_id"`

	// URL is the webhook endpoint URL
	URL string `json:"url"`

	// Secret is used for HMAC signature generation
	Secret string `json:"secret"`

	// EventTypes is the list of event types to subscribe to
	EventTypes []string `json:"event_types"`

	// RetryPolicy defines retry behavior (optional, uses defaults if nil)
	RetryPolicy *RetryPolicy `json:"retry_policy,omitempty"`

	// Headers are custom HTTP headers to include in requests (optional)
	Headers map[string]string `json:"headers,omitempty"`

	// Metadata contains arbitrary key-value pairs (optional)
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// IdempotencyKey ensures this request is processed exactly once (optional)
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// Validate implements the Validator interface for CreateSubscriptionRequest.
func (r *CreateSubscriptionRequest) Validate() error {
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
	if err := validateURL(r.URL); err != nil {
		return err
	}

	// Validate secret
	if r.Secret == "" {
		return cuserr.NewValidationError("secret", ErrMsgMissingSecret)
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
	Metadata *map[string]interface{} `json:"metadata,omitempty"`
}

// Validate implements the Validator interface for UpdateSubscriptionRequest.
func (r *UpdateSubscriptionRequest) Validate() error {
	// Validate URL (if provided)
	if r.URL != nil {
		if *r.URL == "" {
			return cuserr.NewValidationError("url", ErrMsgMissingURL)
		}
		if err := validateURL(*r.URL); err != nil {
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
	Payload map[string]interface{} `json:"payload"`

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

	return nil
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
}

// DeliveryFilter defines filtering criteria for listing deliveries.
type DeliveryFilter struct {
	// TenantID filters by tenant (optional but recommended)
	TenantID string `json:"tenant_id,omitempty"`

	// SubscriptionID filters by subscription (optional)
	SubscriptionID *string `json:"subscription_id,omitempty"`

	// Status filters by delivery status (optional)
	Status *string `json:"status,omitempty"`

	// EventType filters by event type (optional)
	EventType *string `json:"event_type,omitempty"`

	// Limit is the maximum number of results (optional, default: 100)
	Limit int `json:"limit"`

	// Offset is the number of results to skip (optional, default: 0)
	Offset int `json:"offset"`
}

// DeliveryEvent is published to the event bus for delivery lifecycle events.
type DeliveryEvent struct {
	// DeliveryID is the delivery identifier
	DeliveryID string `json:"delivery_id"`

	// SubscriptionID is the subscription identifier
	SubscriptionID string `json:"subscription_id"`

	// TenantID is the tenant identifier
	TenantID string `json:"tenant_id"`

	// EventType is the event type being delivered
	EventType string `json:"event_type"`

	// Status is the current delivery status
	Status string `json:"status"`

	// Timestamp is when this event occurred
	Timestamp time.Time `json:"timestamp"`

	// Metadata contains additional event-specific data
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// AuditEvent is published to the event bus for audit trail.
type AuditEvent struct {
	// Type is the audit event type (e.g., "subscription_created")
	Type string `json:"type"`

	// ResourceID is the identifier of the affected resource
	ResourceID string `json:"resource_id"`

	// TenantID is the tenant identifier
	TenantID string `json:"tenant_id"`

	// Timestamp is when this event occurred
	Timestamp time.Time `json:"timestamp"`

	// Metadata contains additional audit data
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// CircuitBreakerEvent is published to the event bus for circuit breaker state changes.
type CircuitBreakerEvent struct {
	// Endpoint is the URL being monitored
	Endpoint string `json:"endpoint"`

	// State is the new circuit breaker state
	State string `json:"state"`

	// Timestamp is when this event occurred
	Timestamp time.Time `json:"timestamp"`
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

// validateURL validates a URL string.
func validateURL(urlStr string) error {
	if len(urlStr) > MaxURLLength {
		return cuserr.NewValidationError("url", ErrMsgURLTooLong)
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return cuserr.NewValidationError("url", ErrMsgInvalidURL)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return cuserr.NewValidationError("url", ErrMsgInvalidURLScheme)
	}

	if parsedURL.Host == "" {
		return cuserr.NewValidationError("url", ErrMsgMissingURLHost)
	}

	return nil
}

// validateEventTypes validates an event types array.
func validateEventTypes(eventTypes []string) error {
	if len(eventTypes) == 0 {
		return cuserr.NewValidationError("event_types", ErrMsgMissingEventTypes)
	}

	if len(eventTypes) > MaxEventTypesPerSubscription {
		return cuserr.NewValidationError("event_types", ErrMsgTooManyEventTypes)
	}

	for i, et := range eventTypes {
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
