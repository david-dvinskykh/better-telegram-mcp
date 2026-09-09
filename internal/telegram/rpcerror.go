package telegram

import (
	"errors"
	"fmt"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tgerr"
)

// RPCError is a refusal from Telegram itself, kept as a distinct type so the
// tool layer passes its text through.
//
// It exists because the alternative was worse than unhelpful: an MTProto
// failure used to reach the caller as "Operation failed. Check server logs for
// details.", which is what turned a missing access hash into an afternoon of
// guessing. Telegram's own error names -- PEER_ID_INVALID, CHAT_ADMIN_REQUIRED,
// FLOOD_WAIT_30 -- are written for API clients and say nothing about this
// server's internals, so they are safe to show and are the one thing that makes
// the failure actionable.
type RPCError struct {
	// Type is Telegram's short name for the failure, e.g. "PEER_ID_INVALID".
	Type string
	// Code is its HTTP-like class: 400 bad request, 403 forbidden, 420 flood.
	Code int
	// Advice is what the caller can do about it, when there is something.
	Advice string
}

func (e *RPCError) Error() string {
	if e.Advice == "" {
		return fmt.Sprintf("Telegram refused the request: %s (%d).", e.Type, e.Code)
	}
	return fmt.Sprintf("Telegram refused the request: %s (%d). %s", e.Type, e.Code, e.Advice)
}

// AsRPCError renders a Telegram refusal, or reports that the error was not one.
// A peer the client could not resolve is folded in here too: gotd raises its
// own type for that, and to a caller it is the same problem.
func AsRPCError(err error) (*RPCError, bool) {
	var notFound *peers.PeerNotFoundError
	if errors.As(err, &notFound) {
		return &RPCError{
			Type: "PEER_NOT_FOUND",
			Code: 400,
			Advice: "This account cannot see that chat. Get a current id from " +
				"chat(action=\"list\"), or pass a @username or a saved alias.",
		}, true
	}

	rpc, ok := tgerr.As(err)
	if !ok {
		return nil, false
	}
	return &RPCError{Type: rpc.Type, Code: rpc.Code, Advice: rpcAdvice(rpc)}, true
}

// rpcAdvice turns the failures a caller actually hits into a next step. Anything
// unlisted still reaches them by name, which is enough to look up.
func rpcAdvice(rpc *tgerr.Error) string {
	switch {
	case rpc.IsOneOf("PEER_ID_INVALID", "USER_ID_INVALID", "CHANNEL_INVALID", "CHAT_ID_INVALID"):
		return "This account cannot see that chat, or the id belongs to a different " +
			"account. Get a current id from chat(action=\"list\"), or pass a @username " +
			"or a saved alias."
	case rpc.IsType("FLOOD_WAIT"):
		return fmt.Sprintf("Telegram is rate-limiting this account; retry in %d seconds.",
			rpc.Argument)
	case rpc.IsOneOf("AUTH_KEY_UNREGISTERED", "SESSION_REVOKED", "USER_DEACTIVATED"):
		return "The session is no longer signed in. Run " +
			"`better-telegram-mcp auth --phone <+number>` to sign in again."
	case rpc.IsOneOf("CHAT_ADMIN_REQUIRED", "CHAT_WRITE_FORBIDDEN", "USER_BANNED_IN_CHANNEL"):
		return "This account does not have the rights in that chat to do it."
	case rpc.IsOneOf("MESSAGE_ID_INVALID", "MESSAGE_DELETE_FORBIDDEN"):
		return "That message id does not exist in this chat, or is too old to change. " +
			"Read it back with message(action=\"history\") first."
	case rpc.IsType("USERNAME_NOT_OCCUPIED"):
		return "Nobody holds that username."
	case rpc.IsOneOf("USER_PRIVACY_RESTRICTED", "YOU_BLOCKED_USER", "USER_IS_BLOCKED"):
		return "That account's privacy settings do not allow it."
	case rpc.IsType("PREMIUM_ACCOUNT_REQUIRED"):
		return "Telegram Premium is required for that."
	case rpc.Code == 403:
		return "This account is not allowed to do that."
	default:
		return ""
	}
}
