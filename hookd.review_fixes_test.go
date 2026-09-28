package hookd

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pins for the v0.11.0 critical-review findings.

// A worker holding a lapsed claim (re-claimed elsewhere) must not send.
func TestProcessDelivery_StaleClaimNeverSends(t *testing.T) {
	rc := newReceiver(t, "stored-secret-value-123", http.StatusOK)
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations())
	d := seedSubscriptionDelivery(t, m, repo, rc.server.URL, "stored-secret-value-123")
	ctx := context.Background()

	stale, err := repo.ClaimPendingDeliveries(ctx, 1, time.Millisecond)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	time.Sleep(5 * time.Millisecond)
	fresh, err := repo.ClaimPendingDeliveries(ctx, 1, testClaimLease)
	require.NoError(t, err)
	require.Len(t, fresh, 1)

	m.processDelivery(ctx, stale[0])
	assert.Equal(t, int32(0), rc.hits.Load())
	stored, err := repo.GetDelivery(ctx, d.ID)
	require.NoError(t, err)
	assert.True(t, stored.NextRetryAt.Equal(*fresh[0].NextRetryAt), "the new holder's claim is untouched")
}

// An open circuit parks the row until the circuit's retry time.
func TestProcessDelivery_OpenCircuitParksRow(t *testing.T) {
	rc := newReceiver(t, "stored-secret-value-123", http.StatusOK)
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations())
	d := seedSubscriptionDelivery(t, m, repo, rc.server.URL, "stored-secret-value-123")
	ctx := context.Background()
	retryAt := time.Now().Add(10 * time.Minute).Truncate(time.Microsecond)
	require.NoError(t, repo.UpdateCircuitBreakerState(ctx, &CircuitBreakerState{
		Endpoint: rc.server.URL, State: CircuitBreakerStateOpen, NextRetryAt: retryAt,
	}))

	m.processDelivery(ctx, claimForTest(t, m, d))
	assert.Equal(t, int32(0), rc.hits.Load())
	stored, err := repo.GetDelivery(ctx, d.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.NextRetryAt)
	assert.True(t, stored.NextRetryAt.Equal(retryAt), "parked until the circuit may half-open")
}

// Claimed-but-unstarted rows are released (due now) on shutdown.
func TestReleaseClaims_MakesRowsDueNow(t *testing.T) {
	m, repo := newEgressTestManager(t)
	ctx := context.Background()
	for _, id := range []string{"dlv_r1", "dlv_r2"} {
		require.NoError(t, repo.CreateDelivery(ctx, createTestDelivery(t, id, "", "tenant_r")))
	}
	claimed, err := repo.ClaimPendingDeliveries(ctx, 10, testClaimLease)
	require.NoError(t, err)
	require.Len(t, claimed, 2)
	m.releaseClaims(claimed)
	again, err := repo.ClaimPendingDeliveries(ctx, 10, testClaimLease)
	require.NoError(t, err)
	assert.Len(t, again, 2)
}

func TestRetryDeadLetter_OnlyOnce(t *testing.T) {
	m, repo := newEgressTestManager(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateDelivery(ctx, createTestDelivery(t, "dlv_dl", "", "tenant_dl")))
	require.NoError(t, repo.MoveToDeadLetter(ctx, "dlv_dl", "test"))
	require.NoError(t, m.Start(ctx))
	defer func() { _ = m.Stop() }()

	d, err := m.RetryDeadLetter(ctx, "dlv_dl")
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusPending, d.Status)
	_, err = m.RetryDeadLetter(ctx, "dlv_dl")
	assert.ErrorIs(t, err, ErrDeliveryNotDeadLetter, "a second redrive is refused")
	_, err = m.RetryDeadLetter(ctx, "dlv_missing")
	assert.ErrorIs(t, err, ErrDeliveryNotFound)
}

func TestValidateIdempotencyKey(t *testing.T) {
	for key, ok := range map[string]bool{
		"":                            true,
		"evt_123:abc-DEF.ok":          true,
		"has space":                   true,
		"tab\there":                   false,
		"crlf\r\nX-Injected: 1":       false,
		"ünïcode":                     false,
		string(make([]byte, 256)):     false,
		"x" + string(make([]byte, 0)): true,
	} {
		err := validateIdempotencyKey(key)
		if ok {
			assert.NoError(t, err, "%q", key)
		} else {
			assert.Error(t, err, "%q", key)
		}
	}
	req := &QueueInlineDeliveryRequest{URL: "https://example.com/h", EventType: "e", Payload: map[string]any{}, TenantID: "t", IdempotencyKey: "bad\nkey"}
	assert.Error(t, req.Validate())
}

type panickingResolver struct{}

func (panickingResolver) Resolve(context.Context, SecretRequest) (string, error) { panic("boom") }

func TestSecretResolver_PanicAndEmptySecretSendNothing(t *testing.T) {
	for name, r := range map[string]SecretResolver{
		"panic": panickingResolver{},
		"empty": SecretResolverFunc(func(context.Context, SecretRequest) (string, error) { return "", nil }),
	} {
		t.Run(name, func(t *testing.T) {
			rc := newReceiver(t, "", http.StatusOK)
			m, repo := newEgressTestManager(t, WithAllowPrivateDestinations(), WithSecretResolver(r))
			d := seedSubscriptionDelivery(t, m, repo, rc.server.URL, testSecretRef)
			m.processDelivery(context.Background(), claimForTest(t, m, d))
			assert.Equal(t, int32(0), rc.hits.Load())
			attempts, err := repo.GetDeliveryAttempts(context.Background(), d.ID)
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			assert.Equal(t, ErrMsgSigningSecretUnavailable, attempts[0].Error)
		})
	}
}

func TestEffectiveBatchSize(t *testing.T) {
	cfg := NewConfig("postgres://x")
	assert.Equal(t, 1, cfg.EffectiveBatchSize(), "default")
	cfg.MaxBatchSize = 200 // deepr's value
	assert.Equal(t, 2, cfg.EffectiveBatchSize(), "90s lease / (30s+10s) per attempt")
	cfg.ClaimLeaseMs = 10 * 60 * 1000
	assert.Equal(t, 15, cfg.EffectiveBatchSize())
	cfg.MaxBatchSize = 3
	assert.Equal(t, 3, cfg.EffectiveBatchSize())
}

func TestCleanupByFilter_AllTenantsAloneRefusedAtRepo(t *testing.T) {
	for name, repo := range cleanupRepos(t) {
		_, err := repo.DeleteDeliveriesByFilter(context.Background(), &CleanupFilter{AllTenants: true})
		assert.Error(t, err, name)
		_, err = repo.CountDeliveriesByFilter(context.Background(), &CleanupFilter{AllTenants: true})
		assert.Error(t, err, name)
	}
}
