package hookd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIDPrefixes validates ID prefix constants.
func TestIDPrefixes(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{"subscription prefix", PrefixSubscription},
		{"delivery prefix", PrefixDelivery},
		{"attempt prefix", PrefixAttempt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Prefixes should not be empty
			assert.NotEmpty(t, tt.prefix, "prefix should not be empty")

			// Prefixes should be lowercase
			assert.Equal(t, strings.ToLower(tt.prefix), tt.prefix, "prefix should be lowercase")

			// Prefixes should be short (3-4 characters)
			assert.LessOrEqual(t, len(tt.prefix), 4, "prefix should be 3-4 characters")
			assert.GreaterOrEqual(t, len(tt.prefix), 3, "prefix should be 3-4 characters")

			// Prefixes should not contain underscores (underscore is separator)
			assert.NotContains(t, tt.prefix, "_", "prefix should not contain underscores")
		})
	}

	// Prefixes should be unique
	prefixes := []string{PrefixSubscription, PrefixDelivery, PrefixAttempt}
	uniquePrefixes := make(map[string]bool)
	for _, p := range prefixes {
		assert.False(t, uniquePrefixes[p], "prefix %s should be unique", p)
		uniquePrefixes[p] = true
	}
}

// TestSubscriptionStatuses validates subscription status constants.
func TestSubscriptionStatuses(t *testing.T) {
	statuses := []string{
		SubscriptionStatusActive,
		SubscriptionStatusPaused,
		SubscriptionStatusDisabled,
	}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			// Statuses should not be empty
			assert.NotEmpty(t, status, "status should not be empty")

			// Statuses should be lowercase
			assert.Equal(t, strings.ToLower(status), status, "status should be lowercase")

			// Statuses should be reasonable length
			assert.LessOrEqual(t, len(status), 20, "status should be reasonable length")
		})
	}

	// Statuses should be unique
	uniqueStatuses := make(map[string]bool)
	for _, status := range statuses {
		assert.False(t, uniqueStatuses[status], "status %s should be unique", status)
		uniqueStatuses[status] = true
	}
}

// TestDeliveryStatuses validates delivery status constants.
func TestDeliveryStatuses(t *testing.T) {
	statuses := []string{
		DeliveryStatusPending,
		DeliveryStatusSuccess,
		DeliveryStatusFailed,
		DeliveryStatusDeadLetter,
	}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			// Statuses should not be empty
			assert.NotEmpty(t, status, "status should not be empty")

			// Statuses should be lowercase with underscores
			assert.Equal(t, strings.ToLower(status), status, "status should be lowercase")

			// Statuses should be reasonable length
			assert.LessOrEqual(t, len(status), 20, "status should be reasonable length")
		})
	}

	// Statuses should be unique
	uniqueStatuses := make(map[string]bool)
	for _, status := range statuses {
		assert.False(t, uniqueStatuses[status], "status %s should be unique", status)
		uniqueStatuses[status] = true
	}
}

// TestCircuitBreakerStates validates circuit breaker state constants.
func TestCircuitBreakerStates(t *testing.T) {
	states := []string{
		CircuitBreakerStateClosed,
		CircuitBreakerStateHalfOpen,
		CircuitBreakerStateOpen,
	}

	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			// States should not be empty
			assert.NotEmpty(t, state, "state should not be empty")

			// States should be lowercase with underscores
			assert.Equal(t, strings.ToLower(state), state, "state should be lowercase")

			// States should be reasonable length
			assert.LessOrEqual(t, len(state), 20, "state should be reasonable length")
		})
	}

	// States should be unique
	uniqueStates := make(map[string]bool)
	for _, state := range states {
		assert.False(t, uniqueStates[state], "state %s should be unique", state)
		uniqueStates[state] = true
	}
}

// TestEventTopics validates event topic constants.
func TestEventTopics(t *testing.T) {
	topics := []string{
		// Delivery lifecycle
		EventTopicDeliveryQueued,
		EventTopicDeliveryStarted,
		EventTopicDeliverySuccess,
		EventTopicDeliveryFailed,
		EventTopicDeliveryDeadLetter,
		// Circuit breaker
		EventTopicCircuitOpened,
		EventTopicCircuitHalfOpen,
		EventTopicCircuitClosed,
		// Metrics
		EventTopicMetricsDeliveryAttempt,
		EventTopicMetricsRetryTriggered,
		EventTopicMetricsQueueDepth,
		// Audit
		EventTopicAuditSubscriptionCreated,
		EventTopicAuditSubscriptionUpdated,
		EventTopicAuditSubscriptionDeleted,
	}

	for _, topic := range topics {
		t.Run(topic, func(t *testing.T) {
			// Topics should not be empty
			assert.NotEmpty(t, topic, "topic should not be empty")

			// Topics should follow naming convention: category.action
			parts := strings.Split(topic, ".")
			assert.GreaterOrEqual(t, len(parts), 2, "topic should have at least 2 parts (category.action)")

			// Topics should be lowercase with underscores
			assert.Equal(t, strings.ToLower(topic), topic, "topic should be lowercase")

			// Topics should be reasonable length
			assert.LessOrEqual(t, len(topic), 50, "topic should be reasonable length")
		})
	}

	// Topics should be unique
	uniqueTopics := make(map[string]bool)
	for _, topic := range topics {
		assert.False(t, uniqueTopics[topic], "topic %s should be unique", topic)
		uniqueTopics[topic] = true
	}
}

// TestHTTPHeaders validates HTTP header constants.
func TestHTTPHeaders(t *testing.T) {
	headers := []string{
		HeaderSignature,
		HeaderTimestamp,
		HeaderDeliveryID,
		HeaderAttemptNumber,
		HeaderEventType,
	}

	for _, header := range headers {
		t.Run(header, func(t *testing.T) {
			// Headers should not be empty
			assert.NotEmpty(t, header, "header should not be empty")

			// Headers should follow convention: X-Webhook-*
			assert.True(t, strings.HasPrefix(header, "X-Webhook-"), "header should start with X-Webhook-")

			// Headers should use Title-Case
			parts := strings.Split(header, "-")
			for _, part := range parts {
				if len(part) > 0 {
					assert.True(t, part[0] >= 'A' && part[0] <= 'Z' || part == "X", "header parts should be title case")
				}
			}
		})
	}

	// Headers should be unique
	uniqueHeaders := make(map[string]bool)
	for _, header := range headers {
		assert.False(t, uniqueHeaders[header], "header %s should be unique", header)
		uniqueHeaders[header] = true
	}
}

// TestTableNames validates database table name constants.
func TestTableNames(t *testing.T) {
	tables := []string{
		TableSubscriptions,
		TableDeliveries,
		TableDeliveryAttempts,
		TableIdempotencyStore,
		TableCircuitBreakerState,
	}

	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			// Tables should not be empty
			assert.NotEmpty(t, table, "table should not be empty")

			// Tables should be lowercase with underscores
			assert.Equal(t, strings.ToLower(table), table, "table should be lowercase")

			// Tables should be reasonable length
			assert.LessOrEqual(t, len(table), 50, "table should be reasonable length")

			// Tables should not have spaces
			assert.NotContains(t, table, " ", "table should not contain spaces")
		})
	}

	// Tables should be unique
	uniqueTables := make(map[string]bool)
	for _, table := range tables {
		assert.False(t, uniqueTables[table], "table %s should be unique", table)
		uniqueTables[table] = true
	}
}

// TestDefaultConfigValues validates default configuration constants.
func TestDefaultConfigValues(t *testing.T) {
	t.Run("retry defaults", func(t *testing.T) {
		assert.Greater(t, DefaultMaxRetries, 0, "max retries should be positive")
		assert.Greater(t, DefaultInitialBackoffMs, 0, "initial backoff should be positive")
		assert.Greater(t, DefaultMaxBackoffMs, 0, "max backoff should be positive")
		assert.Greater(t, DefaultMaxBackoffMs, DefaultInitialBackoffMs, "max backoff should be greater than initial")
		assert.Greater(t, DefaultBackoffFactor, 1.0, "backoff factor should be greater than 1")
	})

	t.Run("worker pool defaults", func(t *testing.T) {
		assert.Greater(t, DefaultWorkerCount, 0, "worker count should be positive")
		assert.Greater(t, DefaultQueuePollIntervalMs, 0, "poll interval should be positive")
		assert.Greater(t, DefaultDeliveryTimeoutMs, 0, "delivery timeout should be positive")
	})

	t.Run("circuit breaker defaults", func(t *testing.T) {
		assert.Greater(t, DefaultCircuitBreakerThreshold, 0, "threshold should be positive")
		assert.Greater(t, DefaultCircuitBreakerTimeoutMs, 0, "timeout should be positive")
		assert.Greater(t, DefaultCircuitBreakerHalfOpenRequests, 0, "half open requests should be positive")
	})

	t.Run("idempotency defaults", func(t *testing.T) {
		assert.Greater(t, DefaultIdempotencyTTLHours, 0, "idempotency TTL should be positive")
	})
}

// TestValidationLimits validates validation limit constants.
func TestValidationLimits(t *testing.T) {
	limits := map[string]int{
		"MaxURLLength":                 MaxURLLength,
		"MaxEventTypeLength":           MaxEventTypeLength,
		"MaxTenantIDLength":            MaxTenantIDLength,
		"MaxSecretLength":              MaxSecretLength,
		"MaxHeaderKeyLength":           MaxHeaderKeyLength,
		"MaxHeaderValueLength":         MaxHeaderValueLength,
		"MaxMetadataSize":              MaxMetadataSize,
		"MaxPayloadSize":               MaxPayloadSize,
		"MaxEventTypesPerSubscription": MaxEventTypesPerSubscription,
		"MaxHeadersPerSubscription":    MaxHeadersPerSubscription,
		"MinPasswordLength":            MinPasswordLength,
		"MinEventTypeLength":           MinEventTypeLength,
	}

	for name, limit := range limits {
		t.Run(name, func(t *testing.T) {
			// All limits should be positive
			assert.Greater(t, limit, 0, "%s should be positive", name)

			// Min values should be reasonable (not too restrictive)
			if strings.HasPrefix(name, "Min") {
				assert.LessOrEqual(t, limit, 20, "%s should not be too restrictive", name)
			}

			// Max values should be reasonable (not too permissive)
			// Note: Some max values like MaxHeadersPerSubscription may be intentionally lower
			if strings.HasPrefix(name, "Max") && !strings.Contains(name, "Headers") {
				assert.GreaterOrEqual(t, limit, 100, "%s should allow reasonable values", name)
			}
		})
	}

	// Validate relationships between limits
	assert.Greater(t, MaxEventTypeLength, MinEventTypeLength, "max event type length should be greater than min")
	assert.Greater(t, MaxURLLength, 100, "max URL length should allow reasonable URLs")
	assert.Greater(t, MaxPayloadSize, MaxMetadataSize, "max payload should be larger than max metadata")
}

// TestErrorMessages validates error message constants.
func TestErrorMessages(t *testing.T) {
	errorMessages := []string{
		// Validation errors
		ErrMsgInvalidURL,
		ErrMsgInvalidURLScheme,
		ErrMsgMissingURLHost,
		ErrMsgURLTooLong,
		ErrMsgMissingTenantID,
		ErrMsgMissingEventType,
		ErrMsgMissingEventTypes,
		ErrMsgMissingWebhookSecret,
		ErrMsgMissingURL,
		ErrMsgMissingPayload,
		ErrMsgMissingSubscriptionID,
		ErrMsgInvalidStatus,
		ErrMsgEventTypeTooLong,
		ErrMsgTooManyEventTypes,
		ErrMsgTooManyHeaders,
		ErrMsgMetadataTooLarge,
		ErrMsgPayloadTooLarge,
		// Not found errors
		ErrMsgSubscriptionNotFound,
		ErrMsgDeliveryNotFound,
		ErrMsgCircuitBreakerNotFound,
		// Conflict errors
		ErrMsgSubscriptionExists,
		ErrMsgDuplicateDelivery,
		// State errors
		ErrMsgCircuitBreakerOpen,
		ErrMsgSubscriptionNotActive,
		ErrMsgDeliveryAlreadyCompleted,
		// Configuration errors
		ErrMsgInvalidWorkerCount,
		ErrMsgInvalidPollInterval,
		ErrMsgInvalidDeliveryTimeout,
		ErrMsgMissingDatabaseURL,
		ErrMsgInvalidMaxRetries,
		ErrMsgInvalidBackoff,
	}

	for _, msg := range errorMessages {
		t.Run(msg, func(t *testing.T) {
			// Error messages should not be empty
			require.NotEmpty(t, msg, "error message should not be empty")

			// Error messages should start with lowercase (unless acronym like "URL")
			// Allow proper nouns and acronyms
			firstChar := msg[0]
			if firstChar >= 'A' && firstChar <= 'Z' {
				// If starts with uppercase, should be a known acronym/proper noun
				startsWithAcronym := strings.HasPrefix(msg, "URL") ||
					strings.HasPrefix(msg, "HTTP") ||
					strings.HasPrefix(msg, "ID")
				assert.True(t, startsWithAcronym, "error message starting with uppercase should be an acronym")
			}

			// Error messages should not end with punctuation
			assert.False(t, strings.HasSuffix(msg, "."), "error message should not end with period")
			assert.False(t, strings.HasSuffix(msg, "!"), "error message should not end with exclamation")

			// Error messages should be concise
			assert.LessOrEqual(t, len(msg), 100, "error message should be concise")
		})
	}
}

// TestLogMessages validates log message constants.
func TestLogMessages(t *testing.T) {
	logMessages := []string{
		LogMsgManagerInitialized,
		LogMsgManagerStarted,
		LogMsgManagerShutdown,
		LogMsgSubscriptionCreated,
		LogMsgSubscriptionUpdated,
		LogMsgSubscriptionDeleted,
		LogMsgDeliveryQueued,
		LogMsgDeliveryStarted,
		LogMsgDeliverySucceeded,
		LogMsgDeliveryFailed,
		LogMsgDeliveryMovedToDLQ,
		LogMsgCircuitBreakerOpened,
		LogMsgCircuitBreakerClosed,
	}

	for _, msg := range logMessages {
		t.Run(msg, func(t *testing.T) {
			// Log messages should not be empty
			require.NotEmpty(t, msg, "log message should not be empty")

			// Log messages should be lowercase
			assert.Equal(t, strings.ToLower(msg), msg, "log message should be lowercase")

			// Log messages should be concise
			assert.LessOrEqual(t, len(msg), 100, "log message should be concise")
		})
	}
}

// TestComponentNames validates component name constants.
func TestComponentNames(t *testing.T) {
	components := []string{
		ComponentManager,
		ComponentDeliveryEngine,
		ComponentCircuitBreaker,
		ComponentRepository,
		ComponentIdempotencyStore,
	}

	for _, component := range components {
		t.Run(component, func(t *testing.T) {
			// Components should not be empty
			assert.NotEmpty(t, component, "component should not be empty")

			// Components should be PascalCase (for structured logging)
			assert.True(t, component[0] >= 'A' && component[0] <= 'Z', "component should start with uppercase")

			// Components should be reasonable length
			assert.LessOrEqual(t, len(component), 30, "component should be reasonable length")
		})
	}

	// Components should be unique
	uniqueComponents := make(map[string]bool)
	for _, component := range components {
		assert.False(t, uniqueComponents[component], "component %s should be unique", component)
		uniqueComponents[component] = true
	}
}

// TestOperationNames validates operation name constants.
func TestOperationNames(t *testing.T) {
	operations := []string{
		OperationCreateSubscription,
		OperationGetSubscription,
		OperationUpdateSubscription,
		OperationDeleteSubscription,
		OperationListSubscriptions,
		OperationQueueDelivery,
		OperationExecuteDelivery,
		OperationRetryDelivery,
	}

	for _, operation := range operations {
		t.Run(operation, func(t *testing.T) {
			// Operations should not be empty
			assert.NotEmpty(t, operation, "operation should not be empty")

			// Operations should be PascalCase (for tracing/metrics)
			assert.True(t, operation[0] >= 'A' && operation[0] <= 'Z', "operation should start with uppercase")

			// Operations should be reasonable length
			assert.LessOrEqual(t, len(operation), 40, "operation should be reasonable length")
		})
	}

	// Operations should be unique
	uniqueOperations := make(map[string]bool)
	for _, operation := range operations {
		assert.False(t, uniqueOperations[operation], "operation %s should be unique", operation)
		uniqueOperations[operation] = true
	}
}
