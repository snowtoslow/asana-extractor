package asana

import "log/slog"

// Resty's diagnostic arguments can contain headers, URLs, and response bodies.
// Emit fixed messages only; callers receive safe errors from list operations.
type httpLogger struct {
	logger *slog.Logger
}

func (l httpLogger) Errorf(string, ...any) {
	l.logger.Error("asana http client error")
}

func (l httpLogger) Warnf(string, ...any) {
	l.logger.Warn("asana http client warning")
}

// Wire dumps are suppressed even if Resty debug is accidentally enabled.
func (l httpLogger) Debugf(string, ...any) {}
