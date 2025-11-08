// Package internal provides the core webhook management implementation for go-hookd.
//
// This file defines the configuration structures and validation logic for the Manager.
// All configuration uses sensible defaults from constants.go, with validation to
// ensure values are within acceptable ranges.
//
// Configuration is immutable after initialization - create a new Config to change settings.
package internal

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
	MaxBatchSize      int // Maximum deliveries to fetch per poll (default: 100)

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
		MaxBatchSize:      100,

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
	// Database validation
	if c.DatabaseURL == "" {
		return NewConfigurationError("database_url", ErrMsgMissingDatabaseURL)
	}

	// Worker pool validation
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
		return NewConfigurationError("max_batch_size", "max_batch_size must be at least 1")
	}

	// Retry policy validation
	if c.DefaultMaxRetries < 0 {
		return NewConfigurationError("max_retries", ErrMsgInvalidMaxRetries)
	}

	if c.DefaultInitialBackoffMs < 1 {
		return NewConfigurationError("initial_backoff", "initial_backoff must be at least 1ms")
	}

	if c.DefaultMaxBackoffMs < 1 {
		return NewConfigurationError("max_backoff", "max_backoff must be at least 1ms")
	}

	if c.DefaultInitialBackoffMs >= c.DefaultMaxBackoffMs {
		return NewConfigurationError("backoff", ErrMsgInvalidBackoff)
	}

	if c.DefaultBackoffFactor < 1.0 {
		return NewConfigurationError("backoff_factor", "backoff_factor must be at least 1.0")
	}

	// Circuit breaker validation
	if c.CircuitBreakerThreshold < 1 {
		return NewConfigurationError("circuit_breaker_threshold", "circuit_breaker_threshold must be at least 1")
	}

	if c.CircuitBreakerTimeoutMs < 1000 {
		return NewConfigurationError("circuit_breaker_timeout", "circuit_breaker_timeout must be at least 1s")
	}

	if c.CircuitBreakerHalfOpenRequests < 1 {
		return NewConfigurationError("circuit_breaker_half_open_requests", "circuit_breaker_half_open_requests must be at least 1")
	}

	// Idempotency validation
	if c.IdempotencyTTLHours < 1 {
		return NewConfigurationError("idempotency_ttl", "idempotency_ttl must be at least 1 hour")
	}

	// Shutdown validation
	if c.ShutdownTimeoutSeconds < 1 {
		return NewConfigurationError("shutdown_timeout", "shutdown_timeout must be at least 1 second")
	}

	return nil
}

// Helper methods to convert millisecond values to time.Duration

// QueuePollIntervalDuration returns the queue poll interval as a time.Duration.
func (c *Config) QueuePollIntervalDuration() time.Duration {
	return time.Duration(c.QueuePollInterval) * time.Millisecond
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
