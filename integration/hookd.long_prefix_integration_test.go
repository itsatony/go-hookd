//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"

	hookd "github.com/itsatony/go-hookd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// go-hookd#9 against a REAL PostgreSQL: a prefix long enough that derived
// names exceed 63 bytes used to fail EnsureSchema ("relation ... already
// exists", PostgreSQL truncated two names to the same identifier). Every
// accepted length must now create, use, drop and re-create its schema, and the
// objects must carry exactly the names SchemaConfig derives.
func TestLongPrefix_SchemaLifecycleOnPostgres(t *testing.T) {
	dsn := startPostgres(t)
	db := openDB(t, dsn)
	ctx := context.Background()

	// 25 is the longest prefix whose names all fit unshortened; 26+ shorten.
	for _, n := range []int{25, 26, 31, hookd.MaxPrefixLength} {
		prefix := "l" + strings.Repeat("x", n-1)
		t.Run(prefix, func(t *testing.T) {
			cfg, err := hookd.NewSchemaConfig(prefix)
			require.NoError(t, err)
			sm := hookd.NewSchemaManager(db, cfg)
			require.NoError(t, sm.EnsureSchema(ctx), "EnsureSchema with a %d-char prefix", n)
			require.NoError(t, sm.EnsureSchema(ctx), "and again (idempotent)")

			for _, trg := range []string{
				cfg.TriggerName("subscriptions", "updated_at"),
				cfg.TriggerName("circuit_breaker", "updated_at"),
			} {
				var found bool
				require.NoError(t, db.QueryRowContext(ctx,
					`SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = $1)`, trg).Scan(&found))
				assert.True(t, found, "trigger %s", trg)
			}
			for _, fn := range cfg.AllFunctionNames() {
				var found bool
				require.NoError(t, db.QueryRowContext(ctx,
					`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = $1)`, fn).Scan(&found))
				assert.True(t, found, "function %s", fn)
			}
			for _, idx := range []string{
				cfg.IndexName("subscriptions", "tenant_id"),
				cfg.IndexName("subscriptions", "tenant_url"),
				cfg.IndexName("idempotency", "subscription_id"),
			} {
				var found bool
				require.NoError(t, db.QueryRowContext(ctx,
					`SELECT to_regclass($1) IS NOT NULL`, idx).Scan(&found))
				assert.True(t, found, "index %s", idx)
			}

			// The repository works on it, directly and inside a transaction.
			repo, err := hookd.NewPostgresRepository(dsn, hookd.WithTablePrefix(prefix))
			require.NoError(t, err)
			defer func() { _ = repo.Close() }()
			sub := newSub(t, txTenant, "https://example.invalid/long")
			require.NoError(t, repo.CreateSubscription(ctx, sub))
			tx, err := repo.BeginTx(ctx)
			require.NoError(t, err)
			require.NoError(t, tx.CreateDelivery(ctx, newDelivery(t, sub.ID, txTenant)))
			require.NoError(t, tx.Commit())
			n, err := repo.CountDeliveriesByFilter(ctx, &hookd.CleanupFilter{TenantID: txTenant, EventType: strPtr(txEventType)})
			require.NoError(t, err)
			assert.Equal(t, int64(1), n)

			require.NoError(t, sm.DropSchema(ctx))
			exists, err := sm.SchemaExists(ctx)
			require.NoError(t, err)
			assert.False(t, exists)
			for _, fn := range cfg.AllFunctionNames() {
				var found bool
				require.NoError(t, db.QueryRowContext(ctx,
					`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = $1)`, fn).Scan(&found))
				assert.False(t, found, "DropSchema removes function %s", fn)
			}
			require.NoError(t, sm.EnsureSchema(ctx), "re-create after drop")
		})
	}
}
