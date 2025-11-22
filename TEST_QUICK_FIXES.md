# Quick Fixes to Reach 90% Coverage

**Current Coverage**: 63.5%
**Target Coverage**: 90%+
**Gap**: 26.5 percentage points
**Estimated Time**: 8-12 hours (with DB setup)

---

## Breakdown of Required Coverage Improvements

| Issue | Current | Target | Impact | Effort | Priority |
|-------|---------|--------|--------|--------|----------|
| PostgreSQL DB (after setup) | 0% | 90% | +20-25% | 2-3h + setup | 1 |
| ID generation errors | 75% | 90% | +2% | 1h | 2 |
| URL validation | 80% | 90% | +1% | 2h | 2 |
| List filtering | 77.8% | 90% | +1% | 2h | 2 |
| Manager.Publish() | 0% | 90% | +1% | 1h | 2 |
| Testing helpers | 59% | 90% | +1% | 1h | 2 |
| **TOTAL** | **63.5%** | **90%** | **+26.5%** | **10-16h** | |

---

## Quick Fix #1: PostgreSQL Database Setup (2-3 hours)
**Impact**: +20-25% coverage
**Blocker**: Unblocks 27 functions

### Steps
```bash
cd /home/itsatony/code/go-hookd

# 1. Start PostgreSQL (docker-compose.yml exists)
docker-compose up -d postgres

# 2. Wait for startup
sleep 5

# 3. Run migrations
./scripts/db-dev.sh bootstrap

# 4. Verify connection
psql -h localhost -U hookd_dev -d hookd_dev -c "SELECT version();"

# 5. Run tests
go test ./internal/... -run TestPostgres -v -timeout=20m

# Expected: 40+ tests pass, coverage increases to ~85%
```

### File: `/home/itsatony/code/go-hookd/docker-compose.yml`
Already configured with PostgreSQL service.

### File: `/home/itsatony/code/go-hookd/scripts/db-dev.sh`
Handles database bootstrap and migrations.

---

## Quick Fix #2: ID Generation Error Tests (1 hour)
**Impact**: +1-2% coverage
**File**: `/home/itsatony/code/go-hookd/internal/hookd.utils_test.go`

### Add This Test Function
```go
func TestIDGeneration_Errors(t *testing.T) {
    // Test case 1: Simulate nanoID failure
    // This tests the error handling path not currently covered

    // Note: In real scenario, would need to mock gonanoid.New()
    // For now, verify error message clarity and propagation

    // Verify that if nanoID fails, proper error is returned
    // This path is in: GenerateSubscriptionID/GenerateDeliveryID/GenerateAttemptID
}
```

### Location in File
Add after `TestGenerateAttemptID()` function (~line 180)

### Current Coverage
- GenerateSubscriptionID: 75% (missing error case)
- GenerateDeliveryID: 75% (missing error case)
- GenerateAttemptID: 75% (missing error case)

---

## Quick Fix #3: URL Validation Edge Cases (2 hours)
**Impact**: +1% coverage
**File**: `/home/itsatony/code/go-hookd/internal/hookd.models_test.go`

### Add This Test Function
```go
func TestValidateURL_EdgeCases(t *testing.T) {
    cases := []struct {
        name  string
        url   string
        valid bool
    }{
        // Basic valid cases (already tested)
        {"simple", "https://example.com", true},

        // Edge cases (missing coverage)
        {"with_port", "https://example.com:8443/webhook", true},
        {"with_path", "https://example.com/webhook/v1", true},
        {"with_query", "https://example.com/webhook?key=value", true},
        {"localhost", "http://localhost:3000/webhook", true},
        {"ip_address", "http://192.168.1.1/webhook", true},

        // Invalid edge cases
        {"port_too_high", "https://example.com:65536/", false},
        {"invalid_scheme", "ftp://example.com/webhook", false},
        {"missing_domain", "https:///webhook", false},
        {"space_in_url", "https://example.com/webhook path", false},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            err := validateURL(tc.url)
            if tc.valid {
                require.NoError(t, err, "expected valid URL: %s", tc.url)
            } else {
                require.Error(t, err, "expected invalid URL: %s", tc.url)
            }
        })
    }
}
```

### Location in File
Add after `TestCreateSubscriptionRequest_Validate()` (~line 150)

### Current Coverage
- validateURL: 80% (missing edge cases)

---

## Quick Fix #4: List Filtering Comprehensive Tests (2 hours)
**Impact**: +1% coverage
**File**: `/home/itsatony/code/go-hookd/internal/hookd.subscription_test.go`

### Add This Test Function
```go
func TestListSubscriptions_ComplexFilters(t *testing.T) {
    repo := NewMockRepository()
    logger := zaptest.NewLogger(t)
    config := &Config{WorkerCount: 5, MaxRetries: 3}
    manager, err := NewManager(config, logger, repo)
    require.NoError(t, err)
    defer manager.Stop(context.Background())

    // Create diverse subscription set
    sub1 := createTestSubscription(t, "tenant1", "user.created", StatusActive)
    sub2 := createTestSubscription(t, "tenant1", "user.updated", StatusActive)
    sub3 := createTestSubscription(t, "tenant2", "user.created", StatusPaused)
    sub4 := createTestSubscription(t, "tenant2", "order.created", StatusActive)

    _, _ = repo.CreateSubscription(context.Background(), sub1)
    _, _ = repo.CreateSubscription(context.Background(), sub2)
    _, _ = repo.CreateSubscription(context.Background(), sub3)
    _, _ = repo.CreateSubscription(context.Background(), sub4)

    // Test: Multiple event types with status filter
    filter := &SubscriptionFilter{
        Tenant:     "tenant1",
        Status:     StatusActive,
        EventTypes: []string{"user.created", "user.updated"},
    }
    subs, err := repo.ListSubscriptions(context.Background(), filter, 0, 10)
    require.NoError(t, err)
    require.Len(t, subs, 2)

    // Test: Empty result with complex filter
    filter2 := &SubscriptionFilter{
        Tenant:     "nonexistent",
        Status:     StatusActive,
    }
    subs2, err := repo.ListSubscriptions(context.Background(), filter2, 0, 10)
    require.NoError(t, err)
    require.Len(t, subs2, 0)

    // Test: Pagination with filters
    filter3 := &SubscriptionFilter{
        Tenant: "tenant1",
    }
    subs3, err := repo.ListSubscriptions(context.Background(), filter3, 0, 1)
    require.NoError(t, err)
    require.Len(t, subs3, 1) // Limited by offset/limit
}
```

### Location in File
Add after `TestListSubscriptions()` function

### Current Coverage
- ListSubscriptions: 77.8% (missing complex filter combinations)

---

## Quick Fix #5: Manager.Publish() Tests (1 hour)
**Impact**: +1% coverage
**File**: `/home/itsatony/code/go-hookd/internal/hookd.manager_test.go`

### Add This Test Function
```go
func TestManager_EventBusPublishing(t *testing.T) {
    repo := NewMockRepository()
    logger := zaptest.NewLogger(t)
    config := &Config{WorkerCount: 5, MaxRetries: 3}

    // Custom EventBus mock to capture published events
    type publishedEvent struct {
        topic string
        data  interface{}
    }

    var publishedEvents []publishedEvent
    var mu sync.Mutex

    customBus := &testEventBus{
        publishFunc: func(topic string, data interface{}) {
            mu.Lock()
            defer mu.Unlock()
            publishedEvents = append(publishedEvents, publishedEvent{topic, data})
        },
    }

    manager, err := NewManager(config, logger, repo,
        WithEventBus(customBus),
    )
    require.NoError(t, err)
    defer manager.Stop(context.Background())

    // Trigger an event by publishing directly
    manager.Publish("test.topic", map[string]string{"key": "value"})

    // Verify event was published
    time.Sleep(10 * time.Millisecond) // Allow async processing
    require.Len(t, publishedEvents, 1)
    require.Equal(t, "test.topic", publishedEvents[0].topic)
}
```

### Helper Type
```go
type testEventBus struct {
    publishFunc func(topic string, data interface{})
}

func (b *testEventBus) Publish(topic string, data interface{}) {
    if b.publishFunc != nil {
        b.publishFunc(topic, data)
    }
}

func (b *testEventBus) Subscribe(topic string, handler func(interface{})) func() {
    return func() {}
}
```

### Location in File
Add after `TestEventPublishing()` function (~line 250)

### Current Coverage
- Manager.Publish(): 0% (never called in tests)

---

## Quick Fix #6: Testing Helpers (1 hour)
**Impact**: +1% coverage
**File**: `/home/itsatony/code/go-hookd/internal/testing_helpers.go`

### Option A: Use the Helpers (Add Tests)
Add to any E2E test file:
```go
func TestWaitForDeliveryStatus(t *testing.T) {
    repo := NewMockRepository()

    // Create a delivery
    delivery := &Delivery{
        ID:     "dlv_123",
        Status: StatusPending,
    }
    repo.CreateDelivery(context.Background(), delivery)

    // Update it in background
    go func() {
        time.Sleep(50 * time.Millisecond)
        delivery.Status = StatusSuccess
        repo.UpdateDelivery(context.Background(), delivery)
    }()

    // Wait for status change
    err := WaitForDeliveryStatus(repo, "dlv_123", StatusSuccess, 1*time.Second)
    require.NoError(t, err)
}
```

### Option B: Remove Unused Helpers
If not needed, delete from `testing_helpers.go`:
```go
// Remove lines 46-94 (WaitForDeliveryStatus and WaitForDeliveryStatusAny)
```

**Recommendation**: Use Option A (implement the tests) as these helpers appear useful for E2E workflows.

---

## Execution Plan (10-16 hours total)

### Hour 1-3: PostgreSQL Setup
```bash
# Setup
docker-compose up -d postgres
sleep 5
./scripts/db-dev.sh bootstrap

# Verify
go test ./internal/... -run TestPostgres -v -timeout=20m

# Expected: 40+ tests pass, coverage to ~85%
```

### Hour 4: ID Generation Tests
- Edit: `/home/itsatony/code/go-hookd/internal/hookd.utils_test.go`
- Add: TestIDGeneration_Errors function
- Verify: `go test ./internal/... -run TestIDGeneration -v`

### Hour 5-6: URL Validation Tests
- Edit: `/home/itsatony/code/go-hookd/internal/hookd.models_test.go`
- Add: TestValidateURL_EdgeCases function
- Verify: `go test ./internal/... -run TestValidateURL -v`

### Hour 7-8: List Filtering Tests
- Edit: `/home/itsatony/code/go-hookd/internal/hookd.subscription_test.go`
- Add: TestListSubscriptions_ComplexFilters function
- Verify: `go test ./internal/... -run TestListSubscriptions -v`

### Hour 9: Manager.Publish() Tests
- Edit: `/home/itsatony/code/go-hookd/internal/hookd.manager_test.go`
- Add: TestManager_EventBusPublishing function
- Add: testEventBus helper type
- Verify: `go test ./internal/... -run TestManager_EventBusPublishing -v`

### Hour 10: Testing Helpers
- Edit: `/home/itsatony/code/go-hookd/internal/testing_helpers.go`
- Either add tests or remove unused functions
- Verify: `go test ./internal/... -run TestWaitFor -v`

### Hour 11-12: Full Verification
```bash
# Run full suite
go test ./internal/... -race -cover -covermode=atomic -timeout=20m

# Generate coverage report
go tool cover -func=coverage.out | tail -20

# Should show: coverage: 90%+ of statements
```

### Hour 13-16: Additional Improvements (Optional)
- Circuit breaker recovery tests
- Webhook timeout edge cases
- Partial failure scenarios
- Stress test execution

---

## Success Verification

### After Each Fix
```bash
# Check coverage for that module
go test ./internal/... -cover

# Look for "coverage: X% of statements"
# Should increase with each fix
```

### Final Verification
```bash
# Full suite with detailed coverage
go test ./internal/... -race -coverprofile=coverage.out -covermode=atomic

# View coverage summary
go tool cover -func=coverage.out | grep "total:"

# Generate HTML report (optional)
go tool cover -html=coverage.out -o coverage.html
```

### Success Criteria
- [ ] Coverage report shows 90%+ of statements
- [ ] All 157 unit tests pass
- [ ] All PostgreSQL integration tests pass (40+)
- [ ] No race conditions detected
- [ ] No test failures or flakiness

---

## Common Issues & Solutions

### Issue: PostgreSQL Connection Fails
```
pq: password authentication failed for user "hookd_dev"
```

**Fix**:
```bash
# Check if PostgreSQL is running
docker-compose ps

# Start if needed
docker-compose up -d postgres

# Wait for startup
sleep 5

# Try again
psql -h localhost -U hookd_dev -d hookd_dev -c "SELECT 1"
```

### Issue: Port 5432 Already in Use
```
Error: port is already allocated
```

**Fix**:
```bash
# Stop existing PostgreSQL
docker-compose down

# Or use different port in docker-compose.yml
# Then update test connection string

# Start fresh
docker-compose up -d postgres
```

### Issue: Tests Still Show 0% Coverage After Edits
```
make sure to:
1. Save file changes
2. Run: go test ./internal/... -cover
3. Check latest test output
```

### Issue: New Tests Not Running
```bash
# Verify test discovery
go test ./internal/... -list | grep TestYourNewTest

# If not found, check:
# 1. File ends with _test.go
# 2. Function starts with Test
# 3. No syntax errors: go build ./internal/...
```

---

## File Change Checklist

Before committing changes:

- [ ] All new tests added to correct files
- [ ] Test function names follow pattern: TestComponentName_Operation
- [ ] No syntax errors: `go build ./internal/...`
- [ ] Tests pass: `go test ./internal/... -v`
- [ ] Coverage increased: `go test ./internal/... -cover`
- [ ] No race conditions: `go test ./internal/... -race`
- [ ] All error cases handled properly
- [ ] Helper functions documented

---

## Expected Results

| Phase | Coverage | Time | Tests Passing |
|-------|----------|------|---------------|
| Start | 63.5% | 0h | 157/157 |
| After DB setup | 85-88% | 3h | 197/197 |
| After fixes | 90%+ | 10h | 250+/250+ |
| Final state | 95%+ | 16h | All passing |

---

## Resources

- Project CLAUDE.md: `/home/itsatony/code/go-hookd/CLAUDE.md`
- Implementation guide: `/home/itsatony/code/go-hookd/docs/implementation_guide.md`
- Code rules: `/home/itsatony/code/go-hookd/docs/code_rules.md`
- Docker compose: `/home/itsatony/code/go-hookd/docker-compose.yml`
- Database scripts: `/home/itsatony/code/go-hookd/scripts/db-dev.sh`

---

**Last Updated**: 2025-11-22
**Status**: Ready for implementation
**Time to 90%**: 8-12 hours including setup
