package extractor

import "time"

// Ticker supplies scheduled ticks and releases its resources when stopped.
type Ticker interface {
	Ticks() <-chan time.Time
	Stop()
}

// TickerFactory creates tickers for a scheduler's configured interval.
type TickerFactory interface {
	NewTicker(interval time.Duration) Ticker
}

// RealClock implements Clock and TickerFactory using the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now()
}

// NewTicker returns nil for a non-positive interval.
func (RealClock) NewTicker(interval time.Duration) Ticker {
	if interval <= 0 {
		return nil
	}
	return &realTicker{ticker: time.NewTicker(interval)}
}

type realTicker struct {
	ticker *time.Ticker
}

func (t *realTicker) Ticks() <-chan time.Time {
	return t.ticker.C
}

func (t *realTicker) Stop() {
	t.ticker.Stop()
}

var _ Clock = RealClock{}
var _ TickerFactory = RealClock{}
