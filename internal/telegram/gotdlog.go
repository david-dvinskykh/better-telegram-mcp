package telegram

import (
	"context"
	"log/slog"
	"sync"

	gotdlog "github.com/gotd/log"
)

// connectionLog is the bridge between gotd's logging port and this server's
// slog handler.
//
// gotd is where the truth about a connection lives: it names the datacenter it
// dialled, the transport error it got back, and the fact that it is about to
// try again. Without a logger it says none of that, and an MTProto outage
// reaches the operator as a bare "timed out connecting to Telegram" -- which is
// the shape this server was in when a live one had to be diagnosed by reading
// packet captures.
//
// It also keeps the last error gotd reported, because gotd restarts a dead
// connection forever rather than returning: the reason for the restart is the
// only description of the failure anyone will ever get.
type connectionLog struct {
	mu   sync.Mutex
	last error
}

// Enabled keeps debug records out. They are per-request and would drown the
// stderr stream a supervisor collects; info and above carry the connection
// lifecycle, which is what an outage needs.
func (c *connectionLog) Enabled(_ context.Context, level gotdlog.Level) bool {
	return level >= gotdlog.LevelInfo
}

func (c *connectionLog) Log(
	_ context.Context, level gotdlog.Level, msg string, attrs ...gotdlog.Attr,
) {
	fields := make([]any, 0, len(attrs)*2)
	for _, attr := range attrs {
		if attr.Value.Kind() == gotdlog.KindError {
			if err := attr.Value.Error(); err != nil && attr.Key == "error" {
				c.remember(err)
			}
		}
		fields = append(fields, attr.Key, attr.Value.String())
	}
	slog.Log(context.Background(), slogLevel(level), "telegram: "+msg, fields...)
}

func (c *connectionLog) remember(err error) {
	c.mu.Lock()
	c.last = err
	c.mu.Unlock()
}

// lastError is why the connection last failed, or nil if it never has.
func (c *connectionLog) lastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func slogLevel(level gotdlog.Level) slog.Level {
	switch {
	case level >= gotdlog.LevelError:
		return slog.LevelError
	case level >= gotdlog.LevelWarn:
		return slog.LevelWarn
	case level >= gotdlog.LevelInfo:
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}
