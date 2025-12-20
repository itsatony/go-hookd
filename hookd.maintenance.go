// Package hookd provides the core webhook management implementation for go-hookd.
//
// This file implements the maintenance API for cleanup operations and statistics.
// The maintenance API provides controlled cleanup capabilities with dry-run support
// to prevent accidental data loss.
package hookd

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// =============================================================================
// MAINTENANCE API
// =============================================================================

// GetMaintenanceStats retrieves comprehensive statistics for maintenance planning.
//
// This provides visibility into:
//   - Total deliveries and breakdown by status
//   - Oldest and newest delivery timestamps
//   - Total delivery attempts
//   - Idempotency key counts (total and expired)
//
// Use this information to plan cleanup operations and monitor system growth.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) GetMaintenanceStats(ctx context.Context) (*MaintenanceStats, error) {
	stats, err := m.repo.GetMaintenanceStats(ctx)
	if err != nil {
		m.logger.Error("failed to get maintenance stats",
			zap.Error(err),
		)
		return nil, err
	}

	m.logger.Debug("maintenance stats retrieved",
		zap.Int64("total_deliveries", stats.TotalDeliveries),
		zap.Int64("total_attempts", stats.TotalDeliveryAttempts),
		zap.Int64("idempotency_keys", stats.IdempotencyKeys),
		zap.Int64("expired_idempotency_keys", stats.ExpiredIdempotencyKeys),
	)

	return stats, nil
}

// CleanupDeliveries removes deliveries matching the given filter.
//
// This method supports dry-run mode via the dryRun parameter:
//   - dryRun=true: Only counts matching deliveries without deleting
//   - dryRun=false: Actually deletes matching deliveries
//
// The filter must have at least one constraint to prevent accidental mass deletion.
// Related delivery attempts are automatically deleted via CASCADE.
//
// Example usage:
//
//	// Cleanup successful deliveries older than 30 days (dry-run first)
//	filter := &CleanupFilter{
//	    Status: ptr(DeliveryStatusSuccess),
//	    CreatedBefore: ptr(time.Now().AddDate(0, 0, -30)),
//	}
//
//	// Dry-run to see what would be deleted
//	result, err := manager.CleanupDeliveries(ctx, filter, true)
//	fmt.Printf("Would delete %d deliveries\n", result.DeliveriesDeleted)
//
//	// Actually delete
//	result, err = manager.CleanupDeliveries(ctx, filter, false)
//	fmt.Printf("Deleted %d deliveries\n", result.DeliveriesDeleted)
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) CleanupDeliveries(ctx context.Context, filter *CleanupFilter, dryRun bool) (*CleanupResult, error) {
	// Validate filter
	if err := filter.Validate(); err != nil {
		return nil, err
	}

	startTime := time.Now()
	result := &CleanupResult{
		DryRun: dryRun,
	}

	if dryRun {
		// Dry-run: just count
		count, err := m.repo.CountDeliveriesByFilter(ctx, filter)
		if err != nil {
			m.logger.Error("failed to count deliveries for cleanup",
				zap.Error(err),
				zap.Bool("dry_run", dryRun),
			)
			return nil, err
		}
		result.DeliveriesDeleted = count
		// Note: AttemptsDeleted is unknown in dry-run mode (would require separate query)
	} else {
		// Actual deletion
		count, err := m.repo.DeleteDeliveriesByFilter(ctx, filter)
		if err != nil {
			m.logger.Error("failed to delete deliveries",
				zap.Error(err),
			)
			return nil, err
		}
		result.DeliveriesDeleted = count
		// Note: Attempts are deleted via CASCADE, count not tracked

		// Publish cleanup event if we have an event bus
		if m.eventBus != nil && count > 0 {
			m.eventBus.Publish(EventTopicMaintenanceCleanup, map[string]interface{}{
				"type":             "deliveries",
				"deleted_count":    count,
				"filter_status":    filter.Status,
				"filter_tenant_id": filter.TenantID,
				"filter_before":    filter.CreatedBefore,
				"timestamp":        time.Now(),
			})
		}
	}

	result.Duration = time.Since(startTime)

	m.logger.Info("delivery cleanup completed",
		zap.Bool("dry_run", dryRun),
		zap.Int64("deliveries_deleted", result.DeliveriesDeleted),
		zap.Duration("duration", result.Duration),
	)

	return result, nil
}

// CleanupIdempotencyKeys removes expired idempotency keys from the store.
//
// This method supports dry-run mode via the dryRun parameter:
//   - dryRun=true: Only counts expired keys without deleting
//   - dryRun=false: Actually deletes expired keys
//
// Idempotency keys naturally expire, but this method allows explicit cleanup
// to reclaim storage space and improve query performance.
//
// Example usage:
//
//	// Check how many expired keys exist
//	result, err := manager.CleanupIdempotencyKeys(ctx, true)
//	fmt.Printf("Would delete %d expired keys\n", result.KeysDeleted)
//
//	// Actually cleanup
//	result, err = manager.CleanupIdempotencyKeys(ctx, false)
//	fmt.Printf("Deleted %d expired keys\n", result.KeysDeleted)
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) CleanupIdempotencyKeys(ctx context.Context, dryRun bool) (*IdempotencyCleanupResult, error) {
	startTime := time.Now()
	result := &IdempotencyCleanupResult{}

	if dryRun {
		// Dry-run: just count
		count, err := m.repo.CountExpiredIdempotencyKeys(ctx)
		if err != nil {
			m.logger.Error("failed to count expired idempotency keys",
				zap.Error(err),
				zap.Bool("dry_run", dryRun),
			)
			return nil, err
		}
		result.KeysDeleted = count
	} else {
		// Actual deletion
		count, err := m.repo.CleanupExpiredIdempotencyKeys(ctx)
		if err != nil {
			m.logger.Error("failed to cleanup expired idempotency keys",
				zap.Error(err),
			)
			return nil, err
		}
		result.KeysDeleted = count

		// Publish cleanup event if we have an event bus
		if m.eventBus != nil && count > 0 {
			m.eventBus.Publish(EventTopicMaintenanceCleanup, map[string]interface{}{
				"type":          "idempotency_keys",
				"deleted_count": count,
				"timestamp":     time.Now(),
			})
		}
	}

	result.Duration = time.Since(startTime)

	m.logger.Info("idempotency key cleanup completed",
		zap.Bool("dry_run", dryRun),
		zap.Int64("keys_deleted", result.KeysDeleted),
		zap.Duration("duration", result.Duration),
	)

	return result, nil
}

// CleanupSuccessfulDeliveries is a convenience method to cleanup successful deliveries
// older than the specified duration.
//
// This is equivalent to calling CleanupDeliveries with a filter for successful
// deliveries created before the cutoff time.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) CleanupSuccessfulDeliveries(ctx context.Context, olderThan time.Duration, dryRun bool) (*CleanupResult, error) {
	cutoff := time.Now().Add(-olderThan)
	status := DeliveryStatusSuccess
	filter := &CleanupFilter{
		Status:        &status,
		CreatedBefore: &cutoff,
	}
	return m.CleanupDeliveries(ctx, filter, dryRun)
}

// CleanupFailedDeliveries is a convenience method to cleanup failed (non-dead-letter)
// deliveries older than the specified duration.
//
// Note: This does NOT cleanup dead letter deliveries. Use CleanupDeadLetterDeliveries
// for that purpose, or use CleanupDeliveries with the appropriate status filter.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) CleanupFailedDeliveries(ctx context.Context, olderThan time.Duration, dryRun bool) (*CleanupResult, error) {
	cutoff := time.Now().Add(-olderThan)
	status := DeliveryStatusFailed
	filter := &CleanupFilter{
		Status:        &status,
		CreatedBefore: &cutoff,
	}
	return m.CleanupDeliveries(ctx, filter, dryRun)
}

// CleanupDeadLetterDeliveries is a convenience method to cleanup dead letter deliveries
// older than the specified duration.
//
// This is typically used after dead letters have been reviewed/reprocessed.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) CleanupDeadLetterDeliveries(ctx context.Context, olderThan time.Duration, dryRun bool) (*CleanupResult, error) {
	cutoff := time.Now().Add(-olderThan)
	status := DeliveryStatusDeadLetter
	filter := &CleanupFilter{
		Status:        &status,
		CreatedBefore: &cutoff,
	}
	return m.CleanupDeliveries(ctx, filter, dryRun)
}

// CleanupAllCompletedDeliveries is a convenience method to cleanup both successful
// and failed deliveries (but not dead letters) older than the specified duration.
//
// This is useful for routine cleanup of processed deliveries while preserving
// dead letters for review.
//
// Thread Safety: This method is safe for concurrent use.
func (m *Manager) CleanupAllCompletedDeliveries(ctx context.Context, olderThan time.Duration, dryRun bool) (*CleanupResult, error) {
	cutoff := time.Now().Add(-olderThan)

	totalResult := &CleanupResult{
		DryRun: dryRun,
	}

	// Cleanup successful deliveries
	successStatus := DeliveryStatusSuccess
	successFilter := &CleanupFilter{
		Status:        &successStatus,
		CreatedBefore: &cutoff,
	}
	successResult, err := m.CleanupDeliveries(ctx, successFilter, dryRun)
	if err != nil {
		return nil, err
	}
	totalResult.DeliveriesDeleted += successResult.DeliveriesDeleted
	totalResult.Duration += successResult.Duration

	// Cleanup failed deliveries
	failedStatus := DeliveryStatusFailed
	failedFilter := &CleanupFilter{
		Status:        &failedStatus,
		CreatedBefore: &cutoff,
	}
	failedResult, err := m.CleanupDeliveries(ctx, failedFilter, dryRun)
	if err != nil {
		return nil, err
	}
	totalResult.DeliveriesDeleted += failedResult.DeliveriesDeleted
	totalResult.Duration += failedResult.Duration

	return totalResult, nil
}
