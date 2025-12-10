// Package internal provides the core webhook management implementation for go-hookd.
//
// This file implements the Manager, which is the main orchestration layer for webhook delivery.
// The Manager coordinates worker pools, delivery processing, and lifecycle management.
package hookd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/itsatony/go-cuserr"
	"go.uber.org/zap"
)

// Manager is the main webhook management orchestrator.
//
// It coordinates:
// - Worker pool for delivery processing
// - Repository for data persistence
// - Event bus for internal coordination
// - Circuit breakers for endpoint health
// - Graceful startup and shutdown
//
// Thread Safety: Manager is safe for concurrent use after Start() is called.
// EventBus is a simple interface for publishing events.
type EventBus interface {
	Publish(topic string, data any)
	Subscribe(topic string, handler func(any)) func()
}

// Manager is the main webhook management orchestrator.
type Manager struct {
	repo       Repository
	eventBus   EventBus
	ctx        context.Context
	config     *Config
	logger     *zap.Logger
	httpClient *http.Client
	cancel     context.CancelFunc
	workerSem  chan struct{}
	wg         sync.WaitGroup
	startedMu  sync.RWMutex
	started    bool
}

// ManagerOption is a functional option for configuring the Manager.
type ManagerOption func(*Manager) error

// NewManager creates a new Manager instance with the given configuration and options.
//
// The repository must be provided and will be used for all data operations.
// The config will be validated before the Manager is created.
//
// Example:
//
//	repo := NewMockRepository()
//	cfg := DefaultConfig()
//	manager, err := NewManager(cfg, repo,
//	    WithLogger(logger),
//	    WithHTTPClient(client),
//	)
func NewManager(config *Config, repo Repository, opts ...ManagerOption) (*Manager, error) {
	if config == nil {
		return nil, NewConfigurationError("config", ErrMsgMissingConfig)
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	if repo == nil {
		return nil, NewConfigurationError("repository", "repository is required")
	}

	// Create default logger if not provided
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, NewConfigurationError("logger", "failed to create default logger: "+err.Error())
	}

	// Create default HTTP client with timeout
	httpClient := &http.Client{
		Timeout: config.DeliveryTimeout(),
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Create no-op event bus by default
	var eventBus EventBus = &noOpEventBus{}

	m := &Manager{
		config:     config,
		repo:       repo,
		eventBus:   eventBus,
		logger:     logger,
		httpClient: httpClient,
		workerSem:  make(chan struct{}, config.WorkerCount),
	}

	// Apply options
	for _, opt := range opts {
		if err := opt(m); err != nil {
			return nil, err
		}
	}

	return m, nil
}

// WithLogger sets a custom logger for the Manager.
func WithLogger(logger *zap.Logger) ManagerOption {
	return func(m *Manager) error {
		if logger == nil {
			return NewConfigurationError("logger", "logger cannot be nil")
		}
		m.logger = logger
		return nil
	}
}

// WithHTTPClient sets a custom HTTP client for webhook delivery.
func WithHTTPClient(client *http.Client) ManagerOption {
	return func(m *Manager) error {
		if client == nil {
			return NewConfigurationError("http_client", "HTTP client cannot be nil")
		}
		m.httpClient = client
		return nil
	}
}

// WithEventBus sets a custom event bus for internal coordination.
func WithEventBus(eventBus EventBus) ManagerOption {
	return func(m *Manager) error {
		if eventBus == nil {
			return NewConfigurationError("event_bus", "event bus cannot be nil")
		}
		m.eventBus = eventBus
		return nil
	}
}

// =============================================================================
// LIFECYCLE MANAGEMENT
// =============================================================================

// Start starts the Manager and begins processing deliveries.
//
// This method:
// - Validates the Manager is not already started
// - Creates the context for lifecycle management
// - Starts the worker pool
// - Returns immediately (non-blocking)
//
// The Manager will continue processing until Stop() is called.
func (m *Manager) Start(ctx context.Context) error {
	m.startedMu.Lock()
	defer m.startedMu.Unlock()

	if m.started {
		return cuserr.NewValidationError("manager", ErrMsgManagerAlreadyStarted)
	}

	// Create cancellable context
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Start worker pool
	for i := 0; i < m.config.WorkerCount; i++ {
		m.wg.Add(1)
		go m.workerLoop(i)
	}

	m.started = true
	m.logger.Info("manager started",
		zap.Int("worker_count", m.config.WorkerCount),
		zap.Int("poll_interval_ms", m.config.QueuePollInterval),
	)

	return nil
}

// Stop gracefully stops the Manager and waits for all workers to finish.
//
// This method:
// - Cancels the context to signal workers to stop
// - Waits for all workers to complete their current work
// - Closes the repository connection
// - Blocks until shutdown is complete
//
// After Stop() returns, the Manager cannot be restarted.
// Calling Stop() before Start() is safe and returns nil.
func (m *Manager) Stop() error {
	m.startedMu.Lock()
	if !m.started {
		m.startedMu.Unlock()
		// Safe to stop before start
		return nil
	}
	m.startedMu.Unlock()

	m.logger.Info("stopping manager (graceful shutdown)")

	// Cancel context to signal workers
	m.cancel()

	// Wait for all workers to finish
	m.wg.Wait()

	// Close repository
	if err := m.repo.Close(); err != nil {
		m.logger.Error("error closing repository", zap.Error(err))
		return err
	}

	m.logger.Info("manager stopped successfully")
	return nil
}

// IsStarted returns true if the Manager is currently running.
func (m *Manager) IsStarted() bool {
	m.startedMu.RLock()
	defer m.startedMu.RUnlock()
	return m.started
}

// =============================================================================
// WORKER POOL
// =============================================================================

// workerLoop is the main loop for a delivery worker.
//
// Each worker:
// - Polls the repository for pending deliveries
// - Processes deliveries (with circuit breaker checks)
// - Updates delivery status
// - Publishes events
// - Handles retries and dead letter queue.
func (m *Manager) workerLoop(workerID int) {
	defer m.wg.Done()

	m.logger.Debug("worker started", zap.Int("worker_id", workerID))

	pollInterval := time.Duration(m.config.QueuePollInterval) * time.Millisecond
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Debug("worker stopping", zap.Int("worker_id", workerID))
			return

		case <-ticker.C:
			// Acquire semaphore slot
			m.workerSem <- struct{}{}

			// Process deliveries
			m.processDeliveries(workerID)

			// Release semaphore slot
			<-m.workerSem
		}
	}
}

// processDeliveries fetches and processes pending deliveries.
func (m *Manager) processDeliveries(workerID int) {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()

	// Get pending deliveries (SKIP LOCKED in repository)
	deliveries, err := m.repo.GetPendingDeliveries(ctx, 1)
	if err != nil {
		m.logger.Error("failed to get pending deliveries",
			zap.Int("worker_id", workerID),
			zap.Error(err),
		)
		return
	}

	// Process each delivery
	for _, delivery := range deliveries {
		m.processDelivery(ctx, delivery)
	}
}

// processDelivery processes a single delivery.
func (m *Manager) processDelivery(ctx context.Context, delivery *Delivery) {
	var sub *Subscription
	var targetURL string

	// Check if this is an inline delivery (no subscription)
	if delivery.SubscriptionID == "" {
		// Inline delivery - URL and Secret are on the delivery itself
		if delivery.URL == "" {
			m.logger.Error("inline delivery missing URL",
				zap.String("delivery_id", delivery.ID),
			)
			return
		}
		targetURL = delivery.URL
	} else {
		// Subscription-based delivery
		var err error
		sub, err = m.repo.GetSubscription(ctx, delivery.SubscriptionID)
		if err != nil {
			m.logger.Error("failed to get subscription",
				zap.String("delivery_id", delivery.ID),
				zap.String("subscription_id", delivery.SubscriptionID),
				zap.Error(err),
			)
			return
		}

		// Check if subscription is active
		if sub.Status != SubscriptionStatusActive {
			m.logger.Warn("skipping delivery for inactive subscription",
				zap.String("delivery_id", delivery.ID),
				zap.String("subscription_id", delivery.SubscriptionID),
				zap.String("status", sub.Status),
			)
			return
		}
		targetURL = sub.URL
	}

	// Check circuit breaker (using target URL)
	cbState, err := m.repo.GetCircuitBreakerState(ctx, targetURL)
	if err != nil {
		m.logger.Error("failed to get circuit breaker state",
			zap.String("delivery_id", delivery.ID),
			zap.String("url", targetURL),
			zap.Error(err),
		)
	}

	if cbState != nil && cbState.State == CircuitBreakerStateOpen {
		// Circuit is open, check if we should retry
		if cbState.NextRetryAt.After(time.Now()) {
			m.logger.Debug("circuit breaker open, skipping delivery",
				zap.String("delivery_id", delivery.ID),
				zap.String("url", targetURL),
				zap.Time("next_retry_at", cbState.NextRetryAt),
			)
			return
		}

		// Transition to half-open
		cbState.State = CircuitBreakerStateHalfOpen
		cbState.SuccessCount = 0
		if err := m.repo.UpdateCircuitBreakerState(ctx, cbState); err != nil {
			m.logger.Error("failed to update circuit breaker state",
				zap.String("delivery_id", delivery.ID),
				zap.Error(err),
			)
		}
	}

	// Attempt delivery (sub may be nil for inline deliveries)
	m.attemptDelivery(ctx, delivery, sub)
}

// attemptDelivery attempts to deliver a webhook to the subscription endpoint.
// For inline deliveries, sub may be nil - delivery.URL and delivery.Secret are used instead.
func (m *Manager) attemptDelivery(ctx context.Context, delivery *Delivery, sub *Subscription) {
	delivery.AttemptCount++
	attemptNumber := delivery.AttemptCount

	// Build log fields (subscription_id may be empty for inline deliveries)
	logFields := []zap.Field{
		zap.String("delivery_id", delivery.ID),
		zap.Int("attempt_number", attemptNumber),
		zap.Int("max_attempts", delivery.MaxAttempts),
	}
	if sub != nil {
		logFields = append(logFields, zap.String("subscription_id", sub.ID))
	} else {
		logFields = append(logFields, zap.String("url", delivery.URL))
	}
	m.logger.Info("attempting delivery", logFields...)

	// Publish delivery started event
	m.publishDeliveryEvent(EventTopicDeliveryStarted, delivery, sub, nil)

	// Create delivery attempt record
	attemptID, err := GenerateAttemptID()
	if err != nil {
		m.logger.Error("failed to generate attempt ID",
			zap.String("delivery_id", delivery.ID),
			zap.Error(err),
		)
		// Can't continue without attempt ID, mark delivery as failed
		delivery.Status = DeliveryStatusFailed
		if updateErr := m.repo.UpdateDelivery(ctx, delivery); updateErr != nil {
			m.logger.Error("failed to update delivery status after attempt ID generation failure",
				zap.String("delivery_id", delivery.ID),
				zap.Error(updateErr),
			)
		}
		return
	}
	attempt := &DeliveryAttempt{
		ID:            attemptID,
		DeliveryID:    delivery.ID,
		AttemptNumber: attemptNumber,
		AttemptedAt:   time.Now(),
	}

	// Create separate context with timeout for HTTP request
	// This allows in-flight deliveries to complete during graceful shutdown
	httpCtx, httpCancel := context.WithTimeout(context.Background(), m.config.DeliveryTimeout())
	defer httpCancel()

	// Make HTTP request
	startTime := time.Now()
	statusCode, responseBody, responseHeaders, err := m.executeWebhookRequest(httpCtx, delivery, sub)
	duration := time.Since(startTime)

	attempt.DurationMs = duration.Milliseconds()
	attempt.StatusCode = statusCode
	attempt.ResponseBody = truncateString(responseBody, MaxResponseBodyLength)
	attempt.ResponseHeaders = responseHeaders

	if err != nil {
		attempt.Error = err.Error()
	}

	// Save attempt
	if saveErr := m.repo.CreateDeliveryAttempt(ctx, attempt); saveErr != nil {
		m.logger.Error("failed to save delivery attempt",
			zap.String("delivery_id", delivery.ID),
			zap.Error(saveErr),
		)
	}

	// Handle result
	if err == nil && statusCode >= 200 && statusCode < 300 {
		// Success
		m.handleDeliverySuccess(ctx, delivery, sub, attempt)
	} else {
		// Failure
		m.handleDeliveryFailure(ctx, delivery, sub, attempt, err)
	}
}

// executeWebhookRequest makes the HTTP request to the webhook endpoint.
// For inline deliveries, sub may be nil - delivery.URL and delivery.Secret are used instead.
func (m *Manager) executeWebhookRequest(ctx context.Context, delivery *Delivery, sub *Subscription) (int, string, map[string]string, error) {
	// Marshal payload
	payloadJSON, err := marshalJSONB(delivery.Payload)
	if err != nil {
		// marshalJSONB already returns cuserr.InternalError
		return 0, "", nil, err
	}

	// Determine target URL and secret (subscription-based or inline delivery)
	var targetURL, secret string
	var customHeaders map[string]string
	if sub != nil {
		targetURL = sub.URL
		secret = sub.Secret
		customHeaders = sub.Headers
	} else {
		// Inline delivery - use delivery fields
		targetURL = delivery.URL
		secret = delivery.Secret
	}

	// Create request with payload body
	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(payloadJSON))
	if err != nil {
		return 0, "", nil, cuserr.NewInternalError("http_client", err,
			cuserr.WithMetadata("operation", "create_request"),
			cuserr.WithMetadata("url", targetURL),
		)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "go-hookd/0.1.0")

	// Add custom headers from subscription (if any)
	for key, value := range customHeaders {
		req.Header.Set(key, value)
	}

	// Calculate and add signature
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := calculateSignature(secret, timestamp, payloadJSON)
	req.Header.Set(HeaderSignature, signature)
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderDeliveryID, delivery.ID)
	req.Header.Set(HeaderSubscriptionID, delivery.SubscriptionID) // Empty for inline
	req.Header.Set(HeaderEventType, delivery.EventType)
	req.Header.Set(HeaderAttemptNumber, fmt.Sprintf("%d", delivery.AttemptCount))

	// Make request
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", nil, cuserr.NewInternalError("http_client", err,
			cuserr.WithMetadata("operation", "read_response_body"),
		)
	}

	// Extract response headers
	responseHeaders := make(map[string]string)
	for key := range resp.Header {
		responseHeaders[key] = resp.Header.Get(key)
	}

	return resp.StatusCode, string(body), responseHeaders, nil
}

// handleDeliverySuccess handles a successful delivery.
// For inline deliveries, sub may be nil.
func (m *Manager) handleDeliverySuccess(ctx context.Context, delivery *Delivery, sub *Subscription, attempt *DeliveryAttempt) {
	m.logger.Info("delivery succeeded",
		zap.String("delivery_id", delivery.ID),
		zap.Int("attempt_number", attempt.AttemptNumber),
		zap.Int("status_code", attempt.StatusCode),
	)

	// Update delivery status
	delivery.Status = DeliveryStatusSuccess
	now := time.Now()
	delivery.CompletedAt = &now

	if err := m.repo.UpdateDelivery(ctx, delivery); err != nil {
		m.logger.Error("failed to update delivery status",
			zap.String("delivery_id", delivery.ID),
			zap.Error(err),
		)
	}

	// Update circuit breaker (record success) - use sub.URL or delivery.URL for inline
	var endpoint string
	if sub != nil {
		endpoint = sub.URL
	} else {
		endpoint = delivery.URL
	}
	m.updateCircuitBreakerSuccess(ctx, endpoint)

	// Publish success event
	m.publishDeliveryEvent(EventTopicDeliverySuccess, delivery, sub, attempt)
}

// handleDeliveryFailure handles a failed delivery.
// For inline deliveries, sub may be nil.
func (m *Manager) handleDeliveryFailure(ctx context.Context, delivery *Delivery, sub *Subscription, attempt *DeliveryAttempt, err error) {
	m.logger.Warn("delivery failed",
		zap.String("delivery_id", delivery.ID),
		zap.Int("attempt_number", attempt.AttemptNumber),
		zap.Int("status_code", attempt.StatusCode),
		zap.Error(err),
	)

	// Update circuit breaker (record failure) - use sub.URL or delivery.URL for inline
	var endpoint string
	if sub != nil {
		endpoint = sub.URL
	} else {
		endpoint = delivery.URL
	}
	m.updateCircuitBreakerFailure(ctx, endpoint)

	// Check if status code is non-retryable (4xx except 408, 429)
	isNonRetryable := attempt.StatusCode >= 400 && attempt.StatusCode < 500 &&
		attempt.StatusCode != 408 && attempt.StatusCode != 429

	// Check if we should retry
	if delivery.AttemptCount >= delivery.MaxAttempts || isNonRetryable {
		// Exhausted retries or non-retryable error, move to dead letter
		reason := ""
		if isNonRetryable {
			reason = fmt.Sprintf("non-retryable status code: %d", attempt.StatusCode)
			m.logger.Error("delivery failed with non-retryable status, moving to dead letter",
				zap.String("delivery_id", delivery.ID),
				zap.Int("status_code", attempt.StatusCode),
			)
		} else {
			reason = fmt.Sprintf("exhausted %d retry attempts", delivery.AttemptCount)
			m.logger.Error("delivery exhausted retries, moving to dead letter",
				zap.String("delivery_id", delivery.ID),
				zap.Int("attempts", delivery.AttemptCount),
			)
		}

		if err := m.repo.MoveToDeadLetter(ctx, delivery.ID, reason); err != nil {
			m.logger.Error("failed to move delivery to dead letter",
				zap.String("delivery_id", delivery.ID),
				zap.Error(err),
			)
		}

		// Publish dead letter event
		m.publishDeliveryEvent(EventTopicDeliveryDeadLetter, delivery, sub, attempt)
	} else {
		// Schedule retry - use subscription retry policy if available, else default
		var retryPolicy *RetryPolicy
		if sub != nil {
			retryPolicy = sub.RetryPolicy
		}
		if retryPolicy == nil {
			retryPolicy = DefaultRetryPolicy()
		}

		backoff := CalculateBackoff(delivery.AttemptCount, retryPolicy)
		nextRetryAt := time.Now().Add(backoff)
		delivery.Status = DeliveryStatusPending // Keep as pending so it gets picked up for retry
		delivery.NextRetryAt = &nextRetryAt

		if err := m.repo.UpdateDelivery(ctx, delivery); err != nil {
			m.logger.Error("failed to schedule retry",
				zap.String("delivery_id", delivery.ID),
				zap.Error(err),
			)
		}

		m.logger.Info("delivery retry scheduled",
			zap.String("delivery_id", delivery.ID),
			zap.Time("next_retry_at", nextRetryAt),
			zap.Duration("backoff", backoff),
		)

		// Publish retry event
		m.publishDeliveryEvent(EventTopicMetricsRetryTriggered, delivery, sub, attempt)
	}

	// Publish failure event
	m.publishDeliveryEvent(EventTopicDeliveryFailed, delivery, sub, attempt)
}

// =============================================================================
// CIRCUIT BREAKER MANAGEMENT
// =============================================================================

// updateCircuitBreakerSuccess records a successful delivery for circuit breaker.
func (m *Manager) updateCircuitBreakerSuccess(ctx context.Context, endpoint string) {
	state, err := m.repo.GetCircuitBreakerState(ctx, endpoint)
	if err != nil {
		m.logger.Error("failed to get circuit breaker state",
			zap.String("endpoint", endpoint),
			zap.Error(err),
		)
		return
	}

	if state.State == CircuitBreakerStateHalfOpen {
		// In half-open, increment success count
		state.SuccessCount++

		if state.SuccessCount >= m.config.CircuitBreakerHalfOpenRequests {
			// Transition to closed
			state.State = CircuitBreakerStateClosed
			state.FailureCount = 0
			state.SuccessCount = 0

			m.logger.Info("circuit breaker closed",
				zap.String("endpoint", endpoint),
			)

			// Publish circuit breaker event
			m.publishCircuitBreakerEvent(EventTopicCircuitClosed, state)
		}
	} else if state.State == CircuitBreakerStateOpen {
		// Should not happen, but reset to closed
		state.State = CircuitBreakerStateClosed
		state.FailureCount = 0
		state.SuccessCount = 0
	} else {
		// Closed state, reset failure count
		state.FailureCount = 0
	}

	if err := m.repo.UpdateCircuitBreakerState(ctx, state); err != nil {
		m.logger.Error("failed to update circuit breaker state",
			zap.String("endpoint", endpoint),
			zap.Error(err),
		)
	}
}

// updateCircuitBreakerFailure records a failed delivery for circuit breaker.
func (m *Manager) updateCircuitBreakerFailure(ctx context.Context, endpoint string) {
	state, err := m.repo.GetCircuitBreakerState(ctx, endpoint)
	if err != nil {
		m.logger.Error("failed to get circuit breaker state",
			zap.String("endpoint", endpoint),
			zap.Error(err),
		)
		return
	}

	state.FailureCount++
	state.LastFailure = time.Now()

	if state.State == CircuitBreakerStateHalfOpen {
		// In half-open, failure transitions back to open
		state.State = CircuitBreakerStateOpen
		state.SuccessCount = 0
		state.OpenedAt = time.Now()
		state.NextRetryAt = time.Now().Add(m.config.CircuitBreakerTimeout())

		m.logger.Warn("circuit breaker reopened",
			zap.String("endpoint", endpoint),
		)

		// Publish circuit breaker event
		m.publishCircuitBreakerEvent(EventTopicCircuitOpened, state)
	} else if state.State == CircuitBreakerStateClosed {
		// In closed, check if we should open
		if state.FailureCount >= m.config.CircuitBreakerThreshold {
			state.State = CircuitBreakerStateOpen
			state.OpenedAt = time.Now()
			state.NextRetryAt = time.Now().Add(m.config.CircuitBreakerTimeout())

			m.logger.Warn("circuit breaker opened",
				zap.String("endpoint", endpoint),
				zap.Int("failure_count", state.FailureCount),
			)

			// Publish circuit breaker event
			m.publishCircuitBreakerEvent(EventTopicCircuitOpened, state)
		}
	}

	if err := m.repo.UpdateCircuitBreakerState(ctx, state); err != nil {
		m.logger.Error("failed to update circuit breaker state",
			zap.String("endpoint", endpoint),
			zap.Error(err),
		)
	}
}

// =============================================================================
// EVENT PUBLISHING
// =============================================================================

// publishDeliveryEvent publishes a delivery event to the event bus.
func (m *Manager) publishDeliveryEvent(topic string, delivery *Delivery, sub *Subscription, attempt *DeliveryAttempt) {
	event := &DeliveryEvent{
		DeliveryID:     delivery.ID,
		SubscriptionID: delivery.SubscriptionID,
		TenantID:       delivery.TenantID,
		EventType:      delivery.EventType,
		Status:         delivery.Status,
		Timestamp:      time.Now(),
		Metadata: map[string]any{
			"attempt_count": delivery.AttemptCount,
			"max_attempts":  delivery.MaxAttempts,
		},
	}

	if attempt != nil {
		event.Metadata["attempt_number"] = attempt.AttemptNumber
		event.Metadata["status_code"] = attempt.StatusCode
		event.Metadata["duration_ms"] = attempt.DurationMs
		if attempt.Error != "" {
			event.Metadata["error"] = attempt.Error
		}
	}

	m.eventBus.Publish(topic, event)
}

// publishCircuitBreakerEvent publishes a circuit breaker event to the event bus.
func (m *Manager) publishCircuitBreakerEvent(topic string, state *CircuitBreakerState) {
	event := &CircuitBreakerEvent{
		Endpoint:  state.Endpoint,
		State:     state.State,
		Timestamp: time.Now(),
	}

	m.eventBus.Publish(topic, event)
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// truncateString truncates a string to the given maximum length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// =============================================================================
// NO-OP EVENT BUS
// =============================================================================

// noOpEventBus is a no-op implementation of EventBus for testing/default use.
type noOpEventBus struct{}

// Publish is a no-op.
func (n *noOpEventBus) Publish(topic string, data any) {}

// Subscribe is a no-op.
func (n *noOpEventBus) Subscribe(topic string, handler func(any)) func() {
	return func() {}
}
