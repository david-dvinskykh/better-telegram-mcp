package telegram

import (
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// A folder is the one place a person files chats by hand, so the ids it hands
// back are the ones a caller reads next. Telegram returns those as input peers
// with their access hashes, and keeping them is what makes the chats
// addressable -- four chats in one folder were unreadable because these hashes
// were thrown away and the conversations sat too far down the chat list for a
// bounded sweep of the dialogs to find them again.
func TestAFoldersChatsBecomeAddressable(t *testing.T) {
	backend := &UserBackend{}
	backend.rememberFilterPeers([]*tg.DialogFilter{{
		ID: 172,
		IncludePeers: []tg.InputPeerClass{
			&tg.InputPeerUser{UserID: 5646607724, AccessHash: 11},
			&tg.InputPeerChannel{ChannelID: 1755704848, AccessHash: 22},
		},
		PinnedPeers: []tg.InputPeerClass{
			&tg.InputPeerChat{ChatID: 4871258071},
		},
		ExcludePeers: []tg.InputPeerClass{
			&tg.InputPeerUser{UserID: 446000837, AccessHash: 33},
		},
	}})

	// The keys are the Bot-API-shaped ids GetFolder reports, so an id read out
	// of a folder can be passed straight back to any other action.
	for _, id := range []int64{5646607724, -1001755704848, -4871258071, 446000837} {
		if _, ok := backend.knownPeer(id); !ok {
			t.Errorf("id %d should be addressable after reading the folder", id)
		}
	}

	peer, _ := backend.knownPeer(5646607724)
	user, ok := peer.(*tg.InputPeerUser)
	if !ok || user.AccessHash != 11 {
		t.Errorf("the access hash should survive: %#v", peer)
	}
}

// A peer with no id of its own -- an empty one arriving from a dialog the
// server could not name -- must not take the zero slot, or every later lookup
// of an unknown id would answer with it.
func TestAnEmptyPeerIsNotIndexed(t *testing.T) {
	backend := &UserBackend{}
	backend.rememberPeerRef(&tg.InputPeerEmpty{})
	if _, ok := backend.knownPeer(0); ok {
		t.Error("an empty peer should not be indexed")
	}
}

// A sweep short-circuits as soon as the id being resolved turns up, and a
// general sweep (no id) must never think it is done.
func TestASweepOnlyStopsForTheIdItWasGiven(t *testing.T) {
	backend := &UserBackend{}
	backend.rememberPeerRef(&tg.InputPeerUser{UserID: 42, AccessHash: 7})
	if !backend.indexed(42) {
		t.Error("a known id should stop the sweep")
	}
	if backend.indexed(43) {
		t.Error("an unknown id should not stop the sweep")
	}
	if backend.indexed(0) {
		t.Error("a general sweep should walk every source")
	}
}

// cache_clear has to drop this index too: it exists so a caller can force a
// re-read after moving chats around, and a stale access hash is exactly what
// they are clearing.
func TestClearingTheCacheForgetsTheIndexAndTheSweepClock(t *testing.T) {
	backend := &UserBackend{}
	backend.rememberPeerRef(&tg.InputPeerUser{UserID: 42, AccessHash: 7})
	backend.forgetPeers()
	if _, ok := backend.knownPeer(42); ok {
		t.Error("the index should be empty after a cache clear")
	}
	if !backend.sweptAt.IsZero() {
		t.Error("the next sweep should not be rate-limited by the last one")
	}
}

// An id nothing can name used to reach the caller as "Operation failed. Check
// server logs for details.", because gotd reports it as a plain error rather
// than a Telegram refusal. The name and the id are what make it diagnosable.
func TestAnUnresolvableIdSaysSo(t *testing.T) {
	err := unresolvedPeer(5646607724, errors.New("got empty user"))
	message := err.Error()
	if !strings.Contains(message, "PEER_NOT_FOUND") {
		t.Errorf("the failure should be named: %s", message)
	}
	if !strings.Contains(message, "5646607724") {
		t.Errorf("the id should be in the message: %s", message)
	}
	if strings.Contains(message, "Operation failed") {
		t.Errorf("the useless sentence should be gone: %s", message)
	}
}

// When Telegram itself refused, its own answer is better than anything this
// server could write over it.
func TestATelegramRefusalSurvivesResolution(t *testing.T) {
	err := unresolvedPeer(42, tgerr.New(420, "FLOOD_WAIT_30"))
	if !strings.Contains(err.Error(), "FLOOD_WAIT") {
		t.Errorf("Telegram's own refusal should reach the caller: %s", err.Error())
	}
}
