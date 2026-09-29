// Package internal provides the core webhook management implementation for go-hookd.
//
// This file implements the RepositoryTx interface using PostgreSQL transactions.
// It provides atomic multi-operation support with commit and rollback semantics.
package hookd

import (
	"context"
	"database/sql"

	"github.com/itsatony/go-cuserr"
)

// PostgresRepositoryTx implements the RepositoryTx interface using PostgreSQL transactions.
//
// Thread Safety: PostgresRepositoryTx is NOT safe for concurrent use. Each transaction
// should be used by a single goroutine. Create separate transactions for concurrent operations.
//
// Every data operation is the SAME code as PostgresRepository's (the embedded
// pgStore, bound to the transaction), so each method reads and writes the
// prefixed tables with the full column lists and joins the caller's
// transaction: nothing is visible to other sessions until Commit, and
// Rollback undoes all of it.
type PostgresRepositoryTx struct {
	pgStore
	tx *sql.Tx
	db *sql.DB // Keep reference for connection pool info (Ping)
}

// =============================================================================
// TRANSACTION CONTROL
// =============================================================================

// Commit commits the transaction.
// After Commit, the RepositoryTx should not be used.
func (r *PostgresRepositoryTx) Commit() error {
	if err := r.tx.Commit(); err != nil {
		if err == sql.ErrTxDone {
			// Transaction already completed (commit or rollback), this is safe
			return nil
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "commit_transaction"),
		)
	}
	return nil
}

// Rollback rolls back the transaction.
// It's safe to call Rollback after Commit (it will be a no-op).
func (r *PostgresRepositoryTx) Rollback() error {
	if err := r.tx.Rollback(); err != nil {
		if err == sql.ErrTxDone {
			// Transaction already completed (commit or rollback), this is safe
			return nil
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "rollback_transaction"),
		)
	}
	return nil
}

// =============================================================================
// NESTED TRANSACTIONS (NOT SUPPORTED)
// =============================================================================

// BeginTx is not supported within a transaction.
// Attempting to start a nested transaction returns an error.
func (r *PostgresRepositoryTx) BeginTx(ctx context.Context) (RepositoryTx, error) {
	return nil, cuserr.NewValidationError("transaction", "nested transactions are not supported")
}

// =============================================================================
// HEALTH AND MAINTENANCE
// =============================================================================

// Ping checks if the underlying database connection is healthy.
// Note: This pings the connection pool, not the transaction itself.
func (r *PostgresRepositoryTx) Ping(ctx context.Context) error {
	if err := r.db.PingContext(ctx); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "ping_tx"),
		)
	}
	return nil
}

// Close is a no-op for transactions.
// Transactions should be committed or rolled back, not closed.
// This method exists to satisfy the Repository interface.
func (r *PostgresRepositoryTx) Close() error {
	// No-op: transactions are committed or rolled back, not closed
	return nil
}
