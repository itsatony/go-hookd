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
	Error   error `json:"error,omitempty"`
	Result  *T    `json:"result,omitempty"`
	Index   int   `json:"index"`
	Success bool  `json:"success"`
}

// QueueDeliveriesRequest contains multiple delivery requests to be queued.
type QueueDeliveriesRequest struct {
	// Deliveries is the list of delivery requests to queue
	Deliveries []*QueueDeliveryRequest `json:"deliveries"`
}

// QueueDeliveriesResponse contains the results of a batch delivery operation.
type QueueDeliveriesResponse struct {
	Results        []*BatchResult[Delivery] `json:"results"`
	TotalRequested int                      `json:"total_requested"`
	TotalSucceeded int                      `json:"total_succeeded"`
	TotalFailed    int                      `json:"total_failed"`
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
//   - error if the entire batch operation fails (e.g., validation error).
//
// Thread-safe: Yes.
func (m *Manager) QueueDeliveries(ctx context.Context, req *QueueDeliveriesRequest) (*QueueDeliveriesResponse, error) {
	if req == nil {
		return nil, cuserr.NewValidationError("request", ErrMsgRequestRequired)
	}

	if len(req.Deliveries) == 0 {
		return nil, cuserr.NewValidationError("deliveries", ErrMsgAtLeastOneDeliveryRequired)
	}

	if len(req.Deliveries) > MaxBatchSize {
		return nil, cuserr.NewValidationError("deliveries", fmt.Sprintf("%s (%d)", ErrMsgBatchSizeExceedsMaximum, MaxBatchSize))
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
	Results        []*BatchResult[Subscription] `json:"results"`
	TotalRequested int                          `json:"total_requested"`
	TotalSucceeded int                          `json:"total_succeeded"`
	TotalFailed    int                          `json:"total_failed"`
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
//   - error if the entire batch operation fails (e.g., validation error).
//
// Thread-safe: Yes.
func (m *Manager) CreateSubscriptions(ctx context.Context, req *CreateSubscriptionsRequest) (*CreateSubscriptionsResponse, error) {
	if req == nil {
		return nil, cuserr.NewValidationError("request", ErrMsgRequestRequired)
	}

	if len(req.Subscriptions) == 0 {
		return nil, cuserr.NewValidationError("subscriptions", ErrMsgAtLeastOneSubscriptionRequired)
	}

	if len(req.Subscriptions) > MaxBatchSize {
		return nil, cuserr.NewValidationError("subscriptions", fmt.Sprintf("%s (%d)", ErrMsgBatchSizeExceedsMaximum, MaxBatchSize))
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
