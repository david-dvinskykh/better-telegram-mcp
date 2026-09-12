package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/tg"
)

// Chat folders are MTProto "dialog filters": a named subset of the chat list,
// with its own tab in every Telegram client. Only a user account has them --
// there is no Bot API equivalent -- and the whole set is rewritten on each
// edit, which is why the create/add/remove calls all read the current list
// first.

// ListFolders returns every folder on the account, in the order the tabs appear.
func (u *UserBackend) ListFolders(ctx context.Context) ([]Map, error) {
	filters, err := u.dialogFilters(ctx)
	if err != nil {
		return nil, err
	}
	out := []Map{}
	for _, filter := range filters {
		out = append(out, serializeFolder(filter))
	}
	return out, nil
}

// GetFolder returns one folder with the chats it holds.
func (u *UserBackend) GetFolder(ctx context.Context, id int) (Map, error) {
	filter, err := u.findFolder(ctx, id)
	if err != nil {
		return nil, err
	}
	out := serializeFolder(filter)
	out["include_peers"] = peerRefs(filter.IncludePeers)
	out["exclude_peers"] = peerRefs(filter.ExcludePeers)
	out["pinned_peers"] = peerRefs(filter.PinnedPeers)
	return out, nil
}

// CreateFolder adds a folder holding the given chats.
func (u *UserBackend) CreateFolder(ctx context.Context, title string, chats []any) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if title == "" {
		return nil, errors.New("'create' requires title")
	}

	existing, err := u.dialogFilters(ctx)
	if err != nil {
		return nil, err
	}
	id, err := nextFolderID(existing)
	if err != nil {
		return nil, err
	}

	peers := make([]tg.InputPeerClass, 0, len(chats))
	for _, chat := range chats {
		peer, err := u.resolvePeer(ctx, chat)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}

	filter := &tg.DialogFilter{
		ID:           id,
		Title:        tg.TextWithEntities{Text: title},
		IncludePeers: peers,
	}
	request := &tg.MessagesUpdateDialogFilterRequest{ID: id}
	request.SetFilter(filter)
	if _, err := api.MessagesUpdateDialogFilter(ctx, request); err != nil {
		return nil, err
	}
	return serializeFolder(filter), nil
}

// DeleteFolder removes a folder. The chats in it are untouched: a folder is a
// view of the chat list, not a container.
func (u *UserBackend) DeleteFolder(ctx context.Context, id int) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	if _, err := u.findFolder(ctx, id); err != nil {
		return false, err
	}
	// Updating a filter id with no filter attached is how MTProto spells
	// "delete this one".
	return api.MessagesUpdateDialogFilter(ctx, &tg.MessagesUpdateDialogFilterRequest{ID: id})
}

// SetFolderChat adds a chat to a folder, or takes it out again.
func (u *UserBackend) SetFolderChat(ctx context.Context, id int, chat any, remove bool) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	filter, err := u.findFolder(ctx, id)
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chat)
	if err != nil {
		return nil, err
	}

	target := peerRef(peer)
	kept := make([]tg.InputPeerClass, 0, len(filter.IncludePeers)+1)
	found := false
	for _, existing := range filter.IncludePeers {
		if peerRef(existing) == target {
			found = true
			if remove {
				continue
			}
		}
		kept = append(kept, existing)
	}
	switch {
	case remove && !found:
		return nil, fmt.Errorf("that chat is not in folder %d", id)
	case !remove && found:
		return nil, fmt.Errorf("that chat is already in folder %d", id)
	case !remove:
		kept = append(kept, peer)
	}
	filter.IncludePeers = kept

	request := &tg.MessagesUpdateDialogFilterRequest{ID: id}
	request.SetFilter(filter)
	if _, err := api.MessagesUpdateDialogFilter(ctx, request); err != nil {
		return nil, err
	}
	out := serializeFolder(filter)
	out["include_peers"] = peerRefs(filter.IncludePeers)
	return out, nil
}

// ReorderFolders sets the order the tabs appear in. Ids left out keep their
// relative order after the ones named.
func (u *UserBackend) ReorderFolders(ctx context.Context, order []int) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	if len(order) == 0 {
		return false, errors.New("'reorder' requires folder_ids")
	}

	filters, err := u.dialogFilters(ctx)
	if err != nil {
		return false, err
	}
	known := map[int]bool{}
	for _, filter := range filters {
		known[filter.ID] = true
	}

	seen := map[int]bool{}
	final := make([]int, 0, len(filters))
	for _, id := range order {
		if !known[id] {
			return false, fmt.Errorf("no folder has id %d", id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		final = append(final, id)
	}
	for _, filter := range filters {
		if !seen[filter.ID] {
			final = append(final, filter.ID)
		}
	}
	return api.MessagesUpdateDialogFiltersOrder(ctx, final)
}

// --- helpers ---

// dialogFilters returns the editable folders. The "all chats" pseudo-folder and
// the suggested-but-not-created ones are skipped: neither has an id a caller
// can act on.
func (u *UserBackend) dialogFilters(ctx context.Context) ([]*tg.DialogFilter, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, err
	}
	out := []*tg.DialogFilter{}
	for _, item := range result.Filters {
		if filter, ok := item.(*tg.DialogFilter); ok {
			out = append(out, filter)
		}
	}
	// Telegram answers with input peers, access hashes included, so reading the
	// folders is also the cheapest way to learn how to address the chats in
	// them -- including the ones too far down the chat list for a bounded walk
	// of the dialogs to reach.
	u.rememberFilterPeers(out)
	return out, nil
}

// rememberFilterPeers indexes every chat the folders name, whichever list it
// sits in. The ids GetFolder hands back come from exactly these peers, so a
// caller that reads a folder and then reads one of its chats must not be told
// the id does not exist.
func (u *UserBackend) rememberFilterPeers(filters []*tg.DialogFilter) {
	for _, filter := range filters {
		for _, group := range [][]tg.InputPeerClass{
			filter.PinnedPeers, filter.IncludePeers, filter.ExcludePeers,
		} {
			for _, peer := range group {
				u.rememberPeerRef(peer)
			}
		}
	}
}

func (u *UserBackend) findFolder(ctx context.Context, id int) (*tg.DialogFilter, error) {
	filters, err := u.dialogFilters(ctx)
	if err != nil {
		return nil, err
	}
	for _, filter := range filters {
		if filter.ID == id {
			return filter, nil
		}
	}
	return nil, fmt.Errorf("no folder has id %d", id)
}

// nextFolderID picks a free id. Telegram reserves everything below 2 for the
// built-in tabs, so user folders start there.
func nextFolderID(existing []*tg.DialogFilter) (int, error) {
	used := map[int]bool{}
	for _, filter := range existing {
		used[filter.ID] = true
	}
	for id := 2; id < 256; id++ {
		if !used[id] {
			return id, nil
		}
	}
	return 0, errors.New("this account has no free folder slots left")
}

func serializeFolder(filter *tg.DialogFilter) Map {
	return Map{
		"id":       filter.ID,
		"title":    filter.Title.Text,
		"emoticon": emptyToNil(filter.Emoticon),
		"chats":    len(filter.IncludePeers),
		"pinned":   len(filter.PinnedPeers),
		"excluded": len(filter.ExcludePeers),
	}
}

// peerRef renders an input peer as the id a caller can hand back to any other
// action.
func peerRef(peer tg.InputPeerClass) int64 {
	switch typed := peer.(type) {
	case *tg.InputPeerUser:
		return typed.UserID
	case *tg.InputPeerChat:
		return -typed.ChatID
	case *tg.InputPeerChannel:
		return -1_000_000_000_000 - typed.ChannelID
	default:
		return 0
	}
}

func peerRefs(peers []tg.InputPeerClass) []int64 {
	out := make([]int64, 0, len(peers))
	for _, peer := range peers {
		out = append(out, peerRef(peer))
	}
	return out
}
