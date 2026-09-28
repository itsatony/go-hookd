package hookd

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pins for the v0.11.0 security-review findings.

func TestSecretResolver_TenantMismatchFailsClosed(t *testing.T) {
	rc := newReceiver(t, testResolvedKey, http.StatusOK)
	res := &recordingResolver{}
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations(), WithSecretResolver(res))
	d := seedSubscriptionDelivery(t, m, repo, rc.server.URL, testSecretRef)
	d.TenantID = "someone_else"
	require.NoError(t, repo.UpdateDelivery(context.Background(), d))

	m.processDelivery(context.Background(), claimForTest(t, m, d))
	assert.Equal(t, int32(0), res.calls.Load(), "the resolver is never asked under a mismatched tenant")
	assert.Equal(t, int32(0), rc.hits.Load())
	attempts, err := repo.GetDeliveryAttempts(context.Background(), d.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, ErrMsgSigningSecretUnavailable, attempts[0].Error)
}

func TestEgress_NewReservedPrefixesRefused(t *testing.T) {
	strict := egressPolicy{}
	for _, a := range []string{"2001:0:4136:e378:8000:63bf:3fff:fdd2", "::ffff:0:a00:1", "5f00::1", "3fff::1"} {
		assert.False(t, strict.AdmitsIP(netip.MustParseAddr(a)), a)
	}
	assert.True(t, strict.AdmitsIP(netip.MustParseAddr("2606:4700::1111")), "ordinary global unicast stays admitted")
}

func TestStorableResponseHeaders_Bounded(t *testing.T) {
	h := http.Header{}
	for i := 0; i < 100; i++ {
		h.Add("X-H-"+strings.Repeat("a", i%10)+string(rune('A'+i%26))+string(rune('a'+i/26)), strings.Repeat("v", 2000))
	}
	h.Add("X-Multi", "first")
	h.Add("X-Multi", "second")
	out := storableResponseHeaders(h)
	assert.LessOrEqual(t, len(out), MaxStoredResponseHeaders)
	for _, v := range out {
		assert.LessOrEqual(t, len(v), MaxStoredResponseHeaderValueLength+len("..."))
	}
	assert.Equal(t, "first", storableResponseHeaders(http.Header{"X-Multi": {"first", "second"}})["X-Multi"])
}

func TestGuardTransport_CapsHeaderBytesAndDropsJar(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	m, _ := newEgressTestManager(t, WithHTTPClient(&http.Client{Jar: jar, Transport: &http.Transport{MaxResponseHeaderBytes: 10 << 20}}))
	assert.Nil(t, m.httpClient.Jar, "a consumer cookie jar must not cross subscribers")
	tr, ok := m.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, int64(EgressMaxResponseHeaderBytes), tr.MaxResponseHeaderBytes)

	def, _ := newEgressTestManager(t)
	tr, ok = def.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, int64(EgressMaxResponseHeaderBytes), tr.MaxResponseHeaderBytes)
}

func TestTestSubscription_UnknownIDTakesNoRateLimitSlot(t *testing.T) {
	m, _ := newEgressTestManager(t)
	require.NoError(t, m.Start(context.Background()))
	defer func() { _ = m.Stop() }()
	_, err := m.TestSubscription(context.Background(), "sub_does_not_exist")
	require.Error(t, err)
	_, stored := m.testRateLimiter.Load("sub_does_not_exist")
	assert.False(t, stored)
}

func TestSubscriberCustomIdempotencyHeaderNeverSurvives(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Values(HeaderIdempotencyKey)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations())
	ctx := context.Background()
	sub := createTestSubscription(t, "sub_hdr", "tenant_hdr", server.URL)
	sub.Headers = map[string]string{HeaderIdempotencyKey: "forged"}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	d := createTestDelivery(t, "dlv_hdr", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, d))
	m.processDelivery(ctx, claimForTest(t, m, d))
	assert.Empty(t, got)
}

func TestTruncateAndStorableText_ValidUTF8(t *testing.T) {
	s := strings.Repeat("é", 10) // 2 bytes per rune
	cut := truncateString(s, 5)
	assert.True(t, utf8.ValidString(cut), cut)
	assert.True(t, utf8.ValidString(storableText("ok\xff\xfebinary")))
}
