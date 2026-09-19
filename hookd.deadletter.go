// Package hookd provides webhook management functionality.
//
// This file implements dead letter queue (DLQ) management operations.
// Dead letter deliveries are those that have exhausted all retry attempts.
package hookd

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// DeadLetterFilter provides filtering options for dead letter queries.
type DeadLetterFilter struct {
	// TenantID filters by tenant (required)
	TenantID string `json:"tenant_id"`

	// SubscriptionID filters by subscription (optional)
	SubscriptionID *string `json:"subscription_id,omitempty"`

	// EventType filters by event type (optional)
	EventType *string `json:"event_type,omitempty"`

	// CreatedAfter filters deliveries created after this time (optional)
	CreatedAfter *time.Time `json:"created_after,omitempty"`

	// CreatedBefore filters deliveries created before this time (optional)
	CreatedBefore *time.Time `json:"created_before,omitempty"`

	// Limit is the maximum number of results (default: 100)
	Limit int `json:"limit"`

	// Offset is the number of results to skip (default: 0)
	Offset int `json:"offset"`
}

// PurgeFilter provides filtering options for purging dead letter deliveries.
type PurgeFilter struct {
	// TenantID filters by tenant (required)
	TenantID string `json:"tenant_id"`

	// SubscriptionID filters by subscription (optional)
	SubscriptionID *string `json:"subscription_id,omitempty"`

	// OlderThan purges deliveries older than this duration (optional)
	// If not set, purges all matching deliveries
	OlderThan *time.Duration `json:"older_than,omitempty"`
}

// RetryDeadLetterResult contains the result of retrying dead letter deliveries.
type RetryDeadLetterResult struct {
	// TotalFound is the number of dead letter deliveries found
	TotalFound int `json:"total_found"`

	// TotalRetried is the number of deliveries successfully reset for retry
	TotalRetried int `json:"total_retried"`

	// Errors contains any errors encountered during retry
	Errors []string `json:"errors,omitempty"`
}

// PurgeDeadLetterResult contains the result of purging dead letter deliveries.
type PurgeDeadLetterResult struct {
	// TotalPurged is the number of deliveries deleted
	TotalPurged int `json:"total_purged"`
}

// ListDeadLetters retrieves dead letter deliveries matching the filter.
//
// This is a convenience method that wraps ListDeliveries with status="dead_letter".
// Use this to view deliveries that have exhausted all retry attempts and need
// manual intervention.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - filter: Filter criteria for the query
//
// Returns a slice of dead letter deliveries and any error.
//
// Example:
//
//	deadLetters, err := manager.ListDeadLetters(ctx, &DeadLetterFilter{
//	    TenantID: "tenant_123",
//	    Limit:    50,
//	})
func (m *Manager) ListDeadLetters(ctx context.Context, filter *DeadLetterFilter) ([]*Delivery, error) {
	if filter == nil {
		return nil, NewValidationError("filter", ErrMsgFilterRequired)
	}

	if filter.TenantID == "" {
		return nil, NewValidationError("tenant_id", ErrMsgMissingTenantID)
	}

	// Set default limit
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	// Build delivery filter with dead_letter status
	status := DeliveryStatusDeadLetter
	deliveryFilter := &DeliveryFilter{
		TenantID:       filter.TenantID,
		Status:         &status,
		SubscriptionID: filter.SubscriptionID,
		EventType:      filter.EventType,
		Limit:          limit,
		Offset:         filter.Offset,
	}

	deliveries, err := m.repo.ListDeliveries(ctx, deliveryFilter)
	if err != nil {
		m.logger.Error("failed to list dead letters",
			zap.Error(err),
			zap.String("tenant_id", filter.TenantID),
		)
		return nil, err
	}

	// Apply time filters if provided (post-filter since DeliveryFilter may not support them)
	if filter.CreatedAfter != nil || filter.CreatedBefore != nil {
		filtered := make([]*Delivery, 0, len(deliveries))
		for _, d := range deliveries {
			if filter.CreatedAfter != nil && d.CreatedAt.Before(*filter.CreatedAfter) {
				continue
			}
			if filter.CreatedBefore != nil && d.CreatedAt.After(*filter.CreatedBefore) {
				continue
			}
			filtered = append(filtered, d)
		}
		deliveries = filtered
	}

	return deliveries, nil
}

// RetryDeadLetter resets a single dead letter delivery for retry.
//
// This method:
// 1. Verifies the delivery exists and is in dead_letter status
// 2. Resets the status to "pending"
// 3. Resets the attempt count to 0
// 4. Sets next_retry_at to now (immediate retry)
//
// The delivery will be picked up by the next worker poll cycle.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - deliveryID: ID of the delivery to retry
//
// Returns the updated delivery or an error.
//
// Errors:
//   - NotFoundError: If the delivery doesn't exist
//   - ValidationError: If the delivery is not in dead_letter status
//
// Example:
//
//	delivery, err := manager.RetryDeadLetter(ctx, "dlv_abc123")
func (m *Manager) RetryDeadLetter(ctx context.Context, deliveryID string) (*Delivery, error) {
	// Get the delivery
	delivery, err := m.repo.GetDelivery(ctx, deliveryID)
	if err != nil {
		m.logger.Error("failed to get delivery for retry",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		return nil, err
	}

	// Verify it's in dead_letter status
	if delivery.Status != DeliveryStatusDeadLetter {
		return nil, NewValidationError("delivery",
			"delivery must be in dead_letter status to retry (current: "+delivery.Status+")")
	}

	// Reset for retry
	delivery.Status = DeliveryStatusPending
	delivery.AttemptCount = 0
	now := time.Now()
	delivery.NextRetryAt = &now
	delivery.CompletedAt = nil

	// Update the delivery
	if err := m.repo.UpdateDelivery(ctx, delivery); err != nil {
		m.logger.Error("failed to update delivery for retry",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		return nil, err
	}

	m.logger.Info("dead letter delivery reset for retry",
		zap.String("delivery_id", deliveryID),
		zap.String("subscription_id", delivery.SubscriptionID),
		zap.String("tenant_id", delivery.TenantID),
	)

	// Publish event
	m.eventBus.Publish(EventTopicDeliveryQueued, &DeliveryEvent{
		DeliveryID:     delivery.ID,
		SubscriptionID: delivery.SubscriptionID,
		TenantID:       delivery.TenantID,
		EventType:      delivery.EventType,
		Status:         DeliveryStatusPending,
		Timestamp:      now,
		Metadata: map[string]any{
			"retry_source": "dead_letter",
		},
	})

	// Wake the delivery pool. A dead letter reset to pending is an ENQUEUE — the
	// row is claimable the moment UpdateDelivery commits — so it has to wake the
	// workers exactly as QueueDelivery does. Without this the redrive is held
	// for up to QueueIdleMaxInterval on an otherwise idle queue, which is
	// precisely the state a dead-letter sweep runs in: v0.7.0 added the idle
	// backoff, v0.7.1 woke the three QUEUE paths, and this one was reached
	// through neither. The bulk RetryDeadLetters goes through here per delivery,
	// so it is covered by the same call.
	m.Notify()

	return delivery, nil
}

// RetryDeadLetters resets multiple dead letter deliveries for retry.
//
// This is a bulk operation that resets all matching dead letter deliveries
// to pending status. Use with caution as this may cause a large number of
// immediate delivery attempts.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - filter: Filter criteria to select which dead letters to retry
//
// Returns a result containing the number of deliveries processed.
//
// Example:
//
//	result, err := manager.RetryDeadLetters(ctx, &DeadLetterFilter{
//	    TenantID:       "tenant_123",
//	    SubscriptionID: StringPtr("sub_abc"),
//	})
//	fmt.Printf("Retried %d of %d deliveries\n", result.TotalRetried, result.TotalFound)
func (m *Manager) RetryDeadLetters(ctx context.Context, filter *DeadLetterFilter) (*RetryDeadLetterResult, error) {
	// List all matching dead letters
	// Set high limit to get all matching deliveries
	filter.Limit = 1000 // Reasonable batch size
	filter.Offset = 0

	result := &RetryDeadLetterResult{}

	for {
		deadLetters, err := m.ListDeadLetters(ctx, filter)
		if err != nil {
			return result, err
		}

		if len(deadLetters) == 0 {
			break
		}

		result.TotalFound += len(deadLetters)

		// Retry each delivery
		for _, dl := range deadLetters {
			_, err := m.RetryDeadLetter(ctx, dl.ID)
			if err != nil {
				result.Errors = append(result.Errors, dl.ID+": "+err.Error())
			} else {
				result.TotalRetried++
			}
		}

		// If we got less than the limit, we've processed all
		if len(deadLetters) < filter.Limit {
			break
		}

		// Continue with next batch (deliveries move out of dead_letter status,
		// so we don't need to increment offset)
	}

	m.logger.Info("bulk retry dead letters completed",
		zap.String("tenant_id", filter.TenantID),
		zap.Int("total_found", result.TotalFound),
		zap.Int("total_retried", result.TotalRetried),
		zap.Int("error_count", len(result.Errors)),
	)

	return result, nil
}

// PurgeDeadLetters permanently deletes dead letter deliveries matching the filter.
//
// WARNING: This operation is irreversible. Purged deliveries cannot be recovered.
// Consider exporting or archiving dead letter data before purging if needed for
// audit or debugging purposes.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - filter: Filter criteria to select which dead letters to purge
//
// Returns a result containing the number of deliveries deleted.
//
// Example:
//
//	// Purge dead letters older than 30 days
//	thirtyDays := 30 * 24 * time.Hour
//	result, err := manager.PurgeDeadLetters(ctx, &PurgeFilter{
//	    TenantID:  "tenant_123",
//	    OlderThan: &thirtyDays,
//	})
//	fmt.Printf("Purged %d deliveries\n", result.TotalPurged)
func (m *Manager) PurgeDeadLetters(ctx context.Context, filter *PurgeFilter) (*PurgeDeadLetterResult, error) {
	if filter == nil {
		return nil, NewValidationError("filter", ErrMsgFilterRequired)
	}

	if filter.TenantID == "" {
		return nil, NewValidationError("tenant_id", ErrMsgMissingTenantID)
	}

	result := &PurgeDeadLetterResult{}

	// Build dead letter filter
	dlFilter := &DeadLetterFilter{
		TenantID:       filter.TenantID,
		SubscriptionID: filter.SubscriptionID,
		Limit:          1000,
	}

	// Apply time filter if specified
	if filter.OlderThan != nil {
		cutoff := time.Now().Add(-*filter.OlderThan)
		dlFilter.CreatedBefore = &cutoff
	}

	// List and delete in batches
	for {
		deadLetters, err := m.ListDeadLetters(ctx, dlFilter)
		if err != nil {
			return result, err
		}

		if len(deadLetters) == 0 {
			break
		}

		// Delete each delivery
		for _, dl := range deadLetters {
			if err := m.repo.DeleteDelivery(ctx, dl.ID); err != nil {
				m.logger.Error("failed to purge dead letter",
					zap.Error(err),
					zap.String("delivery_id", dl.ID),
				)
				// Continue with other deletions
				continue
			}
			result.TotalPurged++
		}

		// If we got less than the limit, we've processed all
		if len(deadLetters) < dlFilter.Limit {
			break
		}
	}

	m.logger.Info("purge dead letters completed",
		zap.String("tenant_id", filter.TenantID),
		zap.Int("total_purged", result.TotalPurged),
	)

	return result, nil
}

// GetDeadLetterCount returns the count of dead letter deliveries for a tenant.
//
// This is useful for monitoring and alerting on dead letter queue depth.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - tenantID: The tenant to count dead letters for
//
// Returns the count or an error.
func (m *Manager) GetDeadLetterCount(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, NewValidationError("tenant_id", ErrMsgMissingTenantID)
	}

	status := DeliveryStatusDeadLetter
	deliveries, err := m.repo.ListDeliveries(ctx, &DeliveryFilter{
		TenantID: tenantID,
		Status:   &status,
		Limit:    10000, // High limit for counting
	})
	if err != nil {
		return 0, err
	}

	return len(deliveries), nil
}
