//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"

	hookd "github.com/itsatony/go-hookd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureSchema_AddsIdempotencyKeyInPlace: a schema created by v0.10.0 (no
// idempotency_key column) is upgraded IN PLACE — rows survive, the version
// comment is unchanged (so an older binary booting mid-rollout never drops it),
// and concurrent boots add the column once.
func TestEnsureSchema_AddsIdempotencyKeyInPlace(t *testing.T) {
	dsn := setupClaimSchema(t)
	db := openDB(t, dsn)
	ctx := context.Background()
	table := claimPrefix + "_hookd_deliveries"

	// Make it a v0.10.0-shaped schema holding one row.
	_, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s DROP COLUMN idempotency_key`, table))
	require.NoError(t, err)
	_, err = db.Exec(fmt.Sprintf(`INSERT INTO %s (id, tenant_id, event_type, payload, url, status, max_attempts)
		VALUES ('dlv_old', 't1', 'e', '{}', 'https://example.invalid', 'pending', 3)`, table))
	require.NoError(t, err)

	cfg, err := hookd.NewSchemaConfig(claimPrefix)
	require.NoError(t, err)
	before, err := versionOf(t, dsn, cfg)
	require.NoError(t, err)

	errs := ensureConcurrently(t, dsn, claimPrefix, concurrentBooters)
	for _, e := range errs {
		require.NoError(t, e)
	}

	after, err := versionOf(t, dsn, cfg)
	require.NoError(t, err)
	assert.Equal(t, before, after, "an additive column must not change the schema version")

	var rows int
	require.NoError(t, db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id = 'dlv_old'`, table)).Scan(&rows))
	assert.Equal(t, 1, rows, "the upgrade must keep existing rows")

	repo := newRepo(t, dsn)
	old, err := repo.GetDelivery(ctx, "dlv_old")
	require.NoError(t, err)
	assert.Empty(t, old.IdempotencyKey, "pre-upgrade rows read as no key")

	id, err := hookd.GenerateDeliveryID()
	require.NoError(t, err)
	require.NoError(t, repo.CreateDelivery(ctx, &hookd.Delivery{
		ID: id, TenantID: "t1", EventType: "e", Payload: map[string]any{}, URL: "https://example.invalid",
		Status: hookd.DeliveryStatusPending, MaxAttempts: 3, IdempotencyKey: "caller-key",
	}))
	claimed, err := repo.ClaimPendingDeliveries(ctx, 10, claimLease)
	require.NoError(t, err)
	keys := map[string]string{}
	for _, d := range claimed {
		keys[d.ID] = d.IdempotencyKey
	}
	assert.Equal(t, "caller-key", keys[id], "the key round-trips through the claim")
}

func versionOf(t *testing.T, dsn string, cfg *hookd.SchemaConfig) (string, error) {
	t.Helper()
	sm, err := hookd.NewSchemaManagerFromURL(dsn, cfg)
	require.NoError(t, err)
	defer func() { _ = sm.Close() }()
	return sm.GetSchemaVersion(context.Background())
}
