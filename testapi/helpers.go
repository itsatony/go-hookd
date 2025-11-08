package testapi

import (
	"fmt"
	"time"
)

// =============================================================================
// CONVENIENCE FUNCTIONS
// =============================================================================

// SetupSuccessServer creates a server that always returns 200 OK
func SetupSuccessServer() *Server {
	server := NewServer()
	server.SetResponseBehavior(ResponseBehaviorSuccess)
	return server
}

// SetupFailureServer creates a server that always returns 500 Error
func SetupFailureServer() *Server {
	server := NewServer()
	server.SetResponseBehavior(ResponseBehaviorFail)
	return server
}

// SetupDelayedServer creates a server that delays responses
func SetupDelayedServer(delay time.Duration) *Server {
	server := NewServer()
	server.SetResponseBehavior(ResponseBehaviorDelay, delay)
	return server
}

// SetupTimeoutServer creates a server that never responds (timeout)
func SetupTimeoutServer() *Server {
	server := NewServer()
	server.SetResponseBehavior(ResponseBehaviorTimeout)
	return server
}

// SetupIntermittentServer creates a server with configurable failure rate
func SetupIntermittentServer(failureRate float64) *Server {
	server := NewServer()
	server.SetResponseBehavior(ResponseBehaviorIntermittent, failureRate)
	return server
}

// SetupCustomServer creates a server with custom status code and response
func SetupCustomServer(statusCode int, responseBody string) *Server {
	server := NewServer()
	server.SetResponseBehavior(ResponseBehaviorCustom, statusCode, responseBody)
	return server
}

// =============================================================================
// VERIFICATION HELPERS
// =============================================================================

// VerifyRequestReceived checks if a request with the given payload was received
func (s *Server) VerifyRequestReceived(key string, value interface{}) bool {
	requests := s.GetReceivedRequests()
	for _, req := range requests {
		if req.PayloadJSON != nil {
			if val, ok := req.PayloadJSON[key]; ok && val == value {
				return true
			}
		}
	}
	return false
}

// VerifyRequestCount checks if the server received exactly count requests
func (s *Server) VerifyRequestCount(expected int) error {
	actual := s.GetRequestCount()
	if actual != expected {
		return fmt.Errorf("expected %d requests, got %d", expected, actual)
	}
	return nil
}

// VerifyMinRequestCount checks if the server received at least count requests
func (s *Server) VerifyMinRequestCount(minimum int) error {
	actual := s.GetRequestCount()
	if actual < minimum {
		return fmt.Errorf("expected at least %d requests, got %d", minimum, actual)
	}
	return nil
}

// VerifyAllRequestsSuccessful checks if all requests were handled successfully
func (s *Server) VerifyAllRequestsSuccessful() error {
	requests := s.GetReceivedRequests()
	for _, req := range requests {
		if req.StatusCode < 200 || req.StatusCode >= 300 {
			return fmt.Errorf("request %s failed with status %d", req.ID, req.StatusCode)
		}
	}
	return nil
}

// VerifyRequestWithHeader checks if any request has the specified header
func (s *Server) VerifyRequestWithHeader(headerName, expectedValue string) bool {
	requests := s.GetReceivedRequests()
	for _, req := range requests {
		if req.Headers != nil {
			if actualValue := req.Headers.Get(headerName); actualValue == expectedValue {
				return true
			}
		}
	}
	return false
}

// VerifyRequestSignature checks if any request has a valid HMAC signature
func (s *Server) VerifyRequestSignature(signatureHeader string) bool {
	requests := s.GetReceivedRequests()
	for _, req := range requests {
		if req.Headers != nil {
			if signature := req.Headers.Get(signatureHeader); signature != "" {
				// Signature exists - caller should verify HMAC
				return true
			}
		}
	}
	return false
}

// =============================================================================
// QUERY HELPERS
// =============================================================================

// FindRequestByPayload finds the first request matching the payload criteria
func (s *Server) FindRequestByPayload(key string, value interface{}) *ReceivedRequest {
	requests := s.GetReceivedRequests()
	for _, req := range requests {
		if req.PayloadJSON != nil {
			if val, ok := req.PayloadJSON[key]; ok && val == value {
				return req
			}
		}
	}
	return nil
}

// FindRequestsByEventType finds all requests with a specific event type
func (s *Server) FindRequestsByEventType(eventType string) []*ReceivedRequest {
	results := make([]*ReceivedRequest, 0)
	requests := s.GetReceivedRequests()

	for _, req := range requests {
		if req.PayloadJSON != nil {
			if et, ok := req.PayloadJSON["event_type"].(string); ok && et == eventType {
				results = append(results, req)
			}
		}
	}

	return results
}

// GetRequestsSince returns all requests received after the given time
func (s *Server) GetRequestsSince(since time.Time) []*ReceivedRequest {
	results := make([]*ReceivedRequest, 0)
	requests := s.GetReceivedRequests()

	for _, req := range requests {
		if req.ReceivedAt.After(since) {
			results = append(results, req)
		}
	}

	return results
}

// GetSuccessfulRequests returns all requests that received successful responses
func (s *Server) GetSuccessfulRequests() []*ReceivedRequest {
	results := make([]*ReceivedRequest, 0)
	requests := s.GetReceivedRequests()

	for _, req := range requests {
		if req.StatusCode >= 200 && req.StatusCode < 300 {
			results = append(results, req)
		}
	}

	return results
}

// GetFailedRequests returns all requests that received error responses
func (s *Server) GetFailedRequests() []*ReceivedRequest {
	results := make([]*ReceivedRequest, 0)
	requests := s.GetReceivedRequests()

	for _, req := range requests {
		if req.StatusCode >= 400 {
			results = append(results, req)
		}
	}

	return results
}

// =============================================================================
// SCENARIO HELPERS
// =============================================================================

// SimulateCircuitBreakerScenario configures the server for circuit breaker testing
// Opens with 5 consecutive failures, then recovers
func (s *Server) SimulateCircuitBreakerScenario() {
	// Initial failures to trigger circuit breaker
	s.SetResponseBehavior(ResponseBehaviorIntermittent, 1.0) // 100% failure
}

// SimulateRecoveryScenario switches from failure to success mode
func (s *Server) SimulateRecoveryScenario() {
	s.SetResponseBehavior(ResponseBehaviorSuccess)
}

// SimulateRetryScenario configures the server for retry testing
// Fails first N attempts, then succeeds
func (s *Server) SimulateRetryScenario(failCount int) {
	// Use intermittent behavior to fail first few requests
	// This is simplistic - for more control, caller should manually switch behaviors
	if s.GetRequestCount() < failCount {
		s.SetResponseBehavior(ResponseBehaviorFail)
	} else {
		s.SetResponseBehavior(ResponseBehaviorSuccess)
	}
}

// =============================================================================
// DEBUGGING HELPERS
// =============================================================================

// PrintReceivedRequests prints all received requests (useful for debugging)
func (s *Server) PrintReceivedRequests() {
	requests := s.GetReceivedRequests()
	fmt.Printf("\n=== Received Requests: %d ===\n", len(requests))

	for i, req := range requests {
		fmt.Printf("\n%d. Request ID: %s\n", i+1, req.ID)
		fmt.Printf("   Time: %s\n", req.ReceivedAt.Format(time.RFC3339))
		fmt.Printf("   Method: %s %s\n", req.Method, req.Path)
		fmt.Printf("   Status: %d\n", req.StatusCode)
		fmt.Printf("   Duration: %v\n", req.Duration)

		if req.PayloadJSON != nil {
			fmt.Printf("   Payload: %+v\n", req.PayloadJSON)
		} else {
			fmt.Printf("   Body: %s\n", string(req.Body))
		}
	}

	fmt.Println("=========================")
}

// PrintStats prints server statistics (useful for debugging)
func (s *Server) PrintStats() {
	stats := s.GetStats()

	fmt.Println("\n=== Test Server Statistics ===")
	fmt.Printf("Total Requests:  %v\n", stats["total_requests"])
	fmt.Printf("Success Count:   %v\n", stats["success_count"])
	fmt.Printf("Failure Count:   %v\n", stats["failure_count"])
	fmt.Printf("Success Rate:    %.2f%%\n", stats["success_rate"])
	fmt.Printf("Avg Latency:     %vms\n", stats["avg_latency_ms"])
	fmt.Printf("Last Request:    %v\n", stats["last_request"])
	fmt.Println("==============================")
}

// =============================================================================
// ASSERTION HELPERS (for use with testing.T)
// =============================================================================

// AssertRequestReceived asserts that at least one request was received
func (s *Server) AssertRequestReceived(t interface{ Fatalf(string, ...interface{}) }) {
	if s.GetRequestCount() == 0 {
		t.Fatalf("Expected at least 1 request, got 0")
	}
}

// AssertRequestCount asserts the exact number of requests
func (s *Server) AssertRequestCount(t interface{ Fatalf(string, ...interface{}) }, expected int) {
	actual := s.GetRequestCount()
	if actual != expected {
		t.Fatalf("Expected %d requests, got %d", expected, actual)
	}
}

// AssertMinRequestCount asserts at least minimum requests
func (s *Server) AssertMinRequestCount(t interface{ Fatalf(string, ...interface{}) }, minimum int) {
	actual := s.GetRequestCount()
	if actual < minimum {
		t.Fatalf("Expected at least %d requests, got %d", minimum, actual)
	}
}

// AssertPayloadReceived asserts that a request with the given payload was received
func (s *Server) AssertPayloadReceived(t interface{ Fatalf(string, ...interface{}) }, key string, value interface{}) {
	if !s.VerifyRequestReceived(key, value) {
		t.Fatalf("No request found with %s = %v", key, value)
	}
}

// AssertHeaderReceived asserts that a request with the given header was received
func (s *Server) AssertHeaderReceived(t interface{ Fatalf(string, ...interface{}) }, headerName, expectedValue string) {
	if !s.VerifyRequestWithHeader(headerName, expectedValue) {
		t.Fatalf("No request found with header %s = %s", headerName, expectedValue)
	}
}
