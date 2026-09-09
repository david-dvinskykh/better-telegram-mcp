package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The Bot API serves part of the extended message surface and none of the rest.
// Implementing the whole interface here -- with a mode error where the Bot API
// has nothing -- is what lets the dispatch layer call it without asking which
// backend it holds, and what makes each gap say which mode would close it.

func (b *BotBackend) UnpinMessage(ctx context.Context, chatID any, messageID int) (bool, error) {
	return b.callBool(ctx, "unpinChatMessage", Map{"chat_id": chatID, "message_id": messageID})
}

func (b *BotBackend) UnpinAllMessages(ctx context.Context, chatID any) (bool, error) {
	return b.callBool(ctx, "unpinAllChatMessages", Map{"chat_id": chatID})
}

func (b *BotBackend) DeleteMessages(ctx context.Context, chatID any, ids []int) (int, error) {
	if len(ids) == 0 {
		return 0, errors.New("'delete_bulk' requires message_ids")
	}
	if _, err := b.callBool(ctx, "deleteMessages", Map{
		"chat_id":     chatID,
		"message_ids": ids,
	}); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (b *BotBackend) ForwardMessages(ctx context.Context, fromChat, toChat any, ids []int) ([]Map, error) {
	if len(ids) == 0 {
		return nil, errors.New("'forward_bulk' requires message_ids")
	}
	raw, err := b.call(ctx, "forwardMessages", Map{
		"chat_id":      toChat,
		"from_chat_id": fromChat,
		"message_ids":  ids,
	})
	if err != nil {
		return nil, err
	}
	// forwardMessages answers with the ids of the copies, not whole messages.
	var forwarded []struct {
		MessageID int `json:"message_id"`
	}
	if err := json.Unmarshal(raw, &forwarded); err != nil {
		return nil, err
	}
	out := make([]Map, 0, len(forwarded))
	for _, item := range forwarded {
		out = append(out, Map{"message_id": item.MessageID})
	}
	return out, nil
}

func (b *BotBackend) SendPoll(ctx context.Context, chatID any, question string, options []string, opts PollOptions) (Map, error) {
	if question == "" {
		return nil, errors.New("'poll' requires question")
	}
	if len(options) < 2 || len(options) > 10 {
		return nil, errors.New("a poll needs between 2 and 10 options")
	}
	params := Map{
		"chat_id":                 chatID,
		"question":                question,
		"options":                 options,
		"is_anonymous":            opts.Anonymous,
		"allows_multiple_answers": opts.MultipleChoice,
	}
	if opts.Quiz {
		// The Bot API does accept a quiz, but only with correct_option_id,
		// which the tool surface deliberately does not carry -- see the note in
		// the MTProto implementation.
		return nil, errors.New(
			"quiz polls are not supported by this build; send a regular poll " +
				"and say which answer is right in a follow-up message")
	}
	if !opts.CloseAt.IsZero() {
		params["close_date"] = opts.CloseAt.Unix()
	}
	return b.callMap(ctx, "sendPoll", params)
}

// MessageLink builds the public link to a message. A bot can only do this for a
// chat that has a username: a private group has no address the Bot API exposes.
func (b *BotBackend) MessageLink(ctx context.Context, chatID any, messageID int) (Map, error) {
	chat, err := b.callMap(ctx, "getChat", Map{"chat_id": chatID})
	if err != nil {
		return nil, err
	}
	username, _ := chat["username"].(string)
	if username == "" {
		return nil, errors.New(
			"that chat has no public username, so the Bot API cannot build a " +
				"link to it. User mode can, through Telegram's own export call.")
	}
	link := fmt.Sprintf("https://t.me/%s/%d", strings.TrimPrefix(username, "@"), messageID)
	return Map{"message_id": messageID, "link": link}, nil
}

// The rest of the interface exists only for a user account. A bot has no
// mailbox of its own: it cannot hold drafts, queue a message for later, mark a
// conversation read, or read back who reacted.

func (b *BotBackend) MarkRead(context.Context, any, int) (bool, error) { return false, NeedsUser() }

func (b *BotBackend) ListPinned(context.Context, any, int) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) MessageContext(context.Context, any, int, int) ([]Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) ListReactions(context.Context, any, int, int) ([]Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) PurgeHistory(context.Context, any, bool) (Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) SaveDraft(context.Context, any, string) (bool, error) {
	return false, NeedsUser()
}

func (b *BotBackend) ListDrafts(context.Context) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) ScheduleMessage(context.Context, any, string, time.Time) (Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) ListScheduled(context.Context, any) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) CancelScheduled(context.Context, any, []int) (bool, error) {
	return false, NeedsUser()
}
