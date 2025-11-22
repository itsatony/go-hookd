// Package internal provides the core webhook management implementation for go-hookd.
//
// This file implements subscription management operations (CRUD).
// All operations are thread-safe and work with the Manager's lifecycle.
package hookd

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// =============================================================================
// SUBSCRIPTION CRUD OPERATIONS
// =============================================================================

// CreateSubscription creates a new webhook subscription.
//
// This operation:
// - Validates the subscription configuration
// - Checks for duplicate subscriptions (same tenant + URL)
// - Initializes retry policy with defaults if not provided
// - Generates a unique subscription ID
// - Persists to the repository
// - Publishes an audit event
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - req: Subscription creation request with required fields
//
// Returns the created subscription or an error.
//
// Errors:
//   - ValidationError: If the request is invalid
//   - ConflictError: If a subscription already exists for this tenant+URL
//   - DatabaseError: If persistence fails
//
// Example:
//
//	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
//	    TenantID:   "tenant_123",
//	    URL:        "https://example.com/webhook",
//	    EventTypes: []string{"user.created", "user.updated"},
//	    Secret:     "secure_secret_key",
//	})
func (m *Manager) CreateSubscription(ctx context.Context, req *CreateSubscriptionRequest) (*Subscription, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		m.logger.Error("invalid subscription request",
			zap.Error(err),
			zap.String("tenant_id", req.TenantID),
		)
		return nil, err
	}

	// Check if subscription already exists
	existing, err := m.repo.GetSubscriptionByTenantAndURL(ctx, req.TenantID, req.URL)
	if err == nil && existing != nil {
		m.logger.Warn("subscription already exists",
			zap.String("tenant_id", req.TenantID),
			zap.String("url", req.URL),
			zap.String("existing_id", existing.ID),
		)
		return nil, NewSubscriptionExistsError(req.TenantID, req.URL)
	}

	// Generate subscription ID
	id, err := GenerateSubscriptionID()
	if err != nil {
		m.logger.Error("failed to generate subscription ID",
			zap.Error(err),
		)
		// GenerateSubscriptionID already returns cuserr.InternalError
		return nil, err
	}

	// Set default retry policy if not provided
	retryPolicy := req.RetryPolicy
	if retryPolicy == nil {
		retryPolicy = m.config.DefaultRetryPolicy()
	}

	// Validate retry policy
	if err := retryPolicy.Validate(); err != nil {
		m.logger.Error("invalid retry policy",
			zap.Error(err),
		)
		return nil, err
	}

	// Create subscription
	now := time.Now()
	sub := &Subscription{
		ID:          id,
		TenantID:    req.TenantID,
		URL:         normalizeURL(req.URL),
		EventTypes:  req.EventTypes,
		Secret:      req.Secret,
		RetryPolicy: retryPolicy,
		Headers:     req.Headers,
		Metadata:    req.Metadata,
		Status:      SubscriptionStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Persist subscription
	if err := m.repo.CreateSubscription(ctx, sub); err != nil {
		m.logger.Error("failed to create subscription",
			zap.Error(err),
			zap.String("tenant_id", req.TenantID),
			zap.String("url", req.URL),
		)
		// Repository already returns cuserr errors
		return nil, err
	}

	m.logger.Info(LogMsgSubscriptionCreated,
		zap.String("subscription_id", sub.ID),
		zap.String("tenant_id", sub.TenantID),
		zap.String("url", sub.URL),
		zap.Strings("event_types", sub.EventTypes),
	)

	// Publish audit event
	m.publishAuditEvent(EventTopicAuditSubscriptionCreated, sub)

	return sub, nil
}

// GetSubscription retrieves a subscription by ID.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - id: Subscription ID (format: sub_<nanoID>)
//
// Returns the subscription or an error if not found.
//
// Errors:
//   - NotFoundError: If the subscription doesn't exist
//   - DatabaseError: If retrieval fails
//
// Example:
//
//	sub, err := manager.GetSubscription(ctx, "sub_6ByTSYmGzT2c8K3xN1fP2")
func (m *Manager) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	if id == "" {
		return nil, NewValidationError("id", ErrMsgMissingSubscriptionID)
	}

	sub, err := m.repo.GetSubscription(ctx, id)
	if err != nil {
		m.logger.Error("failed to get subscription",
			zap.Error(err),
			zap.String("subscription_id", id),
		)
		return nil, err
	}

	if sub == nil {
		return nil, NewSubscriptionNotFoundError(id)
	}

	return sub, nil
}

// UpdateSubscription updates an existing subscription.
//
// This operation:
// - Validates the update request
// - Retrieves the existing subscription
// - Applies the updates (only non-nil/non-empty fields)
// - Validates the updated subscription
// - Persists changes to the repository
// - Publishes an audit event
//
// Note: TenantID cannot be changed after creation.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - id: Subscription ID to update
//   - req: Update request with fields to change
//
// Returns the updated subscription or an error.
//
// Errors:
//   - NotFoundError: If the subscription doesn't exist
//   - ValidationError: If the update request is invalid
//   - DatabaseError: If persistence fails
//
// Example:
//
//	sub, err := manager.UpdateSubscription(ctx, "sub_xyz", &UpdateSubscriptionRequest{
//	    URL:        StringPtr("https://new-url.com/webhook"),
//	    EventTypes: &[]string{"user.created"},
//	})
func (m *Manager) UpdateSubscription(ctx context.Context, id string, req *UpdateSubscriptionRequest) (*Subscription, error) {
	if id == "" {
		return nil, NewValidationError("id", ErrMsgMissingSubscriptionID)
	}

	// Get existing subscription
	sub, err := m.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}

	// Apply updates (only non-nil fields)
	updated := false

	if req.URL != nil && *req.URL != "" {
		normalizedURL := normalizeURL(*req.URL)
		if normalizedURL != sub.URL {
			sub.URL = normalizedURL
			updated = true
		}
	}

	if req.EventTypes != nil && len(*req.EventTypes) > 0 {
		sub.EventTypes = *req.EventTypes
		updated = true
	}

	if req.RetryPolicy != nil {
		if err := req.RetryPolicy.Validate(); err != nil {
			return nil, err
		}
		sub.RetryPolicy = req.RetryPolicy
		updated = true
	}

	if req.Headers != nil {
		sub.Headers = *req.Headers
		updated = true
	}

	if req.Metadata != nil {
		sub.Metadata = *req.Metadata
		updated = true
	}

	if req.Status != nil && *req.Status != "" {
		// Validate status value
		status := *req.Status
		if status != SubscriptionStatusActive &&
			status != SubscriptionStatusPaused &&
			status != SubscriptionStatusDisabled {
			return nil, NewValidationError("status", ErrMsgInvalidStatus)
		}
		sub.Status = status
		updated = true
	}

	// If nothing changed, return existing
	if !updated {
		return sub, nil
	}

	// Update timestamp
	sub.UpdatedAt = time.Now()

	// Persist update
	if err := m.repo.UpdateSubscription(ctx, sub); err != nil {
		m.logger.Error("failed to update subscription",
			zap.Error(err),
			zap.String("subscription_id", id),
		)
		// Repository already returns cuserr errors
		return nil, err
	}

	m.logger.Info(LogMsgSubscriptionUpdated,
		zap.String("subscription_id", sub.ID),
		zap.String("tenant_id", sub.TenantID),
	)

	// Publish audit event
	m.publishAuditEvent(EventTopicAuditSubscriptionUpdated, sub)

	return sub, nil
}

// DeleteSubscription deletes a subscription.
//
// This operation:
// - Retrieves the subscription to verify it exists
// - Deletes from the repository (cascade deletes related data)
// - Publishes an audit event
//
// Note: This is a hard delete. Consider using UpdateSubscription to set
// status to "disabled" for soft delete behavior.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - id: Subscription ID to delete
//
// Returns an error if deletion fails.
//
// Errors:
//   - NotFoundError: If the subscription doesn't exist
//   - DatabaseError: If deletion fails
//
// Example:
//
//	err := manager.DeleteSubscription(ctx, "sub_6ByTSYmGzT2c8K3xN1fP2")
func (m *Manager) DeleteSubscription(ctx context.Context, id string) error {
	if id == "" {
		return NewValidationError("id", ErrMsgMissingSubscriptionID)
	}

	// Get subscription to verify it exists and for audit
	sub, err := m.GetSubscription(ctx, id)
	if err != nil {
		return err
	}

	// Delete subscription
	if err := m.repo.DeleteSubscription(ctx, id); err != nil {
		m.logger.Error("failed to delete subscription",
			zap.Error(err),
			zap.String("subscription_id", id),
		)
		// Repository already returns cuserr errors
		return err
	}

	m.logger.Info(LogMsgSubscriptionDeleted,
		zap.String("subscription_id", sub.ID),
		zap.String("tenant_id", sub.TenantID),
	)

	// Publish audit event
	m.publishAuditEvent(EventTopicAuditSubscriptionDeleted, sub)

	return nil
}

// ListSubscriptions retrieves subscriptions with filtering.
//
// This operation supports filtering by:
// - Tenant ID (required)
// - Status (optional)
// - Event types (optional)
//
// Results are ordered by created_at DESC.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - filter: Filter criteria
//
// Returns a list of subscriptions matching the filter.
//
// Errors:
//   - ValidationError: If filter is invalid
//   - DatabaseError: If retrieval fails
//
// Example:
//
//	subs, err := manager.ListSubscriptions(ctx, &SubscriptionFilter{
//	    TenantID:   "tenant_123",
//	    Status:     StringPtr("active"),
//	    EventTypes: &[]string{"user.created"},
//	})
func (m *Manager) ListSubscriptions(ctx context.Context, filter *SubscriptionFilter) ([]*Subscription, error) {
	// Validate filter
	if filter == nil {
		return nil, NewValidationError("filter", "filter is required")
	}

	if filter.TenantID == "" {
		return nil, NewValidationError("tenant_id", ErrMsgMissingTenantID)
	}

	// List subscriptions
	subs, err := m.repo.ListSubscriptions(ctx, filter)
	if err != nil {
		m.logger.Error("failed to list subscriptions",
			zap.Error(err),
			zap.String("tenant_id", filter.TenantID),
		)
		// Repository already returns cuserr errors
		return nil, err
	}

	return subs, nil
}

// PauseSubscription pauses a subscription.
//
// This is a convenience method that sets the subscription status to "paused".
// Paused subscriptions do not receive new deliveries, but existing deliveries
// in the queue will still be processed.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - id: Subscription ID to pause
//
// Returns the updated subscription or an error.
//
// Errors:
//   - NotFoundError: If the subscription doesn't exist
//   - DatabaseError: If update fails
//
// Example:
//
//	sub, err := manager.PauseSubscription(ctx, "sub_6ByTSYmGzT2c8K3xN1fP2")
func (m *Manager) PauseSubscription(ctx context.Context, id string) (*Subscription, error) {
	status := SubscriptionStatusPaused
	return m.UpdateSubscription(ctx, id, &UpdateSubscriptionRequest{
		Status: &status,
	})
}

// ResumeSubscription resumes a paused subscription.
//
// This is a convenience method that sets the subscription status to "active".
// The subscription will start receiving new deliveries immediately.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - id: Subscription ID to resume
//
// Returns the updated subscription or an error.
//
// Errors:
//   - NotFoundError: If the subscription doesn't exist
//   - DatabaseError: If update fails
//
// Example:
//
//	sub, err := manager.ResumeSubscription(ctx, "sub_6ByTSYmGzT2c8K3xN1fP2")
func (m *Manager) ResumeSubscription(ctx context.Context, id string) (*Subscription, error) {
	status := SubscriptionStatusActive
	return m.UpdateSubscription(ctx, id, &UpdateSubscriptionRequest{
		Status: &status,
	})
}

// DisableSubscription disables a subscription.
//
// This is a convenience method that sets the subscription status to "disabled".
// Disabled subscriptions do not receive new deliveries and cannot be resumed
// without explicit update. This is effectively a soft-delete.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - id: Subscription ID to disable
//
// Returns the updated subscription or an error.
//
// Errors:
//   - NotFoundError: If the subscription doesn't exist
//   - DatabaseError: If update fails
//
// Example:
//
//	sub, err := manager.DisableSubscription(ctx, "sub_6ByTSYmGzT2c8K3xN1fP2")
func (m *Manager) DisableSubscription(ctx context.Context, id string) (*Subscription, error) {
	status := SubscriptionStatusDisabled
	return m.UpdateSubscription(ctx, id, &UpdateSubscriptionRequest{
		Status: &status,
	})
}

// publishAuditEvent publishes an audit event for subscription operations.
func (m *Manager) publishAuditEvent(topic string, sub *Subscription) {
	event := &AuditEvent{
		Type:       topic,
		ResourceID: sub.ID,
		TenantID:   sub.TenantID,
		Timestamp:  time.Now(),
		Metadata: map[string]interface{}{
			"url":         sub.URL,
			"event_types": sub.EventTypes,
			"status":      sub.Status,
		},
	}

	m.eventBus.Publish(topic, event)
}
