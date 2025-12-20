// Package internal provides the core webhook management implementation for go-hookd.
//
// This file contains utility functions used throughout the package, including:
// - Prefixed nanoID generation (sub_, dlv_, att_)
// - Pointer helper functions for primitive types
// - Common validation helpers
package hookd

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
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
// Example: sub_6ByTSYmGzT2c8K3xN1fP2.
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
// Example: dlv_9Kj2BxYzT3c5K8xM4fQ7.
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
// Example: att_2Mz5CxZyT4d7K9xP1gR3.
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

// StringSlicePtr returns a pointer to the given string slice.
// Useful for optional []string fields in UpdateSubscriptionRequest.
func StringSlicePtr(s []string) *[]string {
	return &s
}

// StringMapPtr returns a pointer to the given string map.
// Useful for optional map[string]string fields in UpdateSubscriptionRequest.
func StringMapPtr(m map[string]string) *map[string]string {
	return &m
}

// InterfaceMapPtr returns a pointer to the given interface map.
// Useful for optional map[string]any fields in UpdateSubscriptionRequest.
func InterfaceMapPtr(m map[string]any) *map[string]any {
	return &m
}

// Value Helpers
//
// These functions safely dereference pointers with default values,
// useful when consuming optional fields from request structs.

// StringValue returns the string value of a pointer, or empty string if nil.
func StringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// IntValue returns the int value of a pointer, or 0 if nil.
func IntValue(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

// BoolValue returns the bool value of a pointer, or false if nil.
func BoolValue(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// Copy Helpers
//
// These functions create deep copies of complex types to avoid shared state.

// CopyStringSlice creates a deep copy of a string slice.
func CopyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// CopyStringMap creates a deep copy of a string map.
func CopyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// CopyInterfaceMap creates a shallow copy of an interface map.
// Note: Values are not deep-copied, only the map structure.
func CopyInterfaceMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
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
//
// NOTE: This function is intentionally duplicated in verify/verify.go to keep
// the verify package standalone for webhook receivers who may not import the
// full hookd package. Any changes here must be mirrored there.
func calculateSignature(secret, timestamp string, payload []byte) string {
	// Create the message to sign: timestamp.payload
	message := fmt.Sprintf("%s.%s", timestamp, string(payload))

	// Calculate HMAC-SHA256
	h := hmac.New(sha256.New, []byte(secret))
	// Note: hash.Hash.Write() never returns an error in practice,
	// but we check it to satisfy linters
	if _, err := h.Write([]byte(message)); err != nil {
		// This should never happen with HMAC, but handle it anyway
		return ""
	}

	// Return hex-encoded signature
	return hex.EncodeToString(h.Sum(nil))
}

// VerifySignature verifies the HMAC-SHA256 signature of a webhook payload.
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
//
// Example:
//
//	func webhookHandler(w http.ResponseWriter, r *http.Request) {
//	    payload, _ := io.ReadAll(r.Body)
//	    signature := r.Header.Get("X-Webhook-Signature")
//	    timestamp := r.Header.Get("X-Webhook-Timestamp")
//	    secret := "your-subscription-secret"
//
//	    if !hookd.VerifySignature(secret, timestamp, payload, signature) {
//	        http.Error(w, "invalid signature", http.StatusUnauthorized)
//	        return
//	    }
//	    // Process webhook...
//	}
func VerifySignature(secret, timestamp string, payload []byte, signature string) bool {
	expectedSignature := calculateSignature(secret, timestamp, payload)
	return hmac.Equal([]byte(expectedSignature), []byte(signature))
}

// MatchEventType checks if an event type matches a pattern.
//
// Supported patterns:
//   - "*" matches any event type
//   - "prefix.*" matches any event type starting with "prefix."
//   - Exact string matches the event type exactly
//
// Examples:
//
//	MatchEventType("*", "order.created") // true
//	MatchEventType("order.*", "order.created") // true
//	MatchEventType("order.*", "order.updated") // true
//	MatchEventType("order.*", "user.created") // false
//	MatchEventType("order.created", "order.created") // true
//	MatchEventType("order.created", "order.updated") // false
func MatchEventType(pattern, eventType string) bool {
	// Universal wildcard matches everything
	if pattern == WildcardAll {
		return true
	}

	// Prefix wildcard (e.g., "order.*")
	if strings.HasSuffix(pattern, WildcardSuffix) {
		prefix := strings.TrimSuffix(pattern, WildcardSuffix)
		return strings.HasPrefix(eventType, prefix+".")
	}

	// Exact match
	return pattern == eventType
}

// MatchesAnyEventType checks if an event type matches any pattern in the list.
//
// This is used to validate that an event type matches at least one of the
// subscription's configured event types.
//
// Returns true if at least one pattern matches.
func MatchesAnyEventType(patterns []string, eventType string) bool {
	for _, pattern := range patterns {
		if MatchEventType(pattern, eventType) {
			return true
		}
	}
	return false
}

// IsWildcardPattern returns true if the pattern contains wildcards.
func IsWildcardPattern(pattern string) bool {
	return pattern == WildcardAll || strings.HasSuffix(pattern, WildcardSuffix)
}

// MatchesMetadata checks if event metadata contains all required filter values.
//
// Returns true if ALL filter key-values match the corresponding metadata values.
// An empty filter map always returns true (no filtering applied).
//
// Examples:
//
//	filters := map[string]string{"status": "failed", "type": "job"}
//	MatchesMetadata(filters, map[string]any{"status": "failed", "type": "job"}) // true
//	MatchesMetadata(filters, map[string]any{"status": "success", "type": "job"}) // false
//	MatchesMetadata(nil, map[string]any{"anything": "value"}) // true (no filters)
func MatchesMetadata(filters map[string]string, metadata map[string]any) bool {
	if len(filters) == 0 {
		return true // No filters means always match
	}

	for key, expectedValue := range filters {
		actualValue, exists := metadata[key]
		if !exists {
			return false // Required key missing
		}

		// Convert to string for comparison
		actualStr := fmt.Sprintf("%v", actualValue)
		if actualStr != expectedValue {
			return false // Value mismatch
		}
	}

	return true
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
