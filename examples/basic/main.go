package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/itsatony/go-hookd"
	"github.com/itsatony/go-version"
)

func main() {
	// STEP 1: Initialize version management FIRST (mandatory per CLAUDE.md)
	// This must happen before any other operations
	if err := version.Initialize(
		version.WithManifestPath("../../versions.yaml"), // Relative to examples/basic/
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

	fmt.Println("=== go-hookd Basic Example ===")
	fmt.Printf("Version: %s (commit: %s)\n", versionInfo.Project.Version, versionInfo.Git.Commit)
	fmt.Printf("Database: %s\n\n", dbURL)

	// Create configuration
	config := hookd.NewConfig(dbURL)
	config.WorkerCount = 5
	config.QueuePollInterval = 1000 // 1 second
	config.DefaultMaxRetries = 3
	config.DefaultInitialBackoffMs = 1000
	config.DefaultMaxBackoffMs = 30000
	config.DefaultBackoffFactor = 2.0

	fmt.Println("Configuration:")
	fmt.Printf("  Workers: %d\n", config.WorkerCount)
	fmt.Printf("  Poll Interval: %dms\n", config.QueuePollInterval)
	fmt.Printf("  Max Retries: %d\n", config.DefaultMaxRetries)
	fmt.Println()

	// For this example, use mock repository (no database required)
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
	fmt.Println()

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nShutting down gracefully...")
		cancel()
		if err := manager.Stop(); err != nil {
			log.Printf("Error stopping manager: %v", err)
		}
		os.Exit(0)
	}()

	// Create a subscription
	fmt.Println("Creating webhook subscription...")
	subscription, err := manager.CreateSubscription(ctx, &hookd.CreateSubscriptionRequest{
		TenantID:   "tenant_demo",
		URL:        "https://webhook.site/unique-id", // Replace with your webhook URL
		EventTypes: []string{"user.created", "user.updated", "user.deleted"},
		Secret:     "demo_secret_key_12345",
	})
	if err != nil {
		log.Fatalf("Failed to create subscription: %v", err)
	}

	fmt.Printf("✓ Subscription created: %s\n", subscription.ID)
	fmt.Printf("  URL: %s\n", subscription.URL)
	fmt.Printf("  Event Types: %v\n", subscription.EventTypes)
	fmt.Println()

	// Queue some deliveries
	fmt.Println("Queueing webhook deliveries...")

	events := []struct {
		eventType string
		payload   map[string]interface{}
	}{
		{
			"user.created",
			map[string]interface{}{
				"user_id":    "user_001",
				"email":      "alice@example.com",
				"created_at": time.Now().Format(time.RFC3339),
			},
		},
		{
			"user.updated",
			map[string]interface{}{
				"user_id":    "user_001",
				"email":      "alice.smith@example.com",
				"updated_at": time.Now().Format(time.RFC3339),
			},
		},
		{
			"user.created",
			map[string]interface{}{
				"user_id":    "user_002",
				"email":      "bob@example.com",
				"created_at": time.Now().Format(time.RFC3339),
			},
		},
	}

	for i, event := range events {
		delivery, err := manager.QueueDelivery(ctx, &hookd.QueueDeliveryRequest{
			SubscriptionID: subscription.ID,
			EventType:      event.eventType,
			Payload:        event.payload,
			IdempotencyKey: fmt.Sprintf("demo_event_%d", i+1),
		})
		if err != nil {
			log.Printf("Failed to queue delivery %d: %v", i+1, err)
			continue
		}

		fmt.Printf("✓ Delivery %d queued: %s (event: %s)\n", i+1, delivery.ID, event.eventType)
	}
	fmt.Println()

	// List all subscriptions
	fmt.Println("Listing subscriptions for tenant...")
	subs, err := manager.ListSubscriptions(ctx, &hookd.SubscriptionFilter{
		TenantID: "tenant_demo",
	})
	if err != nil {
		log.Printf("Failed to list subscriptions: %v", err)
	} else {
		fmt.Printf("Found %d subscription(s) for tenant_demo\n", len(subs))
	}
	fmt.Println()

	// Demonstrate subscription status changes
	fmt.Println("Demonstrating subscription status changes...")

	// Pause
	paused, err := manager.PauseSubscription(ctx, subscription.ID)
	if err != nil {
		log.Printf("Failed to pause subscription: %v", err)
	} else {
		fmt.Printf("✓ Subscription paused (status: %s)\n", paused.Status)
	}

	time.Sleep(500 * time.Millisecond)

	// Resume
	resumed, err := manager.ResumeSubscription(ctx, subscription.ID)
	if err != nil {
		log.Printf("Failed to resume subscription: %v", err)
	} else {
		fmt.Printf("✓ Subscription resumed (status: %s)\n", resumed.Status)
	}
	fmt.Println()

	// Wait for deliveries to be processed
	fmt.Println("Waiting for deliveries to be processed (5 seconds)...")
	fmt.Println("(Note: Deliveries will fail as webhook.site URL is not configured)")
	time.Sleep(5 * time.Second)

	// Check delivery statuses
	fmt.Println("\nChecking delivery statuses...")
	filter := &hookd.SubscriptionFilter{
		TenantID: "tenant_demo",
	}
	subs, _ = manager.ListSubscriptions(ctx, filter)
	for _, sub := range subs {
		fmt.Printf("Subscription: %s\n", sub.ID)
	}

	// Keep running until interrupted
	fmt.Println("\nExample running. Press Ctrl+C to exit.")
	fmt.Println("(Manager will continue processing deliveries in background)")

	select {}
}
