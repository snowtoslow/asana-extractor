package asana

import (
	"errors"
	"log/slog"
	"time"
)

// Option configures client behavior without changing environment configuration.
type Option func(*clientOptions) error

type clientOptions struct {
	retries int
	wait    time.Duration
	maxWait time.Duration
	logger  *slog.Logger
}

// WithRetries sets retries after the initial attempt and the exponential backoff
// bounds. Zero retries disables retries; zero waits permit immediate retries.
// A valid Retry-After header takes precedence over these backoff bounds.
func WithRetries(count int, wait, maxWait time.Duration) Option {
	return func(o *clientOptions) error {
		if count < 0 || wait < 0 || maxWait < wait {
			return errors.New("asana: retries and waits must be non-negative, with max wait >= wait")
		}
		o.retries, o.wait, o.maxWait = count, wait, maxWait
		return nil
	}
}

// WithLogger supplies the structured logger for safe HTTP diagnostics.
func WithLogger(logger *slog.Logger) Option {
	return func(o *clientOptions) error {
		if logger == nil {
			return errors.New("asana: logger must not be nil")
		}
		o.logger = logger
		return nil
	}
}
