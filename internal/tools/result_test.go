package tools

import (
	"strings"
	"testing"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// A refusal the telegram package finished writing must reach the caller as
// written. The catch-all exists to keep a dependency's internals out of the
// result, not to overwrite this server's own diagnosis -- and it did exactly
// that on the live server: an unknown chat id came back as "*telegram.RPCError:
// Operation failed. Check server logs for details."
func TestASanitisedRefusalReachesTheCaller(t *testing.T) {
	message := result(t, SafeError(&telegram.RPCError{
		Type:   "PEER_NOT_FOUND",
		Code:   400,
		Advice: "Get a current id from chat(action=\"list\").",
	}))
	if !strings.Contains(message, "PEER_NOT_FOUND") {
		t.Errorf("the name should survive: %s", message)
	}
	if strings.Contains(message, "Operation failed") {
		t.Errorf("the catch-all should not have claimed it: %s", message)
	}
}
