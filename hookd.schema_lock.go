// Package hookd provides webhook management functionality.
//
// This file implements the advisory lock that serializes SchemaManager's
// schema setup (EnsureSchema / DropSchema) across processes.
//
// Why: several pods booting together (a rolling update, a scale-out) each call
// EnsureSchema on a fresh or outdated database. Without a lock they all see
// "schema missing" and run the DROP/CREATE batch concurrently, which fails with
// duplicate pg_type keys, "relation already exists" or deadlocks.
//
// Excellence. Always.
package hookd

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/itsatony/go-cuserr"
	"github.com/lib/pq"
)

// pgErrCodeLockNotAvailable is SQLSTATE 55P03, raised when lock_timeout expires.
const pgErrCodeLockNotAvailable = "55P03"

// SchemaManagerOption configures a SchemaManager.
type SchemaManagerOption func(*SchemaManager) error

// WithSchemaLockTimeout bounds how long EnsureSchema/DropSchema wait for the
// per-prefix schema lock held by another process (default
// DefaultSchemaLockTimeout = 60s). d must be at least SchemaLockTimeoutMin.
//
// The wait is enforced by PostgreSQL's lock_timeout on the dedicated lock
// connection (reset before any DDL runs, so DDL lock waits are unaffected) and
// additionally by the caller's context.
func WithSchemaLockTimeout(d time.Duration) SchemaManagerOption {
	return func(m *SchemaManager) error {
		if d < SchemaLockTimeoutMin {
			return cuserr.NewValidationError("schema_lock_timeout", ErrMsgSchemaLockTimeoutInvalid)
		}
		m.lockTimeout = d
		return nil
	}
}

// SchemaLockKey returns the two int4 keys of the session-level advisory lock
// that serializes schema setup for the given table prefix:
// pg_advisory_lock(classID, objectID).
//
// classID is always SchemaLockClassID; objectID is an FNV-1a hash of
// SchemaLockNamespace+prefix, so consumers with DIFFERENT prefixes in one
// database do not wait for each other. Exposed for diagnostics (pg_locks has
// classid/objid columns and objsubid = 2 for this key form).
func SchemaLockKey(prefix string) (classID, objectID int32) {
	h := fnv.New32a()
	// hash.Hash.Write never returns an error.
	_, _ = h.Write([]byte(SchemaLockNamespace + prefix))
	return SchemaLockClassID, int32(h.Sum32()) //nolint:gosec // deliberate wrap into int4 key space
}

// schemaDDLLockStatement is prepended to every schema DDL batch. The batch runs
// as ONE implicit transaction (simple query protocol), so the xact lock is held
// exactly for the DDL and released by its commit/rollback.
func schemaDDLLockStatement() string {
	return fmt.Sprintf(sqlSchemaDDLLockFmt, SchemaDDLLockClassID, SchemaDDLLockObjectID)
}

// withSchemaLock runs fn on a dedicated connection while holding the
// per-prefix session advisory lock. The lock is released on every path; if the
// release cannot be confirmed the connection is discarded, which ends the
// session and therefore releases the lock server-side.
func (m *SchemaManager) withSchemaLock(ctx context.Context, fn func(conn *sql.Conn) error) (err error) {
	prefix := m.schemaConfig.Prefix()
	classID, objectID := SchemaLockKey(prefix)

	conn, err := m.db.Conn(ctx)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", opAcquireSchemaConn),
			cuserr.WithMetadata("prefix", prefix),
		)
	}
	defer func() { _ = conn.Close() }()

	if err := m.acquireSchemaLock(ctx, conn, classID, objectID); err != nil {
		discardConn(conn)
		return err
	}
	defer func() {
		if relErr := releaseSchemaLock(conn, classID, objectID); relErr != nil {
			discardConn(conn)
			if err == nil {
				err = cuserr.NewExternalError("database", "postgres", relErr,
					cuserr.WithMetadata("operation", opReleaseSchemaLock),
					cuserr.WithMetadata("prefix", prefix),
				)
			}
		}
	}()

	return fn(conn)
}

// acquireSchemaLock waits (bounded) for the advisory lock, then resets
// lock_timeout so the DDL that follows keeps the server default.
func (m *SchemaManager) acquireSchemaLock(ctx context.Context, conn *sql.Conn, classID, objectID int32) error {
	prefix := m.schemaConfig.Prefix()
	lockErr := func(err error) error {
		var pqErr *pq.Error
		timedOut := (errors.As(err, &pqErr) && string(pqErr.Code) == pgErrCodeLockNotAvailable) ||
			errors.Is(err, context.DeadlineExceeded)
		if timedOut {
			// Classified as a timeout (errors.Is(err, cuserr.ErrTimeout)).
			e := cuserr.NewTimeoutError(opAcquireSchemaLock, err).
				WithMetadata("prefix", prefix).
				WithMetadata("lock_timeout", m.lockTimeout.String())
			e.Message = ErrMsgSchemaLockTimeout
			return e
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", opAcquireSchemaLock),
			cuserr.WithMetadata("prefix", prefix),
		)
	}

	lockCtx, cancel := context.WithTimeout(ctx, m.lockTimeout+SchemaLockReleaseTimeout)
	defer cancel()

	timeout := strconv.FormatInt(m.lockTimeout.Milliseconds(), 10) + schemaLockTimeoutUnit
	if _, err := conn.ExecContext(lockCtx, sqlSchemaLockSetTimeout, timeout); err != nil {
		return lockErr(err)
	}
	if _, err := conn.ExecContext(lockCtx, sqlSchemaLockAcquire, classID, objectID); err != nil {
		return lockErr(err)
	}
	if _, err := conn.ExecContext(lockCtx, sqlSchemaLockResetTimeout); err != nil {
		// The lock IS held; the caller discards the connection, which releases it.
		return lockErr(err)
	}
	return nil
}

// releaseSchemaLock unlocks with a fresh context so a cancelled caller context
// cannot leave the lock held. A false result (lock not held) is an error.
func releaseSchemaLock(conn *sql.Conn, classID, objectID int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), SchemaLockReleaseTimeout)
	defer cancel()

	var released bool
	if err := conn.QueryRowContext(ctx, sqlSchemaLockRelease, classID, objectID).Scan(&released); err != nil {
		return err
	}
	if !released {
		return errSchemaLockNotHeld
	}
	return nil
}

// errSchemaLockNotHeld reports that pg_advisory_unlock returned false.
var errSchemaLockNotHeld = errors.New("go-hookd schema lock was not held at release")

// discardConn marks conn bad so database/sql closes it instead of returning it
// to the pool; closing the session releases every session-level advisory lock.
func discardConn(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}
