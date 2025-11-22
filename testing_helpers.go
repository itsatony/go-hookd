package hookd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// =============================================================================
// TEST HELPER FUNCTIONS
// =============================================================================
// These helpers replace flaky time.Sleep() calls with proper synchronization.

// PollUntil polls a condition function until it returns true or timeout expires.
// This replaces flaky time.Sleep() calls in tests.
func PollUntil(t *testing.T, condition func() bool, timeout time.Duration, interval time.Duration, msgAndArgs ...interface{}) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Check immediately first
	if condition() {
		return
	}

	for {
		select {
		case <-ctx.Done():
			require.Fail(t, "condition not met within timeout", msgAndArgs...)
			return
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}

// WaitForDeliveryStatus polls until delivery reaches expected status.
func WaitForDeliveryStatus(t *testing.T, repo Repository, deliveryID string, expectedStatus string, timeout time.Duration) *Delivery {
	t.Helper()

	var delivery *Delivery
	PollUntil(t, func() bool {
		d, err := repo.GetDelivery(context.Background(), deliveryID)
		if err != nil {
			return false
		}
		delivery = d
		return d.Status == expectedStatus
	}, timeout, 100*time.Millisecond,
		"Delivery %s should reach status %s within %v (current: %s)",
		deliveryID, expectedStatus, timeout, func() string {
			if delivery != nil {
				return delivery.Status
			}
			return "unknown"
		}())

	return delivery
}

// WaitForDeliveryStatusAny polls until delivery reaches any of the expected statuses.
func WaitForDeliveryStatusAny(t *testing.T, repo Repository, deliveryID string, expectedStatuses []string, timeout time.Duration) *Delivery {
	t.Helper()

	var delivery *Delivery
	PollUntil(t, func() bool {
		d, err := repo.GetDelivery(context.Background(), deliveryID)
		if err != nil {
			return false
		}
		delivery = d
		for _, status := range expectedStatuses {
			if d.Status == status {
				return true
			}
		}
		return false
	}, timeout, 100*time.Millisecond,
		"Delivery %s should reach one of statuses %v within %v (current: %s)",
		deliveryID, expectedStatuses, timeout, func() string {
			if delivery != nil {
				return delivery.Status
			}
			return "unknown"
		}())

	return delivery
}

// WaitForCondition is a generic condition waiter with timeout.
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	t.Helper()
	PollUntil(t, condition, timeout, 100*time.Millisecond, message)
}
