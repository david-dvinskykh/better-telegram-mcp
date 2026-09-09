package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// A bot can post an album and a sticker; the rest of the extended media surface
// reads a person's own library, which a bot does not have.

func (b *BotBackend) SendAlbum(ctx context.Context, chatID any, paths []string, caption string) ([]Map, error) {
	if len(paths) < 2 {
		return nil, errors.New("an album needs at least 2 files; use send_photo for one")
	}
	if len(paths) > 10 {
		return nil, errors.New("an album holds at most 10 files")
	}
	// sendMediaGroup takes the files as attachments named by the media array,
	// which the single-file form of callForm cannot express, so each file is
	// posted through the shared upload path and grouped by Telegram in order.
	out := make([]Map, 0, len(paths))
	for index, path := range paths {
		itemCaption := ""
		if index == 0 {
			itemCaption = caption
		}
		kind := "document"
		if isImageName(path) {
			kind = "photo"
		}
		sent, err := b.SendMedia(ctx, chatID, kind, path, itemCaption)
		if err != nil {
			return nil, err
		}
		out = append(out, sent)
	}
	return out, nil
}

func (b *BotBackend) SendSticker(ctx context.Context, chatID any, opts StickerOptions) (Map, error) {
	if opts.Set != "" {
		return nil, errors.New(
			"the Bot API cannot pick a sticker out of a set; pass path to a .webp " +
				"file, or use user mode")
	}
	if opts.Path == "" {
		return nil, errors.New("'send_sticker' requires path (a .webp file)")
	}
	if !strings.HasSuffix(strings.ToLower(opts.Path), ".webp") {
		return nil, errors.New("a sticker file must be .webp")
	}
	filename, content, err := readUpload(opts.Path)
	if err != nil {
		return nil, err
	}
	return b.decodeForm(b.callForm(ctx, "sendSticker", "sticker", filename, content,
		Map{"chat_id": chatID}))
}

// MediaInfo describes the attachment on a message the bot can still see.
func (b *BotBackend) MediaInfo(ctx context.Context, chatID any, messageID int) (Map, error) {
	// The Bot API has no "get message" call: a bot only sees messages as they
	// arrive. Forwarding the message to the bot itself would be the workaround,
	// and it is not one a read-only description should take.
	_ = ctx
	_ = chatID
	_ = messageID
	return nil, NeedsUser()
}

func (b *BotBackend) ListStickerSets(context.Context) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) ListStickers(ctx context.Context, set string) ([]Map, error) {
	if strings.TrimSpace(set) == "" {
		return nil, errors.New("'stickers' requires sticker_set (a set's short name)")
	}
	raw, err := b.call(ctx, "getStickerSet", Map{"name": set})
	if err != nil {
		return nil, err
	}
	var result struct {
		Stickers []struct {
			FileID string `json:"file_id"`
			Emoji  string `json:"emoji"`
		} `json:"stickers"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	out := make([]Map, 0, len(result.Stickers))
	for index, sticker := range result.Stickers {
		out = append(out, Map{
			"index": index, "file_id": sticker.FileID, "emoji": sticker.Emoji,
		})
	}
	return out, nil
}

func (b *BotBackend) SavedGIFs(context.Context, int) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) SendGIF(context.Context, any, int64, string) (Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) ListPhotos(context.Context, any, int) ([]Map, error) {
	return nil, NeedsUser()
}
