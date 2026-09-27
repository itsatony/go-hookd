// Package internal provides the core webhook management implementation for go-hookd.
//
// This file contains ALL string literals and configuration defaults used throughout
// the package. NO magic strings are allowed in any other file - all strings must be
// referenced from this constants file.
//
// Excellence. Always.
package hookd

import "time"

// ID Prefixes - Used for generating prefixed nanoIDs.
// Format: {prefix}_{nanoID} (e.g., sub_6ByTSYmGzT2c).
const (
	// PrefixSubscription is the prefix for subscription IDs.
	PrefixSubscription = "sub"

	// PrefixDelivery is the prefix for delivery IDs.
	PrefixDelivery = "dlv"

	// PrefixAttempt is the prefix for delivery attempt IDs.
	PrefixAttempt = "att"
)

// Subscription Status Constants.
const (
	// SubscriptionStatusActive indicates an active subscription receiving deliveries.
	SubscriptionStatusActive = "active"

	// SubscriptionStatusPaused indicates a paused subscription (no new deliveries).
	SubscriptionStatusPaused = "paused"

	// SubscriptionStatusDisabled indicates a disabled subscription (permanently stopped).
	SubscriptionStatusDisabled = "disabled"
)

// Delivery Status Constants.
const (
	// DeliveryStatusPending indicates a delivery waiting to be processed.
	DeliveryStatusPending = "pending"

	// DeliveryStatusSuccess indicates a delivery was successfully delivered.
	DeliveryStatusSuccess = "success"

	// DeliveryStatusFailed indicates a delivery failed but may be retried.
	DeliveryStatusFailed = "failed"

	// DeliveryStatusDeadLetter indicates a delivery exhausted retries and moved to DLQ.
	DeliveryStatusDeadLetter = "dead_letter"
)

// Circuit Breaker State Constants.
const (
	// CircuitBreakerStateClosed indicates normal operation (requests allowed).
	CircuitBreakerStateClosed = "closed"

	// CircuitBreakerStateHalfOpen indicates testing state (limited requests).
	CircuitBreakerStateHalfOpen = "half_open"

	// CircuitBreakerStateOpen indicates failure state (requests blocked).
	CircuitBreakerStateOpen = "open"
)

// Event Topics - Delivery Lifecycle.
const (
	// EventTopicDeliveryQueued is published when a delivery is queued.
	EventTopicDeliveryQueued = "delivery.queued"

	// EventTopicDeliveryStarted is published when a delivery attempt starts.
	EventTopicDeliveryStarted = "delivery.started"

	// EventTopicDeliverySuccess is published when a delivery succeeds.
	EventTopicDeliverySuccess = "delivery.success"

	// EventTopicDeliveryFailed is published when a delivery fails.
	EventTopicDeliveryFailed = "delivery.failed"

	// EventTopicDeliveryDeadLetter is published when a delivery moves to DLQ.
	EventTopicDeliveryDeadLetter = "delivery.dead_letter"
)

// Event Topics - Circuit Breaker.
const (
	// EventTopicCircuitOpened is published when a circuit breaker opens.
	EventTopicCircuitOpened = "circuit.opened"

	// EventTopicCircuitHalfOpen is published when a circuit breaker enters half-open state.
	EventTopicCircuitHalfOpen = "circuit.half_open"

	// EventTopicCircuitClosed is published when a circuit breaker closes.
	EventTopicCircuitClosed = "circuit.closed"
)

// Event Topics - Metrics.
const (
	// EventTopicMetricsDeliveryAttempt is published for each delivery attempt.
	EventTopicMetricsDeliveryAttempt = "metrics.delivery_attempt"

	// EventTopicMetricsRetryTriggered is published when a retry is scheduled.
	EventTopicMetricsRetryTriggered = "metrics.retry_triggered"

	// EventTopicMetricsQueueDepth is published with current queue depth.
	EventTopicMetricsQueueDepth = "metrics.queue_depth"
)

// Event Topics - Audit.
const (
	// EventTopicAuditSubscriptionCreated is published when a subscription is created.
	EventTopicAuditSubscriptionCreated = "audit.subscription_created"

	// EventTopicAuditSubscriptionUpdated is published when a subscription is updated.
	EventTopicAuditSubscriptionUpdated = "audit.subscription_updated"

	// EventTopicAuditSubscriptionDeleted is published when a subscription is deleted.
	EventTopicAuditSubscriptionDeleted = "audit.subscription_deleted"
)

// Maintenance Event Topics.
const (
	// EventTopicMaintenanceCleanup is published when a cleanup operation completes.
	EventTopicMaintenanceCleanup = "maintenance.cleanup"
)

// Test Event Types.
const (
	// EventTypeTestPing is the event type used for TestSubscription ping requests.
	// Webhook endpoints should handle this gracefully (return 200 OK).
	EventTypeTestPing = "test.ping"
)

// Wildcard Event Type Patterns.
const (
	// WildcardAll matches any event type ("*").
	WildcardAll = "*"

	// WildcardSuffix is the suffix for prefix wildcards (".*").
	// A pattern like "order.*" matches "order.created", "order.updated", etc.
	WildcardSuffix = ".*"
)

// HTTP Headers - Webhook Delivery.
const (
	// HeaderSignature is the HMAC signature of the payload.
	HeaderSignature = "X-Webhook-Signature"

	// HeaderTimestamp is the Unix timestamp of the delivery.
	HeaderTimestamp = "X-Webhook-Timestamp"

	// HeaderDeliveryID is the unique delivery identifier.
	HeaderDeliveryID = "X-Webhook-Delivery-ID"

	// HeaderAttemptNumber is the current attempt number (1-based).
	HeaderAttemptNumber = "X-Webhook-Attempt"

	// HeaderEventType is the event type being delivered.
	HeaderEventType = "X-Webhook-Event-Type"

	// HeaderSubscriptionID is the subscription identifier.
	HeaderSubscriptionID = "X-Webhook-Subscription-ID"

	// HeaderIdempotencyKey is the idempotency key (if provided during queueing).
	HeaderIdempotencyKey = "X-Webhook-Idempotency-Key"
)

// =============================================================================
// DATABASE OBJECT NAMES
// =============================================================================
// Database object names (tables, indexes, functions, triggers) are now
// dynamically generated via SchemaConfig to support multi-service isolation.
// See hookd.schema.go for the SchemaConfig type and name generation methods.
//
// Example:
//   config, _ := NewSchemaConfig("myservice")
//   tableName := config.TableSubscriptions() // "myservice_hookd_subscriptions"

// Default Configuration Values - Retry Policy.
const (
	// DefaultMaxRetries is the default maximum retry attempts.
	DefaultMaxRetries = 10

	// DefaultInitialBackoffMs is the default initial backoff in milliseconds.
	DefaultInitialBackoffMs = 1000

	// DefaultMaxBackoffMs is the default maximum backoff in milliseconds.
	DefaultMaxBackoffMs = 3600000 // 1 hour

	// DefaultBackoffFactor is the default exponential backoff multiplier.
	DefaultBackoffFactor = 2.0
)

// Default Configuration Values - Worker Pool.
const (
	// DefaultWorkerCount is the default number of delivery workers.
	DefaultWorkerCount = 10

	// DefaultQueuePollIntervalMs is the default queue polling interval in milliseconds.
	DefaultQueuePollIntervalMs = 1000

	// DefaultDeliveryTimeoutMs is the default HTTP delivery timeout in milliseconds.
	DefaultDeliveryTimeoutMs = 30000 // 30 seconds

	// DefaultQueueIdleMaxIntervalMs is the default upper bound, in milliseconds, for
	// the adaptive poll interval a worker backs off to while the queue stays empty.
	// Set equal to QueuePollInterval to disable idle backoff.
	DefaultQueueIdleMaxIntervalMs = 30000 // 30 seconds

	// DefaultQueueIdleBackoffFactor is the default multiplier applied to a worker's
	// poll interval after a poll that found no deliveries. 1.0 disables idle backoff.
	DefaultQueueIdleBackoffFactor = 2.0

	// QueueIdleJitterFraction is the fraction of the computed idle interval applied
	// as random jitter, spreading the wake-ups of a worker pool that would otherwise
	// have been started within the same microsecond and would poll in lockstep.
	QueueIdleJitterFraction = 0.2
)

// Default Configuration Values - Circuit Breaker.
const (
	// DefaultCircuitBreakerThreshold is the default failure threshold before opening.
	DefaultCircuitBreakerThreshold = 5

	// DefaultCircuitBreakerTimeoutMs is the default timeout before half-open in milliseconds.
	DefaultCircuitBreakerTimeoutMs = 60000 // 1 minute

	// DefaultCircuitBreakerHalfOpenRequests is the number of requests to test in half-open.
	DefaultCircuitBreakerHalfOpenRequests = 3
)

// Default Configuration Values - Idempotency.
const (
	// DefaultIdempotencyTTLHours is the default idempotency key TTL in hours.
	DefaultIdempotencyTTLHours = 24
)

// Validation Constants - Limits.
const (
	// MaxURLLength is the maximum allowed URL length.
	MaxURLLength = 2048

	// MaxEventTypeLength is the maximum event type string length.
	MaxEventTypeLength = 255

	// MaxTenantIDLength is the maximum tenant ID string length.
	MaxTenantIDLength = 255

	// MaxSecretLength is the maximum secret string length.
	MaxSecretLength = 512

	// MaxHeaderKeyLength is the maximum header key length.
	MaxHeaderKeyLength = 255

	// MaxHeaderValueLength is the maximum header value length.
	MaxHeaderValueLength = 2048

	// MaxMetadataSize is the maximum metadata JSON size in bytes.
	MaxMetadataSize = 65536 // 64KB

	// MaxPayloadSize is the maximum delivery payload size in bytes.
	MaxPayloadSize = 1048576 // 1MB

	// MaxEventTypesPerSubscription is the maximum event types per subscription.
	MaxEventTypesPerSubscription = 100

	// MaxHeadersPerSubscription is the maximum custom headers per subscription.
	MaxHeadersPerSubscription = 50

	// MaxResponseBodyLength is the maximum length of response body to store.
	MaxResponseBodyLength = 10240 // 10KB

	// MaxBatchSize is the maximum number of items in a batch operation.
	MaxBatchSize = 100
)

// Validation Constants - Patterns.
const (
	// MinPasswordLength is the minimum length for secrets/passwords.
	MinPasswordLength = 8

	// MinEventTypeLength is the minimum event type length.
	MinEventTypeLength = 3
)

// Error Messages - Validation.
const (
	// ErrMsgInvalidURL is the error message for invalid URL format.
	ErrMsgInvalidURL = "invalid URL format"

	// ErrMsgInvalidURLScheme is the error message for invalid URL scheme.
	ErrMsgInvalidURLScheme = "URL must use http or https scheme"

	// ErrMsgMissingURLHost is the error message for missing URL host.
	ErrMsgMissingURLHost = "URL must have a host"

	// ErrMsgURLTooLong is the error message for URL exceeding max length.
	ErrMsgURLTooLong = "URL exceeds maximum length"

	// ErrMsgURLDestinationRefused is the write-time refusal for a URL whose host
	// is an IP literal the egress policy refuses (loopback, private, link-local,
	// reserved, ...). A HOSTNAME is never resolved at write time; see
	// hookd.egress.go.
	ErrMsgURLDestinationRefused = "URL host is not a permitted webhook destination"

	// ErrMsgMissingTenantID is the error message for missing tenant ID.
	ErrMsgMissingTenantID = "tenant_id is required"

	// ErrMsgMissingEventType is the error message for missing event type.
	ErrMsgMissingEventType = "event_type is required"

	// ErrMsgMissingEventTypes is the error message for missing event types array.
	ErrMsgMissingEventTypes = "at least one event type is required"

	// ErrMsgMissingWebhookSecret is the error message for missing webhook signing secret.
	ErrMsgMissingWebhookSecret = "webhook signing key is required"

	// ErrMsgMissingURL is the error message for missing URL.
	ErrMsgMissingURL = "URL is required"

	// ErrMsgMissingPayload is the error message for missing payload.
	ErrMsgMissingPayload = "payload is required"

	// ErrMsgMissingSubscriptionID is the error message for missing subscription ID.
	ErrMsgMissingSubscriptionID = "subscription_id is required"

	// ErrMsgInvalidStatus is the error message for invalid status value.
	ErrMsgInvalidStatus = "invalid status value"

	// ErrMsgEventTypeTooLong is the error message for event type exceeding max length.
	ErrMsgEventTypeTooLong = "event_type exceeds maximum length"

	// ErrMsgTooManyEventTypes is the error message for too many event types.
	ErrMsgTooManyEventTypes = "too many event types"

	// ErrMsgTooManyHeaders is the error message for too many headers.
	ErrMsgTooManyHeaders = "too many custom headers"

	// ErrMsgMetadataTooLarge is the error message for metadata exceeding size limit.
	ErrMsgMetadataTooLarge = "metadata exceeds maximum size"

	// ErrMsgPayloadTooLarge is the error message for payload exceeding size limit.
	ErrMsgPayloadTooLarge = "payload exceeds maximum size"
)

// Error Messages - Not Found.
const (
	// ErrMsgSubscriptionNotFound is the error message for subscription not found.
	ErrMsgSubscriptionNotFound = "subscription not found"

	// ErrMsgDeliveryNotFound is the error message for delivery not found.
	ErrMsgDeliveryNotFound = "delivery not found"

	// ErrMsgCircuitBreakerNotFound is the error message for circuit breaker state not found.
	ErrMsgCircuitBreakerNotFound = "circuit breaker state not found"
)

// Error Messages - Conflict.
const (
	// ErrMsgSubscriptionExists is the error message for duplicate subscription.
	ErrMsgSubscriptionExists = "subscription already exists for this tenant and URL"

	// ErrMsgDuplicateDelivery is the error message for duplicate delivery.
	ErrMsgDuplicateDelivery = "duplicate delivery detected"
)

// Error Messages - State.
const (
	// ErrMsgCircuitBreakerOpen is the error message when circuit breaker is open.
	ErrMsgCircuitBreakerOpen = "circuit breaker is open"

	// ErrMsgSubscriptionNotActive is the error message for inactive subscription.
	ErrMsgSubscriptionNotActive = "subscription is not active"

	// ErrMsgEventTypeMismatch is the error message when event type doesn't match subscription patterns.
	ErrMsgEventTypeMismatch = "event type does not match subscription's configured event types"

	// ErrMsgMetadataFilterMismatch is the error message when event metadata doesn't match subscription filters.
	ErrMsgMetadataFilterMismatch = "event metadata does not match subscription's filter criteria"

	// ErrMsgDeliveryAlreadyCompleted is the error message for completed delivery.
	ErrMsgDeliveryAlreadyCompleted = "delivery already completed"
)

// Error Messages - Configuration.
const (
	// ErrMsgInvalidWorkerCount is the error message for invalid worker count.
	ErrMsgInvalidWorkerCount = "worker_count must be at least 1"

	// ErrMsgInvalidPollInterval is the error message for invalid poll interval.
	ErrMsgInvalidPollInterval = "queue_poll_interval must be at least 1ms"

	// ErrMsgInvalidIdleMaxInterval is the error message for an idle ceiling below the base interval.
	ErrMsgInvalidIdleMaxInterval = "queue_idle_max_interval must be >= queue_poll_interval"

	// ErrMsgInvalidIdleBackoffFactor is the error message for an invalid idle backoff factor.
	ErrMsgInvalidIdleBackoffFactor = "queue_idle_backoff_factor must be >= 1.0"

	// ErrMsgInvalidDeliveryTimeout is the error message for invalid delivery timeout.
	ErrMsgInvalidDeliveryTimeout = "delivery_timeout must be at least 1s"

	// ErrMsgMissingDatabaseURL is the error message for missing database URL.
	ErrMsgMissingDatabaseURL = "database_url is required"

	// ErrMsgMissingConfig is the error message for missing configuration.
	ErrMsgMissingConfig = "configuration is required"

	// ErrMsgInvalidMaxRetries is the error message for invalid max retries.
	ErrMsgInvalidMaxRetries = "max_retries must be at least 0"

	// ErrMsgInvalidBackoff is the error message for invalid backoff configuration.
	ErrMsgInvalidBackoff = "initial_backoff must be less than max_backoff"

	// ErrMsgInvalidMaxBatchSize is the error message for invalid max batch size.
	ErrMsgInvalidMaxBatchSize = "max_batch_size must be at least 1"

	// ErrMsgInvalidInitialBackoff is the error message for invalid initial backoff.
	ErrMsgInvalidInitialBackoff = "initial_backoff must be at least 1ms"

	// ErrMsgInvalidMaxBackoff is the error message for invalid max backoff.
	ErrMsgInvalidMaxBackoff = "max_backoff must be at least 1ms"

	// ErrMsgInvalidBackoffFactor is the error message for invalid backoff factor.
	ErrMsgInvalidBackoffFactor = "backoff_factor must be at least 1.0"

	// ErrMsgInvalidCircuitBreakerThreshold is the error message for invalid circuit breaker threshold.
	ErrMsgInvalidCircuitBreakerThreshold = "circuit_breaker_threshold must be at least 1"

	// ErrMsgInvalidCircuitBreakerTimeout is the error message for invalid circuit breaker timeout.
	ErrMsgInvalidCircuitBreakerTimeout = "circuit_breaker_timeout must be at least 1s"

	// ErrMsgInvalidCircuitBreakerHalfOpen is the error message for invalid half-open requests.
	ErrMsgInvalidCircuitBreakerHalfOpen = "circuit_breaker_half_open_requests must be at least 1"

	// ErrMsgInvalidIdempotencyTTL is the error message for invalid idempotency TTL.
	ErrMsgInvalidIdempotencyTTL = "idempotency_ttl must be at least 1 hour"

	// ErrMsgInvalidShutdownTimeout is the error message for invalid shutdown timeout.
	ErrMsgInvalidShutdownTimeout = "shutdown_timeout must be at least 1 second"

	// ErrMsgManagerAlreadyStarted is the error message when manager is already started.
	ErrMsgManagerAlreadyStarted = "manager already started"

	// ErrMsgManagerNotStarted is the error message when manager is not started.
	ErrMsgManagerNotStarted = "manager must be started before use - call Start() first"

	// ErrMsgRequestRequired is the error message when request is nil.
	ErrMsgRequestRequired = "request is required"

	// ErrMsgAtLeastOneDeliveryRequired is the error message when deliveries array is empty.
	ErrMsgAtLeastOneDeliveryRequired = "at least one delivery is required"

	// ErrMsgAtLeastOneSubscriptionRequired is the error message when subscriptions array is empty.
	ErrMsgAtLeastOneSubscriptionRequired = "at least one subscription is required"

	// ErrMsgBatchSizeExceedsMaximum is the error message when batch size exceeds limit.
	ErrMsgBatchSizeExceedsMaximum = "batch size exceeds maximum"

	// ErrMsgDeliveryIDRequired is the error message when delivery ID is missing.
	ErrMsgDeliveryIDRequired = "delivery_id is required"

	// ErrMsgCannotRetrySuccessful is the error message when trying to retry successful delivery.
	ErrMsgCannotRetrySuccessful = "cannot retry successful delivery"

	// ErrMsgFilterRequired is the error message when filter is missing.
	ErrMsgFilterRequired = "filter is required"
)

// Log Messages - Operations.
const (
	// LogMsgManagerInitialized is logged when manager initializes successfully.
	LogMsgManagerInitialized = "manager initialized"

	// LogMsgManagerStarted is logged when manager starts successfully.
	LogMsgManagerStarted = "manager started"

	// LogMsgManagerShutdown is logged when manager begins shutdown.
	LogMsgManagerShutdown = "manager shutting down"

	// LogMsgSubscriptionCreated is logged when subscription is created.
	LogMsgSubscriptionCreated = "subscription created"

	// LogMsgSubscriptionUpdated is logged when subscription is updated.
	LogMsgSubscriptionUpdated = "subscription updated"

	// LogMsgSubscriptionDeleted is logged when subscription is deleted.
	LogMsgSubscriptionDeleted = "subscription deleted"

	// LogMsgDeliveryQueued is logged when delivery is queued.
	LogMsgDeliveryQueued = "delivery queued"

	// LogMsgDeliveryStarted is logged when delivery attempt starts.
	LogMsgDeliveryStarted = "delivery attempt started"

	// LogMsgDeliverySucceeded is logged when delivery succeeds.
	LogMsgDeliverySucceeded = "delivery succeeded"

	// LogMsgDeliveryFailed is logged when delivery fails.
	LogMsgDeliveryFailed = "delivery failed"

	// LogMsgDeliveryMovedToDLQ is logged when delivery moves to dead letter queue.
	LogMsgDeliveryMovedToDLQ = "delivery moved to dead letter queue"

	// LogMsgCircuitBreakerOpened is logged when circuit breaker opens.
	LogMsgCircuitBreakerOpened = "circuit breaker opened"

	// LogMsgCircuitBreakerClosed is logged when circuit breaker closes.
	LogMsgCircuitBreakerClosed = "circuit breaker closed"
)

// Component Names - For Structured Logging.
const (
	// ComponentManager is the manager component name.
	ComponentManager = "Manager"

	// ComponentDeliveryEngine is the delivery engine component name.
	ComponentDeliveryEngine = "DeliveryEngine"

	// ComponentCircuitBreaker is the circuit breaker component name.
	ComponentCircuitBreaker = "CircuitBreaker"

	// ComponentRepository is the repository component name.
	ComponentRepository = "Repository"

	// ComponentIdempotencyStore is the idempotency store component name.
	ComponentIdempotencyStore = "IdempotencyStore"
)

// Operation Names - For Tracing and Metrics.
const (
	// OperationCreateSubscription is the create subscription operation name.
	OperationCreateSubscription = "CreateSubscription"

	// OperationGetSubscription is the get subscription operation name.
	OperationGetSubscription = "GetSubscription"

	// OperationUpdateSubscription is the update subscription operation name.
	OperationUpdateSubscription = "UpdateSubscription"

	// OperationDeleteSubscription is the delete subscription operation name.
	OperationDeleteSubscription = "DeleteSubscription"

	// OperationListSubscriptions is the list subscriptions operation name.
	OperationListSubscriptions = "ListSubscriptions"

	// OperationQueueDelivery is the queue delivery operation name.
	OperationQueueDelivery = "QueueDelivery"

	// OperationExecuteDelivery is the execute delivery operation name.
	OperationExecuteDelivery = "ExecuteDelivery"

	// OperationRetryDelivery is the retry delivery operation name.
	OperationRetryDelivery = "RetryDelivery"
)

// HTTP Constants - Methods and Content Types.
const (
	// HTTPMethodPost is the POST HTTP method.
	HTTPMethodPost = "POST"

	// HTTPMethodGet is the GET HTTP method.
	HTTPMethodGet = "GET"

	// ContentTypeJSON is the JSON content type.
	ContentTypeJSON = "application/json"
)

// User-Agent Constants.
const (
	// UserAgentPrefix is the prefix for the user agent string.
	UserAgentPrefix = "go-hookd"

	// UserAgentVersion is the current version for user agent.
	// This should match versions.yaml project.version.
	UserAgentVersion = "0.9.0"
)

// UserAgent is the complete user agent string used for webhook deliveries.
var UserAgent = UserAgentPrefix + "/" + UserAgentVersion

// Test Subscription Constants.
const (
	// TestPingMessage is the message included in test ping payloads.
	TestPingMessage = "This is a test ping from go-hookd. Your webhook endpoint is being verified."

	// TestPingDeliveryID is the pseudo-delivery ID for test pings.
	TestPingDeliveryID = "test_ping"

	// TestSubscriptionCooldownSeconds is the minimum seconds between TestSubscription calls.
	TestSubscriptionCooldownSeconds = 10
)

// Error Messages - Manager.
const (
	// ErrMsgRepositoryRequired is the error message when repository is nil.
	ErrMsgRepositoryRequired = "repository is required"

	// ErrMsgCreateLoggerFailed is the error message when logger creation fails.
	ErrMsgCreateLoggerFailed = "failed to create default logger"

	// ErrMsgMarshalTestPayload is the error message when test payload marshaling fails.
	ErrMsgMarshalTestPayload = "failed to marshal test payload"

	// ErrMsgCreateRequest is the error message when HTTP request creation fails.
	ErrMsgCreateRequest = "failed to create request"

	// ErrMsgEndpointStatusFmt is the format string for endpoint status errors.
	ErrMsgEndpointStatusFmt = "endpoint returned status %d"

	// ErrMsgRateLimited is the error message for rate limited requests.
	ErrMsgRateLimited = "test subscription rate limited, try again in %d seconds"

	// ErrMsgHTTPClientUnguardable is returned by NewManager when a client passed
	// with WithHTTPClient cannot carry the egress guard (its Transport is not an
	// *http.Transport, or it sets DialTLS/DialTLSContext) and the guarantee was
	// not waived with WithAllowPrivateDestinations.
	ErrMsgHTTPClientUnguardable = "HTTP client cannot carry the egress guard: pass an *http.Transport without DialTLS/DialTLSContext, or opt out with WithAllowPrivateDestinations"

	// ErrMsgEgressRefusalHookNil is returned when WithEgressRefusalHook is given nil.
	ErrMsgEgressRefusalHookNil = "egress refusal hook cannot be nil"
)

// Egress Guard Constants (v0.8.0, see hookd.egress.go).
const (
	// ErrMsgEgressDestinationUnreachable is the ONE text a subscriber can read
	// for a refused destination, a failed lookup and an empty DNS answer alike.
	// They are collapsed on purpose: "refused by policy" versus "no such host"
	// would tell a subscriber which internal names exist.
	ErrMsgEgressDestinationUnreachable = "webhook destination unreachable"

	// EgressRefusalCauseInternalAddress: a candidate address is outside the policy.
	EgressRefusalCauseInternalAddress = "internal_address"

	// EgressRefusalCauseMalformedAddress: the dial address could not be split.
	EgressRefusalCauseMalformedAddress = "malformed_address"

	// EgressRefusalCauseLookupFailed: the one DNS lookup the dial judges failed.
	EgressRefusalCauseLookupFailed = "lookup_failed"

	// EgressRefusalCauseEmptyAnswer: the lookup succeeded with no addresses.
	EgressRefusalCauseEmptyAnswer = "empty_answer"

	// EgressLookupNetwork is the network passed to the resolver: both families,
	// so a mixed A/AAAA answer is judged whole.
	EgressLookupNetwork = "ip"

	// URLSchemeHTTP and URLSchemeHTTPS are the only schemes a webhook URL may use.
	URLSchemeHTTP  = "http"
	URLSchemeHTTPS = "https"

	// ALPNProtocolHTTP2 is stripped from a consumer TLS config's NextProtos, so
	// the guarded transport stays on HTTP/1.1.
	ALPNProtocolHTTP2 = "h2"

	// Default delivery transport pool sizes (unchanged from v0.7.x).
	DefaultHTTPMaxIdleConns               = 100
	DefaultHTTPMaxIdleConnsPerHost        = 10
	DefaultHTTPIdleConnTimeoutSeconds int = 90

	// LogMsgEgressRefused is logged (WARN) with the cause for every refusal.
	LogMsgEgressRefused = "webhook egress refused"

	// LogFieldEgressCause is the log field carrying the refusal cause.
	LogFieldEgressCause = "cause"

	// LogMsgEgressGuardWaived is logged (WARN) at construction when the guard is
	// not applied to a consumer-supplied client (opaque RoundTripper + opt-in).
	LogMsgEgressGuardWaived = "webhook egress guard NOT applied to custom HTTP client (WithAllowPrivateDestinations)"

	// LogMsgEgressPrivateAllowed is logged (WARN) at construction when the
	// private-destination opt-in is active.
	LogMsgEgressPrivateAllowed = "webhook egress admits private destinations (WithAllowPrivateDestinations): not for production"
)

// PostgreSQL connection-pool defaults for PostgresRepository (v0.9.0).
//
// These are the values every version before v0.9.0 hardcoded. They remain the
// defaults so upgrading changes nothing; a consumer with a connection budget
// (managed PostgreSQL, several pods during a rolling update) overrides them with
// WithMaxOpenConns / WithMaxIdleConns / WithConnMaxLifetime / WithConnMaxIdleTime.
const (
	// DefaultPostgresMaxOpenConns is the default database/sql MaxOpenConns.
	DefaultPostgresMaxOpenConns = 25

	// DefaultPostgresMaxIdleConns is the default database/sql MaxIdleConns. When
	// only MaxOpenConns is overridden below this value, the idle default is
	// clamped DOWN to MaxOpenConns (database/sql would clamp it anyway).
	DefaultPostgresMaxIdleConns = 5

	// DefaultPostgresConnMaxLifetime is the default database/sql ConnMaxLifetime.
	DefaultPostgresConnMaxLifetime = 5 * time.Minute

	// DefaultPostgresConnMaxIdleTime is the default database/sql ConnMaxIdleTime.
	DefaultPostgresConnMaxIdleTime = 1 * time.Minute

	// PostgresPingTimeout bounds the connection check in the constructors.
	PostgresPingTimeout = 5 * time.Second

	// PostgresDriverName is the database/sql driver name used by go-hookd.
	PostgresDriverName = "postgres"
)

// Pool-option validation messages.
const (
	// ErrMsgMaxOpenConnsInvalid is returned when WithMaxOpenConns gets n <= 0.
	ErrMsgMaxOpenConnsInvalid = "max_open_conns must be greater than 0"

	// ErrMsgMaxIdleConnsInvalid is returned when WithMaxIdleConns gets n < 0.
	ErrMsgMaxIdleConnsInvalid = "max_idle_conns must be 0 or greater (0 retains no idle connections)"

	// ErrMsgMaxIdleExceedsOpen is returned when an EXPLICIT WithMaxIdleConns is
	// larger than the effective MaxOpenConns.
	ErrMsgMaxIdleExceedsOpen = "max_idle_conns must not exceed max_open_conns"

	// ErrMsgConnMaxLifetimeInvalid is returned when WithConnMaxLifetime gets d <= 0.
	ErrMsgConnMaxLifetimeInvalid = "conn_max_lifetime must be greater than 0"

	// ErrMsgConnMaxIdleTimeInvalid is returned when WithConnMaxIdleTime gets d <= 0.
	ErrMsgConnMaxIdleTimeInvalid = "conn_max_idle_time must be greater than 0"

	// ErrMsgSchemaLockTimeoutInvalid is returned when WithSchemaLockTimeout gets
	// a duration below SchemaLockTimeoutMin.
	ErrMsgSchemaLockTimeoutInvalid = "schema_lock_timeout must be at least 1ms"
)

// SchemaManager pool and advisory-lock constants (v0.9.0).
const (
	// SchemaManagerMaxOpenConns bounds the pool NewSchemaManagerFromURL opens.
	// Schema work runs entirely on ONE dedicated connection, so one is enough.
	SchemaManagerMaxOpenConns = 1

	// SchemaManagerMaxIdleConns is 0 so the manager holds NO connection between
	// calls: consumers keep the SchemaManager for the service lifetime, and a
	// boot-only helper must not occupy a slot of the connection budget forever.
	SchemaManagerMaxIdleConns = 0

	// DefaultSchemaLockTimeout bounds how long EnsureSchema/DropSchema wait for
	// another process holding the same prefix's schema lock. Override with
	// WithSchemaLockTimeout.
	DefaultSchemaLockTimeout = 60 * time.Second

	// SchemaLockTimeoutMin is the smallest accepted lock timeout
	// (PostgreSQL's lock_timeout has millisecond resolution; 0 would disable it).
	SchemaLockTimeoutMin = time.Millisecond

	// SchemaLockReleaseTimeout bounds the unlock round-trip. It uses a fresh
	// context so a cancelled caller context cannot leave the lock held.
	SchemaLockReleaseTimeout = 5 * time.Second

	// SchemaLockClassID is the first key of the two-int4 session advisory lock
	// serializing schema setup PER TABLE PREFIX ("hkds"). The two-int4 key space
	// is disjoint from the single-bigint key space, so it cannot collide with a
	// consumer's own pg_advisory_lock(bigint) keys.
	SchemaLockClassID int32 = 0x686b6473

	// SchemaDDLLockClassID is the class of the TRANSACTION-level advisory lock
	// taken as the first statement of every schema DDL batch ("hkdx", object id
	// SchemaDDLLockObjectID). It serializes the DDL itself across ALL prefixes,
	// because `CREATE EXTENSION IF NOT EXISTS` is database-global and races
	// between two consumers with different prefixes. It is held only for the
	// duration of the DDL transaction (milliseconds), never while waiting.
	SchemaDDLLockClassID int32 = 0x686b6478

	// SchemaDDLLockObjectID is the second key of the DDL lock.
	SchemaDDLLockObjectID int32 = 0

	// SchemaLockNamespace is hashed together with the table prefix into the
	// second key of the per-prefix schema lock.
	SchemaLockNamespace = "go-hookd:schema:"
)

// SQL used by the schema lock.
const (
	sqlSchemaLockSetTimeout   = "SELECT set_config('lock_timeout', $1, false)"
	sqlSchemaLockResetTimeout = "RESET lock_timeout"
	sqlSchemaLockAcquire      = "SELECT pg_advisory_lock($1::int4, $2::int4)"
	sqlSchemaLockRelease      = "SELECT pg_advisory_unlock($1::int4, $2::int4)"
	sqlSchemaDDLLockFmt       = "SELECT pg_advisory_xact_lock(%d, %d);\n"
	schemaLockTimeoutUnit     = "ms"
)

// Schema-lock error and operation labels.
const (
	// ErrMsgSchemaLockTimeout is the error message when the schema lock could not
	// be acquired within the lock timeout.
	ErrMsgSchemaLockTimeout = "timed out waiting for the go-hookd schema lock held by another process (see WithSchemaLockTimeout)"

	opAcquireSchemaConn = "acquire_schema_connection"
	opAcquireSchemaLock = "acquire_schema_lock"
	opReleaseSchemaLock = "release_schema_lock"
	opCreateSchema      = "create_schema"
	opDropSchema        = "drop_schema"
)
