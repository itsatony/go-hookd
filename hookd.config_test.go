package hookd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewConfig tests the NewConfig constructor with defaults.
func TestNewConfig(t *testing.T) {
	cfg := NewConfig("postgres://localhost/test")

	// Verify database URL is set
	assert.Equal(t, "postgres://localhost/test", cfg.DatabaseURL)

	// Verify worker pool defaults
	assert.Equal(t, DefaultWorkerCount, cfg.WorkerCount)
	assert.Equal(t, DefaultQueuePollIntervalMs, cfg.QueuePollInterval)
	assert.Equal(t, DefaultDeliveryTimeoutMs, cfg.DeliveryTimeoutMs)

	// Verify retry defaults
	assert.Equal(t, DefaultMaxRetries, cfg.DefaultMaxRetries)
	assert.Equal(t, DefaultInitialBackoffMs, cfg.DefaultInitialBackoffMs)
	assert.Equal(t, DefaultMaxBackoffMs, cfg.DefaultMaxBackoffMs)
	assert.Equal(t, DefaultBackoffFactor, cfg.DefaultBackoffFactor)

	// Verify circuit breaker defaults
	assert.Equal(t, DefaultCircuitBreakerThreshold, cfg.CircuitBreakerThreshold)
	assert.Equal(t, DefaultCircuitBreakerTimeoutMs, cfg.CircuitBreakerTimeoutMs)
	assert.Equal(t, DefaultCircuitBreakerHalfOpenRequests, cfg.CircuitBreakerHalfOpenRequests)

	// Verify idempotency defaults
	assert.Equal(t, DefaultIdempotencyTTLHours, cfg.IdempotencyTTLHours)
}

// TestConfig_Validate tests configuration validation.
func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		cfg     *Config
		name    string
		errMsg  string
		wantErr bool
	}{
		{
			name:    "valid configuration",
			cfg:     NewConfig("postgres://localhost/test"),
			wantErr: false,
		},
		{
			name: "missing database URL",
			cfg: &Config{
				DatabaseURL:       "",
				WorkerCount:       10,
				QueuePollInterval: 1000,
				DeliveryTimeoutMs: 30000,
			},
			wantErr: true,
			errMsg:  ErrMsgMissingDatabaseURL,
		},
		{
			name: "invalid worker count",
			cfg: &Config{
				DatabaseURL:       "postgres://localhost/test",
				WorkerCount:       0,
				QueuePollInterval: 1000,
				DeliveryTimeoutMs: 30000,
			},
			wantErr: true,
			errMsg:  ErrMsgInvalidWorkerCount,
		},
		{
			name: "invalid poll interval",
			cfg: &Config{
				DatabaseURL:       "postgres://localhost/test",
				WorkerCount:       10,
				QueuePollInterval: 0,
				DeliveryTimeoutMs: 30000,
			},
			wantErr: true,
			errMsg:  ErrMsgInvalidPollInterval,
		},
		{
			name: "invalid delivery timeout",
			cfg: &Config{
				DatabaseURL:       "postgres://localhost/test",
				WorkerCount:       10,
				QueuePollInterval: 1000,
				DeliveryTimeoutMs: 500, // Less than 1 second
			},
			wantErr: true,
			errMsg:  ErrMsgInvalidDeliveryTimeout,
		},
		{
			name: "invalid max batch size",
			cfg: &Config{
				DatabaseURL:       "postgres://localhost/test",
				WorkerCount:       10,
				QueuePollInterval: 1000,
				DeliveryTimeoutMs: 30000,
				MaxBatchSize:      0,
			},
			wantErr: true,
			errMsg:  "max_batch_size must be at least 1",
		},
		{
			name: "negative max retries",
			cfg: &Config{
				DatabaseURL:       "postgres://localhost/test",
				WorkerCount:       10,
				QueuePollInterval: 1000,
				DeliveryTimeoutMs: 30000,
				MaxBatchSize:      100,
				DefaultMaxRetries: -1,
			},
			wantErr: true,
			errMsg:  ErrMsgInvalidMaxRetries,
		},
		{
			name: "invalid backoff configuration",
			cfg: &Config{
				DatabaseURL:             "postgres://localhost/test",
				WorkerCount:             10,
				QueuePollInterval:       1000,
				DeliveryTimeoutMs:       30000,
				MaxBatchSize:            100,
				DefaultMaxRetries:       10,
				DefaultInitialBackoffMs: 10000,
				DefaultMaxBackoffMs:     5000, // Less than initial
				DefaultBackoffFactor:    2.0,
			},
			wantErr: true,
			errMsg:  ErrMsgInvalidBackoff,
		},
		{
			name: "invalid circuit breaker threshold",
			cfg: &Config{
				DatabaseURL:                    "postgres://localhost/test",
				WorkerCount:                    10,
				QueuePollInterval:              1000,
				DeliveryTimeoutMs:              30000,
				MaxBatchSize:                   100,
				DefaultMaxRetries:              10,
				DefaultInitialBackoffMs:        1000,
				DefaultMaxBackoffMs:            3600000,
				DefaultBackoffFactor:           2.0,
				CircuitBreakerThreshold:        0,
				CircuitBreakerTimeoutMs:        60000,
				CircuitBreakerHalfOpenRequests: 3,
			},
			wantErr: true,
			errMsg:  "circuit_breaker_threshold must be at least 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()

			if tt.wantErr {
				require.Error(t, err, "expected validation error")
				assert.Contains(t, err.Error(), tt.errMsg, "error message should contain expected text")
			} else {
				require.NoError(t, err, "expected validation to pass")
			}
		})
	}
}

// TestConfig_DurationHelpers tests the duration conversion methods.
func TestConfig_DurationHelpers(t *testing.T) {
	cfg := &Config{
		QueuePollInterval:       1000,
		DeliveryTimeoutMs:       30000,
		DefaultInitialBackoffMs: 2000,
		DefaultMaxBackoffMs:     3600000,
		CircuitBreakerTimeoutMs: 60000,
		IdempotencyTTLHours:     24,
		ShutdownTimeoutSeconds:  30,
	}

	t.Run("QueuePollIntervalDuration", func(t *testing.T) {
		assert.Equal(t, 1000*time.Millisecond, cfg.QueuePollIntervalDuration())
	})

	t.Run("DeliveryTimeout", func(t *testing.T) {
		assert.Equal(t, 30000*time.Millisecond, cfg.DeliveryTimeout())
		assert.Equal(t, 30*time.Second, cfg.DeliveryTimeout())
	})

	t.Run("InitialBackoff", func(t *testing.T) {
		assert.Equal(t, 2000*time.Millisecond, cfg.InitialBackoff())
		assert.Equal(t, 2*time.Second, cfg.InitialBackoff())
	})

	t.Run("MaxBackoff", func(t *testing.T) {
		assert.Equal(t, 3600000*time.Millisecond, cfg.MaxBackoff())
		assert.Equal(t, 1*time.Hour, cfg.MaxBackoff())
	})

	t.Run("CircuitBreakerTimeout", func(t *testing.T) {
		assert.Equal(t, 60000*time.Millisecond, cfg.CircuitBreakerTimeout())
		assert.Equal(t, 1*time.Minute, cfg.CircuitBreakerTimeout())
	})

	t.Run("IdempotencyTTL", func(t *testing.T) {
		assert.Equal(t, 24*time.Hour, cfg.IdempotencyTTL())
	})

	t.Run("ShutdownTimeout", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, cfg.ShutdownTimeout())
	})
}

// TestConfig_DefaultRetryPolicy tests the DefaultRetryPolicy method.
func TestConfig_DefaultRetryPolicy(t *testing.T) {
	cfg := &Config{
		DefaultMaxRetries:       5,
		DefaultInitialBackoffMs: 2000,
		DefaultMaxBackoffMs:     120000,
		DefaultBackoffFactor:    3.0,
	}

	policy := cfg.DefaultRetryPolicy()

	require.NotNil(t, policy)
	assert.Equal(t, 5, policy.MaxAttempts)
	assert.Equal(t, 2*time.Second, policy.InitialBackoff)
	assert.Equal(t, 120*time.Second, policy.MaxBackoff)
	assert.Equal(t, 3.0, policy.BackoffFactor)

	// Verify the policy is valid
	assert.NoError(t, policy.Validate())
}

// TestConfig_Mutability tests that config modifications work as expected.
func TestConfig_Mutability(t *testing.T) {
	cfg := NewConfig("postgres://localhost/test")

	// Modify configuration
	cfg.WorkerCount = 20
	cfg.DefaultMaxRetries = 15

	assert.Equal(t, 20, cfg.WorkerCount)
	assert.Equal(t, 15, cfg.DefaultMaxRetries)

	// Verify it still validates
	assert.NoError(t, cfg.Validate())
}

// TestConfig_EdgeCases tests edge cases in configuration.
func TestConfig_EdgeCases(t *testing.T) {
	t.Run("zero retries is valid", func(t *testing.T) {
		cfg := NewConfig("postgres://localhost/test")
		cfg.DefaultMaxRetries = 0
		assert.NoError(t, cfg.Validate())
	})

	t.Run("very large values", func(t *testing.T) {
		cfg := NewConfig("postgres://localhost/test")
		cfg.WorkerCount = 1000
		cfg.DefaultMaxRetries = 100
		cfg.DefaultMaxBackoffMs = 86400000 // 24 hours
		assert.NoError(t, cfg.Validate())
	})

	t.Run("minimum valid values", func(t *testing.T) {
		cfg := &Config{
			DatabaseURL:                    "postgres://localhost/test",
			WorkerCount:                    1,
			QueuePollInterval:              1,
			DeliveryTimeoutMs:              1000,
			MaxBatchSize:                   1,
			DefaultMaxRetries:              0,
			DefaultInitialBackoffMs:        1,
			DefaultMaxBackoffMs:            2,
			DefaultBackoffFactor:           1.0,
			CircuitBreakerThreshold:        1,
			CircuitBreakerTimeoutMs:        1000,
			CircuitBreakerHalfOpenRequests: 1,
			IdempotencyTTLHours:            1,
			ShutdownTimeoutSeconds:         1,
		}
		assert.NoError(t, cfg.Validate())
	})
}
