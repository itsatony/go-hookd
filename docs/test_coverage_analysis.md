# Test Coverage Gap Analysis - go-hookd

**Generated:** 2025-11-21
**Current Coverage:** 66.5% (with integration tests)
**Target Coverage:** 90%
**Gap to Close:** 23.5 percentage points

---

## Executive Summary

**Current Status:**
- Overall Coverage: 66.5% (with integration tests)
- Target Coverage: 90%
- Gap to Close: 23.5 percentage points
- Total Functions with 0% coverage: 125
- Functions with <50% coverage: 2
- Functions with 50-80% coverage: 37

**Impact Assessment:**
The largest coverage gap is in the **PostgresRepositoryTx** (transaction support) which has 0% coverage for 17 out of its 19 methods. This single file represents approximately **10-12%** of the total coverage gap.

---

## Top 10 Highest-Impact Areas for Testing

### 1. **hookd.repository.postgres.tx.go** - Transaction Repository (CRITICAL)
**File:** `/home/itsatony/code/go-hookd/internal/hookd.repository.postgres.tx.go`
**File Coverage:** 6.5% (739 lines)
**Functions with 0% coverage:** 17
**Estimated Coverage Gain:** 10-12%

**Missing Coverage:**
- All CRUD operations within transactions (GetSubscription, UpdateSubscription, DeleteSubscription, ListSubscriptions)
- All delivery operations within transactions (CreateDelivery, GetDelivery, UpdateDelivery, GetPendingDeliveries)
- All delivery attempt operations (CreateDeliveryAttempt, GetDeliveryAttempts)
- Idempotency operations (CheckIdempotency, StoreIdempotencyKey)
- Circuit breaker operations (GetCircuitBreakerState, UpdateCircuitBreakerState)
- Transaction management helpers (BeginTx, Ping, Close)

**Test Scenarios Needed:**
```go
// Transaction commit/rollback edge cases
- Test transaction rollback after partial operations
- Test transaction commit with all operations successful
- Test nested transaction behavior (should error)
- Test transaction after connection close

// CRUD within transactions
- Create subscription, rollback, verify not persisted
- Create subscription, commit, verify persisted
- Update within transaction with concurrent non-tx read
- Delete cascade within transaction
- List with filters within transaction

// Delivery operations in transaction
- Create delivery within transaction
- Update delivery status within transaction
- GetPendingDeliveries with SKIP LOCKED within transaction
- MoveToDeadLetter within transaction

// Error handling
- Transaction after sql.ErrTxDone
- Database errors during transaction operations
- JSONB marshaling errors within transaction
- Constraint violations within transaction
```

**Line Numbers:**
- Lines 32-58: Commit/Rollback (40% coverage - need ErrTxDone paths)
- Lines 116-159: GetSubscription, GetSubscriptionByTenantAndURL (0%)
- Lines 162-220: UpdateSubscription, DeleteSubscription (0%)
- Lines 244-310: ListSubscriptions (0%)
- Lines 318-472: Delivery CRUD (0%)
- Lines 476-590: Attempts and Idempotency (0%)
- Lines 612-736: Circuit breaker and management (0%)

---

### 2. **hookd.repository.mock.go** - Mock Repository Transaction Methods
**File:** `/home/itsatony/code/go-hookd/internal/hookd.repository.mock.go`
**File Coverage:** 64.8% (1106 lines)
**Functions with 0% coverage:** 13
**Estimated Coverage Gain:** 2-3%

**Missing Coverage:**
MockRepositoryTx methods (lines 800-1104):
- GetSubscriptionByTenantAndURL (line 800)
- DeleteSubscription (line 828)
- ListSubscriptions (line 856)
- GetDelivery (line 903)
- UpdateDelivery (line 916)
- GetPendingDeliveries (line 933)
- MoveToDeadLetter (line 978)
- GetDeliveryAttempts (line 1015)
- CheckIdempotency (line 1037)
- StoreIdempotencyKey (line 1053)
- GetCircuitBreakerState (line 1068)
- UpdateCircuitBreakerState (line 1085)
- BeginTx, Ping, Close (lines 1094-1104)

**Test Scenarios Needed:**
- Unit tests for each MockRepositoryTx method
- Test transaction isolation (changes not visible outside tx until commit)
- Test rollback restores original state
- Test concurrent access to mock during transaction

---

### 3. **hookd.repository.postgres.go** - Main Repository Error Paths
**File:** `/home/itsatony/code/go-hookd/internal/hookd.repository.postgres.go`
**File Coverage:** 76.9% (993 lines)
**Functions <80% coverage:** 12
**Estimated Coverage Gain:** 3-4%

**Specific Functions Needing More Coverage:**
- `marshalJSONB` (line 67): 66.7% - missing error path for invalid JSON
- `unmarshalJSONB` (line 80): 60.0% - missing error paths
- `CreateSubscription` (line 279): 62.5% - missing duplicate constraint error (pq.Error 23505)
- `UpdateSubscription` (line 381): 68.4% - missing error paths and edge cases
- `DeleteSubscription` (line 443): 70.0% - missing constraint violation cases
- `ListSubscriptions` (line 467): 61.1% - missing filter combinations and error paths
- `CreateDelivery` (line 545): 75.0% - missing JSONB error paths
- `GetDelivery` (line 586): 62.5% - missing error paths
- `UpdateDelivery` (line 611): 69.2% - missing concurrent update cases
- `CreateDeliveryAttempt` (line 745): 60.0% - missing duplicate attempt error (23505)
- `BeginTx` (line 955): 75.0% - missing transaction start failure
- `Close` (line 986): 66.7% - missing already closed error

**Test Scenarios Needed:**
```go
// JSONB marshaling errors
- Test with unmarshalable data (circular references)
- Test with invalid JSONB in database

// Constraint violations
- Duplicate subscription (tenant_id + url unique constraint)
- Duplicate attempt number for same delivery
- Foreign key violations

// Error paths
- Database connection errors
- Query execution failures
- Row scanning errors
- Transaction begin failures
```

---

### 4. **hookd.manager.go** - Manager Error Paths
**File:** `/home/itsatony/code/go-hookd/internal/hookd.manager.go`
**File Coverage:** 84.7% (739 lines)
**Functions <80% coverage:** 4
**Estimated Coverage Gain:** 2-3%

**Specific Functions:**
- `processDeliveries` (line 275): 75.0% - missing error paths in worker loop
- `processDelivery` (line 296): 78.9% - missing HTTP client error scenarios
- `updateCircuitBreakerSuccess` (line 575): 68.4% - missing state transitions
- `truncateString` (line 719): 66.7% - needs edge case tests
- `Publish` (noOpEventBus, line 734): 0.0% - trivial but uncovered

**Test Scenarios Needed:**
```go
// processDeliveries error paths
- Worker panic recovery
- Context cancellation during processing
- Repository errors during GetPendingDeliveries
- High contention scenarios

// processDelivery edge cases
- HTTP timeout during delivery
- Network errors
- Invalid response bodies
- Signature generation failures
- Idempotency check failures

// Circuit breaker state transitions
- Half-open to closed (success count reached)
- Half-open to open (failure during half-open)
- Open state with successful delivery (shouldn't happen)
- Concurrent state updates from multiple workers

// truncateString
- String exactly at max length
- Empty string
- String with unicode characters
```

---

### 5. **hookd.subscription.go** - Subscription Management Edge Cases
**File:** `/home/itsatony/code/go-hookd/internal/hookd.subscription.go`
**File Coverage:** 91.1% (477 lines)
**Functions <80% coverage:** 2
**Estimated Coverage Gain:** 1-2%

**Specific Functions:**
- `CreateSubscription` (line 48): 76.0% - missing error paths
- `ListSubscriptions` (line 362): 77.8% - missing filter combinations

**Test Scenarios Needed:**
```go
// CreateSubscription
- Invalid URL format
- Empty event types array
- Invalid retry policy
- Repository error during creation
- Event publishing failures

// ListSubscriptions
- Empty filter (should return all)
- TenantID filter only
- Status filter only
- Combined filters
- Repository errors
- Empty results
```

---

### 6. **hookd.events.go** - Event Management Functions
**File:** `/home/itsatony/code/go-hookd/internal/hookd.events.go`
**File Coverage:** 81.5% (291 lines)
**Functions <80% coverage:** 2
**Estimated Coverage Gain:** 1%

**Specific Functions:**
- `QueueDelivery` (line 52): 78.8% - missing validation error paths
- `GetDeliveryAttempts` (line 212): 71.4% - missing error paths

**Test Scenarios Needed:**
```go
// QueueDelivery
- Invalid subscription ID (not found)
- Inactive subscription (status != active)
- Idempotency key collision
- Repository errors
- Event publishing failures

// GetDeliveryAttempts
- Delivery not found
- Repository errors
- Empty attempts list
- Large number of attempts
```

---

### 7. **hookd.models.go** - Model Validation Edge Cases
**File:** `/home/itsatony/code/go-hookd/internal/hookd.models.go`
**File Coverage:** 84.2% (540 lines)
**Functions <80% coverage:** 1
**Estimated Coverage Gain:** 1%

**Specific Function:**
- `Validate` (line 283): 78.3% - missing edge cases in validation

**Test Scenarios Needed:**
```go
// CreateSubscriptionRequest.Validate
- URL with unsupported scheme (not http/https)
- URL with port numbers
- EventTypes with whitespace
- EventTypes with duplicates
- Negative retry values
- Zero max attempts
- Extremely large values

// UpdateSubscriptionRequest.Validate
- Empty update (no fields to update)
- Invalid status transitions
- Partial updates with invalid values

// QueueDeliveryRequest.Validate
- Empty subscription ID
- Nil payload
- Invalid event type format
```

---

### 8. **hookd.utils.go** - Utility Function Edge Cases
**File:** `/home/itsatony/code/go-hookd/internal/hookd.utils.go`
**File Coverage:** 92.8% (283 lines)
**Functions <80% coverage:** 4
**Estimated Coverage Gain:** 0.5-1%

**Specific Functions:**
- `GenerateSubscriptionID` (line 32): 75.0% - missing nanoid generation error
- `GenerateDeliveryID` (line 44): 75.0% - missing nanoid generation error
- `GenerateAttemptID` (line 56): 75.0% - missing nanoid generation error
- `CalculateBackoff` (line 233): 75.0% - missing edge cases

**Test Scenarios Needed:**
```go
// ID generation errors
- Simulate nanoid.New() failure (hard to test, may skip)

// CalculateBackoff edge cases
- Attempt 0 (should return base delay)
- Attempt 1 (should return base delay)
- Very high attempt numbers (jitter still bounded)
- Zero base delay
- Negative attempt numbers
- Max delay enforcement
```

---

### 9. **hookd.config.go** - Config Validation
**File:** `/home/itsatony/code/go-hookd/internal/hookd.config.go`
**File Coverage:** 97.7% (219 lines)
**Functions <80% coverage:** 1
**Estimated Coverage Gain:** 0.5%

**Specific Function:**
- `Validate` (line 102): 77.4% - missing edge case validations

**Test Scenarios Needed:**
```go
// Config.Validate edge cases
- WorkerCount = 0
- Negative timeout values
- Extremely large values (overflow checks)
- Circuit breaker thresholds at boundaries
- Idempotency window edge cases
```

---

### 10. **hookd.manager.go** - noOpEventBus Coverage
**Estimated Coverage Gain:** <0.1%

**Missing:**
- `Publish` method (line 734): 0.0% - trivial no-op but uncovered

**Test Scenario:**
```go
func TestNoOpEventBus_Publish(t *testing.T) {
    bus := &noOpEventBus{}
    assert.NotPanics(t, func() {
        bus.Publish("test", nil)
    })
}
```

---

## Summary of Coverage Gains by Priority

| Priority | File | Current Coverage | Functions to Test | Est. Coverage Gain | Lines to Cover |
|----------|------|------------------|-------------------|-------------------|----------------|
| 1 | hookd.repository.postgres.tx.go | 6.5% | 17 | 10-12% | ~650 lines |
| 2 | hookd.repository.mock.go (Tx methods) | 64.8% | 13 | 2-3% | ~300 lines |
| 3 | hookd.repository.postgres.go | 76.9% | 12 | 3-4% | ~200 lines |
| 4 | hookd.manager.go | 84.7% | 4 | 2-3% | ~120 lines |
| 5 | hookd.subscription.go | 91.1% | 2 | 1-2% | ~40 lines |
| 6 | hookd.events.go | 81.5% | 2 | 1% | ~30 lines |
| 7 | hookd.models.go | 84.2% | 1 | 1% | ~50 lines |
| 8 | hookd.utils.go | 92.8% | 4 | 0.5-1% | ~20 lines |
| 9 | hookd.config.go | 97.7% | 1 | 0.5% | ~10 lines |
| 10 | hookd.manager.go (noOp) | - | 1 | <0.1% | 1 line |
| **TOTAL** | | **66.5%** | **57** | **21-28.6%** | **~1,420 lines** |

---

## Recommended Testing Strategy

### Phase 1: Transaction Repository (Week 1) - +10-12% coverage
**New File:** `hookd.repository.postgres.tx_test.go`

Focus on comprehensive testing of PostgresRepositoryTx:
1. Transaction lifecycle (commit, rollback, ErrTxDone handling)
2. All subscription CRUD within transactions
3. All delivery CRUD within transactions
4. Idempotency and circuit breaker within transactions
5. Error handling and constraint violations
6. Concurrent transaction behavior

**Expected outcome:** Coverage jumps from 66.5% to ~77%

### Phase 2: Error Paths in Main Repository (Week 2) - +3-4% coverage
**Extend Files:**
- `hookd.repository.postgres_test.go`
- `hookd.repository.mock_test.go`

Focus on:
1. JSONB marshaling/unmarshaling errors
2. Constraint violation handling
3. Database connection errors
4. MockRepositoryTx method coverage
5. Edge cases in existing repository methods

**Expected outcome:** Coverage reaches ~80-81%

### Phase 3: Manager & Core Logic Error Paths (Week 2-3) - +2-3% coverage
**Extend Files:**
- `hookd.manager_test.go`
- `hookd.subscription_test.go`
- `hookd.events_test.go`

Focus on:
1. HTTP delivery error scenarios
2. Circuit breaker state transitions
3. Worker pool error handling
4. Subscription validation edge cases
5. Event handling errors

**Expected outcome:** Coverage reaches ~83-84%

### Phase 4: Models, Utils, Config Edge Cases (Week 3) - +2-3% coverage
**Extend Files:**
- `hookd.models_test.go`
- `hookd.utils_test.go`
- `hookd.config_test.go`

Focus on:
1. Model validation edge cases
2. Utility function boundary conditions
3. Config validation edge cases
4. ID generation edge cases

**Expected outcome:** Coverage reaches ~86-87%

### Phase 5: Remaining Gaps & Polish (Week 3-4) - +3-4% coverage
**Files:** Various

Focus on:
1. Any remaining untested error paths
2. Race condition scenarios
3. Integration test gaps
4. Performance edge cases

**Expected outcome:** Coverage reaches 90%+ target

---

## Key Testing Patterns to Use

### 1. Transaction Testing Pattern
```go
func TestPostgresRepositoryTx_Operation(t *testing.T) {
    repo := setupTestDB(t)
    ctx := context.Background()

    tx, err := repo.BeginTx(ctx)
    require.NoError(t, err)

    // Perform operation
    err = tx.SomeOperation(ctx, data)
    require.NoError(t, err)

    // Test rollback
    err = tx.Rollback()
    require.NoError(t, err)

    // Verify data not persisted
    _, err = repo.GetSomething(ctx, id)
    assert.Error(t, err)

    // Repeat with commit
    tx, _ = repo.BeginTx(ctx)
    tx.SomeOperation(ctx, data)
    tx.Commit()

    // Verify data persisted
    result, err := repo.GetSomething(ctx, id)
    assert.NoError(t, err)
    assert.NotNil(t, result)
}
```

### 2. Error Path Testing Pattern
```go
func TestFunction_ErrorPaths(t *testing.T) {
    tests := []struct {
        name    string
        setup   func(*testing.T) dependencies
        input   interface{}
        wantErr error
    }{
        {
            name: "database error",
            setup: func(t *testing.T) dependencies {
                return mockFailingDB()
            },
            wantErr: cuserr.ErrExternal,
        },
        // ... more error cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            deps := tt.setup(t)
            err := Function(deps, tt.input)
            assert.Error(t, err)
            if tt.wantErr != nil {
                assert.ErrorIs(t, err, tt.wantErr)
            }
        })
    }
}
```

### 3. Edge Case Testing Pattern
```go
func TestFunction_EdgeCases(t *testing.T) {
    tests := []struct {
        name   string
        input  InputType
        want   OutputType
        wantErr bool
    }{
        {"empty input", InputType{}, OutputType{}, false},
        {"max boundary", InputType{Value: MaxInt}, OutputType{}, false},
        {"min boundary", InputType{Value: MinInt}, OutputType{}, false},
        {"nil input", InputType{Ptr: nil}, OutputType{}, true},
        // ... more edge cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Function(tt.input)
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
                assert.Equal(t, tt.want, got)
            }
        })
    }
}
```

---

## Additional Recommendations

1. **Use testcontainers for integration tests** - Already in place, continue using
2. **Test with race detector** - Run `go test -race` for all new tests
3. **Mock external dependencies** - HTTP clients, time.Now(), random generators
4. **Test concurrent scenarios** - Multiple goroutines accessing same resources
5. **Test error recovery** - Panic recovery in workers, graceful degradation
6. **Measure coverage incrementally** - Run `make coverage` after each phase

---

## Files That DON'T Need More Tests

These files already have excellent coverage:
- `hookd.errors.go` - 100% coverage
- `hookd.constants.go` - N/A (constants only)
- `hookd.interfaces.go` - N/A (interface definitions)

---

## Conclusion

The path to 90% coverage is clear:

1. **PostgresRepositoryTx tests** will provide the biggest gain (10-12%)
2. **Error path testing** in existing code will add another 5-7%
3. **Edge case testing** will close the remaining 3-5% gap

The estimated effort is 3-4 weeks of focused testing work, with Phase 1 being the most critical and highest-impact.

---

**Next Steps:**
1. Start with Phase 1: Create `hookd.repository.postgres.tx_test.go`
2. Test transaction lifecycle and all CRUD operations within transactions
3. Measure coverage after Phase 1 to validate estimated gains
4. Proceed to Phase 2 and beyond based on results
