package telegram

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gotd/td/clock"
)

// MTProto carries the clock in every message: an id is a timestamp, and both
// ends reject an id too far from their own idea of now. gotd allows a server
// message at most 30 seconds into the future before dropping it as a replay,
// so a host clock running half a minute behind makes every reply Telegram
// sends unreadable. The connection then restarts forever without a single
// error that names the clock -- which is exactly how three accounts went dark
// on a Raspberry Pi whose NTP server had stopped answering. The Bot API rides
// on HTTPS, which tolerates the same skew without noticing, so the bot kept
// working and the drift looked like a Telegram-side outage.
//
// Telegram's own protocol says a client should take its time from the server
// rather than insist on its own. This does that before the first connection:
// the Bot API answers over HTTPS with a Date header, which is the same
// infrastructure and needs no NTP, no extra dependency and no open UDP port.

const (
	// clockProbeTimeout bounds the measurement. A server that does not answer
	// quickly is not worth waiting for: the system clock is the fallback and
	// is usually right.
	clockProbeTimeout = 5 * time.Second
	// clockSkewThreshold is how far off the host has to be before its clock is
	// corrected. Below it the difference cannot break MTProto, and adopting a
	// second-resolution measurement would add noise rather than remove it.
	clockSkewThreshold = 5 * time.Second
)

// offsetClock is the system clock shifted by a fixed amount. Only Now moves:
// timers and tickers measure durations, which a wrong wall clock does not
// affect.
type offsetClock struct {
	offset time.Duration
}

func (c offsetClock) Now() time.Time                    { return time.Now().Add(c.offset) }
func (c offsetClock) Timer(d time.Duration) clock.Timer { return clock.System.Timer(d) }
func (c offsetClock) Ticker(d time.Duration) clock.Ticker {
	return clock.System.Ticker(d)
}

// resolveClock returns the clock the MTProto client should use, and says in the
// log when it is not the host's own.
//
// A failed measurement is not an error: it leaves the system clock in place,
// which is what the server used before this existed.
func resolveClock(ctx context.Context, apiBase string) clock.Clock {
	offset, err := measureClockOffset(ctx, apiBase)
	if err != nil {
		slog.Debug("could not check the host clock against Telegram", "error", err)
		return clock.System
	}
	if offset < clockSkewThreshold && offset > -clockSkewThreshold {
		return clock.System
	}

	slog.Warn("this host's clock is wrong; correcting it for Telegram only",
		"offset", offset.Round(time.Second),
		"note", "MTProto rejects messages more than 30s out; fix the host's time sync")
	return offsetClock{offset: offset}
}

// measureClockOffset asks Telegram what time it is over HTTPS and reports how
// far this host is from that answer. A positive offset means the host is
// behind.
func measureClockOffset(ctx context.Context, apiBase string) (time.Duration, error) {
	if apiBase == "" {
		apiBase = DefaultAPIBase
	}
	ctx, cancel := context.WithTimeout(ctx, clockProbeTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodHead, apiBase+"/", nil)
	if err != nil {
		return 0, err
	}

	before := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, err
	}
	after := time.Now()
	_ = response.Body.Close()

	served, err := http.ParseTime(response.Header.Get("Date"))
	if err != nil {
		return 0, err
	}

	// The header is whole seconds and was written somewhere inside the round
	// trip. Compare it against the middle of that trip, and add half a second
	// for the truncation, so a host that is actually right measures near zero.
	middle := before.Add(after.Sub(before) / 2)
	return served.Add(500 * time.Millisecond).Sub(middle), nil
}
