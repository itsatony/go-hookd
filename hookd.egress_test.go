package hookd

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// TEST DOUBLES
// =============================================================================

var (
	errTestMustNotBeReached   = errors.New("must not be reached")
	errTestConnRefused        = errors.New("connection refused (test)")
	errTestOpaqueRoundTripper = errors.New("opaque round tripper (test)")
	errTestPublicDialRefused  = errors.New("dial tcp 93.184.216.34:443: connect: connection refused")
)

// fakeEgressResolver answers from a fixed table and counts lookups.
type fakeEgressResolver struct {
	answers map[string][]netip.Addr
	errs    map[string]error
	calls   int
	mu      sync.Mutex
}

func (f *fakeEgressResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err, ok := f.errs[host]; ok {
		return nil, err
	}
	return f.answers[host], nil
}

func (f *fakeEgressResolver) lookups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// withEgressResolverForTest injects the resolver the guarded dial judges.
// It exists only in _test.go; production always uses net.DefaultResolver.
func withEgressResolverForTest(r egressHostResolver) ManagerOption {
	return func(m *Manager) error {
		m.egressResolver = r
		return nil
	}
}

// causeRecorder collects refusal-hook causes.
type causeRecorder struct {
	causes []string
	mu     sync.Mutex
}

func (c *causeRecorder) hook(cause string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.causes = append(c.causes, cause)
}

func (c *causeRecorder) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.causes...)
}

// countingServer is an httptest server that counts hits and answers 200.
func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// deliverOnce inserts a subscription + delivery directly into the repo (so the
// WRITE-time check is bypassed and only the DIAL-time guard decides), runs one
// delivery attempt, and returns the recorded attempt.
func deliverOnce(t *testing.T, m *Manager, repo *MockRepository, targetURL string) *DeliveryAttempt {
	t.Helper()
	ctx := context.Background()
	subID, err := GenerateSubscriptionID()
	require.NoError(t, err)
	sub := &Subscription{
		ID:          subID,
		TenantID:    "tenant_egress",
		URL:         targetURL,
		EventTypes:  []string{"test.event"},
		Secret:      "test_secret_egress",
		RetryPolicy: DefaultRetryPolicy(),
		Status:      SubscriptionStatusActive,
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	dlvID, err := GenerateDeliveryID()
	require.NoError(t, err)
	now := time.Now()
	delivery := &Delivery{
		ID:             dlvID,
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]any{"k": "v"},
		Status:         DeliveryStatusPending,
		MaxAttempts:    3,
		NextRetryAt:    &now,
	}
	require.NoError(t, repo.CreateDelivery(ctx, delivery))
	m.processDelivery(ctx, delivery)
	attempts, err := repo.GetDeliveryAttempts(ctx, delivery.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	return attempts[0]
}

func newEgressTestManager(t *testing.T, opts ...ManagerOption) (*Manager, *MockRepository) {
	t.Helper()
	cfg := NewConfig("postgres://localhost/test")
	cfg.DeliveryTimeoutMs = 2000
	repo := NewMockRepository()
	m, err := NewManager(cfg, repo, opts...)
	require.NoError(t, err)
	return m, repo
}

// =============================================================================
// THE PREDICATE
// =============================================================================

func TestEgressPolicy_AdmitsIP(t *testing.T) {
	strict := egressPolicy{}
	optIn := egressPolicy{allowPrivate: true}

	cases := []struct {
		addr   string
		strict bool
		optIn  bool
	}{
		// public
		{"93.184.216.34", true, true},
		{"8.8.8.8", true, true},
		{"2606:2800:220:1:248:1893:25c8:1946", true, true},
		{"::ffff:93.184.216.34", true, true}, // mapped public is still public
		// loopback
		{"127.0.0.1", false, true},
		{"127.255.255.254", false, true},
		{"::1", false, true},
		{"::ffff:127.0.0.1", false, true}, // mapped: unmapped FIRST
		// RFC 1918 / ULA
		{"10.0.0.1", false, true},
		{"172.16.0.1", false, true},
		{"172.31.255.255", false, true},
		{"192.168.1.1", false, true},
		{"::ffff:10.1.2.3", false, true},
		// mapped forms of ranges only a PREFIX test catches (netip's Is*
		// predicates unmap internally, Prefix.Contains does not)
		{"::ffff:100.64.0.1", false, true},
		{"::ffff:198.18.0.1", false, false},
		{"::ffff:192.0.2.1", false, false},
		{"::ffff:0.6.6.6", false, false},
		{"::ffff:240.0.0.1", false, false},
		{"fd00::1", false, true},
		{"fc00::1", false, true},
		// CGNAT
		{"100.64.0.1", false, true},
		{"100.127.255.255", false, true},
		// link-local — cloud metadata. Refused EVEN under the opt-in.
		{"169.254.169.254", false, false},
		{"::ffff:169.254.169.254", false, false},
		{"fe80::1", false, false},
		// multicast / unspecified / broadcast
		{"224.0.0.1", false, false},
		{"239.255.255.250", false, false},
		{"ff02::1", false, false},
		{"ff01::1", false, false},
		{"0.0.0.0", false, false},
		{"::", false, false},
		{"255.255.255.255", false, false},
		// "this network" — Linux routes it to the local host
		{"0.6.6.6", false, false},
		{"0.255.255.255", false, false},
		// reserved / documentation / benchmarking
		{"192.0.0.1", false, false},
		{"192.0.2.1", false, false},
		{"198.51.100.1", false, false},
		{"203.0.113.1", false, false},
		{"198.18.0.1", false, false},
		{"198.19.255.255", false, false},
		{"240.0.0.1", false, false},
		{"192.88.99.1", false, false},
		{"2001:db8::1", false, false},
		{"100::1", false, false},
		{"fec0::1", false, false},
		// embedded-IPv4 IPv6 forms IsGlobalUnicast admits
		{"64:ff9b::a00:1", false, false}, // NAT64 of 10.0.0.1
		{"64:ff9b:1::1", false, false},   // NAT64 local-use
		{"2002:a00:1::1", false, false},  // 6to4 of 10.0.0.1
		{"::a00:1", false, false},        // IPv4-compatible 10.0.0.1
		{"::7f00:1", false, false},       // IPv4-compatible 127.0.0.1
		// zones refused outright
		{"fe80::1%eth0", false, false},
		{"2606:2800:220:1::1%eth0", false, false},
		{"::1%lo", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			addr, err := netip.ParseAddr(tc.addr)
			require.NoError(t, err)
			assert.Equal(t, tc.strict, strict.AdmitsIP(addr), "strict")
			assert.Equal(t, tc.optIn, optIn.AdmitsIP(addr), "opt-in")
		})
	}

	t.Run("invalid address refused", func(t *testing.T) {
		assert.False(t, strict.AdmitsIP(netip.Addr{}))
		assert.False(t, optIn.AdmitsIP(netip.Addr{}))
	})
}

// =============================================================================
// WRITE TIME
// =============================================================================

func TestValidateURL_EgressWriteTime(t *testing.T) {
	cases := []struct {
		url     string
		wantMsg string // "" = admitted
	}{
		{"https://hooks.example.com/x", ""},
		{"http://93.184.216.34:8080/x", ""},
		{"https://[2606:2800:220:1::1]/x", ""},
		// hostnames are NEVER resolved at write time — judged at dial
		{"http://localhost:8080/x", ""},
		{"http://vault.vault.svc.cluster.local/x", ""},
		{"http://metadata.google.internal/x", ""},
		// IP literals in refused ranges
		{"http://127.0.0.1:9000/x", ErrMsgURLDestinationRefused},
		{"http://[::1]/x", ErrMsgURLDestinationRefused},
		{"http://[::ffff:127.0.0.1]/x", ErrMsgURLDestinationRefused},
		{"http://169.254.169.254/latest/meta-data", ErrMsgURLDestinationRefused},
		{"http://10.0.0.5/x", ErrMsgURLDestinationRefused},
		{"http://100.64.1.1/x", ErrMsgURLDestinationRefused},
		{"http://0.0.0.0:80/x", ErrMsgURLDestinationRefused},
		{"http://[fe80::1%25eth0]/x", ErrMsgURLDestinationRefused},
		// shape
		{"ftp://example.com/x", ErrMsgInvalidURLScheme},
		{"gopher://example.com/x", ErrMsgInvalidURLScheme},
		{"file:///etc/passwd", ErrMsgInvalidURLScheme},
		{"example.com/x", ErrMsgInvalidURLScheme},
		{"http:///x", ErrMsgMissingURLHost},
		{"http://:8080/x", ErrMsgMissingURLHost},
		{"http://" + strings.Repeat("a", MaxURLLength), ErrMsgURLTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.url[:min(len(tc.url), 60)], func(t *testing.T) {
			err := validateURL(tc.url)
			if tc.wantMsg == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}

	t.Run("opt-in policy admits private literals, still refuses metadata", func(t *testing.T) {
		optIn := egressPolicy{allowPrivate: true}
		assert.NoError(t, validateURLWithPolicy("http://127.0.0.1:9000/x", optIn))
		assert.NoError(t, validateURLWithPolicy("http://10.0.0.5/x", optIn))
		assert.Error(t, validateURLWithPolicy("http://169.254.169.254/x", optIn))
	})
}

func TestManager_WriteTimeUsesManagerPolicy(t *testing.T) {
	ctx := context.Background()
	req := func(u string) *CreateSubscriptionRequest {
		return &CreateSubscriptionRequest{
			TenantID: "tenant_w", URL: u, Secret: "secret_123456", EventTypes: []string{"test.event"},
		}
	}

	t.Run("default refuses an internal literal on create, inline and update", func(t *testing.T) {
		m, _ := newEgressTestManager(t)
		require.NoError(t, m.Start(ctx))
		t.Cleanup(func() { _ = m.Stop() })

		_, err := m.CreateSubscription(ctx, req("http://127.0.0.1:1/x"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), ErrMsgURLDestinationRefused)

		_, err = m.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL: "http://169.254.169.254/x", EventType: "test.event", Payload: map[string]any{"a": 1}, TenantID: "tenant_w",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), ErrMsgURLDestinationRefused)

		sub, err := m.CreateSubscription(ctx, req("https://hooks.example.com/x"))
		require.NoError(t, err)
		_, err = m.UpdateSubscription(ctx, sub.ID, &UpdateSubscriptionRequest{URL: StringPtr("http://10.0.0.1/x")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), ErrMsgURLDestinationRefused)
		_, err = m.UpdateSubscription(ctx, sub.ID, &UpdateSubscriptionRequest{URL: StringPtr("ftp://hooks.example.com/x")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), ErrMsgInvalidURLScheme)
		got, err := m.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, "https://hooks.example.com/x", got.URL, "a refused update must not be stored")
	})

	t.Run("opt-in admits a loopback literal", func(t *testing.T) {
		m, _ := newEgressTestManager(t, WithAllowPrivateDestinations())
		_, err := m.CreateSubscription(ctx, req("http://127.0.0.1:1/x"))
		require.NoError(t, err)
		_, err = m.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL: "http://127.0.0.1:1/x", EventType: "test.event", Payload: map[string]any{"a": 1}, TenantID: "tenant_w",
		})
		require.NoError(t, err)
	})

	t.Run("public Validate stays strict", func(t *testing.T) {
		err := req("http://127.0.0.1:1/x").Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), ErrMsgURLDestinationRefused)
	})
}

// =============================================================================
// DEFAULT-DENY: the key test
// =============================================================================

func TestEgress_DefaultRefusesLoopbackAndOptInAdmitsIt(t *testing.T) {
	srv, hits := countingServer(t)

	t.Run("default refuses the httptest server", func(t *testing.T) {
		rec := &causeRecorder{}
		m, repo := newEgressTestManager(t, WithEgressRefusalHook(rec.hook))
		attempt := deliverOnce(t, m, repo, srv.URL)
		assert.Equal(t, int32(0), hits.Load(), "a refused destination must never be connected to")
		assert.Equal(t, ErrMsgEgressDestinationUnreachable, attempt.Error)
		assert.Equal(t, 0, attempt.StatusCode)
		assert.Equal(t, []string{EgressRefusalCauseInternalAddress}, rec.all())
	})

	t.Run("default refuses localhost by name (resolved at dial)", func(t *testing.T) {
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)
		m, repo := newEgressTestManager(t)
		attempt := deliverOnce(t, m, repo, "http://localhost:"+u.Port()+"/x")
		assert.Equal(t, int32(0), hits.Load())
		assert.Equal(t, ErrMsgEgressDestinationUnreachable, attempt.Error)
	})

	t.Run("default TestSubscription is refused with the opaque error and no body", func(t *testing.T) {
		m, repo := newEgressTestManager(t)
		ctx := context.Background()
		sub := &Subscription{
			ID: "sub_egress_test", TenantID: "t", URL: srv.URL, EventTypes: []string{"test.event"},
			Secret: "secret_123456", Status: SubscriptionStatusActive, RetryPolicy: DefaultRetryPolicy(),
		}
		require.NoError(t, repo.CreateSubscription(ctx, sub))
		res, err := m.TestSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.False(t, res.Success)
		assert.Equal(t, ErrMsgEgressDestinationUnreachable, res.Error)
		assert.Empty(t, res.ResponseBody)
		assert.Equal(t, int32(0), hits.Load())
	})

	t.Run("opt-in admits the same server", func(t *testing.T) {
		m, repo := newEgressTestManager(t, WithAllowPrivateDestinations())
		attempt := deliverOnce(t, m, repo, srv.URL)
		assert.Equal(t, int32(1), hits.Load())
		assert.Equal(t, http.StatusOK, attempt.StatusCode)
		assert.Empty(t, attempt.Error)
	})
}

// =============================================================================
// THE TRANSPORT
// =============================================================================

func TestGuardedDial_MixedAnswerRefusedWhole(t *testing.T) {
	public := netip.MustParseAddr("93.184.216.34")
	for _, order := range [][]netip.Addr{
		{public, netip.MustParseAddr("10.0.0.1")},
		{netip.MustParseAddr("fd00::1"), public},
		{public, netip.MustParseAddr("::ffff:169.254.169.254")},
	} {
		resolver := &fakeEgressResolver{answers: map[string][]netip.Addr{"mixed.example": order}}
		var dialed atomic.Int32
		dial := func(context.Context, string, string) (net.Conn, error) {
			dialed.Add(1)
			return nil, errTestMustNotBeReached
		}
		rec := &causeRecorder{}
		guarded := guardedDialContext(egressPolicy{}, dial, resolver, rec.hook)
		conn, err := guarded(context.Background(), "tcp", "mixed.example:443")
		assert.Nil(t, conn)
		assert.ErrorIs(t, err, ErrEgressDestinationUnreachable)
		assert.Equal(t, int32(0), dialed.Load(), "no candidate may be dialed when any is refused: %v", order)
		assert.Equal(t, []string{EgressRefusalCauseInternalAddress}, rec.all())
	}
}

func TestGuardedDial_DialsTheJudgedLiteralAfterOneLookup(t *testing.T) {
	resolver := &fakeEgressResolver{answers: map[string][]netip.Addr{
		"hooks.example": {netip.MustParseAddr("::ffff:93.184.216.34"), netip.MustParseAddr("2606:2800:220:1::1")},
	}}
	var addrs []string
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		addrs = append(addrs, address)
		return nil, errTestConnRefused
	}
	guarded := guardedDialContext(egressPolicy{}, dial, resolver, func(string) { t.Fatal("no refusal expected") })
	_, err := guarded(context.Background(), "tcp", "hooks.example:8443")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrEgressDestinationUnreachable, "an ordinary dial failure is not a guard decision")
	assert.Equal(t, []string{"93.184.216.34:8443", "[2606:2800:220:1::1]:8443"}, addrs,
		"every dial must name a judged LITERAL (unmapped), never the hostname")
	assert.Equal(t, 1, resolver.lookups(), "exactly one lookup: no second answer can differ")
}

func TestGuardedDial_MalformedAddress(t *testing.T) {
	rec := &causeRecorder{}
	guarded := guardedDialContext(egressPolicy{}, nil, &fakeEgressResolver{}, rec.hook)
	_, err := guarded(context.Background(), "tcp", "no-port-here")
	assert.ErrorIs(t, err, ErrEgressDestinationUnreachable)
	assert.Equal(t, []string{EgressRefusalCauseMalformedAddress}, rec.all())
}

// The subscriber must not be able to tell refused / NXDOMAIN / empty apart.
func TestEgress_OpaqueErrorIdenticalAcrossCauses(t *testing.T) {
	resolver := &fakeEgressResolver{
		answers: map[string][]netip.Addr{
			"refused.example": {netip.MustParseAddr("10.1.2.3")},
			"empty.example":   {},
		},
		errs: map[string]error{
			"nxdomain.example": &net.DNSError{Err: "no such host", Name: "nxdomain.example", IsNotFound: true},
		},
	}
	rec := &causeRecorder{}
	m, repo := newEgressTestManager(t, withEgressResolverForTest(resolver), WithEgressRefusalHook(rec.hook))

	hosts := []string{"refused.example", "nxdomain.example", "empty.example"}
	attemptErrs := make([]string, 0, len(hosts))
	testErrs := make([]string, 0, len(hosts))
	for _, h := range hosts {
		target := "https://" + h + "/hook"
		attempt := deliverOnce(t, m, repo, target)
		attemptErrs = append(attemptErrs, attempt.Error)

		ctx := context.Background()
		sub := &Subscription{
			ID: "sub_opaque_" + strings.Split(h, ".")[0], TenantID: "t", URL: target, EventTypes: []string{"test.event"},
			Secret: "secret_123456", Status: SubscriptionStatusActive, RetryPolicy: DefaultRetryPolicy(),
		}
		require.NoError(t, repo.CreateSubscription(ctx, sub))
		res, err := m.TestSubscription(ctx, sub.ID)
		require.NoError(t, err)
		testErrs = append(testErrs, res.Error)
	}
	for i := range hosts {
		assert.Equal(t, ErrMsgEgressDestinationUnreachable, attemptErrs[i], hosts[i])
		assert.Equal(t, ErrMsgEgressDestinationUnreachable, testErrs[i], hosts[i])
		assert.NotContains(t, attemptErrs[i], hosts[i], "the text must not even echo the host")
	}
	// The operator still sees the difference.
	assert.Equal(t, []string{
		EgressRefusalCauseInternalAddress, EgressRefusalCauseInternalAddress,
		EgressRefusalCauseLookupFailed, EgressRefusalCauseLookupFailed,
		EgressRefusalCauseEmptyAnswer, EgressRefusalCauseEmptyAnswer,
	}, rec.all())
}

func TestEgress_RedirectNotFollowed(t *testing.T) {
	second, secondHits := countingServer(t)
	var firstHits atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits.Add(1)
		http.Redirect(w, r, second.URL+"/internal", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(first.Close)

	// Opt in so the FIRST (loopback) hop is reachable at all; the redirect
	// rule must hold independently of the address policy.
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations())
	attempt := deliverOnce(t, m, repo, first.URL)
	assert.Equal(t, int32(1), firstHits.Load())
	assert.Equal(t, int32(0), secondHits.Load(), "a redirect must not be followed")
	assert.Equal(t, http.StatusTemporaryRedirect, attempt.StatusCode, "the 3xx is recorded as the endpoint's answer")
	assert.Empty(t, attempt.Error)

	// TestSubscription too.
	ctx := context.Background()
	sub := &Subscription{
		ID: "sub_redirect_test", TenantID: "t", URL: first.URL, EventTypes: []string{"test.event"},
		Secret: "secret_123456", Status: SubscriptionStatusActive, RetryPolicy: DefaultRetryPolicy(),
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	res, err := m.TestSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Equal(t, http.StatusTemporaryRedirect, res.StatusCode)
	assert.Equal(t, int32(0), secondHits.Load())
}

func TestEgress_ProxyIgnored(t *testing.T) {
	proxy, proxyHits := countingServer(t)
	target, targetHits := countingServer(t)
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)

	// A consumer transport that names a proxy explicitly (ProxyFromEnvironment
	// never proxies loopback, so an env-based test would prove nothing).
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	m, repo := newEgressTestManager(t, WithAllowPrivateDestinations(), WithHTTPClient(client))
	attempt := deliverOnce(t, m, repo, target.URL)
	assert.Equal(t, http.StatusOK, attempt.StatusCode)
	assert.Equal(t, int32(0), proxyHits.Load(), "the guarded transport must not use a proxy")
	assert.Equal(t, int32(1), targetHits.Load())

	t.Run("default transport has no proxy", func(t *testing.T) {
		m, _ := newEgressTestManager(t)
		tr, ok := m.httpClient.Transport.(*http.Transport)
		require.True(t, ok)
		assert.Nil(t, tr.Proxy)
	})
}

func TestEgress_HTTP2Off(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12},
	}}
	m, _ := newEgressTestManager(t, WithHTTPClient(client))
	tr, ok := m.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.False(t, tr.ForceAttemptHTTP2)
	assert.NotNil(t, tr.TLSNextProto)
	assert.Empty(t, tr.TLSNextProto)
	assert.Equal(t, []string{"http/1.1"}, tr.TLSClientConfig.NextProtos)
	// the consumer's own objects are untouched
	orig, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.True(t, orig.ForceAttemptHTTP2)
	assert.Equal(t, []string{"h2", "http/1.1"}, orig.TLSClientConfig.NextProtos)

	m2, _ := newEgressTestManager(t)
	tr2, ok := m2.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.NotNil(t, tr2.TLSNextProto)
	assert.False(t, tr2.ForceAttemptHTTP2)
}

// =============================================================================
// WithHTTPClient
// =============================================================================

type opaqueRoundTripper struct{ hits atomic.Int32 }

func (o *opaqueRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	o.hits.Add(1)
	return nil, errTestOpaqueRoundTripper
}

func TestWithHTTPClient_EgressSemantics(t *testing.T) {
	cfg := NewConfig("postgres://localhost/test")

	t.Run("custom *http.Transport is guarded (default refuses loopback through it)", func(t *testing.T) {
		srv, hits := countingServer(t)
		var dialed []string
		consumerDial := func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		client := &http.Client{Transport: &http.Transport{DialContext: consumerDial}}
		m, repo := newEgressTestManager(t, WithHTTPClient(client))
		attempt := deliverOnce(t, m, repo, srv.URL)
		assert.Equal(t, int32(0), hits.Load())
		assert.Equal(t, ErrMsgEgressDestinationUnreachable, attempt.Error)
		assert.Empty(t, dialed, "the consumer dialer must not be reached for a refused destination")

		// …and with the opt-in, the consumer's dialer is the one that connects.
		m2, repo2 := newEgressTestManager(t, WithAllowPrivateDestinations(), WithHTTPClient(client))
		attempt2 := deliverOnce(t, m2, repo2, srv.URL)
		assert.Equal(t, http.StatusOK, attempt2.StatusCode)
		u, _ := url.Parse(srv.URL)
		assert.Equal(t, []string{u.Host}, dialed)
	})

	t.Run("nil Transport is guarded", func(t *testing.T) {
		srv, hits := countingServer(t)
		m, repo := newEgressTestManager(t, WithHTTPClient(&http.Client{}))
		deliverOnce(t, m, repo, srv.URL)
		assert.Equal(t, int32(0), hits.Load())
	})

	t.Run("opaque RoundTripper is refused without the opt-in", func(t *testing.T) {
		_, err := NewManager(cfg, NewMockRepository(), WithHTTPClient(&http.Client{Transport: &opaqueRoundTripper{}}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), ErrMsgHTTPClientUnguardable)
	})

	t.Run("DialTLSContext is refused without the opt-in", func(t *testing.T) {
		tr := &http.Transport{DialTLSContext: func(context.Context, string, string) (net.Conn, error) { return nil, nil }}
		_, err := NewManager(cfg, NewMockRepository(), WithHTTPClient(&http.Client{Transport: tr}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), ErrMsgHTTPClientUnguardable)
	})

	t.Run("opaque RoundTripper is used under the opt-in (explicit waiver)", func(t *testing.T) {
		rt := &opaqueRoundTripper{}
		m, err := NewManager(cfg, NewMockRepository(), WithAllowPrivateDestinations(),
			WithHTTPClient(&http.Client{Transport: rt}))
		require.NoError(t, err)
		assert.Same(t, rt, m.httpClient.Transport)
		assert.NotNil(t, m.httpClient.CheckRedirect, "the redirect rule still applies")
	})

	t.Run("option order is irrelevant", func(t *testing.T) {
		rt := &opaqueRoundTripper{}
		_, err := NewManager(cfg, NewMockRepository(),
			WithHTTPClient(&http.Client{Transport: rt}), WithAllowPrivateDestinations())
		require.NoError(t, err)
	})
}

func TestWithEgressRefusalHook_Nil(t *testing.T) {
	_, err := NewManager(NewConfig("postgres://localhost/test"), NewMockRepository(), WithEgressRefusalHook(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrMsgEgressRefusalHookNil)
}

func TestSubscriberVisibleError(t *testing.T) {
	wrapped := &url.Error{Op: "Post", URL: "https://x.example", Err: ErrEgressDestinationUnreachable}
	assert.Equal(t, ErrMsgEgressDestinationUnreachable, subscriberVisibleError(wrapped))
	other := errTestPublicDialRefused
	assert.Equal(t, other.Error(), subscriberVisibleError(other))
}
