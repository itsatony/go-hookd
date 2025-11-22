# Coverage Improvement Roadmap - go-hookd

**Current Status**: 62.0% coverage
**Target Status**: 90%+ coverage
**Gap**: 28 percentage points
**Estimated Effort**: 1-2 days

---

## Priority 1: Critical Error Path Coverage (62% → 75%)

### Task 1.1: HTTP Error Scenarios (hookd.manager.go)
**File**: `hookd.manager.go:417-475` (executeWebhookRequest function - 88.5%)
**Current Gap**: Missing HTTP error scenarios
**Tests Needed**: 8-10 test cases
**Estimated Time**: 4-6 hours

#### Scenarios to Test
```go
// Missing scenarios in executeWebhookRequest:
1. HTTP 429 (Too Many Requests) - rate limiting
2. HTTP 503 (Service Unavailable) - temporary server error
3. HTTP 504 (Gateway Timeout) - slow endpoint
4. Connection Refused - endpoint unreachable
5. DNS Resolution Failure - invalid hostname
6. TLS Certificate Error - HTTPS validation failure
7. Read Timeout - slow response
8. Write Timeout - slow request sending
9. Empty Response Body - malformed webhook response
10. Invalid Response Headers - missing required headers
```

#### Test Template
```go
func TestExecuteWebhookRequest_HTTPErrors(t *testing.T) {
    tests := []struct {
        name           string
        httpStatus     int
        responseTime   time.Duration
        shouldRetry    bool
        expectedStatus string
    }{
        {
            name:           "rate_limit_429",
            httpStatus:     429,
            responseTime:   100 * time.Millisecond,
            shouldRetry:    true,
            expectedStatus: "retryable_error",
        },
        // ... more scenarios
    }
}
```

### Task 1.2: Delivery Error Paths (hookd.events.go)
**File**: `hookd.events.go:165-200` (GetDelivery function - 88.9%)
**Current Gap**: Database error handling not fully tested
**Tests Needed**: 5-6 test cases
**Estimated Time**: 2-3 hours

#### Scenarios to Test
```go
// Missing in GetDelivery:
1. Database timeout
2. Connection pool exhausted
3. Corrupted delivery data
4. Concurrent deletion during read
5. Database constraint violation
6. Transaction isolation issues
```

### Task 1.3: Delivery Failure Handling (hookd.manager.go)
**File**: `hookd.manager.go:502-580` (handleDeliveryFailure - 88.0%)
**Current Gap**: Some error branches untested
**Tests Needed**: 4-5 test cases
**Estimated Time**: 2-3 hours

#### Scenarios to Test
```go
// Missing in handleDeliveryFailure:
1. Max retries exceeded with various error types
2. Circuit breaker opened during failure
3. Database error while updating delivery status
4. Invalid delivery state transitions
5. Concurrent failure and success attempts
```

---

## Priority 2: Retry Logic Improvements (75% → 82%)

### Task 2.1: Exponential Backoff Edge Cases (hookd.events.go)
**File**: `hookd.events.go:247-315` (RetryDelivery function - 86.7%)
**Current Gap**: Boundary conditions not tested
**Tests Needed**: 6-8 test cases
**Estimated Time**: 3-4 hours

#### Scenarios to Test
```go
// Missing in RetryDelivery:
1. Backoff exceeds MaxBackoff limit
2. Jitter application at boundaries
3. Integer overflow in backoff calculation
4. Retry count exhaustion
5. Negative backoff values (edge case)
6. Zero initial backoff
7. Very large MaxBackoff values
8. Floating-point precision issues
```

#### Test Template
```go
func TestRetryDelivery_BackoffBoundaries(t *testing.T) {
    tests := []struct {
        name              string
        currentAttempt    int
        maxAttempts       int
        initialBackoff    time.Duration
        maxBackoff        time.Duration
        expectedMinDelay  time.Duration
        expectedMaxDelay  time.Duration
    }{
        {
            name:              "backoff_exceeds_max",
            currentAttempt:    10,
            maxAttempts:       20,
            initialBackoff:    1 * time.Second,
            maxBackoff:        5 * time.Second,
            expectedMinDelay:  5 * time.Second,
            expectedMaxDelay:  5 * time.Second,
        },
        // ... more scenarios
    }
}
```

---

## Priority 3: Validation Improvements (82% → 87%)

### Task 3.1: URL Validation Edge Cases (hookd.models.go)
**File**: `hookd.models.go:493-512` (validateURL function - 80.0%)
**Current Gap**: Edge cases not tested
**Tests Needed**: 6-8 test cases
**Estimated Time**: 2-3 hours

#### Scenarios to Test
```go
// Missing in validateURL:
1. IPv6 addresses - [::1]:8080, [2001:db8::1]:443
2. Port validation - negative, zero, >65535
3. Localhost variations - localhost, 127.0.0.1, ::1
4. URL with authentication - https://user:pass@example.com
5. URL with query parameters - https://example.com?key=value
6. URL with fragments - https://example.com#section
7. Punycode domains - https://münchen.example.com
8. Very long URLs - exceed max length
```

#### Test Template
```go
func TestValidateURL_EdgeCases(t *testing.T) {
    tests := []struct {
        name      string
        url       string
        valid     bool
        errType   string
    }{
        {
            name:    "ipv6_with_port",
            url:     "http://[::1]:8080/webhook",
            valid:   true,
        },
        {
            name:    "invalid_port_too_high",
            url:     "http://example.com:99999/webhook",
            valid:   false,
            errType: "invalid_port",
        },
        // ... more scenarios
    }
}
```

### Task 3.2: Event Type Validation (hookd.models.go)
**File**: `hookd.models.go:515-535` (validateEventTypes function - 83.3%)
**Current Gap**: Array edge cases not tested
**Tests Needed**: 4-5 test cases
**Estimated Time**: 1-2 hours

#### Scenarios to Test
```go
// Missing in validateEventTypes:
1. Empty event types array
2. Duplicate event types
3. Event types with invalid characters
4. Very long event type names
5. Event type case sensitivity
6. Null/undefined in array
7. Whitespace in event types
```

---

## Priority 4: Circuit Breaker Improvements (87% → 92%)

### Task 4.1: Circuit Breaker State Transitions (hookd.manager.go)
**File**: `hookd.manager.go:628-680` (updateCircuitBreakerFailure - 86.4%)
**Current Gap**: Some transition edge cases untested
**Tests Needed**: 4-5 test cases
**Estimated Time**: 2-3 hours

#### Scenarios to Test
```go
// Missing in circuit breaker logic:
1. Rapid state transitions under load
2. Concurrent updates to same endpoint
3. Half-open to open re-transition
4. Timeout during half-open test
5. Success threshold edge cases
6. Failure threshold at boundary
```

---

## Priority 5: Complete Zero Coverage (92% → 95%)

### Task 5.1: Event Bus Publishing (hookd.manager.go)
**File**: `hookd.manager.go:741-744` (Publish wrapper - 0.0%)
**Current Gap**: Event publishing not tested
**Tests Needed**: 2-3 test cases
**Estimated Time**: 1-2 hours

#### Test Template
```go
func TestPublish_EventBusIntegration(t *testing.T) {
    // Test publishing to event bus
    // Test event marshaling
    // Test subscriber receiving
}
```

### Task 5.2: Transaction ListDeliveries (hookd.repository.mock.go)
**File**: `hookd.repository.mock.go:1098` (ListDeliveries Tx - 0.0%)
**Current Gap**: Transaction version not tested
**Tests Needed**: 2-3 test cases
**Estimated Time**: 1-2 hours

---

## Testing Timeline

### Day 1: Critical Coverage (4-5 hours)
```
Morning Session:
- Task 1.1: HTTP error scenarios (4-6 hours)

Afternoon Session:
- Task 1.2: GetDelivery error paths (2-3 hours)
- Task 1.3: handleDeliveryFailure paths (2-3 hours)
```

### Day 2: Coverage Completion (4-5 hours)
```
Morning Session:
- Task 2.1: Retry logic boundaries (3-4 hours)
- Task 3.1: URL validation (2-3 hours)

Afternoon Session:
- Task 3.2: Event type validation (1-2 hours)
- Task 4.1: Circuit breaker transitions (2-3 hours)
- Task 5.1-5.2: Zero coverage functions (1-2 hours)
```

---

## Verification Checklist

After each task, verify:

```bash
# Run tests
go test -v -race ./... -cover

# Check coverage
go tool cover -func=coverage.out | grep "TASKNAME"

# Verify no regressions
go test -race -count=10 ./...
```

---

## Expected Coverage Progression

| Phase | Duration | Coverage Target | Tests Added | Status |
|-------|----------|-----------------|-------------|--------|
| Baseline | - | 62.0% | 0 | Current |
| Phase 1 | 6-8h | 75.0% | 30-40 | Priority 1 |
| Phase 2 | 4-6h | 82.0% | 20-30 | Priority 2+3 |
| Phase 3 | 4-6h | 90.0% | 20-30 | Priority 4+5 |
| **Total** | **18-24h** | **90%+** | **60-80** | **Target** |

---

## Success Criteria

- [x] All tests pass (already achieved)
- [x] No race conditions (already achieved)
- [ ] Coverage >= 90% (target)
- [ ] All error paths tested
- [ ] All validation edge cases tested
- [ ] All HTTP error scenarios tested
- [ ] All circuit breaker scenarios tested

---

## Resources

- Main coverage report: `/home/itsatony/code/go-hookd/coverage.out`
- Detailed analysis: `/home/itsatony/code/go-hookd/TEST_EXECUTION_COMPLETE_ANALYSIS.md`
- Quick summary: `/home/itsatony/code/go-hookd/TEST_EXECUTION_QUICK_SUMMARY.txt`

---

## Commands Reference

```bash
# Run full test suite with coverage
go test -v -race -cover -coverprofile=coverage.out ./...

# View coverage by function
go tool cover -func=coverage.out

# View coverage by file
go tool cover -html=coverage.out

# Find functions below 90%
go tool cover -func=coverage.out | awk '$NF !~ /100.0%/ && $NF !~ /100%/'

# Run specific test file
go test -v ./... -run TestName -cover

# Stress test (100 iterations)
go test -race -count=100 ./... -run TestName
```

---

## Notes

- All tests must pass the race detector
- Target is 90%+ coverage (not 100%)
- Focus on error paths, not happy paths
- Concurrent scenarios are critical
- Timeout handling is important for production

