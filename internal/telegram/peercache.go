package telegram

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

// MTProto does not address a peer by id alone: every user and channel also has
// an access hash, and a request carrying the wrong one is refused. gotd keeps
// those hashes in a store that starts empty in each process, so an id a caller
// got from somewhere else -- a chat list printed in an earlier session, a
// message a person pasted -- resolves to nothing until this process has seen
// the peer itself.
//
// That is why reading a bot conversation failed while ordinary groups worked:
// a basic group is addressed by bare id, a user or a bot is not. The fix is to
// sweep the account's own dialogs and contacts into the store, and to do it
// again on a miss, because a chat that appeared after the sweep is exactly the
// one a caller is most likely to ask about.

const (
	// peerSweepLimit is how many dialogs one sweep pulls in. It covers an
	// ordinary account's whole chat list in a single call.
	peerSweepLimit = 200
	// peerSweepInterval is the shortest gap between sweeps. Without it, asking
	// repeatedly for an id that does not exist would re-fetch the chat list
	// every time.
	peerSweepInterval = 15 * time.Second
)

// rememberPeers hands entities a call already returned to the peer store, so
// the ids in its result can be used in the next call. A failure here costs a
// later lookup, never the call in hand, so it is logged rather than returned.
func (u *UserBackend) rememberPeers(ctx context.Context, users []tg.UserClass, chats []tg.ChatClass) {
	if u.peers == nil || (len(users) == 0 && len(chats) == 0) {
		return
	}
	if err := u.peers.Apply(ctx, users, chats); err != nil {
		slog.Debug("could not cache Telegram peers", "error", err)
	}
}

// sweepPeers fills the peer store from the account's own dialogs and contacts.
// It is rate-limited: a caller that asks for an id nobody has should not make
// this fetch the whole chat list on every attempt.
func (u *UserBackend) sweepPeers(ctx context.Context) {
	api, err := u.ensure()
	if err != nil {
		return
	}

	u.sweepMu.Lock()
	defer u.sweepMu.Unlock()
	if !u.sweptAt.IsZero() && time.Since(u.sweptAt) < peerSweepInterval {
		return
	}
	u.sweptAt = time.Now()

	if dialogs, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      peerSweepLimit,
	}); err == nil {
		switch typed := dialogs.(type) {
		case *tg.MessagesDialogs:
			u.rememberPeers(ctx, typed.Users, typed.Chats)
		case *tg.MessagesDialogsSlice:
			u.rememberPeers(ctx, typed.Users, typed.Chats)
		}
	} else {
		slog.Debug("could not sweep dialogs into the peer cache", "error", err)
	}

	// Contacts cover the people a caller names who have no open conversation.
	if contacts, err := api.ContactsGetContacts(ctx, 0); err == nil {
		if typed, ok := contacts.(*tg.ContactsContacts); ok {
			u.rememberPeers(ctx, typed.Users, nil)
		}
	}
}

// sweepState is the bookkeeping sweepPeers needs. It lives here rather than on
// UserBackend's main declaration so the whole mechanism reads in one file.
type sweepState struct {
	sweepMu sync.Mutex
	sweptAt time.Time
}
