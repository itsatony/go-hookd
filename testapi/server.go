// Package testapi provides a test HTTP server for integration testing webhook deliveries.
//
// The test API server acts as a webhook endpoint that can:
// - Record all received webhook requests
// - Simulate various response scenarios (success, failure, delays, timeouts)
// - Provide query endpoints to verify received webhooks
// - Support circuit breaker testing with configurable failure rates
//
// Usage:
//
//	server := testapi.NewServer()
//	defer server.Close()
//
//	// Configure response behavior
//	server.SetResponseBehavior(testapi.ResponseBehaviorDelay, 2*time.Second)
//
//	// Use server URL in tests
//	subscription.URL = server.URL() + "/webhook"
//
//	// Query received requests
//	requests := server.GetReceivedRequests()
package testapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// =============================================================================
// REQUEST RECORDING
// =============================================================================

// ReceivedRequest represents a webhook request received by the test server
type ReceivedRequest struct {
	// Request metadata
	ID          string
	ReceivedAt  time.Time
	Method      string
	Path        string
	RemoteAddr  string
	ContentType string

	// Headers and body
	Headers http.Header
	Body    []byte

	// Parsed webhook data (if JSON)
	PayloadJSON map[string]interface{}

	// Response sent
	StatusCode      int
	ResponseBody    string
	ResponseHeaders http.Header
	Duration        time.Duration
}

// =============================================================================
// RESPONSE BEHAVIOR
// =============================================================================

// ResponseBehavior defines how the test server should respond to requests
type ResponseBehavior string

const (
	// ResponseBehaviorSuccess always returns 200 OK
	ResponseBehaviorSuccess ResponseBehavior = "success"

	// ResponseBehaviorFail always returns 500 Internal Server Error
	ResponseBehaviorFail ResponseBehavior = "fail"

	// ResponseBehaviorDelay delays response by configured duration
	ResponseBehaviorDelay ResponseBehavior = "delay"

	// ResponseBehaviorTimeout never responds (simulates timeout)
	ResponseBehaviorTimeout ResponseBehavior = "timeout"

	// ResponseBehaviorIntermittent fails every N requests (configurable)
	ResponseBehaviorIntermittent ResponseBehavior = "intermittent"

	// ResponseBehaviorCustom uses custom response code and body
	ResponseBehaviorCustom ResponseBehavior = "custom"
)

// ResponseConfig configures how the server responds to webhook requests
type ResponseConfig struct {
	Behavior ResponseBehavior

	// For ResponseBehaviorDelay
	Delay time.Duration

	// For ResponseBehaviorIntermittent
	FailureRate float64 // 0.0 - 1.0 (e.g., 0.5 = 50% failure rate)

	// For ResponseBehaviorCustom
	StatusCode   int
	ResponseBody string
	Headers      map[string]string
}

// DefaultResponseConfig returns a success configuration
func DefaultResponseConfig() ResponseConfig {
	return ResponseConfig{
		Behavior: ResponseBehaviorSuccess,
	}
}

// =============================================================================
// TEST SERVER
// =============================================================================

// Server is a test HTTP server for webhook integration testing
type Server struct {
	httpServer *httptest.Server
	mu         sync.RWMutex

	// Request recording
	requests      []*ReceivedRequest
	requestsByID  map[string]*ReceivedRequest
	requestCount  int
	lastRequestAt time.Time

	// Response configuration
	responseConfig ResponseConfig

	// Statistics
	successCount int
	failureCount int
	totalLatency time.Duration
}

// NewServer creates and starts a new test API server
func NewServer() *Server {
	s := &Server{
		requests:       make([]*ReceivedRequest, 0),
		requestsByID:   make(map[string]*ReceivedRequest),
		responseConfig: DefaultResponseConfig(),
	}

	// Create HTTP test server with webhook handler
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/webhook/", s.handleWebhook) // Support path variations
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/requests", s.handleGetRequests)
	mux.HandleFunc("/api/requests/", s.handleGetRequest)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/reset", s.handleReset)
	mux.HandleFunc("/api/stats", s.handleStats)

	s.httpServer = httptest.NewServer(mux)

	return s
}

// Close stops the test server
func (s *Server) Close() {
	if s.httpServer != nil {
		s.httpServer.Close()
	}
}

// CloseClientConnections forcibly closes any open HTTP connections
// This is useful for tests that timeout and need immediate cleanup
func (s *Server) CloseClientConnections() {
	if s.httpServer != nil {
		s.httpServer.CloseClientConnections()
	}
}

// URL returns the base URL of the test server
func (s *Server) URL() string {
	if s.httpServer != nil {
		return s.httpServer.URL
	}
	return ""
}

// WebhookURL returns the full webhook endpoint URL
func (s *Server) WebhookURL() string {
	return s.URL() + "/webhook"
}

// =============================================================================
// RESPONSE CONFIGURATION
// =============================================================================

// SetResponseBehavior configures how the server responds to requests
func (s *Server) SetResponseBehavior(behavior ResponseBehavior, options ...interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.responseConfig.Behavior = behavior

	// Parse options based on behavior
	switch behavior {
	case ResponseBehaviorDelay:
		if len(options) > 0 {
			if delay, ok := options[0].(time.Duration); ok {
				s.responseConfig.Delay = delay
			}
		}
	case ResponseBehaviorIntermittent:
		if len(options) > 0 {
			if rate, ok := options[0].(float64); ok {
				s.responseConfig.FailureRate = rate
			}
		}
	case ResponseBehaviorCustom:
		if len(options) >= 2 {
			if code, ok := options[0].(int); ok {
				s.responseConfig.StatusCode = code
			}
			if body, ok := options[1].(string); ok {
				s.responseConfig.ResponseBody = body
			}
		}
	}
}

// SetResponseConfig sets the full response configuration
func (s *Server) SetResponseConfig(config ResponseConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responseConfig = config
}

// GetResponseConfig returns the current response configuration
func (s *Server) GetResponseConfig() ResponseConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.responseConfig
}

// =============================================================================
// REQUEST RECORDING
// =============================================================================

// GetReceivedRequests returns all received webhook requests
func (s *Server) GetReceivedRequests() []*ReceivedRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return copy to prevent external modification
	result := make([]*ReceivedRequest, len(s.requests))
	copy(result, s.requests)
	return result
}

// GetReceivedRequest returns a specific request by ID
func (s *Server) GetReceivedRequest(id string) *ReceivedRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requestsByID[id]
}

// GetRequestCount returns the total number of requests received
func (s *Server) GetRequestCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requestCount
}

// GetLastRequest returns the most recently received request
func (s *Server) GetLastRequest() *ReceivedRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

// Reset clears all recorded requests and statistics
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = make([]*ReceivedRequest, 0)
	s.requestsByID = make(map[string]*ReceivedRequest)
	s.requestCount = 0
	s.successCount = 0
	s.failureCount = 0
	s.totalLatency = 0
	s.lastRequestAt = time.Time{}
}

// GetStats returns server statistics
func (s *Server) GetStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	avgLatency := time.Duration(0)
	if s.requestCount > 0 {
		avgLatency = s.totalLatency / time.Duration(s.requestCount)
	}

	successRate := 0.0
	if s.requestCount > 0 {
		successRate = float64(s.successCount) / float64(s.requestCount) * 100
	}

	return map[string]interface{}{
		"total_requests": s.requestCount,
		"success_count":  s.successCount,
		"failure_count":  s.failureCount,
		"success_rate":   successRate,
		"avg_latency_ms": avgLatency.Milliseconds(),
		"last_request":   s.lastRequestAt,
	}
}

// WaitForRequests waits until the server has received at least count requests
// or the timeout is reached. Returns true if the condition was met.
func (s *Server) WaitForRequests(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		if s.GetRequestCount() >= count {
			return true
		}
		<-ticker.C
	}

	return false
}

// WaitForRequestWithPayload waits for a request with specific payload content
func (s *Server) WaitForRequestWithPayload(key string, value interface{}, timeout time.Duration) *ReceivedRequest {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		requests := s.GetReceivedRequests()
		for _, req := range requests {
			if req.PayloadJSON != nil {
				if val, ok := req.PayloadJSON[key]; ok && val == value {
					return req
				}
			}
		}
		<-ticker.C
	}

	return nil
}

// =============================================================================
// INTERNAL HELPERS
// =============================================================================

// recordRequest stores a received request
func (s *Server) recordRequest(req *ReceivedRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, req)
	s.requestsByID[req.ID] = req
	s.requestCount++
	s.lastRequestAt = req.ReceivedAt

	if req.StatusCode >= 200 && req.StatusCode < 300 {
		s.successCount++
	} else {
		s.failureCount++
	}

	s.totalLatency += req.Duration
}

// shouldFail determines if an intermittent request should fail
func (s *Server) shouldFail() bool {
	s.mu.RLock()
	rate := s.responseConfig.FailureRate
	count := s.requestCount
	s.mu.RUnlock()

	// Simple deterministic failure pattern based on request count
	// For 50% failure rate: fail on even requests
	// For 33% failure rate: fail every 3rd request
	if rate <= 0 {
		return false
	}
	if rate >= 1.0 {
		return true
	}

	// Convert rate to "fail every N requests"
	interval := int(1.0 / rate)
	if interval < 2 {
		interval = 2
	}

	return (count % interval) == 0
}

// generateRequestID generates a unique request ID
func (s *Server) generateRequestID() string {
	s.mu.RLock()
	count := s.requestCount
	s.mu.RUnlock()

	return fmt.Sprintf("req_%d_%d", time.Now().UnixNano(), count)
}
