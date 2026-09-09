package telegram

import (
	"context"
	"errors"
)

// A bot has no address book: contacts belong to a person's account. The one
// thing it can answer is who it has blocked, and the Bot API does not expose
// that either, so this whole capability reports the mode that serves it.

func (b *BotBackend) DeleteContact(context.Context, int64) (bool, error) {
	return false, NeedsUser()
}

func (b *BotBackend) ImportContacts(context.Context, []ContactEntry) (Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) ExportContacts(context.Context) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) ListBlocked(context.Context, int) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) LastInteraction(context.Context, int64) (Map, error) {
	return nil, NeedsUser()
}

// SendContactCard shares a contact card. A bot can send one, but it has no way
// to look up the number behind a user id, so the caller has to supply it --
// which the tool surface does not carry, on purpose.
func (b *BotBackend) SendContactCard(context.Context, any, int64) (Map, error) {
	return nil, errors.New(
		"a bot cannot read a person's phone number, and a contact card needs " +
			"one; use user mode")
}

func (b *BotBackend) DirectChat(context.Context, string) (Map, error) { return nil, NeedsUser() }
