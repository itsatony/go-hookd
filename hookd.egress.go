// Package hookd — the egress guard (v0.8.0).
//
// This file holds the destination policy every webhook delivery and every
// TestSubscription is dialed through, and the guarded HTTP client built from it.
//
// WHY: a webhook URL is chosen by a SUBSCRIBER, and the process that dials it
// sits inside the consumer's network. Until v0.8.0 hookd dialed whatever the
// URL named and followed redirects, so a subscriber could aim deliveries (and
// the synchronous TestSubscription, which returns the response body) at
// loopback, cloud metadata (169.254.169.254), RFC 1918 ranges, in-cluster
// Service names — directly, through a 3xx, or by re-pointing a DNS name after
// a write-time check had passed (DNS rebinding).
//
// THE RULE (modeled on conduit ADR-075 and obol ADR-185):
//
//   - An ALLOW rule, not a deny list: global unicast only, minus the
//     special-purpose ranges IsGlobalUnicast admits (egressReservedPrefixes).
//   - IPv4-mapped IPv6 is unmapped BEFORE the test (netip.Prefix.Contains does
//     not unmap); an address with a zone is refused outright.
//   - RESOLVE ONCE, JUDGE EVERY CANDIDATE, DIAL THE LITERAL: the dial resolves
//     the host itself, refuses the WHOLE dial if ANY answer is refused (judging
//     only the address Happy Eyeballs picks would let a mixed answer through
//     about half the time), then connects to the judged literal, so no second
//     lookup can return a different answer. TLS still verifies the certificate
//     against the URL's hostname, because the request's Host is unchanged.
//   - Proxy nil (a proxy would make the dial judge the PROXY's address), no
//     redirect followed, HTTP/1.1 only.
//   - ONE opaque error for refused / lookup failed / empty answer in anything a
//     subscriber can read. The cause goes to the operator: a WARN log line and
//     the optional WithEgressRefusalHook callback.
//   - Write time (validateURL) refuses only IP-LITERAL hosts the policy
//     refuses. A hostname is NOT resolved at write time: that would be TOCTOU
//     (rebinding) and would itself be an oracle for which internal names exist.
package hookd

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/itsatony/go-cuserr"
	"go.uber.org/zap"
)

// ErrEgressDestinationUnreachable is the one error a guarded dial returns for a
// refused destination, a failed lookup and an empty answer alike. It reaches a
// caller of http.Client.Do wrapped in *url.Error, so test it with errors.Is.
// Delivery attempts and TestResult record ErrMsgEgressDestinationUnreachable,
// never the underlying cause.
var ErrEgressDestinationUnreachable = cuserr.NewExternalError("webhook-endpoint", "dial",
	errEgressDestinationUnreachableCause)

// errEgressDestinationUnreachableCause is the static cause wrapped above.
var errEgressDestinationUnreachableCause = errors.New(ErrMsgEgressDestinationUnreachable)

// Dialer bounds for the guarded default transport, matching http.DefaultTransport's.
const (
	EgressDialTimeout   = 30 * time.Second
	EgressDialKeepAlive = 30 * time.Second
	// EgressTLSHandshakeTimeout bounds a TLS handshake (http.DefaultTransport's
	// value; the default hookd transport previously had none).
	EgressTLSHandshakeTimeout = 10 * time.Second
	// EgressMaxResponseHeaderBytes caps a delivery response's header block.
	EgressMaxResponseHeaderBytes = 16 << 10
	// MaxStoredResponseHeaders caps how many response headers an attempt stores.
	MaxStoredResponseHeaders = 32
	// MaxStoredResponseHeaderValueLength caps each stored header value.
	MaxStoredResponseHeaderValueLength = 512
)

// egressReservedPrefixes are special-purpose ranges that IsGlobalUnicast and
// IsPrivate do not already refuse, each with the RFC that reserves it.
var egressReservedPrefixes = []netip.Prefix{
	// RFC 791 "this network". IsGlobalUnicast admits 0.6.6.6 (only 0.0.0.0 is
	// unspecified), and Linux routes a dial to 0.0.0.0/8 to the local host.
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("::/96"),           // RFC 4291 IPv4-compatible (deprecated)
	netip.MustParsePrefix("64:ff9b::/96"),    // RFC 6052 NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"),  // RFC 8215 NAT64 local-use
	netip.MustParsePrefix("2002::/16"),       // RFC 3056 6to4 (embeds an IPv4 address)
	netip.MustParsePrefix("fec0::/10"),       // RFC 3879 deprecated site-local
	netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 CGNAT (several CNI plugins use it)
	netip.MustParsePrefix("192.0.0.0/24"),    // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("192.88.99.0/24"),  // RFC 7526 deprecated 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // RFC 2544 benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),     // RFC 1112 reserved (incl. 255.255.255.255)
	netip.MustParsePrefix("192.0.2.0/24"),    // RFC 5737 TEST-NET-1
	netip.MustParsePrefix("198.51.100.0/24"), // RFC 5737 TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // RFC 5737 TEST-NET-3
	netip.MustParsePrefix("2001:db8::/32"),   // RFC 3849 documentation
	netip.MustParsePrefix("100::/64"),        // RFC 6666 discard-only
	// v0.11.0 (security review): ranges that embed or translate to an IPv4
	// address, or are not globally routable, and that IsGlobalUnicast admits.
	netip.MustParsePrefix("2001::/32"),       // RFC 4380 Teredo (embeds an obfuscated IPv4)
	netip.MustParsePrefix("::ffff:0:0:0/96"), // RFC 6145 / 7915 IPv4-translated (SIIT)
	netip.MustParsePrefix("5f00::/16"),       // RFC 9602 SRv6 SIDs
	netip.MustParsePrefix("3fff::/20"),       // RFC 9637 documentation
}

// egressCGNATPrefix is the one reserved range WithAllowPrivateDestinations
// re-admits, because pod networks of several CNI plugins live in it.
var egressCGNATPrefix = netip.MustParsePrefix("100.64.0.0/10")

// egressPolicy judges a destination address.
//
// The zero value is the production default: STRICT. allowPrivate is set only
// by WithAllowPrivateDestinations and re-admits loopback, RFC 1918 / ULA
// (IsPrivate) and CGNAT — the ranges tests (httptest on 127.0.0.1) and dev
// clusters (Service and pod IPs) live in. It never re-admits link-local
// (169.254.0.0/16 holds the cloud metadata endpoint), multicast, unspecified,
// 0.0.0.0/8, the documentation/benchmarking/reserved ranges, or a zoned address.
type egressPolicy struct {
	allowPrivate bool
}

// strictEgressPolicy is the policy request Validate() methods apply.
var strictEgressPolicy = egressPolicy{}

// AdmitsIP reports whether addr may be dialed.
func (p egressPolicy) AdmitsIP(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	// The opt-in re-admits EXACTLY egressPrivateRange; everything else it does
	// not name (link-local, multicast, unspecified, 0.0.0.0/8, reserved) falls
	// through to the strict rule below and stays refused.
	if p.allowPrivate && egressPrivateRange(addr) {
		return true
	}
	if !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, prefix := range egressReservedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// egressPrivateRange is what WithAllowPrivateDestinations re-admits.
func egressPrivateRange(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || egressCGNATPrefix.Contains(addr)
}

// validateURLWithPolicy is the write-time URL check: length, absolute http(s),
// a host, and — only when the host is an IP LITERAL — the policy. A hostname is
// never resolved here (TOCTOU, and an oracle); the dial judges it.
func validateURLWithPolicy(urlStr string, policy egressPolicy) error {
	if len(urlStr) > MaxURLLength {
		return cuserr.NewValidationError("url", ErrMsgURLTooLong)
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return cuserr.NewValidationError("url", ErrMsgInvalidURL)
	}

	if parsedURL.Scheme != URLSchemeHTTP && parsedURL.Scheme != URLSchemeHTTPS {
		return cuserr.NewValidationError("url", ErrMsgInvalidURLScheme)
	}

	host := parsedURL.Hostname()
	if parsedURL.Host == "" || host == "" {
		return cuserr.NewValidationError("url", ErrMsgMissingURLHost)
	}

	if addr, parseErr := netip.ParseAddr(host); parseErr == nil && !policy.AdmitsIP(addr) {
		return cuserr.NewValidationError("url", ErrMsgURLDestinationRefused)
	}

	return nil
}

// egressHostResolver is the one lookup the dial judges (injectable for tests).
type egressHostResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// dialContextFunc is the shape of http.Transport.DialContext.
type dialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// guardedDialContext applies the policy to every connection the transport opens.
// dial is the underlying dialer; it is only ever handed a judged IP literal.
func guardedDialContext(
	policy egressPolicy, dial dialContextFunc, resolver egressHostResolver, onRefusal func(cause string),
) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			onRefusal(EgressRefusalCauseMalformedAddress)
			return nil, ErrEgressDestinationUnreachable
		}

		var candidates []netip.Addr
		if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
			candidates = []netip.Addr{literal}
		} else {
			resolved, lookupErr := resolver.LookupNetIP(ctx, EgressLookupNetwork, host)
			if lookupErr != nil {
				// Not a policy refusal, and deliberately indistinguishable from one.
				onRefusal(EgressRefusalCauseLookupFailed)
				return nil, ErrEgressDestinationUnreachable
			}
			if len(resolved) == 0 {
				onRefusal(EgressRefusalCauseEmptyAnswer)
				return nil, ErrEgressDestinationUnreachable
			}
			candidates = resolved
		}

		for _, candidate := range candidates {
			if !policy.AdmitsIP(candidate) {
				onRefusal(EgressRefusalCauseInternalAddress)
				return nil, ErrEgressDestinationUnreachable
			}
		}

		var lastErr error
		for _, candidate := range candidates {
			conn, dialErr := dial(ctx, network, net.JoinHostPort(candidate.Unmap().String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
}

// noFollowRedirect is the CheckRedirect of every client hookd dials with.
//
// A webhook is not followed through a redirect: a hop is a destination the
// subscriber did not register (and the classic way around a write-time check).
// http.ErrUseLastResponse rather than an error, because the 3xx IS the
// endpoint's answer: the attempt records its status code and headers (the
// Location is the subscriber's own statement), and the delivery fails and is
// retried like any other non-2xx, non-4xx response. An error would record an
// opaque transport failure and hide from the subscriber why it failed.
func noFollowRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// guardTransport turns base (which the caller owns and has already cloned)
// into the guarded transport, in place.
//
// Each property closes a bypass:
//   - Proxy nil: with ProxyFromEnvironment the dial would judge the PROXY's
//     address, and the proxy would reach the target unjudged.
//   - DialContext wraps the existing dialer (or a default one) and hands it only
//     judged literals; the deprecated Dial is folded in and cleared.
//   - HTTP/2 off (ForceAttemptHTTP2 false, an empty non-nil TLSNextProto, "h2"
//     stripped from ALPN): a webhook is one POST per connection use, so
//     multiplexing buys nothing, and pinning HTTP/1.1 keeps exactly one
//     connection path — net/http's own dial through DialContext — in the audit
//     surface, instead of also depending on how the bundled HTTP/2 stack pools
//     and reuses connections. conduit ADR-075 and obol ADR-185 do the same.
func guardTransport(
	base *http.Transport, policy egressPolicy, resolver egressHostResolver, onRefusal func(cause string),
) {
	dial := dialContextFunc(base.DialContext)
	if base.DialContext == nil {
		if base.Dial != nil { //nolint:staticcheck // folding the deprecated field into the guard
			legacy := base.Dial //nolint:staticcheck // see above
			dial = func(_ context.Context, network, address string) (net.Conn, error) {
				return legacy(network, address)
			}
		} else {
			dial = (&net.Dialer{Timeout: EgressDialTimeout, KeepAlive: EgressDialKeepAlive}).DialContext
		}
	}
	base.Dial = nil //nolint:staticcheck // cleared so it cannot bypass DialContext
	base.DialContext = guardedDialContext(policy, dial, resolver, onRefusal)
	base.Proxy = nil
	// Bounded: a subscriber endpoint must not be able to hand the worker (and
	// the attempt row) megabytes of headers.
	if base.MaxResponseHeaderBytes <= 0 || base.MaxResponseHeaderBytes > EgressMaxResponseHeaderBytes {
		base.MaxResponseHeaderBytes = EgressMaxResponseHeaderBytes
	}
	base.ForceAttemptHTTP2 = false
	base.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	if base.TLSClientConfig != nil {
		cfg := base.TLSClientConfig.Clone()
		cfg.NextProtos = slices.DeleteFunc(slices.Clone(cfg.NextProtos), func(p string) bool {
			return p == ALPNProtocolHTTP2
		})
		base.TLSClientConfig = cfg
	}
}

// newDefaultDeliveryTransport is the transport hookd builds when the consumer
// passes no client (pool sizes unchanged from v0.7.x).
func newDefaultDeliveryTransport() *http.Transport {
	return &http.Transport{
		TLSHandshakeTimeout: EgressTLSHandshakeTimeout,
		MaxIdleConns:        DefaultHTTPMaxIdleConns,
		MaxIdleConnsPerHost: DefaultHTTPMaxIdleConnsPerHost,
		IdleConnTimeout:     time.Duration(DefaultHTTPIdleConnTimeoutSeconds) * time.Second,
	}
}

// buildHTTPClient derives the client every delivery and TestSubscription uses.
// It runs after all ManagerOptions are applied, so option order is irrelevant.
//
// WithHTTPClient semantics: the consumer's client is NEVER mutated and NEVER
// used unguarded by default. hookd uses a shallow copy (Timeout, Jar kept) whose
// CheckRedirect is replaced by noFollowRedirect and whose Transport is a CLONE,
// guarded by guardTransport:
//   - Transport nil            → a clone of http.DefaultTransport, guarded.
//   - *http.Transport          → its clone, guarded; its own DialContext is kept
//     as the underlying dialer and only ever handed judged literals.
//   - anything else (an opaque RoundTripper, e.g. an instrumentation wrapper),
//     or an *http.Transport with DialTLS/DialTLSContext set (which would bypass
//     DialContext for https) → NewManager returns a configuration error. Since
//     v0.11.0 there is no exception (WithAllowPrivateDestinations used to waive
//     the guard for such a client entirely).
func (m *Manager) buildHTTPClient(resolver egressHostResolver) error {
	onRefusal := m.recordEgressRefusal

	if m.customHTTPClient == nil {
		transport := newDefaultDeliveryTransport()
		guardTransport(transport, m.egressPolicy, resolver, onRefusal)
		m.httpClient = &http.Client{
			Timeout:       m.config.DeliveryTimeout(),
			Transport:     transport,
			CheckRedirect: noFollowRedirect,
		}
		return nil
	}

	derived := *m.customHTTPClient
	derived.CheckRedirect = noFollowRedirect
	// A cookie jar would carry one subscriber's cookies to another's endpoint.
	derived.Jar = nil

	var base *http.Transport
	switch t := derived.Transport.(type) {
	case nil:
		if def, ok := http.DefaultTransport.(*http.Transport); ok {
			base = def.Clone()
		} else {
			base = newDefaultDeliveryTransport()
		}
	case *http.Transport:
		//nolint:staticcheck // the deprecated DialTLS bypasses DialContext too
		if t.DialTLSContext == nil && t.DialTLS == nil {
			base = t.Clone()
		}
	default:
		// An opaque RoundTripper: the guard cannot reach its dialer.
	}

	if base == nil {
		// ⛔ Always refused (v0.11.0). Until v0.10.0 WithAllowPrivateDestinations
		// ALSO accepted such a client unguarded — an opt-in named for three
		// address ranges silently switched off the whole dial guard, link-local
		// cloud metadata included. No option waives the guard now.
		return NewConfigurationError("http_client", ErrMsgHTTPClientUnguardable)
	}

	guardTransport(base, m.egressPolicy, resolver, onRefusal)
	derived.Transport = base
	m.httpClient = &derived
	return nil
}

// recordEgressRefusal gives the operator the cause the subscriber never sees.
// The logger is read at call time, so WithLogger's order does not matter.
func (m *Manager) recordEgressRefusal(cause string) {
	m.logger.Warn(LogMsgEgressRefused, zap.String(LogFieldEgressCause, cause))
	if m.egressRefusalHook != nil {
		m.egressRefusalHook(cause)
	}
}

// subscriberVisibleError is the text a delivery attempt or TestResult records
// for a transport error: the guard's opaque text for anything the guard decided,
// the error itself otherwise.
func subscriberVisibleError(err error) string {
	if errors.Is(err, ErrEgressDestinationUnreachable) {
		return ErrMsgEgressDestinationUnreachable
	}
	if errors.Is(err, ErrSigningSecretUnavailable) {
		return ErrMsgSigningSecretUnavailable
	}
	return err.Error()
}

// WithAllowPrivateDestinations re-admits loopback, RFC 1918 / ULA and CGNAT
// destinations — for TESTS (httptest binds 127.0.0.1) and DEV clusters whose
// webhook receivers are in-cluster Services. ⚠ Never in production: it is the
// explicit waiver of the SSRF guarantee for those ranges. Link-local (cloud
// metadata), multicast, unspecified, 0.0.0.0/8 and reserved ranges stay
// refused, redirects stay unfollowed, and the error stays opaque. It does NOT
// waive the guard for a client it cannot wrap (it did before v0.11.0).
func WithAllowPrivateDestinations() ManagerOption {
	return func(m *Manager) error {
		m.egressPolicy.allowPrivate = true
		return nil
	}
}

// WithEgressRefusalHook registers a callback invoked with the cause of every
// guarded-dial refusal (EgressRefusalCause* constants), for the consumer's
// metrics, e.g. a counter labeled by cause. It is called synchronously on the
// delivery goroutine and must not block. It is the OPERATOR's signal; the
// subscriber only ever sees ErrMsgEgressDestinationUnreachable.
func WithEgressRefusalHook(hook func(cause string)) ManagerOption {
	return func(m *Manager) error {
		if hook == nil {
			return NewConfigurationError("egress_refusal_hook", ErrMsgEgressRefusalHookNil)
		}
		m.egressRefusalHook = hook
		return nil
	}
}

// storableResponseHeaders keeps what an attempt row stores of a subscriber's
// response headers bounded: at most MaxStoredResponseHeaders keys (sorted, so the
// choice is deterministic), the first value of each, each cut to
// MaxStoredResponseHeaderValueLength. The transport already refuses a header
// block above EgressMaxResponseHeaderBytes.
func storableResponseHeaders(h http.Header) map[string]string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) > MaxStoredResponseHeaders {
		keys = keys[:MaxStoredResponseHeaders]
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = storableText(truncateString(h.Get(k), MaxStoredResponseHeaderValueLength))
	}
	return out
}

// storableText makes subscriber-supplied text safe for a TEXT/JSONB column:
// invalid UTF-8 (a binary body, or a cut inside a rune) would fail the insert
// and lose the attempt row.
func storableText(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
}
