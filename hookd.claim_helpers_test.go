package hookd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// claimForTest gives delivery a real claim token on m's MockRepository, exactly
// as ClaimPendingDeliveries would, but for this one row only — so a test can
// drive processDelivery for a chosen delivery (the worker only sends a delivery
// whose claim it holds).
func claimForTest(t *testing.T, m *Manager, delivery *Delivery) *Delivery {
	t.Helper()
	repo, ok := m.repo.(*MockRepository)
	require.True(t, ok, "claimForTest needs a *MockRepository")
	repo.mu.Lock()
	defer repo.mu.Unlock()
	stored, ok := repo.deliveries[delivery.ID]
	require.True(t, ok, "delivery %s not in repository", delivery.ID)
	token := mockClaimTime().Add(m.config.ClaimLease())
	stored.NextRetryAt = &token
	claimed := copyDelivery(stored)
	return claimed
}

// testClaimLease is the lease tests claim with directly through a repository.
const testClaimLease = time.Minute
