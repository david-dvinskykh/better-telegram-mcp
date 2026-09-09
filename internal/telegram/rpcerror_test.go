package telegram

import (
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// A refusal from Telegram has to reach the caller by name. Reporting every one
// of them as "Operation failed" is what turned a missing access hash into a
// problem that had to be diagnosed from the outside.
func TestTelegramRefusalsKeepTheirName(t *testing.T) {
	rpc, ok := AsRPCError(tgerr.New(400, "PEER_ID_INVALID"))
	if !ok {
		t.Fatal("a Telegram RPC error should be recognised")
	}
	if rpc.Type != "PEER_ID_INVALID" || rpc.Code != 400 {
		t.Errorf("unexpected error: %#v", rpc)
	}
	if !strings.Contains(rpc.Error(), "PEER_ID_INVALID") {
		t.Errorf("the name should survive into the text: %s", rpc.Error())
	}
	if !strings.Contains(rpc.Error(), "chat(action=\"list\")") {
		t.Errorf("the advice should name the way out: %s", rpc.Error())
	}
}

// A rate limit is the one refusal where the number matters: it says how long to
// wait, and a caller that cannot read it will just retry into the same wall.
func TestAFloodWaitReportsTheDelay(t *testing.T) {
	rpc, ok := AsRPCError(tgerr.New(420, "FLOOD_WAIT_30"))
	if !ok {
		t.Fatal("a flood wait should be recognised")
	}
	if !strings.Contains(rpc.Error(), "30 seconds") {
		t.Errorf("the wait should be in the message: %s", rpc.Error())
	}
}

// gotd raises its own type when it cannot resolve a peer; to a caller that is
// the same problem as Telegram refusing the id.
func TestAnUnresolvablePeerReadsLikeARefusal(t *testing.T) {
	err := &peers.PeerNotFoundError{Peer: &tg.PeerUser{UserID: 1}}
	rpc, ok := AsRPCError(err)
	if !ok {
		t.Fatal("an unresolved peer should be recognised")
	}
	if rpc.Type != "PEER_NOT_FOUND" {
		t.Errorf("unexpected type %q", rpc.Type)
	}
}

func TestAnOrdinaryErrorIsNotMistakenForARefusal(t *testing.T) {
	if _, ok := AsRPCError(errors.New("dial tcp: connection refused")); ok {
		t.Error("a transport failure is not a Telegram refusal")
	}
}

// A signed-out session is worth naming on its own: the way out is a command,
// not a retry.
func TestARevokedSessionSaysHowToSignInAgain(t *testing.T) {
	rpc, ok := AsRPCError(tgerr.New(401, "AUTH_KEY_UNREGISTERED"))
	if !ok {
		t.Fatal("expected a recognised refusal")
	}
	if !strings.Contains(rpc.Error(), "auth --phone") {
		t.Errorf("the advice should name the command: %s", rpc.Error())
	}
}
