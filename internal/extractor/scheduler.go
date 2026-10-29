package extractor

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/asana"
)

// Runner performs one context-aware extraction and returns only after its work
// and child goroutines have stopped. It must cooperate with cancellation.
type Runner interface {
	Run(ctx context.Context) error
}

// Scheduler serializes runs, skipping ticks while work is active.
// Construct it with NewScheduler and run one scheduling loop at a time.
type Scheduler struct {
	runner   Runner
	clock    TickerFactory
	interval time.Duration
	logger   *slog.Logger
}

// NewScheduler accepts the two supported modes and an injected ticker factory.
func NewScheduler(
	runner Runner,
	clock TickerFactory,
	interval time.Duration,
	logger *slog.Logger,
) (*Scheduler, error) {
	if runner == nil {
		return nil, errors.New("scheduler: runner is required")
	}
	if clock == nil {
		return nil, errors.New("scheduler: ticker factory is required")
	}
	if interval != 5*time.Minute && interval != 30*time.Second {
		return nil, errors.New("scheduler: interval must be 5m or 30s")
	}
	if logger == nil {
		return nil, errors.New("scheduler: logger is required")
	}
	return &Scheduler{runner: runner, clock: clock, interval: interval, logger: logger}, nil
}

// Run starts immediately, then runs on ticks. API/run failures are logged and
// do not terminate the loop. Cancellation is a normal shutdown: Run waits for
// active work to finish or abort and returns nil. It does not impose a shutdown
// timeout on a Runner that ignores cancellation.
func (s *Scheduler) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ticker := s.clock.NewTicker(s.interval)
	if ticker == nil {
		return errors.New("scheduler: ticker factory returned nil")
	}
	defer ticker.Stop()
	ticks := ticker.Ticks()
	if ticks == nil {
		return errors.New("scheduler: ticker channel is required")
	}
	// Each worker sends exactly one result. Every shutdown path receives that
	// result before returning, so workers cannot outlive the scheduling loop.
	completed := make(chan error)
	isRunning := false
	start := func() {
		isRunning = true
		go func() { completed <- s.runner.Run(runCtx) }()
	}
	start()
	for {
		if ctx.Err() != nil {
			cancel()
			if isRunning {
				<-completed
			}
			return nil
		}
		select {
		case <-ctx.Done():
			cancel()
			if isRunning {
				<-completed
			}
			return nil
		case err := <-completed:
			isRunning = false
			if err != nil && ctx.Err() == nil {
				s.logFailure(ctx, err)
			}
			// Tickers may buffer a tick while a run is finishing. Discard
			// queued ticks rather than replaying them after completion.
			for draining := true; draining; {
				select {
				case _, ok := <-ticks:
					if !ok {
						return errors.New("scheduler: ticker channel closed unexpectedly")
					}
					s.logger.InfoContext(ctx, "extraction tick skipped; run still active")
				default:
					draining = false
				}
			}
		case _, ok := <-ticks:
			if !ok {
				cancel()
				if isRunning {
					<-completed
				}
				return errors.New("scheduler: ticker channel closed unexpectedly")
			}
			if ctx.Err() != nil {
				continue
			}
			if isRunning {
				s.logger.InfoContext(ctx, "extraction tick skipped; run still active")
				continue
			}
			start()
		}
	}
}

func (s *Scheduler) logFailure(ctx context.Context, err error) {
	// Unknown error strings may contain credentials or paths. Log only known
	// status codes and fixed messages, leaving raw details out of diagnostics.
	if statusError, ok := errors.AsType[*asana.HTTPError](err); ok {
		if statusError.StatusCode == http.StatusUnauthorized {
			s.logger.ErrorContext(ctx, "extraction failed: invalid or revoked personal access token")
			return
		}
		s.logger.ErrorContext(
			ctx,
			"extraction failed",
			"http_status",
			statusError.StatusCode,
		)
		return
	}
	s.logger.ErrorContext(ctx, "extraction failed")
}

var _ Runner = (*Extractor)(nil)
