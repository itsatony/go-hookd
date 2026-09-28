// Package internal provides the core webhook management implementation for go-hookd.
//
// This file defines the configuration structures and validation logic for the Manager.
// All configuration uses sensible defaults from constants.go, with validation to
// ensure values are within acceptable ranges.
//
// Configuration is immutable after initialization - create a new Config to change settings.
package hookd

import (
	"time"
)

// Config holds all configuration options for the webhook Manager.
//
// All duration values are in milliseconds for consistency with the constants file.
// Use the provided helper methods (e.g., DeliveryTimeout()) to get time.Duration values.
//
// Thread Safety: Config instances are immutable after creation and safe for concurrent read access.
type Config struct {
	// Database configuration
	DatabaseURL string // PostgreSQL connection string (required)

	// Worker pool configuration
	WorkerCount       int // Number of concurrent delivery workers (default: 10)
	QueuePollInterval int // Queue polling interval in milliseconds (default: 1000)
	DeliveryTimeoutMs int // HTTP delivery timeout in milliseconds (default: 30000)
	MaxBatchSize      int // Deliveries one worker claims per poll (default: 1, see DefaultMaxBatchSize)

	// ClaimLeaseMs is how long, in milliseconds, a worker's claim on a delivery
	// lasts (v0.11.0). A claimed delivery is invisible to every other worker until
	// the lease expires; the worker renews it immediately before sending, so one
	// lease must outlast one attempt: it must be >= DeliveryTimeoutMs +
	// DeliveryBookkeepingTimeout + MinClaimLeaseMarginMs. 0 (the default) means
	// DeliveryTimeoutMs + DefaultClaimLeaseMarginMs. It is also the delay before a
	// delivery held by a crashed worker is picked up again. Leases are measured
	// on the DATABASE clock (NOW()); a failover to a primary whose clock runs
	// ahead shortens in-flight leases by that skew.
	ClaimLeaseMs int

	// QueueIdleMaxInterval is the ceiling, in milliseconds, that a worker's poll
	// interval backs off to while consecutive polls return no deliveries
	// (default: 30000). Set equal to QueuePollInterval to poll at a fixed rate.
	//
	// The idle rate of a pool is WorkerCount/QueuePollInterval, which is paid
	// continuously whether or not anything is enqueued; backoff makes that cost
	// proportional to traffic instead of to worker count. A worker resets to
	// QueuePollInterval the moment a poll returns work or Manager.Notify is called,
	// so backoff never slows a queue that has work in it.
	QueueIdleMaxInterval int

	// QueueIdleBackoffFactor is the multiplier applied to a worker's poll interval
	// after an empty poll (default: 2.0). 1.0 disables idle backoff.
	QueueIdleBackoffFactor float64

	// Retry policy defaults (can be overridden per-subscription)
	DefaultMaxRetries       int     // Maximum retry attempts (default: 10)
	DefaultInitialBackoffMs int     // Initial backoff in milliseconds (default: 1000)
	DefaultMaxBackoffMs     int     // Maximum backoff in milliseconds (default: 3600000)
	DefaultBackoffFactor    float64 // Exponential backoff multiplier (default: 2.0)

	// Circuit breaker configuration
	CircuitBreakerThreshold        int // Failures before opening (default: 5)
	CircuitBreakerTimeoutMs        int // Timeout before half-open in milliseconds (default: 60000)
	CircuitBreakerHalfOpenRequests int // Test requests in half-open state (default: 3)

	// Idempotency configuration
	IdempotencyTTLHours int // Idempotency key TTL in hours (default: 24)

	// Graceful shutdown configuration
	ShutdownTimeoutSeconds int // Maximum time to wait for graceful shutdown (default: 30)
}

// NewConfig creates a new Config with sensible defaults.
//
// Only DatabaseURL is required - all other fields use defaults from constants.go.
//
// Example:
//
//	cfg := NewConfig("postgres://localhost/webhooks")
//	cfg.WorkerCount = 20  // Override default
//	if err := cfg.Validate(); err != nil {
//	    log.Fatal(err)
//	}
func NewConfig(databaseURL string) *Config {
	return &Config{
		// Database
		DatabaseURL: databaseURL,

		// Worker pool
		WorkerCount:       DefaultWorkerCount,
		QueuePollInterval: DefaultQueuePollIntervalMs,
		DeliveryTimeoutMs: DefaultDeliveryTimeoutMs,
		MaxBatchSize:      DefaultMaxBatchSize,

		QueueIdleMaxInterval:   DefaultQueueIdleMaxIntervalMs,
		QueueIdleBackoffFactor: DefaultQueueIdleBackoffFactor,

		// Retry policy
		DefaultMaxRetries:       DefaultMaxRetries,
		DefaultInitialBackoffMs: DefaultInitialBackoffMs,
		DefaultMaxBackoffMs:     DefaultMaxBackoffMs,
		DefaultBackoffFactor:    DefaultBackoffFactor,

		// Circuit breaker
		CircuitBreakerThreshold:        DefaultCircuitBreakerThreshold,
		CircuitBreakerTimeoutMs:        DefaultCircuitBreakerTimeoutMs,
		CircuitBreakerHalfOpenRequests: DefaultCircuitBreakerHalfOpenRequests,

		// Idempotency
		IdempotencyTTLHours: DefaultIdempotencyTTLHours,

		// Graceful shutdown
		ShutdownTimeoutSeconds: 30,
	}
}

// Validate validates the configuration and returns an error if invalid.
//
// Validation rules:
// - DatabaseURL must not be empty
// - Worker count must be at least 1
// - Queue poll interval must be at least 1ms
// - Delivery timeout must be at least 1 second (1000ms)
// - Max retries must be at least 0
// - Initial backoff must be less than max backoff
// - Circuit breaker threshold must be at least 1
// - All other numeric values must be positive
//
// Returns nil if validation succeeds.
func (c *Config) Validate() error {
	if err := c.validateDatabase(); err != nil {
		return err
	}
	if err := c.validateWorkerPool(); err != nil {
		return err
	}
	if err := c.validateRetryPolicy(); err != nil {
		return err
	}
	if err := c.validateCircuitBreaker(); err != nil {
		return err
	}
	if err := c.validateIdempotency(); err != nil {
		return err
	}
	if err := c.validateShutdown(); err != nil {
		return err
	}
	return nil
}

// validateDatabase validates database configuration.
func (c *Config) validateDatabase() error {
	if c.DatabaseURL == "" {
		return NewConfigurationError("database_url", ErrMsgMissingDatabaseURL)
	}
	return nil
}

// validateWorkerPool validates worker pool and queue configuration.
func (c *Config) validateWorkerPool() error {
	if c.WorkerCount < 1 {
		return NewConfigurationError("worker_count", ErrMsgInvalidWorkerCount)
	}
	if c.QueuePollInterval < 1 {
		return NewConfigurationError("queue_poll_interval", ErrMsgInvalidPollInterval)
	}
	if c.DeliveryTimeoutMs < 1000 {
		return NewConfigurationError("delivery_timeout", ErrMsgInvalidDeliveryTimeout)
	}
	if c.MaxBatchSize < 1 {
		return NewConfigurationError("max_batch_size", ErrMsgInvalidMaxBatchSize)
	}
	// A Config built as a struct literal by a caller that predates these two fields
	// carries their zero values. Refusing that would turn a library upgrade into a
	// boot failure for every such caller, so an unset field adopts the default here
	// rather than being reported as an invalid one. Only the zero value is treated
	// this way: any explicitly set out-of-range value below is still refused.
	if c.QueueIdleMaxInterval == 0 {
		// Never below the caller's own base interval: a caller that set a poll
		// interval slower than the default ceiling must not be refused for a field
		// it never named.
		c.QueueIdleMaxInterval = DefaultQueueIdleMaxIntervalMs
		if c.QueuePollInterval > c.QueueIdleMaxInterval {
			c.QueueIdleMaxInterval = c.QueuePollInterval
		}
	}
	if c.QueueIdleBackoffFactor == 0 {
		c.QueueIdleBackoffFactor = DefaultQueueIdleBackoffFactor
	}
	if c.QueueIdleMaxInterval < c.QueuePollInterval {
		return NewConfigurationError("queue_idle_max_interval", ErrMsgInvalidIdleMaxInterval)
	}
	if c.QueueIdleBackoffFactor < 1.0 {
		return NewConfigurationError("queue_idle_backoff_factor", ErrMsgInvalidIdleBackoffFactor)
	}
	// ClaimLeaseMs 0 = derived (see ClaimLease); an explicit value must outlast
	// one attempt, or a slow send could be re-claimed and delivered twice.
	if c.ClaimLeaseMs != 0 && c.ClaimLeaseMs < c.minClaimLeaseMs() {
		return NewConfigurationError("claim_lease_ms", ErrMsgInvalidClaimLease)
	}
	return nil
}

// minClaimLeaseMs is the shortest lease that outlasts one delivery attempt.
func (c *Config) minClaimLeaseMs() int {
	return c.DeliveryTimeoutMs + int(DeliveryBookkeepingTimeout/time.Millisecond) + MinClaimLeaseMarginMs
}

// EffectiveBatchSize is how many deliveries one poll actually claims:
// MaxBatchSize, capped so a worker can finish its whole batch within one lease
// (ClaimLease / (DeliveryTimeout + DeliveryBookkeepingTimeout), at least 1).
// Rows beyond that would sit hidden from idle workers until the lease lapsed.
func (c *Config) EffectiveBatchSize() int {
	perAttempt := c.DeliveryTimeout() + DeliveryBookkeepingTimeout
	limit := int(c.ClaimLease() / perAttempt)
	if limit < 1 {
		limit = 1
	}
	if c.MaxBatchSize < limit {
		return c.MaxBatchSize
	}
	return limit
}

// ClaimLease returns the claim lease as a time.Duration: ClaimLeaseMs, or when it
// is 0, DeliveryTimeoutMs + DefaultClaimLeaseMarginMs.
func (c *Config) ClaimLease() time.Duration {
	if c.ClaimLeaseMs != 0 {
		return time.Duration(c.ClaimLeaseMs) * time.Millisecond
	}
	return time.Duration(c.DeliveryTimeoutMs+DefaultClaimLeaseMarginMs) * time.Millisecond
}

// validateRetryPolicy validates retry policy configuration.
func (c *Config) validateRetryPolicy() error {
	if c.DefaultMaxRetries < 0 {
		return NewConfigurationError("max_retries", ErrMsgInvalidMaxRetries)
	}
	if c.DefaultInitialBackoffMs < 1 {
		return NewConfigurationError("initial_backoff", ErrMsgInvalidInitialBackoff)
	}
	if c.DefaultMaxBackoffMs < 1 {
		return NewConfigurationError("max_backoff", ErrMsgInvalidMaxBackoff)
	}
	if c.DefaultInitialBackoffMs >= c.DefaultMaxBackoffMs {
		return NewConfigurationError("backoff", ErrMsgInvalidBackoff)
	}
	if c.DefaultBackoffFactor < 1.0 {
		return NewConfigurationError("backoff_factor", ErrMsgInvalidBackoffFactor)
	}
	return nil
}

// validateCircuitBreaker validates circuit breaker configuration.
func (c *Config) validateCircuitBreaker() error {
	if c.CircuitBreakerThreshold < 1 {
		return NewConfigurationError("circuit_breaker_threshold", ErrMsgInvalidCircuitBreakerThreshold)
	}
	if c.CircuitBreakerTimeoutMs < 1000 {
		return NewConfigurationError("circuit_breaker_timeout", ErrMsgInvalidCircuitBreakerTimeout)
	}
	if c.CircuitBreakerHalfOpenRequests < 1 {
		return NewConfigurationError("circuit_breaker_half_open_requests", ErrMsgInvalidCircuitBreakerHalfOpen)
	}
	return nil
}

// validateIdempotency validates idempotency configuration.
func (c *Config) validateIdempotency() error {
	if c.IdempotencyTTLHours < 1 {
		return NewConfigurationError("idempotency_ttl", ErrMsgInvalidIdempotencyTTL)
	}
	return nil
}

// validateShutdown validates shutdown configuration.
func (c *Config) validateShutdown() error {
	if c.ShutdownTimeoutSeconds < 1 {
		return NewConfigurationError("shutdown_timeout", ErrMsgInvalidShutdownTimeout)
	}
	return nil
}

// Helper methods to convert millisecond values to time.Duration

// QueuePollIntervalDuration returns the queue poll interval as a time.Duration.
func (c *Config) QueuePollIntervalDuration() time.Duration {
	return time.Duration(c.QueuePollInterval) * time.Millisecond
}

// QueueIdleMaxIntervalDuration returns the idle poll ceiling as a time.Duration.
func (c *Config) QueueIdleMaxIntervalDuration() time.Duration {
	return time.Duration(c.QueueIdleMaxInterval) * time.Millisecond
}

// DeliveryTimeout returns the delivery timeout as a time.Duration.
func (c *Config) DeliveryTimeout() time.Duration {
	return time.Duration(c.DeliveryTimeoutMs) * time.Millisecond
}

// InitialBackoff returns the initial backoff as a time.Duration.
func (c *Config) InitialBackoff() time.Duration {
	return time.Duration(c.DefaultInitialBackoffMs) * time.Millisecond
}

// MaxBackoff returns the maximum backoff as a time.Duration.
func (c *Config) MaxBackoff() time.Duration {
	return time.Duration(c.DefaultMaxBackoffMs) * time.Millisecond
}

// CircuitBreakerTimeout returns the circuit breaker timeout as a time.Duration.
func (c *Config) CircuitBreakerTimeout() time.Duration {
	return time.Duration(c.CircuitBreakerTimeoutMs) * time.Millisecond
}

// IdempotencyTTL returns the idempotency TTL as a time.Duration.
func (c *Config) IdempotencyTTL() time.Duration {
	return time.Duration(c.IdempotencyTTLHours) * time.Hour
}

// ShutdownTimeout returns the shutdown timeout as a time.Duration.
func (c *Config) ShutdownTimeout() time.Duration {
	return time.Duration(c.ShutdownTimeoutSeconds) * time.Second
}

// DefaultRetryPolicy creates a RetryPolicy using the default configuration values.
//
// This is used when creating subscriptions without an explicit retry policy.
func (c *Config) DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:    c.DefaultMaxRetries,
		InitialBackoff: c.InitialBackoff(),
		MaxBackoff:     c.MaxBackoff(),
		BackoffFactor:  c.DefaultBackoffFactor,
	}
}
