package verify

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignature_ValidSignature(t *testing.T) {
	secret := "test_secret"
	payload := []byte(`{"event":"test","data":"hello"}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := calculateSignature(secret, timestamp, payload)

	result, err := Signature(SignatureParams{
		Secret:    secret,
		Signature: signature,
		Timestamp: timestamp,
		Payload:   payload,
	})

	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.True(t, result.Age < time.Second)
}

func TestSignature_InvalidSignature(t *testing.T) {
	secret := "test_secret"
	payload := []byte(`{"event":"test","data":"hello"}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	result, err := Signature(SignatureParams{
		Secret:    secret,
		Signature: "invalid_signature",
		Timestamp: timestamp,
		Payload:   payload,
	})

	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestSignature_WrongSecret(t *testing.T) {
	payload := []byte(`{"event":"test"}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := calculateSignature("secret_1", timestamp, payload)

	result, err := Signature(SignatureParams{
		Secret:    "secret_2", // Different secret
		Signature: signature,
		Timestamp: timestamp,
		Payload:   payload,
	})

	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestSignature_TamperedPayload(t *testing.T) {
	secret := "test_secret"
	originalPayload := []byte(`{"amount":100}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := calculateSignature(secret, timestamp, originalPayload)

	tamperedPayload := []byte(`{"amount":10000}`)

	result, err := Signature(SignatureParams{
		Secret:    secret,
		Signature: signature,
		Timestamp: timestamp,
		Payload:   tamperedPayload,
	})

	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestSignature_MaxAge(t *testing.T) {
	secret := "test_secret"
	payload := []byte(`{"event":"test"}`)

	t.Run("within_max_age", func(t *testing.T) {
		// Request from 1 minute ago
		oldTimestamp := fmt.Sprintf("%d", time.Now().Add(-1*time.Minute).Unix())
		signature := calculateSignature(secret, oldTimestamp, payload)

		result, err := Signature(SignatureParams{
			Secret:    secret,
			Signature: signature,
			Timestamp: oldTimestamp,
			Payload:   payload,
			MaxAge:    5 * time.Minute, // 5 minute max age
		})

		require.NoError(t, err)
		assert.True(t, result.Valid)
		assert.True(t, result.Age >= 59*time.Second && result.Age <= 61*time.Second)
	})

	t.Run("exceeds_max_age", func(t *testing.T) {
		// Request from 10 minutes ago
		oldTimestamp := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())
		signature := calculateSignature(secret, oldTimestamp, payload)

		result, err := Signature(SignatureParams{
			Secret:    secret,
			Signature: signature,
			Timestamp: oldTimestamp,
			Payload:   payload,
			MaxAge:    5 * time.Minute,
		})

		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrTimestampTooOld)
	})

	t.Run("no_max_age_check", func(t *testing.T) {
		// Very old request (1 hour ago)
		oldTimestamp := fmt.Sprintf("%d", time.Now().Add(-1*time.Hour).Unix())
		signature := calculateSignature(secret, oldTimestamp, payload)

		// MaxAge = 0 means no age checking
		result, err := Signature(SignatureParams{
			Secret:    secret,
			Signature: signature,
			Timestamp: oldTimestamp,
			Payload:   payload,
			MaxAge:    0,
		})

		require.NoError(t, err)
		assert.True(t, result.Valid)
	})
}

func TestSignature_FutureTimestamp(t *testing.T) {
	secret := "test_secret"
	payload := []byte(`{"event":"test"}`)

	t.Run("future_timestamp_rejected", func(t *testing.T) {
		// Request from 5 minutes in the future
		futureTimestamp := fmt.Sprintf("%d", time.Now().Add(5*time.Minute).Unix())
		signature := calculateSignature(secret, futureTimestamp, payload)

		result, err := Signature(SignatureParams{
			Secret:    secret,
			Signature: signature,
			Timestamp: futureTimestamp,
			Payload:   payload,
		})

		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrTimestampInFuture)
	})

	t.Run("slight_future_allowed", func(t *testing.T) {
		// Request from 30 seconds in the future (within 1 minute tolerance)
		futureTimestamp := fmt.Sprintf("%d", time.Now().Add(30*time.Second).Unix())
		signature := calculateSignature(secret, futureTimestamp, payload)

		result, err := Signature(SignatureParams{
			Secret:    secret,
			Signature: signature,
			Timestamp: futureTimestamp,
			Payload:   payload,
		})

		require.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("future_allowed_when_enabled", func(t *testing.T) {
		// Request from 5 minutes in the future, but AllowFuture=true
		futureTimestamp := fmt.Sprintf("%d", time.Now().Add(5*time.Minute).Unix())
		signature := calculateSignature(secret, futureTimestamp, payload)

		result, err := Signature(SignatureParams{
			Secret:      secret,
			Signature:   signature,
			Timestamp:   futureTimestamp,
			Payload:     payload,
			AllowFuture: true,
		})

		require.NoError(t, err)
		assert.True(t, result.Valid)
	})
}

func TestSignature_MissingParameters(t *testing.T) {
	secret := "test_secret"
	payload := []byte(`{"event":"test"}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := calculateSignature(secret, timestamp, payload)

	t.Run("missing_secret", func(t *testing.T) {
		result, err := Signature(SignatureParams{
			Secret:    "",
			Signature: signature,
			Timestamp: timestamp,
			Payload:   payload,
		})

		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrMissingSecret)
	})

	t.Run("missing_signature", func(t *testing.T) {
		result, err := Signature(SignatureParams{
			Secret:    secret,
			Signature: "",
			Timestamp: timestamp,
			Payload:   payload,
		})

		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrMissingSignature)
	})

	t.Run("missing_timestamp", func(t *testing.T) {
		result, err := Signature(SignatureParams{
			Secret:    secret,
			Signature: signature,
			Timestamp: "",
			Payload:   payload,
		})

		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrMissingTimestamp)
	})

	t.Run("empty_payload_allowed", func(t *testing.T) {
		// Empty payload is valid
		emptyPayload := []byte{}
		emptySig := calculateSignature(secret, timestamp, emptyPayload)

		result, err := Signature(SignatureParams{
			Secret:    secret,
			Signature: emptySig,
			Timestamp: timestamp,
			Payload:   emptyPayload,
		})

		require.NoError(t, err)
		assert.True(t, result.Valid)
	})
}

func TestSignature_InvalidTimestamp(t *testing.T) {
	secret := "test_secret"
	payload := []byte(`{"event":"test"}`)

	testCases := []string{
		"not_a_number",
		"12.34",
		"",
		"abc123",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			result, err := Signature(SignatureParams{
				Secret:    secret,
				Signature: "some_signature",
				Timestamp: tc,
				Payload:   payload,
			})

			assert.Nil(t, result)
			if tc == "" {
				assert.ErrorIs(t, err, ErrMissingTimestamp)
			} else {
				assert.ErrorIs(t, err, ErrInvalidTimestamp)
			}
		})
	}
}

func TestQuick(t *testing.T) {
	secret := "test_secret"
	payload := []byte(`{"event":"test","data":"hello"}`)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := calculateSignature(secret, timestamp, payload)

	t.Run("valid_signature", func(t *testing.T) {
		valid := Quick(secret, timestamp, payload, signature)
		assert.True(t, valid)
	})

	t.Run("invalid_signature", func(t *testing.T) {
		valid := Quick(secret, timestamp, payload, "invalid")
		assert.False(t, valid)
	})

	t.Run("wrong_secret", func(t *testing.T) {
		valid := Quick("wrong_secret", timestamp, payload, signature)
		assert.False(t, valid)
	})
}

func TestCalculateSignature_Compatibility(t *testing.T) {
	// Test that our signature calculation matches expected format
	// This ensures compatibility with the main hookd package

	secret := "my_webhook_secret"
	timestamp := "1700000000"
	payload := []byte(`{"order_id":"123","amount":99.99}`)

	sig := calculateSignature(secret, timestamp, payload)

	// Signature should be a hex string
	assert.Len(t, sig, 64) // SHA256 produces 32 bytes = 64 hex chars

	// Same inputs should always produce same output
	sig2 := calculateSignature(secret, timestamp, payload)
	assert.Equal(t, sig, sig2)

	// Different inputs should produce different output
	sig3 := calculateSignature(secret+"x", timestamp, payload)
	assert.NotEqual(t, sig, sig3)
}
