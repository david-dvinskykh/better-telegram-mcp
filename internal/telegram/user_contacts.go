package telegram

import (
	"context"
	"errors"
	"strings"

	"github.com/gotd/td/tg"
)

// DeleteContact removes someone from the address book. The conversation with
// them stays; only the contact entry goes.
func (u *UserBackend) DeleteContact(ctx context.Context, userID int64) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	user, err := u.inputUser(ctx, userID)
	if err != nil {
		return false, err
	}
	if _, err := api.ContactsDeleteContacts(ctx, []tg.InputUserClass{user}); err != nil {
		return false, err
	}
	return true, nil
}

// ImportContacts adds several people at once and reports which phone numbers
// had no Telegram account behind them.
func (u *UserBackend) ImportContacts(ctx context.Context, entries []ContactEntry) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("'import' requires contacts")
	}

	input := make([]tg.InputPhoneContact, 0, len(entries))
	for index, entry := range entries {
		if entry.Phone == "" || entry.FirstName == "" {
			return nil, errors.New("every imported contact needs phone and first_name")
		}
		input = append(input, tg.InputPhoneContact{
			// ClientID only has to be unique within this request; Telegram
			// echoes it back to say which rows were retried.
			ClientID:  int64(index),
			Phone:     entry.Phone,
			FirstName: entry.FirstName,
			LastName:  entry.LastName,
		})
	}

	imported, err := api.ContactsImportContacts(ctx, input)
	if err != nil {
		return nil, err
	}
	failed := []string{}
	for _, retry := range imported.RetryContacts {
		if int(retry) < len(entries) {
			failed = append(failed, entries[retry].Phone)
		}
	}
	return Map{
		"imported":        len(imported.Imported),
		"users":           serializeUsers(imported.Users),
		"not_on_telegram": failed,
	}, nil
}

// ExportContacts returns the whole address book, which is what a caller backs
// up or diffs against another source.
func (u *UserBackend) ExportContacts(ctx context.Context) ([]Map, error) {
	return u.ListContacts(ctx)
}

// ListBlocked returns the accounts this one has blocked.
func (u *UserBackend) ListBlocked(ctx context.Context, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	result, err := api.ContactsGetBlocked(ctx, &tg.ContactsGetBlockedRequest{
		Limit: limitOr(limit, 50, 100),
	})
	if err != nil {
		return nil, err
	}
	switch typed := result.(type) {
	case *tg.ContactsBlocked:
		return serializeUsers(typed.Users), nil
	case *tg.ContactsBlockedSlice:
		return serializeUsers(typed.Users), nil
	default:
		return []Map{}, nil
	}
}

// LastInteraction reports when someone was last seen and when the conversation
// with them last moved, which together answer "is this contact still live".
func (u *UserBackend) LastInteraction(ctx context.Context, userID int64) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, userID)
	if err != nil {
		return nil, err
	}

	target, err := u.inputUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := Map{"user_id": userID}
	users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{target})
	if err != nil {
		return nil, err
	}
	for _, item := range users {
		if user, ok := item.(*tg.User); ok {
			out["name"] = strings.TrimSpace(user.FirstName + " " + user.LastName)
			out["status"] = userStatus(user.Status)
		}
	}

	history, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  peer,
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	messages := serializeMessages(history)
	if len(messages) == 0 {
		out["last_message"] = nil
		return out, nil
	}
	out["last_message"] = messages[0]
	return out, nil
}

// SendContactCard shares someone's contact card into a chat.
func (u *UserBackend) SendContactCard(ctx context.Context, chatID any, userID int64) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	target, err := u.inputUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{target})
	if err != nil {
		return nil, err
	}
	var card *tg.User
	for _, item := range users {
		if user, ok := item.(*tg.User); ok {
			card = user
		}
	}
	if card == nil {
		return nil, errors.New("Telegram returned no account for that user id.")
	}
	if card.Phone == "" {
		return nil, errors.New(
			"that account's phone number is not visible to you, and a contact " +
				"card cannot be shared without one")
	}

	updates, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaContact{
			PhoneNumber: card.Phone,
			FirstName:   card.FirstName,
			LastName:    card.LastName,
		},
		RandomID: randomID(),
	})
	if err != nil {
		return nil, err
	}
	return firstMessage(updates), nil
}

// DirectChat finds the one-to-one conversation with a person, so a caller
// holding only a name ends up with the id every other action takes.
func (u *UserBackend) DirectChat(ctx context.Context, query string) (Map, error) {
	if _, err := u.ensure(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("'direct' requires query")
	}
	peer, err := u.resolvePeer(ctx, query)
	if err != nil {
		return nil, err
	}
	user, ok := peer.(*tg.InputPeerUser)
	if !ok {
		return nil, errors.New("that reference is a group or channel, not a person")
	}
	return u.GetUserInfo(ctx, user.UserID)
}
