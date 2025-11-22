package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/itsatony/go-hookd"
	"github.com/itsatony/go-version"
)

// =============================================================================
// HTTP SERVER
// =============================================================================

type Server struct {
	manager *hookd.Manager
	port    string
	server  *http.Server
}

func NewServer(manager *hookd.Manager, port string) *Server {
	return &Server{
		manager: manager,
		port:    port,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", s.handleHealth)

	// Subscription endpoints
	mux.HandleFunc("POST /api/v1/subscriptions", s.handleCreateSubscription)
	mux.HandleFunc("GET /api/v1/subscriptions/{id}", s.handleGetSubscription)
	mux.HandleFunc("PUT /api/v1/subscriptions/{id}", s.handleUpdateSubscription)
	mux.HandleFunc("DELETE /api/v1/subscriptions/{id}", s.handleDeleteSubscription)
	mux.HandleFunc("GET /api/v1/subscriptions", s.handleListSubscriptions)
	mux.HandleFunc("POST /api/v1/subscriptions/{id}/pause", s.handlePauseSubscription)
	mux.HandleFunc("POST /api/v1/subscriptions/{id}/resume", s.handleResumeSubscription)

	// Delivery endpoints
	mux.HandleFunc("POST /api/v1/deliveries", s.handleQueueDelivery)
	mux.HandleFunc("GET /api/v1/deliveries/{id}", s.handleGetDelivery)
	mux.HandleFunc("GET /api/v1/deliveries/{id}/attempts", s.handleGetDeliveryAttempts)
	mux.HandleFunc("POST /api/v1/deliveries/{id}/retry", s.handleRetryDelivery)

	s.server = &http.Server{
		Addr:         ":" + s.port,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	fmt.Printf("Starting HTTP server on :%s\n", s.port)
	return s.server.ListenAndServe()
}

func (s *Server) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// =============================================================================
// HEALTH CHECK
// =============================================================================

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// =============================================================================
// SUBSCRIPTION HANDLERS
// =============================================================================

type CreateSubscriptionRequestHTTP struct {
	TenantID    string                 `json:"tenant_id"`
	URL         string                 `json:"url"`
	EventTypes  []string               `json:"event_types"`
	Secret      string                 `json:"secret"`
	Headers     map[string]string      `json:"headers,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	RetryPolicy *hookd.RetryPolicy  `json:"retry_policy,omitempty"`
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var req CreateSubscriptionRequestHTTP
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	sub, err := s.manager.CreateSubscription(r.Context(), &hookd.CreateSubscriptionRequest{
		TenantID:    req.TenantID,
		URL:         req.URL,
		EventTypes:  req.EventTypes,
		Secret:      req.Secret,
		Headers:     req.Headers,
		Metadata:    req.Metadata,
		RetryPolicy: req.RetryPolicy,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, sub)
}

func (s *Server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	sub, err := s.manager.GetSubscription(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Subscription not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, sub)
}

type UpdateSubscriptionRequestHTTP struct {
	URL         *string                 `json:"url,omitempty"`
	EventTypes  *[]string               `json:"event_types,omitempty"`
	Headers     *map[string]string      `json:"headers,omitempty"`
	Metadata    *map[string]interface{} `json:"metadata,omitempty"`
	RetryPolicy *hookd.RetryPolicy   `json:"retry_policy,omitempty"`
	Status      *string                 `json:"status,omitempty"`
}

func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req UpdateSubscriptionRequestHTTP
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	sub, err := s.manager.UpdateSubscription(r.Context(), id, &hookd.UpdateSubscriptionRequest{
		URL:         req.URL,
		EventTypes:  req.EventTypes,
		Headers:     req.Headers,
		Metadata:    req.Metadata,
		RetryPolicy: req.RetryPolicy,
		Status:      req.Status,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Subscription not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, sub)
}

func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.manager.DeleteSubscription(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Subscription not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	filter := &hookd.SubscriptionFilter{
		TenantID: r.URL.Query().Get("tenant_id"),
		Status:   r.URL.Query().Get("status"),
	}

	eventTypes := r.URL.Query().Get("event_types")
	if eventTypes != "" {
		filter.EventTypes = strings.Split(eventTypes, ",")
	}

	subs, err := s.manager.ListSubscriptions(r.Context(), filter)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, subs)
}

func (s *Server) handlePauseSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	sub, err := s.manager.PauseSubscription(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Subscription not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, sub)
}

func (s *Server) handleResumeSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	sub, err := s.manager.ResumeSubscription(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Subscription not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, sub)
}

// =============================================================================
// DELIVERY HANDLERS
// =============================================================================

type QueueDeliveryRequestHTTP struct {
	SubscriptionID string                 `json:"subscription_id"`
	EventType      string                 `json:"event_type"`
	Payload        map[string]interface{} `json:"payload"`
	IdempotencyKey string                 `json:"idempotency_key,omitempty"`
}

func (s *Server) handleQueueDelivery(w http.ResponseWriter, r *http.Request) {
	var req QueueDeliveryRequestHTTP
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	delivery, err := s.manager.QueueDelivery(r.Context(), &hookd.QueueDeliveryRequest{
		SubscriptionID: req.SubscriptionID,
		EventType:      req.EventType,
		Payload:        req.Payload,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Subscription not found")
			return
		}
		if strings.Contains(err.Error(), "idempotency") {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, delivery)
}

func (s *Server) handleGetDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	delivery, err := s.manager.GetDelivery(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Delivery not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, delivery)
}

func (s *Server) handleGetDeliveryAttempts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	attempts, err := s.manager.GetDeliveryAttempts(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Delivery not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, attempts)
}

func (s *Server) handleRetryDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	delivery, err := s.manager.RetryDelivery(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "Delivery not found")
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, delivery)
}

// =============================================================================
// UTILITIES
// =============================================================================

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{
		"error": message,
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("%s %s - started", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Printf("%s %s - completed in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// =============================================================================
// MAIN
// =============================================================================

func main() {
	// STEP 1: Initialize version management FIRST (mandatory per CLAUDE.md)
	if err := version.Initialize(
		version.WithManifestPath("../../versions.yaml"), // Relative to examples/http-server/
		version.WithGitInfo(),
		version.WithBuildInfo(),
		version.WithValidators(
			version.NewSchemaValidator("postgres_main", "1"),
		),
	); err != nil {
		log.Fatalf("Failed to initialize version management: %v", err)
	}

	versionInfo := version.MustGet()

	// Database URL from environment or default
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://hookd:hookd@localhost:54321/hookd?sslmode=disable"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("=== go-hookd HTTP Server Example ===")
	fmt.Printf("Version: %s (commit: %s)\n", versionInfo.Project.Version, versionInfo.Git.Commit)
	fmt.Printf("Database: %s\n", dbURL)
	fmt.Printf("Port: %s\n\n", port)

	// Create configuration
	config := hookd.NewConfig(dbURL)
	config.WorkerCount = 5
	config.QueuePollInterval = 1000
	config.DefaultMaxRetries = 3
	config.DefaultInitialBackoffMs = 1000
	config.DefaultMaxBackoffMs = 30000
	config.DefaultBackoffFactor = 2.0

	// For this example, use mock repository
	fmt.Println("Using mock repository (no database required)")
	repo := hookd.NewMockRepository()

	// Initialize manager
	manager, err := hookd.NewManager(config, repo)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	// Start manager in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		log.Fatalf("Failed to start manager: %v", err)
	}
	fmt.Println("✓ Manager started")

	// Create HTTP server
	server := NewServer(manager, port)

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nShutting down gracefully...")

		// Shutdown HTTP server
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Stop(shutdownCtx); err != nil {
			log.Printf("Error stopping HTTP server: %v", err)
		}

		// Stop manager
		cancel()
		if err := manager.Stop(); err != nil {
			log.Printf("Error stopping manager: %v", err)
		}
		os.Exit(0)
	}()

	// Start HTTP server
	fmt.Println("✓ HTTP server started")
	fmt.Println("Example API calls:")
	fmt.Println("  curl http://localhost:8080/health")
	fmt.Println("  curl -X POST http://localhost:8080/api/v1/subscriptions -d '{...}'")
	fmt.Println("  curl http://localhost:8080/api/v1/subscriptions")
	fmt.Println("\nPress Ctrl+C to exit.")

	if err := server.Start(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}
