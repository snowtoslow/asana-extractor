package extractor

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestRealClock(t *testing.T) {
	t.Parallel()
	if (RealClock{}).NewTicker(0) != nil || (RealClock{}).NewTicker(-time.Second) != nil {
		t.Fatal("non-positive ticker interval accepted")
	}
	synctest.Test(t, func(t *testing.T) {
		clock := RealClock{}
		start := clock.Now()
		ticker := clock.NewTicker(30 * time.Second)
		defer ticker.Stop()
		synctest.Sleep(30 * time.Second)
		select {
		case tick := <-ticker.Ticks():
			if !tick.Equal(start.Add(30 * time.Second)) {
				t.Fatalf("tick = %s", tick)
			}
		default:
			t.Fatal("missing scheduled tick")
		}
		if !clock.Now().Equal(start.Add(30 * time.Second)) {
			t.Fatal("clock did not advance with synthetic time")
		}
		ticker.Stop()
		synctest.Sleep(30 * time.Second)
		select {
		case <-ticker.Ticks():
			t.Fatal("stopped ticker emitted another tick")
		default:
		}
	})
}
