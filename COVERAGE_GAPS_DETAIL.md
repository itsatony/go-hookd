# Detailed Coverage Gaps Analysis

## Overview
This document provides a detailed breakdown of all code coverage gaps in go-hookd, mapped to specific files and functions.

## Critical Issues (0% Coverage)

### 1. PostgreSQL Repository Layer (BLOCKING FOR PRODUCTION)

#### File: /home/itsatony/code/go-hookd/internal/hookd.repository.postgres.go

**Status:** 0% COVERAGE - All database operations untested

**Functions with 0% coverage (45 functions total):**

**JSON/Scanning Helpers:**
- `unmarshalJSONB` - JSON unmarshaling for database reads
- `scanSubscription` - Subscription database scanning
- `scanDelivery` - Delivery database scanning
- `scanDeliveryAttempt` - Delivery attempt database scanning
- `scanCircuitBreakerState` - Circuit breaker state database scanning

**Subscription Repository Methods:**
- `CreateSubscription` - INSERT operation
- `GetSubscription` - SELECT by ID
- `GetSubscriptionByTenantAndURL` - SELECT by tenant+URL (critical for duplicate detection)
- `UpdateSubscription` - UPDATE operation
- `DeleteSubscription` - DELETE operation
- `ListSubscriptions` - SELECT with filters

**Delivery Repository Methods:**
- `CreateDelivery` - INSERT operation
- `GetDelivery` - SELECT by ID
- `UpdateDelivery` - UPDATE operation
- `GetPendingDeliveries` - SELECT with SKIP LOCKED (CRITICAL for queue processing)
- `MoveToDeadLetter` - State transition operation

**Delivery Attempt Repository Methods:**
- `CreateDeliveryAttempt` - INSERT operation
- `GetDeliveryAttempts` - SELECT with pagination

**Idempotency Repository Methods:**
- `CheckIdempotency` - SELECT with key
- `StoreIdempotencyKey` - INSERT/UPDATE operation

**Circuit Breaker Repository Methods:**
- `GetCircuitBreakerState` - SELECT state
- `UpdateCircuitBreakerState` - UPDATE state

**Connection/Transaction Methods:**
- `BeginTx` - Transaction start
- `Ping` - Connection health check
- `Close` - Connection cleanup

**Why This Is Critical:**

1. **GetPendingDeliveries** - The core delivery queue operation
   - Uses SKIP LOCKED for lock-free row selection
   - No validation that SKIP LOCKED works correctly
   - No testing of query performance with large datasets
   - No verification of ordering guarantees

2. **Transaction Operations** - Data consistency guarantee
   - Commit/Rollback semantics unknown
   - No deadlock testing
   - No concurrent transaction validation

3. **Schema Validation** - Database integrity
   - No verification that schema matches model definitions
   - No constraint violation handling testing
   - No foreign key relationship testing

---

#### File: /home/itsatony/code/go-hookd/internal/hookd.repository.postgres.tx.go

**Status:** 0% COVERAGE - All transaction methods untested

**Functions with 0% coverage (25 functions total):**

**Transaction Management:**
- `Commit` - Transaction commit
- `Rollback` - Transaction rollback
- `CreateSubscription` - Transactional INSERT
- `GetSubscription` - Transactional SELECT
- `GetSubscriptionByTenantAndURL` - Transactional SELECT
- `UpdateSubscription` - Transactional UPDATE
- `DeleteSubscription` - Transactional DELETE
- `ListSubscriptions` - Transactional SELECT
- `CreateDelivery` - Transactional INSERT
- `GetDelivery` - Transactional SELECT
- `UpdateDelivery` - Transactional UPDATE
- `GetPendingDeliveries` - Transactional queue select
- `MoveToDeadLetter` - Transactional state change
- `CreateDeliveryAttempt` - Transactional INSERT
- `GetDeliveryAttempts` - Transactional SELECT
- `CheckIdempotency` - Transactional SELECT
- `StoreIdempotencyKey` - Transactional INSERT/UPDATE
- `GetCircuitBreakerState` - Transactional SELECT
- `UpdateCircuitBreakerState` - Transactional UPDATE
- `BeginTx` - Start new transaction
- `Ping` - Health check
- `Close` - Connection cleanup

**Impact:**
- All transactional guarantees are unvalidated
- Isolation level not tested
- Concurrent transaction behavior unknown
- Rollback semantics not verified

---

## High Priority Issues (50-85% Coverage)

### 2. Testing Helpers - /home/itsatony/code/go-hookd/internal/testing_helpers.go

**Status:** 30.8% COVERAGE

**Uncovered Functions:**
```
- WaitForDeliveryStatus (0.0%) - Helper for integration tests
- WaitForDeliveryStatusAny (0.0%) - Helper for multi-status tests
```

**Impact:** Integration test infrastructure incomplete
- Cannot reliably wait for delivery status changes
- Test timing/synchronization compromised
- E2E test stability affected

**Covered Functions:**
- `PollUntil` (76.9%)
- `WaitForCondition` (100.0%)

---

### 3. Model Validation - /home/itsatony/code/go-hookd/internal/hookd.models.go

**Status:** 80.0% COVERAGE

**Uncovered Functions:**
```
- validateURL (80.0%) - Edge cases not covered
  - Specific invalid URL formats
  - URL normalization edge cases
  - Query parameter handling
```

**Impact:** Model validation edge cases not tested
- Potential invalid URLs could be accepted
- URL normalization behavior unpredictable
- Duplicate detection may fail on edge cases

---

### 4. Subscription Operations - /home/itsatony/code/go-hookd/internal/hookd.subscription.go

**Status:** 95.8% COVERAGE

**Uncovered Functions:**
```
- CreateSubscription (84.0%) - Some error paths missing
- GetSubscription (88.9%) - Edge cases uncovered
- DeleteSubscription (81.8%) - Error handling incomplete
- ListSubscriptions (77.8%) - Filter logic edge cases
```

**Impact:** Subscription CRUD operations have incomplete error testing
- Some error conditions not handled
- Filter logic not fully validated
- Race conditions in subscription lifecycle possible

---

### 5. Utility Functions - /home/itsatony/code/go-hookd/internal/hookd.utils.go

**Status:** 96.7% COVERAGE

**Uncovered Functions:**
```
- GenerateSubscriptionID (75.0%) - Error path untested
- GenerateDeliveryID (75.0%) - Error path untested
- GenerateAttemptID (75.0%) - Error path untested
- CalculateBackoff (80.0%) - Edge cases uncovered
```

**Impact:** ID generation error handling untested
- NanoID library errors not handled
- Backoff calculation edge cases possible
- Large attempt numbers not tested

---

### 6. Manager Publishing - /home/itsatony/code/go-hookd/internal/hookd.manager.go

**Status:** 100.0% overall, but one critical function at 0%

**Uncovered Function:**
```
- Publish (0.0%) - Event broker publishing untested
```

**Impact:** Event publishing not tested
- Event broker functionality unknown
- Publishing failures not handled
- Event delivery to subscribers not validated

---

## Medium Priority Issues (85-95% Coverage)

### 7. Repository Mock - /home/itsatony/code/go-hookd/internal/hookd.repository.mock.go

**Status:** 97.0% COVERAGE (mostly good)

**Minor Gaps:**
```
- copyCircuitBreakerState (66.7%) - Some state combinations not tested
- MoveToDeadLetter (90.0%) - Error case uncovered
- DeleteSubscription (76.9%) - Rare error path uncovered
```

**Impact:** Minimal - mock repository is well-tested overall

---

## Summary Table

| File | Coverage | Priority | Issue Type |
|------|----------|----------|------------|
| hookd.repository.postgres.go | 0% | CRITICAL | All database ops untested |
| hookd.repository.postgres.tx.go | 0% | CRITICAL | All transactions untested |
| testing_helpers.go | 30.8% | HIGH | Integration test support incomplete |
| hookd.models.go | 80% | HIGH | URL validation edge cases |
| hookd.subscription.go | 84-88% | HIGH | CRUD error paths incomplete |
| hookd.utils.go | 75-96% | MEDIUM | ID generation error paths |
| hookd.repository.mock.go | 97% | MEDIUM | Minor gaps in mock |
| **OVERALL** | **63.5%** | **CRITICAL** | **Below 90% target** |

---

## Testing Recommendations by Category

### For Database Layer (45+ functions)
- Set up PostgreSQL integration test container
- Create comprehensive CRUD tests with real schema
- Test SKIP LOCKED behavior under load
- Validate transaction semantics
- Test constraint violations and error handling
- Estimated effort: 40-60 hours

### For Testing Helpers (2 functions)
- Complete WaitForDeliveryStatus implementations
- Test with concurrent delivery updates
- Validate timeout behavior
- Estimated effort: 3-5 hours

### For Model Validation (1 function)
- Add URL edge case tests
- Test URL normalization consistency
- Add property-based tests for URL handling
- Estimated effort: 2-3 hours

### For Subscription Operations (4 functions)
- Complete error path testing
- Test filter combinations
- Add concurrent operation tests
- Estimated effort: 5-8 hours

### For Utility Functions (4 functions)
- Test NanoID error conditions
- Add backoff calculation stress tests
- Test with extreme attempt numbers
- Estimated effort: 3-5 hours

### For Manager Publishing (1 function)
- Test event publishing success/failure
- Validate broker integration
- Test concurrent publishers
- Estimated effort: 2-3 hours

---

## Database Operations Not Tested

The following critical database operations have 0% test coverage:

### Queue Operations (CRITICAL for delivery engine)
- `GetPendingDeliveries()` - Core queue operation with SKIP LOCKED
  - Untested: Lock behavior, ordering, batching, performance
  - Risk: Deliveries could be lost or duplicated
  - Priority: MUST TEST BEFORE PRODUCTION

### State Transitions
- `UpdateDelivery()` - Status state changes
  - Untested: Concurrent updates, state machine validation
  - Risk: Deliveries could get stuck in invalid states
  - Priority: MUST TEST BEFORE PRODUCTION

- `UpdateCircuitBreakerState()` - Circuit breaker transitions
  - Untested: State machine, timing windows
  - Risk: Circuit breakers could malfunction
  - Priority: MUST TEST BEFORE PRODUCTION

### Data Consistency
- All transaction methods - ACID guarantees
  - Untested: Isolation, consistency, durability
  - Risk: Data corruption under concurrent load
  - Priority: MUST TEST BEFORE PRODUCTION

- Foreign key constraints - Relational integrity
  - Untested: Cascade deletes, orphaned records
  - Risk: Referential integrity violations
  - Priority: MUST TEST BEFORE PRODUCTION

---

## Conclusion

The 0% coverage of the PostgreSQL repository layer represents an **unacceptable risk** for production deployment. This is THE critical data persistence layer, and it's completely untested.

**Required Actions (In Order):**
1. Implement PostgreSQL repository integration tests
2. Achieve 90%+ coverage on database operations
3. Validate SKIP LOCKED query behavior
4. Test transaction semantics
5. Re-assess production readiness

**Timeline:** 2-3 weeks minimum with focused effort
