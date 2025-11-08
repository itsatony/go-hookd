package testapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// =============================================================================
// HTTP HANDLERS
// =============================================================================

// handleWebhook handles incoming webhook requests
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Generate request ID
	requestID := s.generateRequestID()

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse JSON payload if content type is JSON
	var payloadJSON map[string]interface{}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(body, &payloadJSON); err == nil {
			// Successfully parsed JSON
		}
	}

	// Get current response configuration
	config := s.GetResponseConfig()

	// Determine response based on behavior
	var statusCode int
	var responseBody string
	var responseHeaders http.Header = make(http.Header)

	switch config.Behavior {
	case ResponseBehaviorSuccess:
		statusCode = http.StatusOK
		responseBody = `{"status":"ok","message":"webhook received"}`
		responseHeaders.Set("Content-Type", "application/json")

	case ResponseBehaviorFail:
		statusCode = http.StatusInternalServerError
		responseBody = `{"status":"error","message":"internal server error"}`
		responseHeaders.Set("Content-Type", "application/json")

	case ResponseBehaviorDelay:
		// Add delay before responding
		time.Sleep(config.Delay)
		statusCode = http.StatusOK
		responseBody = `{"status":"ok","message":"webhook received (delayed)"}`
		responseHeaders.Set("Content-Type", "application/json")

	case ResponseBehaviorTimeout:
		// Never respond - wait until client times out or context cancelled
		// This allows the server to close properly while still testing timeout behavior
		select {
		case <-r.Context().Done():
			// Client disconnected or test finished
			return
		case <-time.After(2 * time.Minute):
			// Fallback timeout to prevent indefinite hangs
			return
		}

	case ResponseBehaviorIntermittent:
		// Fail based on failure rate
		if s.shouldFail() {
			statusCode = http.StatusInternalServerError
			responseBody = `{"status":"error","message":"intermittent failure"}`
		} else {
			statusCode = http.StatusOK
			responseBody = `{"status":"ok","message":"webhook received"}`
		}
		responseHeaders.Set("Content-Type", "application/json")

	case ResponseBehaviorCustom:
		statusCode = config.StatusCode
		responseBody = config.ResponseBody
		for k, v := range config.Headers {
			responseHeaders.Set(k, v)
		}

	default:
		statusCode = http.StatusOK
		responseBody = `{"status":"ok","message":"webhook received"}`
		responseHeaders.Set("Content-Type", "application/json")
	}

	// Record the request
	duration := time.Since(startTime)
	receivedReq := &ReceivedRequest{
		ID:              requestID,
		ReceivedAt:      startTime,
		Method:          r.Method,
		Path:            r.URL.Path,
		RemoteAddr:      r.RemoteAddr,
		ContentType:     r.Header.Get("Content-Type"),
		Headers:         r.Header.Clone(),
		Body:            body,
		PayloadJSON:     payloadJSON,
		StatusCode:      statusCode,
		ResponseBody:    responseBody,
		ResponseHeaders: responseHeaders,
		Duration:        duration,
	}

	s.recordRequest(receivedReq)

	// Send response
	for k, values := range responseHeaders {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(statusCode)
	w.Write([]byte(responseBody))
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	stats := s.GetStats()
	response := map[string]interface{}{
		"status": "healthy",
		"stats":  stats,
	}

	json.NewEncoder(w).Encode(response)
}

// handleGetRequests returns all received requests
func (s *Server) handleGetRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requests := s.GetReceivedRequests()

	// Convert to simplified format for JSON
	simplified := make([]map[string]interface{}, len(requests))
	for i, req := range requests {
		simplified[i] = map[string]interface{}{
			"id":           req.ID,
			"received_at":  req.ReceivedAt.Format(time.RFC3339),
			"method":       req.Method,
			"path":         req.Path,
			"content_type": req.ContentType,
			"body":         string(req.Body),
			"payload":      req.PayloadJSON,
			"status_code":  req.StatusCode,
			"duration_ms":  req.Duration.Milliseconds(),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"count":    len(requests),
		"requests": simplified,
	})
}

// handleGetRequest returns a specific request by ID
func (s *Server) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract request ID from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid request ID", http.StatusBadRequest)
		return
	}
	requestID := parts[3]

	req := s.GetReceivedRequest(requestID)
	if req == nil {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}

	// Convert to detailed format
	response := map[string]interface{}{
		"id":               req.ID,
		"received_at":      req.ReceivedAt.Format(time.RFC3339),
		"method":           req.Method,
		"path":             req.Path,
		"remote_addr":      req.RemoteAddr,
		"content_type":     req.ContentType,
		"headers":          req.Headers,
		"body":             string(req.Body),
		"payload":          req.PayloadJSON,
		"status_code":      req.StatusCode,
		"response_body":    req.ResponseBody,
		"response_headers": req.ResponseHeaders,
		"duration_ms":      req.Duration.Milliseconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleConfig handles response configuration
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Get current config
		config := s.GetResponseConfig()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"behavior":     config.Behavior,
			"delay_ms":     config.Delay.Milliseconds(),
			"failure_rate": config.FailureRate,
			"status_code":  config.StatusCode,
			"response":     config.ResponseBody,
		})

	case http.MethodPost, http.MethodPut:
		// Update config
		var input struct {
			Behavior    ResponseBehavior  `json:"behavior"`
			DelayMs     int               `json:"delay_ms,omitempty"`
			FailureRate float64           `json:"failure_rate,omitempty"`
			StatusCode  int               `json:"status_code,omitempty"`
			Response    string            `json:"response,omitempty"`
			Headers     map[string]string `json:"headers,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}

		config := ResponseConfig{
			Behavior:     input.Behavior,
			Delay:        time.Duration(input.DelayMs) * time.Millisecond,
			FailureRate:  input.FailureRate,
			StatusCode:   input.StatusCode,
			ResponseBody: input.Response,
			Headers:      input.Headers,
		}

		s.SetResponseConfig(config)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"message": "configuration updated",
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleReset resets the server state
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.Reset()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "server state reset",
	})
}

// handleStats returns server statistics
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := s.GetStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
