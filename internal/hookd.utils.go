// Package internal provides the core webhook management implementation for go-hookd.
//
// This file contains utility functions used throughout the package, including:
// - Prefixed nanoID generation (sub_, dlv_, att_)
// - Pointer helper functions for primitive types
// - Common validation helpers
package internal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/itsatony/go-cuserr"
	gonanoid "github.com/matoous/go-nanoid/v2"
)

// ID Generation Functions
//
// All IDs in go-hookd use prefixed nanoIDs for better readability and type safety.
// Format: {prefix}_{nanoID} (e.g., sub_6ByTSYmGzT2c)
//
// NanoIDs are URL-safe, compact (21 characters), and have collision resistance
// comparable to UUIDs while being shorter and more readable.

// GenerateSubscriptionID generates a prefixed nanoID for subscriptions.
//
// Format: sub_{nanoID}
// Example: sub_6ByTSYmGzT2c8K3xN1fP2
func GenerateSubscriptionID() (string, error) {
	id, err := gonanoid.New()
	if err != nil {
		return "", cuserr.NewInternalError("id_generator", err,
			cuserr.WithMetadata("type", "subscription"),
		)
	}
	return fmt.Sprintf("%s_%s", PrefixSubscription, id), nil
}

// GenerateDeliveryID generates a prefixed nanoID for deliveries.
//
// Format: dlv_{nanoID}
// Example: dlv_9Kj2BxYzT3c5K8xM4fQ7
func GenerateDeliveryID() (string, error) {
	id, err := gonanoid.New()
	if err != nil {
		return "", cuserr.NewInternalError("id_generator", err,
			cuserr.WithMetadata("type", "delivery"),
		)
	}
	return fmt.Sprintf("%s_%s", PrefixDelivery, id), nil
}

// GenerateAttemptID generates a prefixed nanoID for delivery attempts.
//
// Format: att_{nanoID}
// Example: att_2Mz5CxZyT4d7K9xP1gR3
func GenerateAttemptID() (string, error) {
	id, err := gonanoid.New()
	if err != nil {
		return "", cuserr.NewInternalError("id_generator", err,
			cuserr.WithMetadata("type", "attempt"),
		)
	}
	return fmt.Sprintf("%s_%s", PrefixAttempt, id), nil
}

// Pointer Helper Functions
//
// These functions create pointers to primitive types, useful for optional fields
// in structs and avoiding the awkward &T{value} syntax.

// StringPtr returns a pointer to the given string.
func StringPtr(s string) *string {
	return &s
}

// IntPtr returns a pointer to the given int.
func IntPtr(i int) *int {
	return &i
}

// Int64Ptr returns a pointer to the given int64.
func Int64Ptr(i int64) *int64 {
	return &i
}

// Float64Ptr returns a pointer to the given float64.
func Float64Ptr(f float64) *float64 {
	return &f
}

// BoolPtr returns a pointer to the given bool.
func BoolPtr(b bool) *bool {
	return &b
}

// TimePtr returns a pointer to the given time.Time.
func TimePtr(t time.Time) *time.Time {
	return &t
}

// DurationPtr returns a pointer to the given time.Duration.
func DurationPtr(d time.Duration) *time.Duration {
	return &d
}

// Signature and Cryptographic Functions

// calculateSignature calculates the HMAC-SHA256 signature for a webhook payload.
//
// This is used to sign outgoing webhook deliveries so recipients can verify
// the authenticity of the request.
//
// The signature format is: hex(hmac-sha256(secret, timestamp + "." + payload))
//
// Parameters:
//   - secret: The subscription's secret key
//   - timestamp: Unix timestamp of the delivery (as string)
//   - payload: The JSON payload being delivered (as bytes)
//
// Returns the hex-encoded signature string.
func calculateSignature(secret, timestamp string, payload []byte) string {
	// Create the message to sign: timestamp.payload
	message := fmt.Sprintf("%s.%s", timestamp, string(payload))

	// Calculate HMAC-SHA256
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))

	// Return hex-encoded signature
	return hex.EncodeToString(h.Sum(nil))
}

// verifySignature verifies the HMAC-SHA256 signature of a webhook payload.
//
// This is the counterpart to calculateSignature, used by webhook recipients
// to verify the authenticity of incoming requests.
//
// Parameters:
//   - secret: The subscription's secret key
//   - timestamp: Unix timestamp from the X-Webhook-Timestamp header
//   - payload: The received JSON payload (as bytes)
//   - signature: The signature from the X-Webhook-Signature header
//
// Returns true if the signature is valid, false otherwise.
func verifySignature(secret, timestamp string, payload []byte, signature string) bool {
	expectedSignature := calculateSignature(secret, timestamp, payload)
	return hmac.Equal([]byte(expectedSignature), []byte(signature))
}

// calculateBackoff calculates the next retry delay using exponential backoff.
//
// Formula: min(initialBackoff * (factor ^ attemptNumber), maxBackoff)
//
// Parameters:
//   - attemptNumber: The current attempt number (0-based)
//   - policy: The retry policy with backoff configuration
//
// Returns the duration to wait before the next retry.
func calculateBackoff(attemptNumber int, policy *RetryPolicy) time.Duration {
	if attemptNumber < 0 {
		attemptNumber = 0
	}

	// Calculate exponential backoff
	backoff := float64(policy.InitialBackoff)
	for i := 0; i < attemptNumber; i++ {
		backoff *= policy.BackoffFactor
		// Prevent overflow
		if backoff > float64(policy.MaxBackoff) {
			return policy.MaxBackoff
		}
	}

	duration := time.Duration(backoff)
	if duration > policy.MaxBackoff {
		return policy.MaxBackoff
	}

	return duration
}

// calculateNextRetryTime calculates the absolute time for the next retry attempt.
//
// This combines the current time with the calculated backoff delay.
//
// Parameters:
//   - attemptNumber: The current attempt number (0-based)
//   - policy: The retry policy with backoff configuration
//
// Returns the time.Time when the next retry should occur.
func calculateNextRetryTime(attemptNumber int, policy *RetryPolicy) time.Time {
	backoff := calculateBackoff(attemptNumber, policy)
	return time.Now().Add(backoff)
}

// isRetryableStatusCode determines if an HTTP status code should trigger a retry.
//
// Retryable status codes:
// - 429 (Too Many Requests) - rate limit, retry with backoff
// - 500+ (Server Errors) - temporary server issues
//
// Non-retryable status codes:
// - 2xx (Success) - delivery succeeded
// - 4xx except 429 (Client Errors) - permanent failure (bad request, auth, not found, etc.)
//
// Parameters:
//   - statusCode: The HTTP status code from the delivery attempt
//
// Returns true if the status code indicates a retryable error.
func isRetryableStatusCode(statusCode int) bool {
	// Rate limit - retry with backoff
	if statusCode == 429 {
		return true
	}

	// Server errors (5xx) - retry
	if statusCode >= 500 {
		return true
	}

	// Success (2xx) and client errors (4xx except 429) - don't retry
	return false
}

// normalizeURL normalizes a URL string for comparison purposes.
//
// This ensures consistent URL comparison when checking for duplicate subscriptions.
// - Converts scheme and host to lowercase
// - Preserves path, query, and fragment
//
// Parameters:
//   - rawURL: The URL string to normalize
//
// Returns the normalized URL string, or the original string if parsing fails.
func normalizeURL(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	// url.Parse automatically normalizes scheme and host to lowercase
	return parsedURL.String()
}

// DefaultRetryPolicy returns the default retry policy for subscriptions.
//
// This uses the default configuration values for retry behavior.
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:    DefaultMaxRetries,
		InitialBackoff: time.Duration(DefaultInitialBackoffMs) * time.Millisecond,
		MaxBackoff:     time.Duration(DefaultMaxBackoffMs) * time.Millisecond,
		BackoffFactor:  DefaultBackoffFactor,
	}
}

// CalculateBackoff calculates the backoff duration for a delivery attempt.
//
// Uses exponential backoff with the given retry policy.
//
// Parameters:
//   - attemptNumber: The current attempt number (1-based)
//   - policy: The retry policy to use
//
// Returns the backoff duration (capped at policy.MaxBackoff).
func CalculateBackoff(attemptNumber int, policy *RetryPolicy) time.Duration {
	if policy == nil {
		policy = DefaultRetryPolicy()
	}

	if attemptNumber <= 1 {
		return policy.InitialBackoff
	}

	// Calculate exponential backoff
	backoff := float64(policy.InitialBackoff)
	for i := 1; i < attemptNumber; i++ {
		backoff *= policy.BackoffFactor
		if backoff >= float64(policy.MaxBackoff) {
			return policy.MaxBackoff
		}
	}

	return time.Duration(backoff)
}
