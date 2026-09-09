package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// UnpinMessage takes one message off the pinned list.
func (u *UserBackend) UnpinMessage(ctx context.Context, chatID any, messageID int) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	request := &tg.MessagesUpdatePinnedMessageRequest{Peer: peer, ID: messageID}
	request.Unpin = true
	_, err = api.MessagesUpdatePinnedMessage(ctx, request)
	return err == nil, err
}

// UnpinAllMessages clears the pinned list of a chat.
func (u *UserBackend) UnpinAllMessages(ctx context.Context, chatID any) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	_, err = api.MessagesUnpinAllMessages(ctx, &tg.MessagesUnpinAllMessagesRequest{Peer: peer})
	return err == nil, err
}

// MarkRead moves the read marker. A zero maxID marks the whole chat read,
// which is the usual intent.
func (u *UserBackend) MarkRead(ctx context.Context, chatID any, maxID int) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	if channel, ok := peer.(*tg.InputPeerChannel); ok {
		return api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			MaxID:   maxID,
		})
	}
	if _, err := api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{
		Peer:  peer,
		MaxID: maxID,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// ListPinned returns the pinned messages of a chat, newest first.
func (u *UserBackend) ListPinned(ctx context.Context, chatID any, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:   peer,
		Filter: &tg.InputMessagesFilterPinned{},
		Limit:  limitOr(limit, 20, 100),
	})
	if err != nil {
		return nil, err
	}
	return serializeMessages(result), nil
}

// MessageContext returns the messages around one message, so a hit from search
// can be read in the conversation it belongs to.
func (u *UserBackend) MessageContext(ctx context.Context, chatID any, messageID, around int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	around = limitOr(around, 5, 50)
	// A positive AddOffset walks backwards from the offset id, so asking for
	// 2*around+1 messages starting `around` newer than the target centres the
	// window on it.
	result, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:      peer,
		OffsetID:  messageID,
		AddOffset: -around,
		Limit:     2*around + 1,
	})
	if err != nil {
		return nil, err
	}
	return serializeMessages(result), nil
}

// MessageLink returns the t.me link to one message. Only channels and
// supergroups have one -- a private conversation is not addressable.
func (u *UserBackend) MessageLink(ctx context.Context, chatID any, messageID int) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	channel, err := u.inputChannel(ctx, chatID)
	if err != nil {
		return nil, errors.New(
			"only a channel or supergroup message has a link; a private chat or " +
				"basic group is not addressable by URL")
	}
	link, err := api.ChannelsExportMessageLink(ctx, &tg.ChannelsExportMessageLinkRequest{
		Channel: channel,
		ID:      messageID,
	})
	if err != nil {
		return nil, err
	}
	return Map{"message_id": messageID, "link": link.Link, "html": link.HTML}, nil
}

// ListReactions reports who reacted to a message and with what.
func (u *UserBackend) ListReactions(ctx context.Context, chatID any, messageID, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesGetMessageReactionsList(ctx, &tg.MessagesGetMessageReactionsListRequest{
		Peer:  peer,
		ID:    messageID,
		Limit: limitOr(limit, 50, 100),
	})
	if err != nil {
		return nil, err
	}

	u.rememberPeers(ctx, result.Users, nil)

	names := map[int64]string{}
	for _, item := range result.Users {
		if user, ok := item.(*tg.User); ok {
			names[user.ID] = strings.TrimSpace(user.FirstName + " " + user.LastName)
		}
	}
	out := []Map{}
	for _, reaction := range result.Reactions {
		id := peerID(reaction.PeerID)
		out = append(out, Map{
			"user_id":  id,
			"name":     emptyToNil(names[id]),
			"reaction": reactionEmoji(reaction.Reaction),
			"date":     time.Unix(int64(reaction.Date), 0).UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// reactionEmoji renders a reaction as the character a person sees, or the id of
// the custom emoji when there is no such character.
func reactionEmoji(reaction tg.ReactionClass) any {
	switch typed := reaction.(type) {
	case *tg.ReactionEmoji:
		return typed.Emoticon
	case *tg.ReactionCustomEmoji:
		return fmt.Sprintf("custom:%d", typed.DocumentID)
	default:
		return nil
	}
}

// DeleteMessages removes several messages in one call and reports how many were
// accepted.
func (u *UserBackend) DeleteMessages(ctx context.Context, chatID any, ids []int) (int, error) {
	api, err := u.ensure()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, errors.New("'delete_bulk' requires message_ids")
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return 0, err
	}
	if channel, ok := peer.(*tg.InputPeerChannel); ok {
		affected, err := api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			ID:      ids,
		})
		if err != nil {
			return 0, err
		}
		return affected.PtsCount, nil
	}
	affected, err := api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
		Revoke: true,
		ID:     ids,
	})
	if err != nil {
		return 0, err
	}
	return affected.PtsCount, nil
}

// PurgeHistory clears a whole conversation. revoke also removes it for the
// other side, which cannot be undone -- the tool layer requires it to be asked
// for explicitly.
func (u *UserBackend) PurgeHistory(ctx context.Context, chatID any, revoke bool) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if channel, ok := peer.(*tg.InputPeerChannel); ok {
		request := &tg.ChannelsDeleteHistoryRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			MaxID:   0,
		}
		request.ForEveryone = revoke
		if _, err := api.ChannelsDeleteHistory(ctx, request); err != nil {
			return nil, err
		}
		return Map{"cleared": true, "for_everyone": revoke}, nil
	}
	request := &tg.MessagesDeleteHistoryRequest{Peer: peer}
	request.Revoke = revoke
	affected, err := api.MessagesDeleteHistory(ctx, request)
	if err != nil {
		return nil, err
	}
	return Map{"cleared": true, "for_everyone": revoke, "messages": affected.PtsCount}, nil
}

// SendPoll posts a poll or a quiz.
func (u *UserBackend) SendPoll(ctx context.Context, chatID any, question string, options []string, opts PollOptions) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if question == "" {
		return nil, errors.New("'poll' requires question")
	}
	if len(options) < 2 || len(options) > 10 {
		return nil, errors.New("a poll needs between 2 and 10 options")
	}
	if opts.Quiz {
		// A quiz is a poll plus a correct answer, and the correct answer is the
		// one part gotd v0.161 cannot encode: its generated InputMediaPoll
		// types correct_answers as a vector of ints where the schema says a
		// vector of byte strings, so anything sent there is rejected by
		// Telegram. Refusing is better than posting a quiz nobody can win.
		return nil, errors.New(
			"quiz polls are not supported by this build; send a regular poll " +
				"and say which answer is right in a follow-up message")
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}

	// Each answer is identified by an opaque byte string; its index is the
	// simplest one that stays stable for the correct-answer reference below.
	answers := make([]tg.PollAnswerClass, 0, len(options))
	for index, option := range options {
		answers = append(answers, &tg.PollAnswer{
			Text:   tg.TextWithEntities{Text: option},
			Option: []byte{byte(index)},
		})
	}

	poll := tg.Poll{
		Question:       tg.TextWithEntities{Text: question},
		Answers:        answers,
		MultipleChoice: opts.MultipleChoice,
		Quiz:           opts.Quiz,
		// PublicVoters is the inverse of the anonymous poll a caller asks for.
		PublicVoters: !opts.Anonymous,
	}
	if !opts.CloseAt.IsZero() {
		poll.SetCloseDate(int(opts.CloseAt.Unix()))
	}
	media := &tg.InputMediaPoll{Poll: poll}

	updates, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    media,
		RandomID: randomID(),
	})
	if err != nil {
		return nil, err
	}
	sent := firstMessage(updates)
	sent["question"] = question
	sent["options"] = options
	return sent, nil
}

// --- drafts ---

// SaveDraft stores unsent text in a chat, the way typing into it and walking
// away would. An empty text clears the draft.
func (u *UserBackend) SaveDraft(ctx context.Context, chatID any, text string) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	return api.MessagesSaveDraft(ctx, &tg.MessagesSaveDraftRequest{
		Peer:    peer,
		Message: text,
	})
}

// ListDrafts returns every chat with unsent text in it.
func (u *UserBackend) ListDrafts(ctx context.Context) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	updates, err := api.MessagesGetAllDrafts(ctx)
	if err != nil {
		return nil, err
	}
	out := []Map{}
	for _, update := range updatesOf(updates) {
		draft, ok := update.(*tg.UpdateDraftMessage)
		if !ok {
			continue
		}
		message, ok := draft.Draft.(*tg.DraftMessage)
		if !ok {
			continue
		}
		entry := Map{
			"chat_id": peerID(draft.Peer),
			"text":    message.Message,
			"date":    time.Unix(int64(message.Date), 0).UTC().Format(time.RFC3339),
		}
		out = append(out, entry)
	}
	return out, nil
}

// --- scheduled messages ---

// ScheduleMessage queues a message for a time in the future.
func (u *UserBackend) ScheduleMessage(ctx context.Context, chatID any, text string, at time.Time) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if !at.After(time.Now()) {
		return nil, errors.New("the scheduled time must be in the future")
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	request := &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: randomID(),
	}
	request.SetScheduleDate(int(at.Unix()))
	updates, err := api.MessagesSendMessage(ctx, request)
	if err != nil {
		return nil, err
	}
	scheduled := firstMessage(updates)
	scheduled["scheduled_for"] = at.UTC().Format(time.RFC3339)
	return scheduled, nil
}

// ListScheduled returns the messages queued in a chat but not yet sent.
func (u *UserBackend) ListScheduled(ctx context.Context, chatID any) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesGetScheduledHistory(ctx, &tg.MessagesGetScheduledHistoryRequest{
		Peer: peer,
	})
	if err != nil {
		return nil, err
	}
	return serializeMessages(result), nil
}

// CancelScheduled drops queued messages before they go out.
func (u *UserBackend) CancelScheduled(ctx context.Context, chatID any, ids []int) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return false, errors.New("'schedule_cancel' requires message_ids")
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	_, err = api.MessagesDeleteScheduledMessages(ctx, &tg.MessagesDeleteScheduledMessagesRequest{
		Peer: peer,
		ID:   ids,
	})
	return err == nil, err
}

// ForwardMessages moves several messages at once, keeping them grouped the way
// forwarding them one at a time would not.
func (u *UserBackend) ForwardMessages(ctx context.Context, fromChat, toChat any, ids []int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("'forward_bulk' requires message_ids")
	}
	from, err := u.resolvePeer(ctx, fromChat)
	if err != nil {
		return nil, err
	}
	to, err := u.resolvePeer(ctx, toChat)
	if err != nil {
		return nil, err
	}

	random := make([]int64, len(ids))
	for index := range random {
		random[index] = randomID()
	}
	updates, err := api.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
		FromPeer: from,
		ToPeer:   to,
		ID:       ids,
		RandomID: random,
	})
	if err != nil {
		return nil, err
	}

	out := []Map{}
	for _, update := range updatesOf(updates) {
		switch typed := update.(type) {
		case *tg.UpdateNewMessage:
			if msg, ok := typed.Message.(*tg.Message); ok {
				out = append(out, serializeMessage(msg))
			}
		case *tg.UpdateNewChannelMessage:
			if msg, ok := typed.Message.(*tg.Message); ok {
				out = append(out, serializeMessage(msg))
			}
		}
	}
	return out, nil
}
