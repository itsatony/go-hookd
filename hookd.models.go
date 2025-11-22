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
	CreatedAt   time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at" db:"updated_at"`
	RetryPolicy *RetryPolicy      `json:"retry_policy" db:"retry_policy"`
	Headers     map[string]string `json:"headers,omitempty" db:"headers"`
	Metadata    map[string]any    `json:"metadata,omitempty" db:"metadata"`
	ID          string            `json:"id" db:"id"`
	TenantID    string            `json:"tenant_id" db:"tenant_id"`
	URL         string            `json:"url" db:"url"`
	Secret      string            `json:"-" db:"secret"`
	Status      string            `json:"status" db:"status"`
	EventTypes  []string          `json:"event_types" db:"event_types"`
}

// Delivery represents a webhook delivery instance.
//
// A delivery is created when an event matching a subscription occurs.
// It tracks the delivery lifecycle, retry attempts, and completion status.
type Delivery struct {
	CreatedAt      time.Time      `json:"created_at" db:"created_at"`
	Payload        map[string]any `json:"payload" db:"payload"`
	NextRetryAt    *time.Time     `json:"next_retry_at,omitempty" db:"next_retry_at"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty" db:"completed_at"`
	ID             string         `json:"id" db:"id"`
	SubscriptionID string         `json:"subscription_id" db:"subscription_id"`
	TenantID       string         `json:"tenant_id" db:"tenant_id"`
	EventType      string         `json:"event_type" db:"event_type"`
	Status         string         `json:"status" db:"status"`
	AttemptCount   int            `json:"attempt_count" db:"attempt_count"`
	MaxAttempts    int            `json:"max_attempts" db:"max_attempts"`
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
	RetryPolicy    *RetryPolicy      `json:"retry_policy,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	TenantID       string            `json:"tenant_id"`
	URL            string            `json:"url"`
	Secret         string            `json:"secret"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	EventTypes     []string          `json:"event_types"`
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
	Payload map[string]any `json:"payload"`

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
	SubscriptionID *string `json:"subscription_id,omitempty"`
	Status         *string `json:"status,omitempty"`
	EventType      *string `json:"event_type,omitempty"`
	TenantID       string  `json:"tenant_id,omitempty"`
	Limit          int     `json:"limit"`
	Offset         int     `json:"offset"`
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
