// Package verify provides signature verification utilities for webhook receivers.
//
// This package helps webhook endpoint implementations verify that incoming
// webhook requests are authentic and haven't been tampered with.
//
// Usage:
//
//	import "github.com/itsatony/go-hookd/verify"
//
//	func webhookHandler(w http.ResponseWriter, r *http.Request) {
//	    body, _ := io.ReadAll(r.Body)
//
//	    result, err := verify.Signature(verify.SignatureParams{
//	        Secret:    os.Getenv("WEBHOOK_SECRET"),
//	        Signature: r.Header.Get("X-Webhook-Signature"),
//	        Timestamp: r.Header.Get("X-Webhook-Timestamp"),
//	        Payload:   body,
//	        MaxAge:    5 * time.Minute, // Optional: reject old requests
//	    })
//	    if err != nil {
//	        http.Error(w, "Invalid signature", http.StatusUnauthorized)
//	        return
//	    }
//
//	    // Process webhook...
//	}
package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ErrInvalidSignature is returned when the signature doesn't match.
var ErrInvalidSignature = errors.New("invalid signature")

// ErrTimestampTooOld is returned when the timestamp exceeds MaxAge.
var ErrTimestampTooOld = errors.New("timestamp too old")

// ErrTimestampInFuture is returned when the timestamp is in the future.
var ErrTimestampInFuture = errors.New("timestamp is in the future")

// ErrMissingSecret is returned when the secret is empty.
var ErrMissingSecret = errors.New("secret is required")

// ErrMissingSignature is returned when the signature is empty.
var ErrMissingSignature = errors.New("signature is required")

// ErrMissingTimestamp is returned when the timestamp is empty.
var ErrMissingTimestamp = errors.New("timestamp is required")

// ErrInvalidTimestamp is returned when the timestamp can't be parsed.
var ErrInvalidTimestamp = errors.New("invalid timestamp format")

// SignatureParams contains the parameters for signature verification.
type SignatureParams struct {
	// Secret is the shared secret used for HMAC signing (required).
	Secret string

	// Signature is the X-Webhook-Signature header value (required).
	Signature string

	// Timestamp is the X-Webhook-Timestamp header value (required).
	// This should be a Unix timestamp string.
	Timestamp string

	// Payload is the raw request body (required).
	Payload []byte

	// MaxAge is the maximum allowed age of the request (optional).
	// If set, requests with timestamps older than this will be rejected.
	// This provides protection against replay attacks.
	// If zero, no age validation is performed.
	MaxAge time.Duration

	// AllowFuture controls whether to allow timestamps in the future (optional).
	// By default (false), timestamps more than 1 minute in the future are rejected.
	// Set to true to allow future timestamps.
	AllowFuture bool
}

// Result contains the verification result.
type Result struct {
	// Valid indicates if the signature is valid.
	Valid bool

	// Timestamp is the parsed timestamp from the request.
	Timestamp time.Time

	// Age is how old the request is (time since timestamp).
	Age time.Duration
}

// Signature verifies a webhook signature with optional timestamp validation.
//
// The function:
// 1. Validates all required parameters are present
// 2. Parses and validates the timestamp
// 3. Checks timestamp age (if MaxAge is set)
// 4. Verifies the HMAC-SHA256 signature
//
// Returns a Result and nil error if verification succeeds.
// Returns nil and an error if verification fails.
//
// Example:
//
//	result, err := verify.Signature(verify.SignatureParams{
//	    Secret:    "your_secret",
//	    Signature: req.Header.Get("X-Webhook-Signature"),
//	    Timestamp: req.Header.Get("X-Webhook-Timestamp"),
//	    Payload:   body,
//	    MaxAge:    5 * time.Minute,
//	})
//	if err != nil {
//	    // Handle verification failure
//	}
func Signature(params SignatureParams) (*Result, error) {
	// Validate required parameters
	if params.Secret == "" {
		return nil, ErrMissingSecret
	}
	if params.Signature == "" {
		return nil, ErrMissingSignature
	}
	if params.Timestamp == "" {
		return nil, ErrMissingTimestamp
	}

	// Parse timestamp
	unixTime, err := strconv.ParseInt(params.Timestamp, 10, 64)
	if err != nil {
		return nil, ErrInvalidTimestamp
	}
	timestamp := time.Unix(unixTime, 0)

	// Calculate age
	now := time.Now()
	age := now.Sub(timestamp)

	// Check for future timestamps (with 1 minute tolerance)
	if !params.AllowFuture && age < -time.Minute {
		return nil, ErrTimestampInFuture
	}

	// Check timestamp age
	if params.MaxAge > 0 && age > params.MaxAge {
		return nil, fmt.Errorf("%w: request is %v old, max allowed is %v", ErrTimestampTooOld, age.Round(time.Second), params.MaxAge)
	}

	// Calculate expected signature
	expectedSignature := calculateSignature(params.Secret, params.Timestamp, params.Payload)

	// Constant-time comparison
	if !hmac.Equal([]byte(expectedSignature), []byte(params.Signature)) {
		return nil, ErrInvalidSignature
	}

	return &Result{
		Valid:     true,
		Timestamp: timestamp,
		Age:       age,
	}, nil
}

// Quick performs a simple signature verification without timestamp validation.
//
// This is a convenience function for cases where you just want to verify
// the signature is correct, without any replay protection.
//
// Example:
//
//	valid := verify.Quick(secret, timestamp, payload, signature)
func Quick(secret, timestamp string, payload []byte, signature string) bool {
	expectedSignature := calculateSignature(secret, timestamp, payload)
	return hmac.Equal([]byte(expectedSignature), []byte(signature))
}

// calculateSignature generates an HMAC-SHA256 signature.
// This matches the signing logic in the main hookd package.
//
// NOTE: This function is intentionally duplicated from hookd.utils.go to keep
// the verify package standalone for webhook receivers who may not import the
// full hookd package. Any changes here must be mirrored there.
func calculateSignature(secret, timestamp string, payload []byte) string {
	// Create the signature payload: timestamp.payload
	signaturePayload := fmt.Sprintf("%s.%s", timestamp, string(payload))

	// Calculate HMAC-SHA256
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(signaturePayload))

	return hex.EncodeToString(h.Sum(nil))
}
