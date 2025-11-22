// Package main demonstrates the use of helper functions for cleaner API usage.
package main

import (
	"context"
	"fmt"
	"log"

	hookd "github.com/itsatony/go-hookd"
)

func main() {
	fmt.Println("=== go-hookd Helper Functions Example ===")

	// Example 1: Pointer Helpers
	fmt.Println("1. UpdateSubscriptionRequest with pointer helpers:")
	updateReq := &hookd.UpdateSubscriptionRequest{
		URL:        hookd.StringPtr("https://new-endpoint.example.com/webhook"),
		EventTypes: hookd.StringSlicePtr([]string{"order.created", "order.updated"}),
		Headers: hookd.StringMapPtr(map[string]string{
			"X-Custom-Header": "custom-value",
		}),
		Metadata: hookd.InterfaceMapPtr(map[string]interface{}{
			"version": 2,
		}),
	}
	fmt.Printf("   URL: %s\n", *updateReq.URL)
	fmt.Printf("   Event Types: %v\n\n", *updateReq.EventTypes)

	// Example 2: Value Helpers
	fmt.Println("2. Value helpers for safe dereferencing:")
	url := hookd.StringValue(updateReq.URL)
	empty := hookd.StringValue(nil) // Returns "" for nil
	fmt.Printf("   URL: %s\n", url)
	fmt.Printf("   Empty from nil: %q\n\n", empty)

	// Example 3: Copy Helpers
	fmt.Println("3. Copy helpers:")
	original := []string{"order.created", "order.updated"}
	copied := hookd.CopyStringSlice(original)
	copied[0] = "modified"
	fmt.Printf("   Original: %v\n", original)
	fmt.Printf("   Copied:   %v\n\n", copied)

	// Example 4: Real Usage
	fmt.Println("4. Real-world example:")
	repo := hookd.NewMockRepository()
	config := hookd.NewConfig("postgres://localhost/hookd")
	manager, err := hookd.NewManager(config, repo)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	ctx := context.Background()
	if err := manager.Start(ctx); err != nil {
		log.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	sub, err := manager.CreateSubscription(ctx, &hookd.CreateSubscriptionRequest{
		TenantID:   "tenant-123",
		URL:        "https://example.com/webhook",
		EventTypes: []string{"order.created"},
		Secret:     "test-secret-key-12345",
	})
	if err != nil {
		log.Fatalf("Failed to create: %v", err)
	}
	fmt.Printf("   Created: %s\n", sub.ID)

	updated, err := manager.UpdateSubscription(ctx, sub.ID, &hookd.UpdateSubscriptionRequest{
		EventTypes: hookd.StringSlicePtr([]string{"order.created", "order.updated"}),
	})
	if err != nil {
		log.Fatalf("Failed to update: %v", err)
	}
	fmt.Printf("   Updated: %v\n\n", updated.EventTypes)

	fmt.Println("✓ Helper functions improve API ergonomics")
}
