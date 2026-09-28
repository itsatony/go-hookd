package hookd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/itsatony/go-hookd/verify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingResolver counts calls and maps refs to secrets.
type recordingResolver struct {
	mu    sync.Mutex
	refs  []string
	calls atomic.Int32
	fail  atomic.Bool
}

const (
	testSecretRef   = "tresor://agora/webhooks/sub-1"
	testResolvedKey = "resolved-signing-key-xyz"
	testVaultCause  = "tresor: permission denied for path tresor://agora/webhooks/sub-1"
)

func (r *recordingResolver) Resolve(_ context.Context, ref string) (string, error) {
	r.calls.Add(1)
	r.mu.Lock()
	r.refs = append(r.refs, ref)
	r.mu.Unlock()
	if r.fail.Load() {
		return "", errors.New(testVaultCause)
	}
	return testResolvedKey, nil
}

// receiver records each request's signature verification against key.
type receiver struct {
	hits     atomic.Int32
	verified atomic.Int32
	server   *httptest.Server
}

func newReceiver(t *testing.T, key string, status int) *receiver {
	t.Helper()
	rc := &receiver{}
	rc.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		if verify.Quick(key, r.Header.Get(HeaderTimestamp), body, r.Header.Get(HeaderSignature)) {
			rc.verified.Add(1)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(rc.server.Close)
	return rc
}

func seedSubscriptionDelivery(t *testing.T, m *Manager, repo *MockRepository, url, secretRef string) *Delivery {
	t.Helper()
	ctx := context.Background()
	sub := createTestSubscription(t, "sub_res", "tenant_res", url)
	sub.Secret = secretRef
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	d := createTestDelivery(t, "dlv_res", sub.ID, sub.TenantID)
	d.MaxAttempts = 5
	require.NoError(t, repo.CreateDelivery(ctx, d))
	return d
}

func TestSecretResolver_ResolvedPerAttemptAndUsedToSign(t *testing.T) {
	rc := newReceiver(t, testResolvedKey, http.StatusInternalServerError) // force a retry
	res := &recordingResolver{}
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations(), WithSecretResolver(res))
	d := seedSubscriptionDelivery(t, m, repo, rc.server.URL, testSecretRef)
	ctx := context.Background()

	m.processDelivery(ctx, claimForTest(t, m, d))
	m.processDelivery(ctx, claimForTest(t, m, d))

	assert.Equal(t, int32(2), res.calls.Load(), "Resolve runs once PER ATTEMPT, never cached")
	assert.Equal(t, []string{testSecretRef, testSecretRef}, res.refs, "the stored value is passed verbatim as the ref")
	assert.Equal(t, int32(2), rc.hits.Load())
	assert.Equal(t, int32(2), rc.verified.Load(), "signed with the RESOLVED secret")
}

func TestSecretResolver_FailureSendsNothingAndStaysOpaque(t *testing.T) {
	rc := newReceiver(t, testResolvedKey, http.StatusOK)
	res := &recordingResolver{}
	res.fail.Store(true)
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations(), WithSecretResolver(res))
	d := seedSubscriptionDelivery(t, m, repo, rc.server.URL, testSecretRef)
	ctx := context.Background()

	m.processDelivery(ctx, claimForTest(t, m, d))

	assert.Equal(t, int32(0), rc.hits.Load(), "no unsigned / wrongly signed webhook may leave")
	attempts, err := repo.GetDeliveryAttempts(ctx, d.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, ErrMsgSigningSecretUnavailable, attempts[0].Error)
	assert.NotContains(t, attempts[0].Error, "tresor", "the resolver's cause must not reach the row")

	stored, err := repo.GetDelivery(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusPending, stored.Status, "retried like a transport failure")
}

func TestSecretResolver_TestSubscription(t *testing.T) {
	rc := newReceiver(t, testResolvedKey, http.StatusOK)
	res := &recordingResolver{}
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations(), WithSecretResolver(res))
	seedSubscriptionDelivery(t, m, repo, rc.server.URL, testSecretRef)
	require.NoError(t, m.Start(context.Background()))
	defer func() { _ = m.Stop() }()

	result, err := m.TestSubscription(context.Background(), "sub_res")
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, int32(1), rc.verified.Load())

	res.fail.Store(true)
	m.testRateLimiter.Delete("sub_res")
	result, err = m.TestSubscription(context.Background(), "sub_res")
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Equal(t, ErrMsgSigningSecretUnavailable, result.Error)
	assert.Equal(t, int32(1), rc.hits.Load(), "a failed resolve sends no ping")
}

func TestSecretResolver_DefaultIsStoredSecret(t *testing.T) {
	rc := newReceiver(t, "stored-secret-value-123", http.StatusOK)
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations())
	d := seedSubscriptionDelivery(t, m, repo, rc.server.URL, "stored-secret-value-123")
	m.processDelivery(context.Background(), claimForTest(t, m, d))
	assert.Equal(t, int32(1), rc.verified.Load())
}

func TestWithSecretResolver_Nil(t *testing.T) {
	_, err := NewManager(NewConfig("postgres://test"), NewMockRepository(), WithSecretResolver(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrMsgSecretResolverNil)
}

func TestSecretResolverFunc(t *testing.T) {
	f := SecretResolverFunc(func(_ context.Context, ref string) (string, error) { return strings.ToUpper(ref), nil })
	got, err := f.Resolve(context.Background(), "abc")
	require.NoError(t, err)
	assert.Equal(t, "ABC", got)
}

// TestDelivery_ResponseBodyReadIsBounded: a subscriber endpoint cannot stream a
// worker out of memory; at most MaxResponseBodyLength bytes are kept.
func TestDelivery_ResponseBodyReadIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := []byte(strings.Repeat("x", 64*1024))
		for i := 0; i < 64; i++ { // 4 MiB
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations())
	d := seedSubscriptionDelivery(t, m, repo, server.URL, "stored-secret-value-123")
	m.processDelivery(context.Background(), claimForTest(t, m, d))

	attempts, err := repo.GetDeliveryAttempts(context.Background(), d.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, strings.Repeat("x", MaxResponseBodyLength)+"...", attempts[0].ResponseBody, "kept body is capped (truncateString marks the cut)")
}
