package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// MediaArgs are the arguments of the `media` tool.
type MediaArgs struct {
	Action        string `json:"action" jsonschema:"send_photo|send_file|send_voice|send_video|send_album|send_sticker|send_gif|download|info|photos|sticker_sets|stickers|gifs"`
	ChatID        any    `json:"chat_id,omitempty" jsonschema:"chat id, @username or saved alias"`
	FilePathOrURL string `json:"file_path_or_url,omitempty" jsonschema:"local path or HTTP(S) URL"`
	MessageID     int    `json:"message_id,omitempty"`
	Caption       string `json:"caption,omitempty"`
	OutputDir     string `json:"output_dir,omitempty"`
	FileID        string `json:"file_id,omitempty" jsonschema:"Telegram file_id; required to download in bot mode"`

	Files      []string `json:"files,omitempty" jsonschema:"2 to 10 paths or URLs to post as one album"`
	StickerSet string   `json:"sticker_set,omitempty" jsonschema:"a sticker set's short name"`
	Emoji      string   `json:"emoji,omitempty" jsonschema:"pick the sticker standing for this emoji"`
	Index      int      `json:"index,omitempty" jsonschema:"pick the sticker at this position in the set"`
	DocumentID int64    `json:"document_id,omitempty" jsonschema:"a saved GIF's id, from the gifs action"`
	Limit      int      `json:"limit,omitempty"`
}

// actionMediaType maps a send action to the media kind it uploads.
var actionMediaType = map[string]string{
	"send_photo": "photo",
	"send_file":  "document",
	"send_voice": "voice",
	"send_video": "video",
}

var mediaActions = []string{
	"send_photo", "send_file", "send_voice", "send_video",
	"send_album", "send_sticker", "send_gif",
	"download", "info", "photos", "sticker_sets", "stickers", "gifs",
}

// HandleMedia dispatches one call of the `media` tool.
func HandleMedia(ctx context.Context, backend telegram.Backend, args MediaArgs) Result {
	if mediaType, ok := actionMediaType[args.Action]; ok {
		if isEmpty(args.ChatID) || args.FilePathOrURL == "" {
			return Err("'%s' requires chat_id and file_path_or_url. "+
				"file_path_or_url: local path or HTTP(S) URL. "+
				"Limits: photo 10MB, file/voice/video 50MB (2GB local in user mode).",
				args.Action)
		}
		result, err := backend.SendMedia(ctx, args.ChatID, mediaType, args.FilePathOrURL, args.Caption)
		if err != nil {
			return SafeError(err)
		}
		return Ok(result)
	}

	if args.Action == "download" {
		if isEmpty(args.ChatID) || args.MessageID == 0 {
			return Err("'download' requires chat_id and message_id")
		}
		path, err := backend.DownloadMedia(ctx, args.ChatID, args.MessageID, args.FileID, args.OutputDir)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"path": path})
	}

	return handleMediaExtra(ctx, backend, args)
}

// handleMediaExtra dispatches the actions that need a capability beyond the
// core send/download pair.
func handleMediaExtra(ctx context.Context, backend telegram.Backend, args MediaArgs) Result {
	if !contains(mediaActions, args.Action) {
		return unknownAction(args.Action, mediaActions)
	}
	extras, err := telegram.Media(backend)
	if err != nil {
		return SafeError(err)
	}

	switch args.Action {
	case "send_album":
		if isEmpty(args.ChatID) || len(args.Files) < 2 {
			return Err("'send_album' requires chat_id and 2 to 10 files")
		}
		messages, err := extras.SendAlbum(ctx, args.ChatID, args.Files, args.Caption)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"messages": messages, "count": len(messages)})

	case "send_sticker":
		if isEmpty(args.ChatID) {
			return Err("'send_sticker' requires chat_id, and either " +
				"file_path_or_url (a .webp file) or sticker_set with emoji or index")
		}
		sent, err := extras.SendSticker(ctx, args.ChatID, telegram.StickerOptions{
			Path:  args.FilePathOrURL,
			Set:   args.StickerSet,
			Emoji: args.Emoji,
			Index: args.Index,
		})
		if err != nil {
			return SafeError(err)
		}
		return Ok(sent)

	case "send_gif":
		if isEmpty(args.ChatID) || args.DocumentID == 0 {
			return Err("'send_gif' requires chat_id and document_id from the gifs action")
		}
		sent, err := extras.SendGIF(ctx, args.ChatID, args.DocumentID, args.Caption)
		if err != nil {
			return SafeError(err)
		}
		return Ok(sent)

	case "info":
		if isEmpty(args.ChatID) || args.MessageID == 0 {
			return Err("'info' requires chat_id and message_id")
		}
		info, err := extras.MediaInfo(ctx, args.ChatID, args.MessageID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(info)

	case "photos":
		if isEmpty(args.ChatID) {
			return Err("'photos' requires chat_id")
		}
		photos, err := extras.ListPhotos(ctx, args.ChatID, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"photos": photos, "count": len(photos)})

	case "sticker_sets":
		sets, err := extras.ListStickerSets(ctx)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"sets": sets, "count": len(sets)})

	case "stickers":
		if args.StickerSet == "" {
			return Err("'stickers' requires sticker_set")
		}
		stickers, err := extras.ListStickers(ctx, args.StickerSet)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"stickers": stickers, "count": len(stickers)})

	case "gifs":
		gifs, err := extras.SavedGIFs(ctx, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"gifs": gifs, "count": len(gifs)})

	default:
		return unknownAction(args.Action, mediaActions)
	}
}
