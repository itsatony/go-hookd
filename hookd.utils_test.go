package hookd

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateSubscriptionID tests subscription ID generation.
func TestGenerateSubscriptionID(t *testing.T) {
	t.Run("generates valid ID", func(t *testing.T) {
		id, err := GenerateSubscriptionID()
		require.NoError(t, err)
		assert.NotEmpty(t, id)
		assert.True(t, strings.HasPrefix(id, PrefixSubscription+"_"))
	})

	t.Run("generates unique IDs", func(t *testing.T) {
		ids := make(map[string]bool)
		for i := 0; i < 100; i++ {
			id, err := GenerateSubscriptionID()
			require.NoError(t, err)
			assert.False(t, ids[id], "ID should be unique")
			ids[id] = true
		}
		assert.Equal(t, 100, len(ids))
	})

	t.Run("ID format", func(t *testing.T) {
		id, err := GenerateSubscriptionID()
		require.NoError(t, err)

		// Should start with prefix + _
		assert.True(t, strings.HasPrefix(id, PrefixSubscription+"_"))

		// Get the nanoID part (after first underscore)
		nanoIDPart := strings.TrimPrefix(id, PrefixSubscription+"_")
		assert.NotEmpty(t, nanoIDPart, "nanoID part should not be empty")
		assert.Greater(t, len(nanoIDPart), 10, "nanoID should be reasonably long")
	})
}

// TestGenerateDeliveryID tests delivery ID generation.
func TestGenerateDeliveryID(t *testing.T) {
	t.Run("generates valid ID", func(t *testing.T) {
		id, err := GenerateDeliveryID()
		require.NoError(t, err)
		assert.NotEmpty(t, id)
		assert.True(t, strings.HasPrefix(id, PrefixDelivery+"_"))
	})

	t.Run("generates unique IDs", func(t *testing.T) {
		ids := make(map[string]bool)
		for i := 0; i < 100; i++ {
			id, err := GenerateDeliveryID()
			require.NoError(t, err)
			assert.False(t, ids[id], "ID should be unique")
			ids[id] = true
		}
		assert.Equal(t, 100, len(ids))
	})
}

// TestGenerateAttemptID tests attempt ID generation.
func TestGenerateAttemptID(t *testing.T) {
	t.Run("generates valid ID", func(t *testing.T) {
		id, err := GenerateAttemptID()
		require.NoError(t, err)
		assert.NotEmpty(t, id)
		assert.True(t, strings.HasPrefix(id, PrefixAttempt+"_"))
	})

	t.Run("generates unique IDs", func(t *testing.T) {
		ids := make(map[string]bool)
		for i := 0; i < 100; i++ {
			id, err := GenerateAttemptID()
			require.NoError(t, err)
			assert.False(t, ids[id], "ID should be unique")
			ids[id] = true
		}
		assert.Equal(t, 100, len(ids))
	})
}

// TestPointerHelpers tests pointer helper functions.
func TestPointerHelpers(t *testing.T) {
	t.Run("StringPtr", func(t *testing.T) {
		str := "test"
		ptr := StringPtr(str)
		require.NotNil(t, ptr)
		assert.Equal(t, str, *ptr)
	})

	t.Run("IntPtr", func(t *testing.T) {
		num := 42
		ptr := IntPtr(num)
		require.NotNil(t, ptr)
		assert.Equal(t, num, *ptr)
	})

	t.Run("Int64Ptr", func(t *testing.T) {
		num := int64(9223372036854775807)
		ptr := Int64Ptr(num)
		require.NotNil(t, ptr)
		assert.Equal(t, num, *ptr)
	})

	t.Run("Float64Ptr", func(t *testing.T) {
		num := 3.14159
		ptr := Float64Ptr(num)
		require.NotNil(t, ptr)
		assert.Equal(t, num, *ptr)
	})

	t.Run("BoolPtr", func(t *testing.T) {
		val := true
		ptr := BoolPtr(val)
		require.NotNil(t, ptr)
		assert.Equal(t, val, *ptr)
	})

	t.Run("TimePtr", func(t *testing.T) {
		now := time.Now()
		ptr := TimePtr(now)
		require.NotNil(t, ptr)
		assert.Equal(t, now, *ptr)
	})

	t.Run("DurationPtr", func(t *testing.T) {
		dur := 5 * time.Minute
		ptr := DurationPtr(dur)
		require.NotNil(t, ptr)
		assert.Equal(t, dur, *ptr)
	})
}

// TestCalculateSignature tests HMAC signature calculation.
func TestCalculateSignature(t *testing.T) {
	secret := "test-secret"
	timestamp := "1234567890"
	payload := []byte(`{"user_id":"123"}`)

	sig1 := calculateSignature(secret, timestamp, payload)
	sig2 := calculateSignature(secret, timestamp, payload)

	// Same inputs should produce same signature
	assert.Equal(t, sig1, sig2)

	// Signature should be hex-encoded (only hex characters)
	assert.Regexp(t, "^[0-9a-f]+$", sig1)

	// Different secret should produce different signature
	sig3 := calculateSignature("different-secret", timestamp, payload)
	assert.NotEqual(t, sig1, sig3)

	// Different timestamp should produce different signature
	sig4 := calculateSignature(secret, "9876543210", payload)
	assert.NotEqual(t, sig1, sig4)

	// Different payload should produce different signature
	sig5 := calculateSignature(secret, timestamp, []byte(`{"user_id":"456"}`))
	assert.NotEqual(t, sig1, sig5)
}

// TestVerifySignature tests signature verification.
func TestVerifySignature(t *testing.T) {
	secret := "test-secret"
	timestamp := "1234567890"
	payload := []byte(`{"user_id":"123"}`)

	sig := calculateSignature(secret, timestamp, payload)

	t.Run("valid signature", func(t *testing.T) {
		assert.True(t, verifySignature(secret, timestamp, payload, sig))
	})

	t.Run("invalid signature", func(t *testing.T) {
		assert.False(t, verifySignature(secret, timestamp, payload, "invalid-signature"))
	})

	t.Run("wrong secret", func(t *testing.T) {
		assert.False(t, verifySignature("wrong-secret", timestamp, payload, sig))
	})

	t.Run("wrong timestamp", func(t *testing.T) {
		assert.False(t, verifySignature(secret, "9999999999", payload, sig))
	})

	t.Run("wrong payload", func(t *testing.T) {
		assert.False(t, verifySignature(secret, timestamp, []byte(`{"user_id":"999"}`), sig))
	})
}

// TestCalculateBackoff tests exponential backoff calculation.
func TestCalculateBackoff(t *testing.T) {
	policy := &RetryPolicy{
		MaxAttempts:    10,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     1 * time.Hour,
		BackoffFactor:  2.0,
	}

	tests := []struct {
		name          string
		attemptNumber int
		expected      time.Duration
	}{
		{"first attempt", 0, 1 * time.Second},
		{"second attempt", 1, 2 * time.Second},
		{"third attempt", 2, 4 * time.Second},
		{"fourth attempt", 3, 8 * time.Second},
		{"very high attempt", 20, 1 * time.Hour}, // Capped at max
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backoff := calculateBackoff(tt.attemptNumber, policy)
			assert.Equal(t, tt.expected, backoff)
		})
	}

	t.Run("negative attempt number", func(t *testing.T) {
		backoff := calculateBackoff(-1, policy)
		assert.Equal(t, policy.InitialBackoff, backoff)
	})

	t.Run("different backoff factor", func(t *testing.T) {
		policy := &RetryPolicy{
			MaxAttempts:    10,
			InitialBackoff: 1 * time.Second,
			MaxBackoff:     1 * time.Hour,
			BackoffFactor:  3.0, // Triple instead of double
		}

		assert.Equal(t, 1*time.Second, calculateBackoff(0, policy))
		assert.Equal(t, 3*time.Second, calculateBackoff(1, policy))
		assert.Equal(t, 9*time.Second, calculateBackoff(2, policy))
		assert.Equal(t, 27*time.Second, calculateBackoff(3, policy))
	})
}

// TestCalculateNextRetryTime tests next retry time calculation.
func TestCalculateNextRetryTime(t *testing.T) {
	policy := &RetryPolicy{
		MaxAttempts:    10,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     1 * time.Hour,
		BackoffFactor:  2.0,
	}

	before := time.Now()
	nextRetry := calculateNextRetryTime(0, policy)
	after := time.Now()

	// Next retry should be approximately 1 second from now
	expectedMin := before.Add(1 * time.Second)
	expectedMax := after.Add(1 * time.Second)

	assert.True(t, nextRetry.After(expectedMin) || nextRetry.Equal(expectedMin))
	assert.True(t, nextRetry.Before(expectedMax) || nextRetry.Equal(expectedMax))
}

// TestIsRetryableStatusCode tests HTTP status code retry logic.
func TestIsRetryableStatusCode(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		retryable  bool
	}{
		// Success codes - don't retry
		{"200 OK", 200, false},
		{"201 Created", 201, false},
		{"204 No Content", 204, false},

		// Client errors - don't retry (except 429)
		{"400 Bad Request", 400, false},
		{"401 Unauthorized", 401, false},
		{"403 Forbidden", 403, false},
		{"404 Not Found", 404, false},

		// Rate limit - DO retry
		{"429 Too Many Requests", 429, true},

		// Server errors - DO retry
		{"500 Internal Server Error", 500, true},
		{"502 Bad Gateway", 502, true},
		{"503 Service Unavailable", 503, true},
		{"504 Gateway Timeout", 504, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.retryable, isRetryableStatusCode(tt.statusCode))
		})
	}
}

// TestNormalizeURL tests URL normalization.
func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple URL",
			input:    "https://example.com/webhook",
			expected: "https://example.com/webhook",
		},
		{
			name:     "URL with query params",
			input:    "https://example.com/webhook?foo=bar&baz=qux",
			expected: "https://example.com/webhook?foo=bar&baz=qux",
		},
		{
			name:     "URL with uppercase scheme",
			input:    "HTTPS://example.com/webhook",
			expected: "https://example.com/webhook",
		},
		{
			name:     "URL with uppercase host",
			input:    "https://EXAMPLE.COM/webhook",
			expected: "https://EXAMPLE.COM/webhook", // url.Parse preserves host case
		},
		{
			name:     "URL with mixed case",
			input:    "HTTPS://EXAMPLE.COM/webhook",
			expected: "https://EXAMPLE.COM/webhook", // scheme normalized, host preserved
		},
		{
			name:     "URL with port",
			input:    "https://example.com:8080/webhook",
			expected: "https://example.com:8080/webhook",
		},
		{
			name:     "invalid URL returns original",
			input:    "not-a-valid-url",
			expected: "not-a-valid-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeURL(tt.input))
		})
	}

	t.Run("normalization for duplicate detection", func(t *testing.T) {
		url1 := "https://example.com/webhook"
		url2 := "HTTPS://example.com/webhook" // Same host case

		normalized1 := normalizeURL(url1)
		normalized2 := normalizeURL(url2)

		assert.Equal(t, normalized1, normalized2, "normalized URLs should match when only scheme differs")

		// Note: For true duplicate detection with case-insensitive hosts,
		// applications should use strings.ToLower() on the normalized URL
	})
}

// TestEdgeCases tests edge cases in utility functions.
func TestEdgeCases(t *testing.T) {
	t.Run("empty string signature", func(t *testing.T) {
		sig := calculateSignature("", "", []byte(""))
		assert.NotEmpty(t, sig, "should generate signature even for empty inputs")
	})

	t.Run("very long payload", func(t *testing.T) {
		payload := []byte(strings.Repeat("a", 10000))
		sig := calculateSignature("secret", "123", payload)
		assert.NotEmpty(t, sig)
	})

	t.Run("zero attempt backoff", func(t *testing.T) {
		policy := &RetryPolicy{
			MaxAttempts:    10,
			InitialBackoff: 100 * time.Millisecond,
			MaxBackoff:     1 * time.Hour,
			BackoffFactor:  2.0,
		}
		backoff := calculateBackoff(0, policy)
		assert.Equal(t, 100*time.Millisecond, backoff)
	})
}
