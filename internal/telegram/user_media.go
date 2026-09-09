package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

// SendAlbum posts several files as one grouped message, the way dragging a
// folder of photos into a chat does. Sending them one at a time would show up
// as separate messages.
func (u *UserBackend) SendAlbum(ctx context.Context, chatID any, paths []string, caption string) ([]Map, error) {
	if _, err := u.ensure(); err != nil {
		return nil, err
	}
	if len(paths) < 2 {
		return nil, errors.New("an album needs at least 2 files; use send_photo for one")
	}
	if len(paths) > 10 {
		return nil, errors.New("an album holds at most 10 files")
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}

	// Only the first item carries the caption: Telegram shows one caption for
	// the whole group.
	options := make([]message.MultiMediaOption, 0, len(paths))
	for index, path := range paths {
		file, name, err := u.uploadLocal(ctx, path)
		if err != nil {
			return nil, err
		}
		var captions []message.StyledTextOption
		if index == 0 && caption != "" {
			captions = append(captions, styling.Plain(caption))
		}
		if isImageName(name) {
			options = append(options, message.UploadedPhoto(file, captions...))
			continue
		}
		options = append(options, message.UploadedDocument(file, captions...))
	}

	updates, err := u.sender.To(peer).Album(ctx, options[0], options[1:]...)
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

// isImageName reports whether a file should go as a photo rather than a
// document, which is what decides how Telegram displays it.
func isImageName(name string) bool {
	lower := strings.ToLower(name)
	for _, extension := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp"} {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

// SendSticker sends a sticker, either a local .webp file or one from a set.
func (u *UserBackend) SendSticker(ctx context.Context, chatID any, opts StickerOptions) (Map, error) {
	if _, err := u.ensure(); err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	builder := u.sender.To(peer)

	if opts.Set != "" {
		stickers := builder.Sticker(message.StickerSetName(opts.Set))
		var updates tg.UpdatesClass
		switch {
		case opts.Emoji != "":
			updates, err = stickers.ByEmoji(ctx, opts.Emoji)
		default:
			updates, err = stickers.ByIndex(ctx, opts.Index)
		}
		if err != nil {
			return nil, err
		}
		return firstMessage(updates), nil
	}

	if opts.Path == "" {
		return nil, errors.New(
			"'send_sticker' needs either path (a .webp file) or sticker_set " +
				"with emoji or index")
	}
	file, name, err := u.uploadLocal(ctx, opts.Path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(strings.ToLower(name), ".webp") {
		return nil, errors.New("a sticker file must be .webp")
	}
	updates, err := builder.Media(ctx,
		message.UploadedDocument(file).MIME("image/webp").
			Attributes(&tg.DocumentAttributeSticker{Alt: opts.Emoji,
				Stickerset: &tg.InputStickerSetEmpty{}}))
	if err != nil {
		return nil, err
	}
	return firstMessage(updates), nil
}

// ListStickerSets returns the sticker packs installed on the account.
func (u *UserBackend) ListStickerSets(ctx context.Context) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesGetAllStickers(ctx, 0)
	if err != nil {
		return nil, err
	}
	sets, ok := result.(*tg.MessagesAllStickers)
	if !ok {
		return []Map{}, nil
	}
	out := []Map{}
	for _, set := range sets.Sets {
		out = append(out, Map{
			"title":      set.Title,
			"short_name": set.ShortName,
			"count":      set.Count,
			"masks":      set.Masks,
		})
	}
	return out, nil
}

// ListStickers returns the stickers in one set, with the emoji each stands for
// so a caller can name one in send_sticker.
func (u *UserBackend) ListStickers(ctx context.Context, set string) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(set) == "" {
		return nil, errors.New("'stickers' requires sticker_set (a set's short name)")
	}
	result, err := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{
		Stickerset: &tg.InputStickerSetShortName{ShortName: set},
	})
	if err != nil {
		return nil, err
	}
	modified, ok := result.AsModified()
	if !ok {
		return []Map{}, nil
	}
	out := []Map{}
	for index, item := range modified.Documents {
		document, ok := item.(*tg.Document)
		if !ok {
			continue
		}
		entry := Map{"index": index, "document_id": document.ID}
		for _, attribute := range document.Attributes {
			if sticker, ok := attribute.(*tg.DocumentAttributeSticker); ok {
				entry["emoji"] = sticker.Alt
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// SavedGIFs returns the account's saved GIFs.
//
// Telegram removed the GIF search method from its schema, and a bare search
// against an inline bot is not the same thing, so this reports what the account
// itself has saved -- which is what a caller can then send.
func (u *UserBackend) SavedGIFs(ctx context.Context, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesGetSavedGifs(ctx, 0)
	if err != nil {
		return nil, err
	}
	saved, ok := result.(*tg.MessagesSavedGifs)
	if !ok {
		return []Map{}, nil
	}

	limit = limitOr(limit, 20, 100)
	out := []Map{}
	for _, item := range saved.Gifs {
		if len(out) >= limit {
			break
		}
		document, ok := item.(*tg.Document)
		if !ok {
			continue
		}
		out = append(out, Map{
			"document_id": document.ID,
			"mime_type":   document.MimeType,
			"size":        document.Size,
		})
	}
	return out, nil
}

// SendGIF posts one of the saved GIFs by the id SavedGIFs reported.
func (u *UserBackend) SendGIF(ctx context.Context, chatID any, documentID int64, caption string) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}

	// A document can only be re-sent with its access hash and file reference,
	// which the saved list is where this account can get them from.
	result, err := api.MessagesGetSavedGifs(ctx, 0)
	if err != nil {
		return nil, err
	}
	saved, ok := result.(*tg.MessagesSavedGifs)
	if !ok {
		return nil, errors.New("This account has no saved GIFs.")
	}
	for _, item := range saved.Gifs {
		document, ok := item.(*tg.Document)
		if !ok || document.ID != documentID {
			continue
		}
		var captions []message.StyledTextOption
		if caption != "" {
			captions = append(captions, styling.Plain(caption))
		}
		updates, err := u.sender.To(peer).Media(ctx, message.Document(&tg.InputDocument{
			ID:            document.ID,
			AccessHash:    document.AccessHash,
			FileReference: document.FileReference,
		}, captions...))
		if err != nil {
			return nil, err
		}
		return firstMessage(updates), nil
	}
	return nil, fmt.Errorf("no saved GIF has document_id %d; list them first", documentID)
}

// MediaInfo describes the media attached to a message without downloading it.
func (u *UserBackend) MediaInfo(ctx context.Context, chatID any, messageID int) (Map, error) {
	if _, err := u.ensure(); err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	msg, err := u.fetchMessage(ctx, peer, messageID)
	if err != nil {
		return nil, err
	}
	media, ok := msg.GetMedia()
	if !ok {
		return Map{"message_id": messageID, "has_media": false}, nil
	}
	info := describeMedia(media)
	info["message_id"] = messageID
	info["has_media"] = true
	return info, nil
}

// describeMedia reports the shape and size of an attachment, which is what
// decides whether downloading it is worth doing.
func describeMedia(media tg.MessageMediaClass) Map {
	switch typed := media.(type) {
	case *tg.MessageMediaPhoto:
		out := Map{"type": "photo"}
		if photo, ok := typed.Photo.(*tg.Photo); ok {
			out["photo_id"] = photo.ID
			out["date"] = time.Unix(int64(photo.Date), 0).UTC().Format(time.RFC3339)
		}
		return out

	case *tg.MessageMediaDocument:
		out := Map{"type": "document"}
		document, ok := typed.Document.(*tg.Document)
		if !ok {
			return out
		}
		out["document_id"] = document.ID
		out["mime_type"] = document.MimeType
		out["size"] = document.Size
		out["file_name"] = documentName(document)
		for _, attribute := range document.Attributes {
			switch attr := attribute.(type) {
			case *tg.DocumentAttributeVideo:
				out["type"] = "video"
				out["duration"] = attr.Duration
				out["width"] = attr.W
				out["height"] = attr.H
			case *tg.DocumentAttributeAudio:
				out["type"] = "audio"
				if attr.Voice {
					out["type"] = "voice"
				}
				out["duration"] = attr.Duration
			case *tg.DocumentAttributeSticker:
				out["type"] = "sticker"
				out["emoji"] = attr.Alt
			case *tg.DocumentAttributeAnimated:
				out["type"] = "gif"
			}
		}
		return out

	case *tg.MessageMediaWebPage:
		return Map{"type": "web_page"}
	case *tg.MessageMediaContact:
		return Map{"type": "contact", "phone": typed.PhoneNumber}
	case *tg.MessageMediaGeo:
		return Map{"type": "location"}
	case *tg.MessageMediaPoll:
		return Map{"type": "poll"}
	default:
		return Map{"type": "other"}
	}
}

// ListPhotos indexes the photos posted in a chat, without transferring any of
// them -- the ids are what a download call then takes.
func (u *UserBackend) ListPhotos(ctx context.Context, chatID any, limit int) ([]Map, error) {
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
		Filter: &tg.InputMessagesFilterPhotos{},
		Limit:  limitOr(limit, 20, 100),
	})
	if err != nil {
		return nil, err
	}

	out := []Map{}
	for _, item := range messagesOf(result) {
		msg, ok := item.(*tg.Message)
		if !ok {
			continue
		}
		entry := serializeMessage(msg)
		if media, ok := msg.GetMedia(); ok {
			for key, value := range describeMedia(media) {
				entry[key] = value
			}
		}
		out = append(out, entry)
	}
	return out, nil
}
