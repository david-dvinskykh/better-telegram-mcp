package telegram

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
)

// serializeMessage flattens an MTProto message into the same shape the Bot API
// side returns, so a tool result reads the same whichever backend produced it.
func serializeMessage(msg *tg.Message) Map {
	out := Map{
		"message_id": msg.ID,
		"text":       msg.Message,
		"date":       nil,
		"sender_id":  nil,
		"topic_id":   nil,
	}
	if msg.Date != 0 {
		out["date"] = time.Unix(int64(msg.Date), 0).UTC().Format(time.RFC3339)
	}
	if from, ok := msg.GetFromID(); ok {
		out["sender_id"] = peerID(from)
	}
	if replyTo, ok := msg.GetReplyTo(); ok {
		if header, ok := replyTo.(*tg.MessageReplyHeader); ok && header.ForumTopic {
			// A forum message points at its topic root; an ordinary reply does
			// not carry ForumTopic, so this stays nil for it.
			if topID, ok := header.GetReplyToTopID(); ok {
				out["topic_id"] = topID
			} else if replyID, ok := header.GetReplyToMsgID(); ok {
				out["topic_id"] = replyID
			}
		}
	}
	return out
}

// serializeMessages pulls the messages out of any of the messages.* response
// shapes and serializes them.
func serializeMessages(result tg.MessagesMessagesClass) []Map {
	raw := messagesOf(result)
	out := make([]Map, 0, len(raw))
	for _, item := range raw {
		if msg, ok := item.(*tg.Message); ok {
			out = append(out, serializeMessage(msg))
		}
	}
	return out
}

func messagesOf(result tg.MessagesMessagesClass) []tg.MessageClass {
	switch typed := result.(type) {
	case *tg.MessagesMessages:
		return typed.Messages
	case *tg.MessagesMessagesSlice:
		return typed.Messages
	case *tg.MessagesChannelMessages:
		return typed.Messages
	default:
		return nil
	}
}

func serializeUser(user *tg.User) Map {
	return Map{
		"id":         user.ID,
		"first_name": user.FirstName,
		"last_name":  user.LastName,
		"username":   emptyToNil(user.Username),
		"phone":      emptyToNil(user.Phone),
	}
}

func serializeUsers(users []tg.UserClass) []Map {
	out := make([]Map, 0, len(users))
	for _, item := range users {
		if user, ok := item.(*tg.User); ok {
			out = append(out, serializeUser(user))
		}
	}
	return out
}

// peerID renders a peer the way the Bot API spells it: a plain id for a user, a
// negative one for a legacy group, and -100<id> for a channel or supergroup.
func peerID(peer tg.PeerClass) int64 {
	switch typed := peer.(type) {
	case *tg.PeerUser:
		return typed.UserID
	case *tg.PeerChat:
		return -typed.ChatID
	case *tg.PeerChannel:
		return -1_000_000_000_000 - typed.ChannelID
	default:
		return 0
	}
}

// titleIndex maps the peer ids in a dialog list to their display names.
func titleIndex(users []tg.UserClass, chats []tg.ChatClass) map[int64]string {
	index := map[int64]string{}
	for _, item := range users {
		if user, ok := item.(*tg.User); ok {
			name := user.FirstName
			if user.LastName != "" {
				name += " " + user.LastName
			}
			index[user.ID] = name
		}
	}
	for _, item := range chats {
		switch chat := item.(type) {
		case *tg.Chat:
			index[-chat.ID] = chat.Title
		case *tg.Channel:
			index[-1_000_000_000_000-chat.ID] = chat.Title
		}
	}
	return index
}

// firstMessage digs the sent message out of an Updates response so a send/edit
// can report the id the caller needs for a follow-up.
func firstMessage(updates tg.UpdatesClass) Map {
	for _, update := range updatesOf(updates) {
		switch typed := update.(type) {
		case *tg.UpdateNewMessage:
			if msg, ok := typed.Message.(*tg.Message); ok {
				return serializeMessage(msg)
			}
		case *tg.UpdateNewChannelMessage:
			if msg, ok := typed.Message.(*tg.Message); ok {
				return serializeMessage(msg)
			}
		case *tg.UpdateEditMessage:
			if msg, ok := typed.Message.(*tg.Message); ok {
				return serializeMessage(msg)
			}
		case *tg.UpdateEditChannelMessage:
			if msg, ok := typed.Message.(*tg.Message); ok {
				return serializeMessage(msg)
			}
		}
	}
	// Telegram can answer a send with a bare id instead of a full message.
	if short, ok := updates.(*tg.UpdateShortSentMessage); ok {
		return Map{
			"message_id": short.ID,
			"text":       "",
			"date":       time.Unix(int64(short.Date), 0).UTC().Format(time.RFC3339),
			"sender_id":  nil,
			"topic_id":   nil,
		}
	}
	return Map{}
}

// firstChat digs the created chat out of an Updates response.
func firstChat(updates tg.UpdatesClass, fallbackTitle string) Map {
	var chats []tg.ChatClass
	switch typed := updates.(type) {
	case *tg.Updates:
		chats = typed.Chats
	case *tg.UpdatesCombined:
		chats = typed.Chats
	}
	for _, item := range chats {
		switch chat := item.(type) {
		case *tg.Chat:
			return Map{"id": -chat.ID, "title": chat.Title}
		case *tg.Channel:
			return Map{"id": -1_000_000_000_000 - chat.ID, "title": chat.Title}
		}
	}
	return Map{"title": fallbackTitle}
}

func updatesOf(updates tg.UpdatesClass) []tg.UpdateClass {
	switch typed := updates.(type) {
	case *tg.Updates:
		return typed.Updates
	case *tg.UpdatesCombined:
		return typed.Updates
	case *tg.UpdateShort:
		return []tg.UpdateClass{typed.Update}
	default:
		return nil
	}
}

// mediaLocation turns a message's media into something the downloader can pull,
// plus the filename to save it under.
func mediaLocation(media tg.MessageMediaClass) (tg.InputFileLocationClass, string, error) {
	switch typed := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := typed.Photo.(*tg.Photo)
		if !ok {
			return nil, "", errors.New("Message has no media to download.")
		}
		size := largestPhotoSize(photo)
		return &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     size,
		}, fmt.Sprintf("photo_%d.jpg", photo.ID), nil

	case *tg.MessageMediaDocument:
		document, ok := typed.Document.(*tg.Document)
		if !ok {
			return nil, "", errors.New("Message has no media to download.")
		}
		return &tg.InputDocumentFileLocation{
			ID:            document.ID,
			AccessHash:    document.AccessHash,
			FileReference: document.FileReference,
		}, documentName(document), nil

	default:
		return nil, "", errors.New("Message has no media to download.")
	}
}

// largestPhotoSize picks the biggest available rendition, which is what a
// caller asking to download a photo wants.
func largestPhotoSize(photo *tg.Photo) string {
	best := ""
	bestBytes := -1
	for _, size := range photo.Sizes {
		switch typed := size.(type) {
		case *tg.PhotoSize:
			if typed.Size > bestBytes {
				best, bestBytes = typed.Type, typed.Size
			}
		case *tg.PhotoSizeProgressive:
			total := 0
			for _, chunk := range typed.Sizes {
				if chunk > total {
					total = chunk
				}
			}
			if total > bestBytes {
				best, bestBytes = typed.Type, total
			}
		}
	}
	if best == "" {
		best = "x"
	}
	return best
}

func documentName(document *tg.Document) string {
	for _, attribute := range document.Attributes {
		if named, ok := attribute.(*tg.DocumentAttributeFilename); ok && named.FileName != "" {
			return named.FileName
		}
	}
	return fmt.Sprintf("document_%d", document.ID)
}

// randomID is the client-side dedupe key every MTProto send needs.
func randomID() int64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return time.Now().UnixNano()
	}
	return int64(binary.LittleEndian.Uint64(buf[:]))
}
