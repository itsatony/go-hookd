// Package hookd provides batch operations for improved efficiency.
//
// This file implements batch operations that allow creating multiple
// subscriptions or queuing multiple deliveries in a single call.
package hookd

import (
	"context"
	"fmt"
	"sync"

	"github.com/itsatony/go-cuserr"
)

// =============================================================================
// BATCH OPERATIONS
// =============================================================================

// BatchResult represents the result of a batch operation.
type BatchResult[T any] struct {
	// Success indicates whether the operation succeeded
	Success bool `json:"success"`

	// Result contains the successful result (nil if failed)
	Result *T `json:"result,omitempty"`

	// Error contains the error if the operation failed
	Error error `json:"error,omitempty"`

	// Index is the position in the original batch request
	Index int `json:"index"`
}

// QueueDeliveriesRequest contains multiple delivery requests to be queued.
type QueueDeliveriesRequest struct {
	// Deliveries is the list of delivery requests to queue
	Deliveries []*QueueDeliveryRequest `json:"deliveries"`
}

// QueueDeliveriesResponse contains the results of a batch delivery operation.
type QueueDeliveriesResponse struct {
	// TotalRequested is the number of deliveries requested
	TotalRequested int `json:"total_requested"`

	// TotalSucceeded is the number of deliveries successfully queued
	TotalSucceeded int `json:"total_succeeded"`

	// TotalFailed is the number of deliveries that failed
	TotalFailed int `json:"total_failed"`

	// Results contains the individual results for each delivery
	Results []*BatchResult[Delivery] `json:"results"`
}

// QueueDeliveries queues multiple webhook deliveries in a single operation.
//
// This operation processes all delivery requests concurrently and returns
// the results for each one. Failures in individual deliveries do not affect
// other deliveries in the batch.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - req: The batch request containing multiple delivery requests
//
// Returns:
//   - QueueDeliveriesResponse with individual results
//   - error if the entire batch operation fails (e.g., validation error)
//
// Thread-safe: Yes
func (m *Manager) QueueDeliveries(ctx context.Context, req *QueueDeliveriesRequest) (*QueueDeliveriesResponse, error) {
	if req == nil {
		return nil, cuserr.NewValidationError("request", "request is required")
	}

	if len(req.Deliveries) == 0 {
		return nil, cuserr.NewValidationError("deliveries", "at least one delivery is required")
	}

	if len(req.Deliveries) > MaxBatchSize {
		return nil, cuserr.NewValidationError("deliveries", fmt.Sprintf("batch size exceeds maximum (%d)", MaxBatchSize))
	}

	response := &QueueDeliveriesResponse{
		TotalRequested: len(req.Deliveries),
		Results:        make([]*BatchResult[Delivery], len(req.Deliveries)),
	}

	// Process deliveries concurrently
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, deliveryReq := range req.Deliveries {
		wg.Add(1)
		go func(index int, dreq *QueueDeliveryRequest) {
			defer wg.Done()

			// Queue the delivery
			delivery, err := m.QueueDelivery(ctx, dreq)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				response.Results[index] = &BatchResult[Delivery]{
					Success: false,
					Error:   err,
					Index:   index,
				}
				response.TotalFailed++
			} else {
				response.Results[index] = &BatchResult[Delivery]{
					Success: true,
					Result:  delivery,
					Index:   index,
				}
				response.TotalSucceeded++
			}
		}(i, deliveryReq)
	}

	wg.Wait()

	return response, nil
}

// CreateSubscriptionsRequest contains multiple subscription creation requests.
type CreateSubscriptionsRequest struct {
	// Subscriptions is the list of subscription requests to create
	Subscriptions []*CreateSubscriptionRequest `json:"subscriptions"`
}

// CreateSubscriptionsResponse contains the results of a batch subscription creation.
type CreateSubscriptionsResponse struct {
	// TotalRequested is the number of subscriptions requested
	TotalRequested int `json:"total_requested"`

	// TotalSucceeded is the number of subscriptions successfully created
	TotalSucceeded int `json:"total_succeeded"`

	// TotalFailed is the number of subscriptions that failed
	TotalFailed int `json:"total_failed"`

	// Results contains the individual results for each subscription
	Results []*BatchResult[Subscription] `json:"results"`
}

// CreateSubscriptions creates multiple webhook subscriptions in a single operation.
//
// This operation processes all subscription requests concurrently and returns
// the results for each one. Failures in individual subscriptions do not affect
// other subscriptions in the batch.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - req: The batch request containing multiple subscription requests
//
// Returns:
//   - CreateSubscriptionsResponse with individual results
//   - error if the entire batch operation fails (e.g., validation error)
//
// Thread-safe: Yes
func (m *Manager) CreateSubscriptions(ctx context.Context, req *CreateSubscriptionsRequest) (*CreateSubscriptionsResponse, error) {
	if req == nil {
		return nil, cuserr.NewValidationError("request", "request is required")
	}

	if len(req.Subscriptions) == 0 {
		return nil, cuserr.NewValidationError("subscriptions", "at least one subscription is required")
	}

	if len(req.Subscriptions) > MaxBatchSize {
		return nil, cuserr.NewValidationError("subscriptions", fmt.Sprintf("batch size exceeds maximum (%d)", MaxBatchSize))
	}

	response := &CreateSubscriptionsResponse{
		TotalRequested: len(req.Subscriptions),
		Results:        make([]*BatchResult[Subscription], len(req.Subscriptions)),
	}

	// Process subscriptions concurrently
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, subReq := range req.Subscriptions {
		wg.Add(1)
		go func(index int, sreq *CreateSubscriptionRequest) {
			defer wg.Done()

			// Create the subscription
			subscription, err := m.CreateSubscription(ctx, sreq)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				response.Results[index] = &BatchResult[Subscription]{
					Success: false,
					Error:   err,
					Index:   index,
				}
				response.TotalFailed++
			} else {
				response.Results[index] = &BatchResult[Subscription]{
					Success: true,
					Result:  subscription,
					Index:   index,
				}
				response.TotalSucceeded++
			}
		}(i, subReq)
	}

	wg.Wait()

	return response, nil
}
