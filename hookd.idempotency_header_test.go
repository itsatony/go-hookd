package hookd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDelivery_SendsIdempotencyKeyHeader pins go-hookd#2: the key a queuer gives
// is stored on the delivery and sent as X-Webhook-Idempotency-Key; without one
// the header is absent (never an empty value).
func TestDelivery_SendsIdempotencyKeyHeader(t *testing.T) {
	var (
		mu  sync.Mutex
		got = map[string][]string{} // delivery id -> header values
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.Header.Get(HeaderDeliveryID)] = r.Header.Values(HeaderIdempotencyKey)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repo := NewMockRepository()
	cfg := NewConfig("postgres://test")
	cfg.QueuePollInterval = 5
	cfg.QueueIdleMaxInterval = 20
	m, err := NewManager(cfg, repo, WithAllowPrivateDestinations())
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, m.Start(ctx))
	defer func() { _ = m.Stop() }()

	sub, err := m.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID: "t1", URL: server.URL, Secret: "s3cr3t-s3cr3t-s3cr3t", EventTypes: []string{"a.b"},
	})
	require.NoError(t, err)

	withKey, err := m.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID, EventType: "a.b", Payload: map[string]any{"x": 1}, IdempotencyKey: "caller-key-1",
	})
	require.NoError(t, err)
	inline, err := m.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
		URL: server.URL, EventType: "a.b", Payload: map[string]any{"x": 2}, TenantID: "t1", IdempotencyKey: "caller-key-2",
	})
	require.NoError(t, err)
	withoutKey, err := m.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID, EventType: "a.b", Payload: map[string]any{"x": 3},
	})
	require.NoError(t, err)

	stored, err := repo.GetDelivery(ctx, withKey.ID)
	require.NoError(t, err)
	assert.Equal(t, "caller-key-1", stored.IdempotencyKey)

	// The real worker loop claims, renews and sends them.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 3
	}, 5*time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"caller-key-1"}, got[withKey.ID])
	assert.Equal(t, []string{"caller-key-2"}, got[inline.ID])
	assert.Empty(t, got[withoutKey.ID], "no key -> no header")
	assert.Len(t, got, 3)
}
