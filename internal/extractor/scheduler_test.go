package extractor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/asana"
)

func TestNewScheduler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		interval time.Duration
		missing  string
	}{
		{name: "nil runner", interval: 30 * time.Second, missing: "runner"},
		{name: "nil clock", interval: 30 * time.Second, missing: "clock"},
		{name: "nil logger", interval: 30 * time.Second, missing: "logger"},
		{name: "zero interval"},
		{name: "negative interval", interval: -time.Second},
		{name: "unsupported interval", interval: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var runner Runner = runnerFunc(func(context.Context) error { return nil })
			var clock TickerFactory = &fakeTickerFactory{ticker: &fakeTicker{ticks: make(chan time.Time)}}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			switch tt.missing {
			case "runner":
				runner = nil
			case "clock":
				clock = nil
			case "logger":
				logger = nil
			}
			_, err := NewScheduler(
				runner,
				clock,
				tt.interval,
				logger,
			)
			if err == nil {
				t.Fatal("invalid scheduler accepted")
			}
		})
	}
}

func TestSchedulerTicksAndErrors(t *testing.T) {
	t.Parallel()
	failures := []struct {
		name    string
		err     error
		message string
	}{
		{name: "generic", err: errors.New("synthetic-test-pat"), message: "extraction failed"},
		{name: "unauthorized", err: fmt.Errorf("synthetic-test-pat: %w", &asana.HTTPError{StatusCode: 401}), message: "invalid or revoked personal access token"},
		{name: "server error", err: &asana.HTTPError{StatusCode: 500}, message: "extraction failed"},
	}
	for _, interval := range []time.Duration{5 * time.Minute, 30 * time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			for _, failure := range failures {
				t.Run(failure.name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						ticker := &fakeTicker{ticks: make(chan time.Time)}
						clock := &fakeTickerFactory{ticker: ticker}
						var calls atomic.Int32
						runner := runnerFunc(func(context.Context) error {
							if calls.Add(1) == 1 {
								return failure.err
							}
							return nil
						})
						var logs bytes.Buffer
						scheduler := testScheduler(t, runner, clock, interval, &logs)
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						stopped := make(chan error)
						go func() { stopped <- scheduler.Run(ctx) }()
						synctest.Wait()
						if calls.Load() != 1 {
							t.Fatalf("immediate runs = %d", calls.Load())
						}
						if clock.interval != interval || clock.calls != 1 {
							t.Fatal("wrong ticker interval or count")
						}
						for range 3 {
							ticker.ticks <- time.Now()
							synctest.Wait()
						}
						if calls.Load() != 4 {
							t.Fatalf("runs after three ticks = %d, want 4", calls.Load())
						}
						cancel()
						if err := <-stopped; err != nil {
							t.Fatal(err)
						}
						if !ticker.isStopped {
							t.Fatal("ticker not stopped")
						}
						if !strings.Contains(logs.String(), failure.message) {
							t.Fatalf("missing failure log: %s", logs.String())
						}
						decodeLogs(t, &logs)
					})
				})
			}
		})
	}
}

func TestSchedulerSkipsOverlappingTicks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ticker := &fakeTicker{ticks: make(chan time.Time)}
		clock := &fakeTickerFactory{ticker: ticker}
		release := make(chan struct{})
		var calls, active atomic.Int32
		runner := runnerFunc(func(ctx context.Context) error {
			if active.Add(1) != 1 {
				t.Error("overlapping extraction runs")
			}
			defer active.Add(-1)
			if calls.Add(1) == 1 {
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
		var logs bytes.Buffer
		scheduler := testScheduler(t, runner, clock, 30*time.Second, &logs)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stopped := make(chan error)
		go func() { stopped <- scheduler.Run(ctx) }()
		synctest.Wait()
		for range 3 {
			ticker.ticks <- time.Now()
			synctest.Wait()
		}
		if calls.Load() != 1 {
			t.Fatalf("busy run received extra starts: %d", calls.Load())
		}
		if strings.Count(logs.String(), "tick skipped") != 3 {
			t.Fatalf("missing skipped tick logs: %s", logs.String())
		}
		close(release)
		synctest.Wait()
		ticker.ticks <- time.Now()
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("idle tick did not start next run: %d", calls.Load())
		}
		cancel()
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		if active.Load() != 0 {
			t.Fatal("run still active after shutdown")
		}
	})
}

func TestSchedulerDiscardsQueuedTicks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// These ticks are queued by the active runner before it returns.
		ticker := &fakeTicker{ticks: make(chan time.Time, 3)}
		clock := &fakeTickerFactory{ticker: ticker}
		var calls atomic.Int32
		runner := runnerFunc(func(context.Context) error {
			if calls.Add(1) == 1 {
				for range 3 {
					ticker.ticks <- time.Now()
				}
			}
			return nil
		})
		var logs bytes.Buffer
		scheduler := testScheduler(t, runner, clock, 30*time.Second, &logs)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stopped := make(chan error)
		go func() { stopped <- scheduler.Run(ctx) }()
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("queued busy ticks were replayed: %d runs", calls.Load())
		}
		if strings.Count(logs.String(), "tick skipped") != 3 {
			t.Fatalf("skipped ticks = %s", logs.String())
		}
		ticker.ticks <- time.Now()
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("new tick failed to start run")
		}
		cancel()
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
	})
}

func TestSchedulerWaitsForCanceledRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ticker := &fakeTicker{ticks: make(chan time.Time)}
		clock := &fakeTickerFactory{ticker: ticker}
		canceled, release := make(chan struct{}), make(chan struct{})
		var cleaned atomic.Bool
		runner := runnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			close(canceled)
			<-release
			cleaned.Store(true)
			return ctx.Err()
		})
		var logs bytes.Buffer
		scheduler := testScheduler(t, runner, clock, 30*time.Second, &logs)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stopped := make(chan error)
		go func() { stopped <- scheduler.Run(ctx) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-canceled:
		default:
			t.Fatal("run did not receive cancellation")
		}
		select {
		case <-stopped:
			t.Fatal("scheduler returned before active run cleaned up")
		default:
		}
		close(release)
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		if !cleaned.Load() || !ticker.isStopped {
			t.Fatal("shutdown leaked work or ticker")
		}
		if strings.Contains(logs.String(), "extraction failed") {
			t.Fatal("normal shutdown logged failure")
		}
	})
}

func TestSchedulerAlreadyCanceled(t *testing.T) {
	t.Parallel()
	clock := &fakeTickerFactory{ticker: &fakeTicker{ticks: make(chan time.Time)}}
	runner := runnerFunc(func(context.Context) error { t.Error("canceled scheduler started work"); return nil })
	var logs bytes.Buffer
	scheduler := testScheduler(t, runner, clock, 30*time.Second, &logs)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := scheduler.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if clock.calls != 0 {
		t.Fatal("already canceled scheduler created ticker")
	}
}

func TestSchedulerInvalidTicker(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"nil ticker", "nil channel", "closed channel"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ticker := &fakeTicker{}
				if name == "closed channel" {
					ticker.ticks = make(chan time.Time)
				}
				clock := &fakeTickerFactory{}
				if name != "nil ticker" {
					clock.ticker = ticker
				}
				runner := runnerFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
				var logs bytes.Buffer
				scheduler := testScheduler(t, runner, clock, 30*time.Second, &logs)
				stopped := make(chan error)
				go func() { stopped <- scheduler.Run(t.Context()) }()
				if name == "closed channel" {
					synctest.Wait()
					close(ticker.ticks)
				}
				if err := <-stopped; err == nil {
					t.Fatal("invalid ticker accepted")
				}
				if name != "nil ticker" && !ticker.isStopped {
					t.Fatal("invalid ticker not stopped")
				}
			})
		})
	}
}

type runnerFunc func(context.Context) error

func (f runnerFunc) Run(ctx context.Context) error { return f(ctx) }

type fakeTicker struct {
	ticks     chan time.Time
	isStopped bool
}

func (t *fakeTicker) Ticks() <-chan time.Time { return t.ticks }
func (t *fakeTicker) Stop()                   { t.isStopped = true }

type fakeTickerFactory struct {
	ticker   *fakeTicker
	interval time.Duration
	calls    int
}

func (f *fakeTickerFactory) NewTicker(interval time.Duration) Ticker {
	f.interval = interval
	f.calls++
	if f.ticker == nil {
		return nil
	}
	return f.ticker
}

// This test helper keeps the logger capture alongside the four scheduler inputs.
func testScheduler(
	t *testing.T,
	runner Runner,
	clock TickerFactory,
	interval time.Duration,
	logs *bytes.Buffer,
) *Scheduler {
	t.Helper()
	scheduler, err := NewScheduler(
		runner,
		clock,
		interval,
		slog.New(slog.NewJSONHandler(logs, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return scheduler
}
