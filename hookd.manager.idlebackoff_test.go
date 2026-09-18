// Package hookd tests for the adaptive idle poll backoff of the delivery worker pool.
//
// Purpose: pin the property that the pool's database cost while the queue is empty is
// proportional to traffic rather than to WorkerCount. Before this behaviour existed, a
// default pool polled at WorkerCount/QueuePollInterval forever — measured live at 10
// queries per second against a queue that had been empty for its entire life.
//
// Every rate assertion here carries a control run with backoff disabled, because a test
// that only asserts "few polls happened" passes just as well against a loop that is
// broken and polls never.
package hookd

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pollRecorder wraps a Repository and counts GetPendingDeliveries calls, optionally
// answering with one delivery so the "queue has work" path can be driven.
type pollRecorder struct {
	Repository
	polls   atomic.Int64
	mu      sync.Mutex
	hasWork bool
}

func (p *pollRecorder) GetPendingDeliveries(_ context.Context, _ int) ([]*Delivery, error) {
	p.polls.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.hasWork {
		return []*Delivery{}, nil
	}
	return []*Delivery{{ID: "d1", URL: "http://127.0.0.1:1/never", Status: DeliveryStatusPending, MaxAttempts: 1}}, nil
}

func (p *pollRecorder) setWork(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hasWork = v
}

// runPool starts a one-worker Manager with the given idle settings and returns the
// recorder plus a stop function.
func runPool(t *testing.T, baseMs, ceilingMs int, factor float64) (*pollRecorder, *Manager, func()) {
	t.Helper()

	rec := &pollRecorder{Repository: NewMockRepository()}

	cfg := NewConfig("postgres://test")
	cfg.WorkerCount = 1
	cfg.QueuePollInterval = baseMs
	cfg.QueueIdleMaxInterval = ceilingMs
	cfg.QueueIdleBackoffFactor = factor

	m, err := NewManager(cfg, rec)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return rec, m, func() { m.cancel(); m.wg.Wait() }
}

func TestWorkerLoopBacksOffWhileQueueIsEmpty(t *testing.T) {
	const (
		base    = 10
		ceiling = 200
		window  = 600 * time.Millisecond
	)

	// Control: backoff disabled. This run establishes what the loop does at a fixed
	// rate, so the assertion below cannot be satisfied by a loop that simply stopped.
	fixedRec, _, stopFixed := runPool(t, base, ceiling, 1.0)
	time.Sleep(window)
	stopFixed()
	fixed := fixedRec.polls.Load()

	backoffRec, _, stopBackoff := runPool(t, base, ceiling, 2.0)
	time.Sleep(window)
	stopBackoff()
	backed := backoffRec.polls.Load()

	if fixed < 10 {
		t.Fatalf("control run polled %d times in %v; the loop is not polling at all, so the comparison below would be vacuous", fixed, window)
	}
	if backed >= fixed {
		t.Errorf("idle backoff did not reduce polling: %d polls with backoff vs %d without", backed, fixed)
	}
	// Growth is 10,20,40,80,160,200,200... so a 600ms window admits well under 15 polls
	// while the fixed control admits roughly 60.
	if backed > fixed/2 {
		t.Errorf("idle backoff reduced polling by less than half: %d vs control %d", backed, fixed)
	}
}

func TestWorkerLoopStaysAtBaseIntervalWhileQueueHasWork(t *testing.T) {
	const (
		base    = 10
		ceiling = 400
		window  = 400 * time.Millisecond
	)

	busyRec, _, stopBusy := runPool(t, base, ceiling, 2.0)
	busyRec.setWork(true)
	time.Sleep(window)
	stopBusy()
	busy := busyRec.polls.Load()

	idleRec, _, stopIdle := runPool(t, base, ceiling, 2.0)
	time.Sleep(window)
	stopIdle()
	idle := idleRec.polls.Load()

	// The point of the reset-on-work arm: backoff must never slow a queue that has
	// work in it, so a busy pool with backoff enabled polls far more than an idle one.
	if busy <= idle {
		t.Errorf("a queue with work polled %d times, no more than an empty one at %d; the reset-on-work arm is not firing", busy, idle)
	}
}

func TestNotifyWakesABackedOffWorker(t *testing.T) {
	// The worker must first have CLIMBED past the observation window, not merely been
	// started with a high ceiling: during the climb ordinary polls still arrive, and a
	// test that observes one of those passes with Notify removed entirely.
	// climb: 5,20,80,320,1280... so after ~900ms the next poll is >1s away.
	const (
		climb   = 900 * time.Millisecond
		observe = 300 * time.Millisecond
	)

	// Control arm: same pool, same climb, NO Notify. It must observe no poll in the
	// window, which is what makes the Notify arm below evidence of anything.
	ctrl, _, stopCtrl := runPool(t, 5, 10000, 4.0)
	time.Sleep(climb)
	ctrlBefore := ctrl.polls.Load()
	time.Sleep(observe)
	ctrlAfter := ctrl.polls.Load()
	stopCtrl()
	if ctrlAfter != ctrlBefore {
		t.Fatalf("control arm polled %d times during the observation window without Notify; the worker has not backed off past it, so the Notify arm would be vacuous", ctrlAfter-ctrlBefore)
	}

	rec, m, stop := runPool(t, 5, 10000, 4.0)
	defer stop()
	time.Sleep(climb)
	before := rec.polls.Load()

	m.Notify()

	deadline := time.After(observe)
	for {
		if rec.polls.Load() > before {
			return // woken
		}
		select {
		case <-deadline:
			t.Fatalf("Notify did not wake the worker: still %d polls after %v", before, observe)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestNotifyIsSafeBeforeStartAndNeverBlocks(t *testing.T) {
	cfg := NewConfig("postgres://test")
	cfg.WorkerCount = 1
	m, err := NewManager(cfg, NewMockRepository())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// More calls than the wake channel can buffer: the excess must be dropped,
		// never block, or an enqueueing caller would stall on a busy pool.
		for i := 0; i < cfg.WorkerCount*10; i++ {
			m.Notify()
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked")
	}
}

func TestNextIdleInterval(t *testing.T) {
	const (
		base    = 100 * time.Millisecond
		ceiling = 800 * time.Millisecond
	)

	tests := []struct {
		name    string
		current time.Duration
		base    time.Duration
		ceiling time.Duration
		factor  float64
		want    time.Duration
	}{
		{"grows by the factor", base, base, ceiling, 2.0, 200 * time.Millisecond},
		{"clamps at the ceiling", 600 * time.Millisecond, base, ceiling, 2.0, ceiling},
		{"stays at the ceiling", ceiling, base, ceiling, 2.0, ceiling},
		{"factor 1.0 disables backoff", 400 * time.Millisecond, base, ceiling, 1.0, base},
		{"factor below 1 disables backoff", 400 * time.Millisecond, base, ceiling, 0.5, base},
		{"ceiling equal to base disables backoff", base, base, base, 2.0, base},
		{"never returns below base", time.Millisecond, base, ceiling, 2.0, base},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextIdleInterval(tt.current, tt.base, tt.ceiling, tt.factor); got != tt.want {
				t.Errorf("nextIdleInterval(%v, %v, %v, %v) = %v, want %v", tt.current, tt.base, tt.ceiling, tt.factor, got, tt.want)
			}
		})
	}
}

func TestJitterStaysWithinBoundsAndVaries(t *testing.T) {
	const d = 1000 * time.Millisecond
	low := time.Duration(float64(d) * (1 - QueueIdleJitterFraction))
	high := time.Duration(float64(d) * (1 + QueueIdleJitterFraction))

	seen := map[time.Duration]struct{}{}
	for i := 0; i < 200; i++ {
		got := jitter(d)
		if got < low || got > high {
			t.Fatalf("jitter(%v) = %v, outside [%v, %v]", d, got, low, high)
		}
		seen[got] = struct{}{}
	}
	// A jitter that always returned the same value would spread nothing, which is the
	// whole reason it exists.
	if len(seen) < 2 {
		t.Error("jitter returned a constant; a lockstep pool would not be spread")
	}

	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0) = %v, want 0", got)
	}
	if got := jitter(time.Microsecond); got < time.Millisecond {
		t.Errorf("jitter(1µs) = %v, want a floor of at least 1ms", got)
	}
}

func TestConfigIdleBackoffValidation(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Config)
		wantErr     bool
		wantCeiling int
		wantFactor  float64
	}{
		{
			name:        "defaults are set by NewConfig",
			mutate:      func(*Config) {},
			wantCeiling: DefaultQueueIdleMaxIntervalMs,
			wantFactor:  DefaultQueueIdleBackoffFactor,
		},
		{
			name:        "an unset ceiling adopts the default rather than being refused",
			mutate:      func(c *Config) { c.QueueIdleMaxInterval = 0; c.QueueIdleBackoffFactor = 0 },
			wantCeiling: DefaultQueueIdleMaxIntervalMs,
			wantFactor:  DefaultQueueIdleBackoffFactor,
		},
		{
			name: "an unset ceiling never lands below a slower base interval",
			mutate: func(c *Config) {
				c.QueuePollInterval = DefaultQueueIdleMaxIntervalMs * 2
				c.QueueIdleMaxInterval = 0
			},
			wantCeiling: DefaultQueueIdleMaxIntervalMs * 2,
			wantFactor:  DefaultQueueIdleBackoffFactor,
		},
		{
			name:    "a ceiling explicitly below the base interval is refused",
			mutate:  func(c *Config) { c.QueuePollInterval = 5000; c.QueueIdleMaxInterval = 1000 },
			wantErr: true,
		},
		{
			name:    "a factor explicitly below 1.0 is refused",
			mutate:  func(c *Config) { c.QueueIdleBackoffFactor = 0.5 },
			wantErr: true,
		},
		{
			name:        "a ceiling equal to the base interval is accepted as opting out",
			mutate:      func(c *Config) { c.QueuePollInterval = 1000; c.QueueIdleMaxInterval = 1000 },
			wantCeiling: 1000,
			wantFactor:  DefaultQueueIdleBackoffFactor,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfig("postgres://test")
			tt.mutate(cfg)

			err := cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected a validation error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if cfg.QueueIdleMaxInterval != tt.wantCeiling {
				t.Errorf("QueueIdleMaxInterval = %d, want %d", cfg.QueueIdleMaxInterval, tt.wantCeiling)
			}
			if cfg.QueueIdleBackoffFactor != tt.wantFactor {
				t.Errorf("QueueIdleBackoffFactor = %v, want %v", cfg.QueueIdleBackoffFactor, tt.wantFactor)
			}
		})
	}
}

// TestEveryEnqueuePathWakesTheWorkerPool pins the property that a caller never has to
// know about Notify: every Manager entry point that leaves a delivery in `pending`
// wakes the pool itself.
//
// Without this the idle backoff would trade a real database saving for a delivery
// latency of up to QueueIdleMaxInterval on the library's own public API, which is not a
// trade any consumer asked for.
func TestEveryEnqueuePathWakesTheWorkerPool(t *testing.T) {
	// drain empties the wake channel so each subtest observes only its own signal.
	drain := func(m *Manager) {
		for {
			select {
			case <-m.wake:
			default:
				return
			}
		}
	}

	tests := []struct {
		name   string
		invoke func(t *testing.T, m *Manager, repo *MockRepository)
	}{
		{
			name: "QueueInlineDelivery",
			invoke: func(t *testing.T, m *Manager, _ *MockRepository) {
				if _, err := m.QueueInlineDelivery(context.Background(), &QueueInlineDeliveryRequest{
					URL:       "https://example.test/hook",
					TenantID:  "t1",
					EventType: "thing.happened",
					Payload:   map[string]any{"a": 1},
				}); err != nil {
					t.Fatalf("QueueInlineDelivery: %v", err)
				}
			},
		},
		{
			name: "RetryDelivery",
			invoke: func(t *testing.T, m *Manager, repo *MockRepository) {
				d := &Delivery{
					ID: "d-retry", TenantID: "t1", EventType: "thing.happened",
					URL: "https://example.test/hook", Status: DeliveryStatusFailed,
					MaxAttempts: 3, CreatedAt: time.Now(),
				}
				if err := repo.CreateDelivery(context.Background(), d); err != nil {
					t.Fatalf("seed CreateDelivery: %v", err)
				}
				if _, err := m.RetryDelivery(context.Background(), d.ID); err != nil {
					t.Fatalf("RetryDelivery: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := NewMockRepository()
			cfg := NewConfig("postgres://test")
			cfg.WorkerCount = 1
			m, err := NewManager(cfg, repo)
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}
			// Deliberately NOT started: the wake must be queued by the enqueue path
			// itself, not by a running worker happening to poll.
			drain(m)

			tt.invoke(t, m, repo)

			select {
			case <-m.wake:
			default:
				t.Errorf("%s persisted a pending delivery without waking the worker pool", tt.name)
			}
		})
	}
}
