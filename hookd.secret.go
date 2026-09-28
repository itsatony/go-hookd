// Package hookd — the signing-secret resolver (v0.11.0, go-hookd#3).
//
// WHY: until v0.11.0 the HMAC key was a database column (subscriptions.secret,
// deliveries.secret for inline deliveries) and there was no other path, so every
// consumer held a live signing key at rest in PostgreSQL. A service whose rules
// forbid that (agora AGO-R-015: the subscription carries a REFERENCE, resolved at
// delivery time and never written down) could not use hookd at all.
//
// THE CONTRACT:
//   - The column now holds a REFERENCE. What a reference means is the resolver's
//     business; for the default resolver it IS the secret, so nothing changes for
//     a consumer that sets no resolver.
//   - Resolve is called PER ATTEMPT (every delivery attempt and every
//     TestSubscription), never once per subscription load, and hookd keeps the
//     result only on the stack of the request that signs with it. A cache on the
//     subscription would quietly reintroduce the durable copy a resolver exists
//     to remove; a resolver that wants caching owns that decision (and its TTL).
//   - A failed resolve sends NOTHING (an unsigned or wrongly signed webhook is
//     worse than a late one): the attempt fails with the opaque
//     ErrMsgSigningSecretUnavailable, is retried like any transport failure, and
//     the cause goes to the operator (WARN log) — never into the attempt row,
//     which subscribers may read, because a resolver error can name vault paths.
package hookd

import (
	"context"
	"errors"
	"fmt"

	"github.com/itsatony/go-cuserr"
	"go.uber.org/zap"
)

// SecretRequest is what a resolver is asked. Every field comes from the stored
// ROW (subscription or inline delivery), never from a request a subscriber
// shaped in flight — so a resolver that scopes its lookup by tenant (agora
// checks the vault scope against the row's org and owner) can rely on them.
type SecretRequest struct {
	// Ref is the stored secret column: the secret itself for the default
	// resolver, a reference (e.g. a vault name) for any other.
	Ref string
	// TenantID is the row's tenant.
	TenantID string
	// SubscriptionID is the subscription's id; empty for an inline delivery.
	SubscriptionID string
	// DeliveryID is hookd's delivery id; TestPingDeliveryID for TestSubscription.
	DeliveryID string
	// IdempotencyKey is the queuer's key for the delivery (empty if none, and
	// for TestSubscription). A consumer that queues inline deliveries from its
	// own outbox can put its own row id here and resolve against that row.
	IdempotencyKey string
}

// SecretResolver turns the reference a subscription (or inline delivery)
// carries into the HMAC signing secret. It must be safe for concurrent use.
type SecretResolver interface {
	// Resolve returns the signing secret for req. It is called on every attempt;
	// see the file comment. An empty secret with a nil error is allowed (and is
	// what the default resolver returns for an empty stored secret).
	Resolve(ctx context.Context, req SecretRequest) (string, error)
}

// SecretResolverFunc adapts a function to SecretResolver.
type SecretResolverFunc func(ctx context.Context, req SecretRequest) (string, error)

// Resolve calls f.
func (f SecretResolverFunc) Resolve(ctx context.Context, req SecretRequest) (string, error) {
	return f(ctx, req)
}

// StoredSecretResolver is the default: the stored value is the secret itself
// (the behaviour of every release before v0.11.0).
type StoredSecretResolver struct{}

// Resolve returns req.Ref unchanged.
func (StoredSecretResolver) Resolve(_ context.Context, req SecretRequest) (string, error) {
	return req.Ref, nil
}

// ErrSigningSecretUnavailable is what an attempt fails with when the resolver
// could not produce a secret. Test with errors.Is.
var ErrSigningSecretUnavailable = cuserr.NewExternalError("secret-resolver", "resolve",
	errSigningSecretUnavailableCause)

var errSigningSecretUnavailableCause = errors.New(ErrMsgSigningSecretUnavailable)

// WithSecretResolver makes hookd resolve every signing secret through r, so the
// secret column holds a reference (e.g. a vault path) instead of a live key.
// ⚠ Existing rows are passed to r verbatim: switching resolvers on a table that
// still holds raw secrets means r must recognise (or refuse) them.
func WithSecretResolver(r SecretResolver) ManagerOption {
	return func(m *Manager) error {
		if r == nil {
			return NewConfigurationError("secret_resolver", ErrMsgSecretResolverNil)
		}
		m.secretResolver = r
		return nil
	}
}

// resolveSigningSecret resolves the secret for one attempt. A failure is logged
// and returned as the opaque ErrSigningSecretUnavailable. The resolver's error
// is logged only by its TYPE: its text may name a vault path or embed the ref,
// and a log line is not where a credential's address belongs.
func (m *Manager) resolveSigningSecret(ctx context.Context, req SecretRequest) (string, error) {
	secret, err := m.secretResolver.Resolve(ctx, req)
	if err != nil {
		m.logger.Warn(LogMsgSigningSecretUnavailable,
			zap.String(LogFieldDeliveryID, req.DeliveryID),
			zap.String(LogFieldErrorType, fmt.Sprintf("%T", err)),
		)
		return "", ErrSigningSecretUnavailable
	}
	return secret, nil
}
