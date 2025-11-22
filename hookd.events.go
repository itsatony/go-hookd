// Package internal provides the core webhook management implementation for go-hookd.
//
// This file implements delivery queueing and event management operations.
// These functions allow applications to queue webhook deliveries and query their status.
package hookd

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// =============================================================================
// DELIVERY QUEUEING AND MANAGEMENT
// =============================================================================

// QueueDelivery queues a new webhook delivery for processing.
//
// This is the primary entry point for sending webhooks. The delivery will be:
// - Validated and persisted to the database
// - Picked up by a worker for delivery
// - Retried according to the subscription's retry policy if it fails
//
// The operation is idempotent when an idempotency key is provided - duplicate
// requests with the same key within the TTL window will return the existing delivery.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - req: Delivery request with required fields
//
// Returns the queued delivery or an error.
//
// Errors:
//   - ValidationError: If the request is invalid
//   - NotFoundError: If the subscription doesn't exist
//   - ConflictError: If idempotency key was used for a different delivery
//   - DatabaseError: If persistence fails
//
// Example:
//
//	delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
//	    SubscriptionID: "sub_6ByTSYmGzT2c8K3xN1fP2",
//	    EventType:      "user.created",
//	    Payload: map[string]interface{}{
//	        "user_id": "123",
//	        "email":   "user@example.com",
//	    },
//	    IdempotencyKey: StringPtr("evt_user_created_123"),
//	})
func (m *Manager) QueueDelivery(ctx context.Context, req *QueueDeliveryRequest) (*Delivery, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		m.logger.Error("invalid delivery request",
			zap.Error(err),
			zap.String("subscription_id", req.SubscriptionID),
		)
		return nil, err
	}

	// Get subscription
	sub, err := m.GetSubscription(ctx, req.SubscriptionID)
	if err != nil {
		return nil, err
	}

	// Check if subscription is active
	if sub.Status != SubscriptionStatusActive {
		m.logger.Warn("subscription not active",
			zap.String("subscription_id", sub.ID),
			zap.String("status", sub.Status),
		)
		return nil, NewValidationError("subscription", ErrMsgSubscriptionNotActive)
	}

	// Store idempotency key FIRST to prevent race condition (TOCTOU)
	// The database unique constraint will enforce atomicity
	if req.IdempotencyKey != "" {
		expiresAt := time.Now().Add(time.Duration(m.config.IdempotencyTTLHours) * time.Hour)
		err := m.repo.StoreIdempotencyKey(ctx, req.IdempotencyKey, req.SubscriptionID, expiresAt)
		if err != nil {
			// Check if it's a conflict (duplicate key)
			if IsConflictError(err) {
				m.logger.Info("idempotent delivery request detected",
					zap.String("idempotency_key", req.IdempotencyKey),
					zap.String("subscription_id", req.SubscriptionID),
				)
				return nil, NewIdempotencyError(req.IdempotencyKey)
			}

			// Other database error
			m.logger.Error("failed to store idempotency key",
				zap.Error(err),
				zap.String("idempotency_key", req.IdempotencyKey),
			)
			return nil, err
		}
	}

	// Generate delivery ID
	id, err := GenerateDeliveryID()
	if err != nil {
		m.logger.Error("failed to generate delivery ID",
			zap.Error(err),
		)
		// GenerateDeliveryID already returns cuserr.InternalError
		return nil, err
	}

	// Create delivery
	now := time.Now()
	delivery := &Delivery{
		ID:             id,
		SubscriptionID: req.SubscriptionID,
		TenantID:       sub.TenantID,
		EventType:      req.EventType,
		Payload:        req.Payload,
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    sub.RetryPolicy.MaxAttempts,
		NextRetryAt:    &now, // Available immediately
		CreatedAt:      now,
	}

	// Persist delivery
	if err := m.repo.CreateDelivery(ctx, delivery); err != nil {
		m.logger.Error("failed to create delivery",
			zap.Error(err),
			zap.String("subscription_id", req.SubscriptionID),
			zap.String("event_type", req.EventType),
		)
		// Repository already returns cuserr errors
		return nil, err
	}

	m.logger.Info(LogMsgDeliveryQueued,
		zap.String("delivery_id", delivery.ID),
		zap.String("subscription_id", delivery.SubscriptionID),
		zap.String("tenant_id", delivery.TenantID),
		zap.String("event_type", delivery.EventType),
	)

	// Publish delivery queued event
	m.publishDeliveryEvent(EventTopicDeliveryQueued, delivery, sub, nil)

	return delivery, nil
}

// GetDelivery retrieves a delivery by ID.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - id: Delivery ID (format: dlv_<nanoID>)
//
// Returns the delivery or an error if not found.
//
// Errors:
//   - ValidationError: If ID is empty
//   - NotFoundError: If the delivery doesn't exist
//   - DatabaseError: If retrieval fails
//
// Example:
//
//	delivery, err := manager.GetDelivery(ctx, "dlv_9Kj2BxYzT3c5K8xM4fQ7")
func (m *Manager) GetDelivery(ctx context.Context, id string) (*Delivery, error) {
	if id == "" {
		return nil, NewValidationError("id", ErrMsgDeliveryIDRequired)
	}

	delivery, err := m.repo.GetDelivery(ctx, id)
	if err != nil {
		m.logger.Error("failed to get delivery",
			zap.Error(err),
			zap.String("delivery_id", id),
		)
		return nil, err
	}

	if delivery == nil {
		return nil, NewDeliveryNotFoundError(id)
	}

	return delivery, nil
}

// GetDeliveryAttempts retrieves all attempts for a delivery.
//
// Returns attempts in chronological order (oldest first).
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - deliveryID: Delivery ID
//
// Returns a list of delivery attempts.
//
// Errors:
//   - ValidationError: If deliveryID is empty
//   - DatabaseError: If retrieval fails
//
// Example:
//
//	attempts, err := manager.GetDeliveryAttempts(ctx, "dlv_9Kj2BxYzT3c5K8xM4fQ7")
func (m *Manager) GetDeliveryAttempts(ctx context.Context, deliveryID string) ([]*DeliveryAttempt, error) {
	if deliveryID == "" {
		return nil, NewValidationError("delivery_id", ErrMsgDeliveryIDRequired)
	}

	attempts, err := m.repo.GetDeliveryAttempts(ctx, deliveryID)
	if err != nil {
		m.logger.Error("failed to get delivery attempts",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		// Repository already returns cuserr errors
		return nil, err
	}

	return attempts, nil
}

// RetryDelivery manually retries a failed delivery.
//
// This operation:
// - Verifies the delivery exists and is in a retryable state
// - Resets the delivery status to pending
// - Schedules it for immediate retry
// - Does NOT count against the retry limit
//
// Use this for manual intervention when deliveries fail due to temporary
// issues that have been resolved.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - deliveryID: Delivery ID to retry
//
// Returns the updated delivery or an error.
//
// Errors:
//   - ValidationError: If deliveryID is empty
//   - NotFoundError: If the delivery doesn't exist
//   - ValidationError: If the delivery cannot be retried (e.g., already succeeded)
//   - DatabaseError: If update fails
//
// Example:
//
//	delivery, err := manager.RetryDelivery(ctx, "dlv_9Kj2BxYzT3c5K8xM4fQ7")
func (m *Manager) RetryDelivery(ctx context.Context, deliveryID string) (*Delivery, error) {
	if deliveryID == "" {
		return nil, NewValidationError("delivery_id", ErrMsgDeliveryIDRequired)
	}

	// Get delivery
	delivery, err := m.GetDelivery(ctx, deliveryID)
	if err != nil {
		return nil, err
	}

	// Check if delivery can be retried
	if delivery.Status == DeliveryStatusSuccess {
		return nil, NewValidationError("status", ErrMsgCannotRetrySuccessful)
	}

	// Reset delivery for retry
	now := time.Now()
	delivery.Status = DeliveryStatusPending
	delivery.NextRetryAt = &now

	// Update delivery
	if err := m.repo.UpdateDelivery(ctx, delivery); err != nil {
		m.logger.Error("failed to update delivery for retry",
			zap.Error(err),
			zap.String("delivery_id", deliveryID),
		)
		// Repository already returns cuserr errors
		return nil, err
	}

	m.logger.Info("delivery scheduled for manual retry",
		zap.String("delivery_id", delivery.ID),
		zap.String("subscription_id", delivery.SubscriptionID),
	)

	return delivery, nil
}
