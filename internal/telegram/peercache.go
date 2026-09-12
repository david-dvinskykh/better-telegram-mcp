package telegram

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
)

// MTProto does not address a peer by id alone: every user and channel also has
// an access hash, and a request carrying the wrong one is refused. gotd keeps
// those hashes in a store that starts empty in each process, so an id a caller
// got from somewhere else -- a chat list printed in an earlier session, a
// folder's own contents, a message a person pasted -- resolves to nothing until
// this process has seen the peer itself.
//
// That is why reading a bot conversation failed while ordinary groups worked:
// a basic group is addressed by bare id, a user or a bot is not. The fix is to
// sweep the account's own peers into this process and to do it again on a miss,
// because a chat that appeared after the sweep is exactly the one a caller is
// most likely to ask about.
//
// A sweep has to cover more than the head of the main chat list. An account
// with hundreds of conversations keeps the older ones past any single page, and
// the archive is a separate list that `messages.getDialogs` does not return at
// all -- yet a folder happily names chats from both. Four chats in one folder
// were unreadable for exactly that reason: their ids were real, their
// conversations were simply too far down the list for one 200-dialog fetch to
// reach. So the sweep now walks the main list and the archive in pages, reads
// the folders (which carry an access hash for every chat in them, in one
// request), and stops the moment the id being resolved turns up.

const (
	// peerSweepLimit is how many dialogs one page of a sweep pulls in.
	peerSweepLimit = 100
	// peerSweepPages bounds the walk. An account with thousands of
	// conversations must not turn one unknown id into an unbounded crawl of
	// the chat list, which Telegram would answer with a flood wait.
	peerSweepPages = 10
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

// rememberPeerRef indexes an input peer under the Bot-API-shaped id a caller
// hands back, so that id can be addressed without another lookup.
func (u *UserBackend) rememberPeerRef(peer tg.InputPeerClass) {
	id := peerRef(peer)
	if id == 0 {
		return
	}
	u.peerMu.Lock()
	defer u.peerMu.Unlock()
	if u.knownPeers == nil {
		u.knownPeers = map[int64]tg.InputPeerClass{}
	}
	u.knownPeers[id] = peer
}

// knownPeer returns the input peer a sweep learned for a Bot-API-shaped id.
func (u *UserBackend) knownPeer(id int64) (tg.InputPeerClass, bool) {
	u.peerMu.Lock()
	defer u.peerMu.Unlock()
	peer, ok := u.knownPeers[id]
	return peer, ok
}

// indexed reports whether the id a sweep is after is now known. A zero id means
// the caller wanted a general sweep, which nothing short-circuits.
func (u *UserBackend) indexed(id int64) bool {
	if id == 0 {
		return false
	}
	_, ok := u.knownPeer(id)
	return ok
}

// forgetPeers drops everything the sweeps learned, so config(action =
// "cache_clear") clears this index along with the peer manager's own store.
func (u *UserBackend) forgetPeers() {
	u.peerMu.Lock()
	u.knownPeers = nil
	u.peerMu.Unlock()

	u.sweepMu.Lock()
	u.sweptAt = time.Time{}
	u.sweepMu.Unlock()
}

// sweepPeers teaches this process about peers it has not seen yet. want is the
// Bot-API-shaped id the caller is after: the sweep stops as soon as that id
// turns up, so the ordinary case costs one or two requests and the whole chat
// list is walked only when the answer really is at the end of it. A zero want
// sweeps the cheap sources and returns.
//
// It is rate-limited: a caller that asks for an id nobody has should not make
// this fetch the chat list on every attempt.
func (u *UserBackend) sweepPeers(ctx context.Context, want int64) {
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

	// Folders come first because they are the cheapest and the most likely to
	// hold the answer: one request returns an input peer, access hash
	// included, for every chat a person has filed by hand.
	if filters, err := u.dialogFilters(ctx); err == nil {
		u.rememberFilterPeers(filters)
	} else {
		slog.Debug("could not sweep folders into the peer cache", "error", err)
	}
	if u.indexed(want) {
		return
	}

	// Contacts cover the people a caller names who have no open conversation.
	u.sweepContacts(ctx, api)
	if u.indexed(want) {
		return
	}

	if u.sweepDialogs(ctx, api, 0, want) {
		return
	}
	u.sweepDialogs(ctx, api, archiveFolder, want)
}

// sweepContacts indexes the account's contacts and hands them to the peer
// manager, which needs them for phone-number lookups.
func (u *UserBackend) sweepContacts(ctx context.Context, api *tg.Client) {
	contacts, err := api.ContactsGetContacts(ctx, 0)
	if err != nil {
		slog.Debug("could not sweep contacts into the peer cache", "error", err)
		return
	}
	typed, ok := contacts.(*tg.ContactsContacts)
	if !ok {
		return
	}
	u.rememberPeers(ctx, typed.Users, nil)
	for _, item := range typed.Users {
		user, ok := item.(*tg.User)
		if !ok || user.Min {
			continue
		}
		u.rememberPeerRef(&tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash})
	}
}

// sweepDialogs walks one chat list -- folder 0 is the ordinary one, folder 1
// the archive -- in pages, indexing each dialog's peer. It reports whether the
// id being resolved has been found, so the caller can stop.
func (u *UserBackend) sweepDialogs(ctx context.Context, api *tg.Client, folder int, want int64) bool {
	iter := dialogs.NewQueryBuilder(api).GetDialogs().
		FolderID(folder).
		BatchSize(peerSweepLimit).
		Iter()
	for seen := 0; seen < peerSweepPages*peerSweepLimit; seen++ {
		if !iter.Next(ctx) {
			if err := iter.Err(); err != nil {
				slog.Debug("could not sweep dialogs into the peer cache",
					"folder", folder, "error", err)
			}
			break
		}
		u.rememberPeerRef(iter.Value().Peer)
		if u.indexed(want) {
			return true
		}
	}
	return u.indexed(want)
}

// sweepState is the bookkeeping the sweeps need. It lives here rather than on
// UserBackend's main declaration so the whole mechanism reads in one file.
type sweepState struct {
	sweepMu sync.Mutex
	sweptAt time.Time

	// knownPeers maps a Bot-API-shaped id to the input peer that can address
	// it. It is separate from the peer manager's store because the cheapest
	// source of an access hash -- a folder's contents -- arrives as input
	// peers, which that store has no way to accept.
	peerMu     sync.Mutex
	knownPeers map[int64]tg.InputPeerClass
}
