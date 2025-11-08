package testapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// BASIC SERVER TESTS
// =============================================================================

func TestServer_CreateAndClose(t *testing.T) {
	server := NewServer()
	defer server.Close()

	assert.NotEmpty(t, server.URL())
	assert.NotEmpty(t, server.WebhookURL())
	assert.Contains(t, server.WebhookURL(), "/webhook")
}

func TestServer_SuccessResponse(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send webhook request
	payload := map[string]interface{}{
		"event_type": "test.event",
		"data":       "test data",
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(server.WebhookURL(), "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify request was recorded
	assert.Equal(t, 1, server.GetRequestCount())

	lastReq := server.GetLastRequest()
	require.NotNil(t, lastReq)
	assert.Equal(t, "POST", lastReq.Method)
	assert.Equal(t, "/webhook", lastReq.Path)
	assert.Equal(t, "test.event", lastReq.PayloadJSON["event_type"])
}

func TestServer_FailureResponse(t *testing.T) {
	server := SetupFailureServer()
	defer server.Close()

	// Send webhook request
	resp, err := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify error response
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// Verify request was recorded
	assert.Equal(t, 1, server.GetRequestCount())

	lastReq := server.GetLastRequest()
	require.NotNil(t, lastReq)
	assert.Equal(t, http.StatusInternalServerError, lastReq.StatusCode)
}

func TestServer_DelayedResponse(t *testing.T) {
	delay := 500 * time.Millisecond
	server := SetupDelayedServer(delay)
	defer server.Close()

	// Send webhook request and measure time
	start := time.Now()
	resp, err := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	duration := time.Since(start)

	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response was delayed
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.GreaterOrEqual(t, duration, delay, "Response should be delayed")
}

func TestServer_IntermittentFailures(t *testing.T) {
	// 50% failure rate
	server := SetupIntermittentServer(0.5)
	defer server.Close()

	// Send multiple requests
	successCount := 0
	failureCount := 0

	for i := 0; i < 10; i++ {
		resp, err := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
		require.NoError(t, err)

		if resp.StatusCode == http.StatusOK {
			successCount++
		} else {
			failureCount++
		}
		resp.Body.Close()
	}

	// Verify we got both successes and failures
	assert.Greater(t, successCount, 0, "Should have some successes")
	assert.Greater(t, failureCount, 0, "Should have some failures")
	assert.Equal(t, 10, server.GetRequestCount())
}

func TestServer_CustomResponse(t *testing.T) {
	statusCode := 418 // I'm a teapot
	responseBody := `{"error":"coffee not available"}`

	server := SetupCustomServer(statusCode, responseBody)
	defer server.Close()

	// Send webhook request
	resp, err := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify custom response
	assert.Equal(t, statusCode, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, responseBody, string(body))
}

// =============================================================================
// REQUEST RECORDING TESTS
// =============================================================================

func TestServer_RecordMultipleRequests(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send 5 requests
	for i := 0; i < 5; i++ {
		payload := map[string]interface{}{
			"index": i,
			"test":  "recording",
		}
		body, _ := json.Marshal(payload)

		resp, err := http.Post(server.WebhookURL(), "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		resp.Body.Close()
	}

	// Verify all recorded
	assert.Equal(t, 5, server.GetRequestCount())

	requests := server.GetReceivedRequests()
	assert.Len(t, requests, 5)

	// Verify each request has correct index
	for i, req := range requests {
		assert.Equal(t, float64(i), req.PayloadJSON["index"])
		assert.Equal(t, "recording", req.PayloadJSON["test"])
	}
}

func TestServer_Reset(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send request
	resp, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	resp.Body.Close()

	assert.Equal(t, 1, server.GetRequestCount())

	// Reset
	server.Reset()

	assert.Equal(t, 0, server.GetRequestCount())
	assert.Empty(t, server.GetReceivedRequests())
}

func TestServer_GetStats(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send 3 successful requests
	for i := 0; i < 3; i++ {
		resp, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
		resp.Body.Close()
	}

	// Change to failure mode and send 2 failed requests
	server.SetResponseBehavior(ResponseBehaviorFail)
	for i := 0; i < 2; i++ {
		resp, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
		resp.Body.Close()
	}

	stats := server.GetStats()

	assert.Equal(t, 5, stats["total_requests"])
	assert.Equal(t, 3, stats["success_count"])
	assert.Equal(t, 2, stats["failure_count"])
	assert.Equal(t, 60.0, stats["success_rate"]) // 3/5 = 60%
}

// =============================================================================
// WAIT HELPERS TESTS
// =============================================================================

func TestServer_WaitForRequests(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send request in background
	go func() {
		time.Sleep(100 * time.Millisecond)
		http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	}()

	// Wait for request
	success := server.WaitForRequests(1, 2*time.Second)
	assert.True(t, success, "Should receive request within timeout")
}

func TestServer_WaitForRequestsTimeout(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Don't send any request
	success := server.WaitForRequests(1, 100*time.Millisecond)
	assert.False(t, success, "Should timeout when no request received")
}

func TestServer_WaitForRequestWithPayload(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send request in background with specific payload
	go func() {
		time.Sleep(100 * time.Millisecond)
		payload := map[string]interface{}{
			"order_id": "123",
			"status":   "completed",
		}
		body, _ := json.Marshal(payload)
		http.Post(server.WebhookURL(), "application/json", bytes.NewReader(body))
	}()

	// Wait for request with specific payload
	req := server.WaitForRequestWithPayload("order_id", "123", 2*time.Second)
	require.NotNil(t, req, "Should find request with order_id=123")
	assert.Equal(t, "completed", req.PayloadJSON["status"])
}

// =============================================================================
// VERIFICATION HELPERS TESTS
// =============================================================================

func TestServer_VerifyRequestReceived(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send request
	payload := map[string]interface{}{
		"event_type": "order.created",
		"order_id":   "123",
	}
	body, _ := json.Marshal(payload)
	resp, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// Verify
	assert.True(t, server.VerifyRequestReceived("event_type", "order.created"))
	assert.True(t, server.VerifyRequestReceived("order_id", "123"))
	assert.False(t, server.VerifyRequestReceived("order_id", "456"))
}

func TestServer_VerifyRequestCount(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send 3 requests
	for i := 0; i < 3; i++ {
		resp, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
		resp.Body.Close()
	}

	// Verify count
	assert.NoError(t, server.VerifyRequestCount(3))
	assert.Error(t, server.VerifyRequestCount(2))
	assert.Error(t, server.VerifyRequestCount(4))

	assert.NoError(t, server.VerifyMinRequestCount(3))
	assert.NoError(t, server.VerifyMinRequestCount(2))
	assert.Error(t, server.VerifyMinRequestCount(4))
}

func TestServer_FindRequestByPayload(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send multiple requests
	for i := 1; i <= 3; i++ {
		payload := map[string]interface{}{
			"order_id": fmt.Sprintf("order_%d", i),
			"amount":   100 * i,
		}
		body, _ := json.Marshal(payload)
		resp, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader(body))
		resp.Body.Close()
	}

	// Find specific request
	req := server.FindRequestByPayload("order_id", "order_2")
	require.NotNil(t, req)
	assert.Equal(t, float64(200), req.PayloadJSON["amount"])
}

func TestServer_FindRequestsByEventType(t *testing.T) {
	server := SetupSuccessServer()
	defer server.Close()

	// Send requests with different event types
	events := []string{"user.created", "order.created", "user.created"}
	for _, eventType := range events {
		payload := map[string]interface{}{
			"event_type": eventType,
		}
		body, _ := json.Marshal(payload)
		resp, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader(body))
		resp.Body.Close()
	}

	// Find by event type
	userEvents := server.FindRequestsByEventType("user.created")
	orderEvents := server.FindRequestsByEventType("order.created")

	assert.Len(t, userEvents, 2)
	assert.Len(t, orderEvents, 1)
}

// =============================================================================
// CONFIGURATION TESTS
// =============================================================================

func TestServer_ChangeResponseBehavior(t *testing.T) {
	server := NewServer()
	defer server.Close()

	// Start with success
	server.SetResponseBehavior(ResponseBehaviorSuccess)
	resp1, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	assert.Equal(t, http.StatusOK, resp1.StatusCode)
	resp1.Body.Close()

	// Change to failure
	server.SetResponseBehavior(ResponseBehaviorFail)
	resp2, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	assert.Equal(t, http.StatusInternalServerError, resp2.StatusCode)
	resp2.Body.Close()

	// Change back to success
	server.SetResponseBehavior(ResponseBehaviorSuccess)
	resp3, _ := http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	assert.Equal(t, http.StatusOK, resp3.StatusCode)
	resp3.Body.Close()

	// Verify all recorded
	assert.Equal(t, 3, server.GetRequestCount())
}

// =============================================================================
// HTTP API TESTS
// =============================================================================

func TestServer_HealthEndpoint(t *testing.T) {
	server := NewServer()
	defer server.Close()

	resp, err := http.Get(server.URL() + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	assert.Equal(t, "healthy", result["status"])
	assert.NotNil(t, result["stats"])
}

func TestServer_ConfigEndpoint(t *testing.T) {
	server := NewServer()
	defer server.Close()

	// Update config via API
	configUpdate := map[string]interface{}{
		"behavior":     "fail",
		"failure_rate": 0.5,
	}
	body, _ := json.Marshal(configUpdate)

	resp, err := http.Post(server.URL()+"/api/config", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify config was updated
	config := server.GetResponseConfig()
	assert.Equal(t, ResponseBehaviorFail, config.Behavior)
}

func TestServer_ResetEndpoint(t *testing.T) {
	server := NewServer()
	defer server.Close()

	// Send request
	http.Post(server.WebhookURL(), "application/json", bytes.NewReader([]byte("{}")))
	assert.Equal(t, 1, server.GetRequestCount())

	// Reset via API
	resp, err := http.Post(server.URL()+"/api/reset", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, server.GetRequestCount())
}
