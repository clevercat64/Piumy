package governor

import (
	"context"
	"testing"
	"time"
)

func TestNewDelayWindowFallback(t *testing.T) {
	cases := []struct {
		name           string
		min, max       time.Duration
		defMin, defMax time.Duration
		wantMin        time.Duration
		wantMax        time.Duration
	}{
		{"valid passthrough", 2 * time.Second, 6 * time.Second, time.Second, 5 * time.Second, 2 * time.Second, 6 * time.Second},
		{"non-positive min falls back", 0, 6 * time.Second, time.Second, 5 * time.Second, time.Second, 6 * time.Second},
		{"non-positive max falls back", 2 * time.Second, 0, time.Second, 5 * time.Second, 2 * time.Second, 5 * time.Second},
		{"inverted max falls back", 4 * time.Second, 1 * time.Second, time.Second, 5 * time.Second, 4 * time.Second, 5 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := NewDelayWindow(c.min, c.max, c.defMin, c.defMax)
			if w.Min != c.wantMin || w.Max != c.wantMax {
				t.Errorf("got Min=%v Max=%v, want Min=%v Max=%v", w.Min, w.Max, c.wantMin, c.wantMax)
			}
		})
	}
}

func TestDelayWindowRandomInBounds(t *testing.T) {
	w := DelayWindow{Min: 10 * time.Millisecond, Max: 30 * time.Millisecond}
	for i := 0; i < 50; i++ {
		d := w.Random()
		if d < w.Min || d >= w.Max {
			t.Fatalf("Random() = %v, want in [%v, %v)", d, w.Min, w.Max)
		}
	}
}

// TestDelayWindowRandomVaries guards the property pacing exists for: never
// the same interval twice (a fixed gap is a bot's fingerprint). A Random that
// always returned Min passed TestDelayWindowRandomInBounds above (T184). 200
// draws over 20 ms in nanoseconds: P(all equal) = (1/2e7)^199, and no clock is
// involved — measuring real waits through the scheduler's jitter detected
// that same sabotage in only 57% of runs, this cannot flake in either
// direction.
func TestDelayWindowRandomVaries(t *testing.T) {
	w := DelayWindow{Min: 10 * time.Millisecond, Max: 30 * time.Millisecond}
	first := w.Random()
	for i := 0; i < 200; i++ {
		if w.Random() != first {
			return
		}
	}
	t.Fatalf("200 draws from [%v, %v) were all %v — a fixed interval is the pattern pacing exists to avoid", w.Min, w.Max, first)
}

func TestDelayWindowRandomDegenerate(t *testing.T) {
	w := DelayWindow{Min: 5 * time.Millisecond, Max: 5 * time.Millisecond}
	if d := w.Random(); d != w.Min {
		t.Errorf("Max<=Min: Random() = %v, want %v", d, w.Min)
	}
}

func TestDelayWindowSleepCancels(t *testing.T) {
	w := DelayWindow{Min: time.Hour, Max: 2 * time.Hour} // would hang the test if ctx is ignored
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	w.Sleep(ctx)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Sleep did not respect ctx cancellation, took %v", elapsed)
	}
}
