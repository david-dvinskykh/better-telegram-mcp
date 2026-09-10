package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gotd/td/clock"
)

// dateServer answers like the Bot API host does: a Date header, whatever else.
func dateServer(t *testing.T, now func() time.Time) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Date", now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestOffsetIsNearZeroWhenTheHostIsRight(t *testing.T) {
	base := dateServer(t, time.Now)
	offset, err := measureClockOffset(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if offset > 2*time.Second || offset < -2*time.Second {
		t.Errorf("a correct host measured %s off", offset)
	}
}

// The failure this exists for: the host is behind, so every message Telegram
// sends looks like it was created in the future and gets dropped.
func TestOffsetFindsAHostThatIsBehind(t *testing.T) {
	base := dateServer(t, func() time.Time { return time.Now().Add(35 * time.Second) })
	offset, err := measureClockOffset(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if offset < 33*time.Second || offset > 37*time.Second {
		t.Errorf("expected about +35s, got %s", offset)
	}
}

func TestOffsetFindsAHostThatIsAhead(t *testing.T) {
	base := dateServer(t, func() time.Time { return time.Now().Add(-90 * time.Second) })
	offset, err := measureClockOffset(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if offset > -88*time.Second || offset < -92*time.Second {
		t.Errorf("expected about -90s, got %s", offset)
	}
}

func TestSkewedHostGetsACorrectedClock(t *testing.T) {
	base := dateServer(t, func() time.Time { return time.Now().Add(35 * time.Second) })
	corrected := resolveClock(context.Background(), base)
	if _, ok := corrected.(offsetClock); !ok {
		t.Fatalf("expected a corrected clock, got %T", corrected)
	}
	drift := corrected.Now().Sub(time.Now())
	if drift < 33*time.Second || drift > 37*time.Second {
		t.Errorf("the corrected clock is %s from the host, want about 35s", drift)
	}
}

// A host that is right keeps its own clock: adopting a second-resolution
// measurement would add noise rather than remove it.
func TestCorrectHostKeepsTheSystemClock(t *testing.T) {
	base := dateServer(t, time.Now)
	if got := resolveClock(context.Background(), base); got != clock.Clock(clock.System) {
		t.Errorf("expected the system clock, got %T", got)
	}
}

// No answer is not an error: the server has to start anyway.
func TestUnreachableProbeFallsBackToTheSystemClock(t *testing.T) {
	if got := resolveClock(context.Background(), "http://127.0.0.1:1"); got != clock.Clock(clock.System) {
		t.Errorf("expected the system clock, got %T", got)
	}
}

// Timers measure durations, which a wrong wall clock does not affect, so the
// offset must not leak into them.
func TestOffsetClockDoesNotSkewTimers(t *testing.T) {
	c := offsetClock{offset: time.Hour}
	started := time.Now()
	timer := c.Timer(20 * time.Millisecond)
	<-timer.C()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("the timer waited %s; the offset leaked into it", elapsed)
	}
}
