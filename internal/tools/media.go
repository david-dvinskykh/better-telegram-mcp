package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// MediaArgs are the arguments of the `media` tool.
type MediaArgs struct {
	Action        string `json:"action" jsonschema:"send_photo|send_file|send_voice|send_video|download"`
	ChatID        any    `json:"chat_id,omitempty" jsonschema:"chat id or @username"`
	FilePathOrURL string `json:"file_path_or_url,omitempty" jsonschema:"local path or HTTP(S) URL"`
	MessageID     int    `json:"message_id,omitempty"`
	Caption       string `json:"caption,omitempty"`
	OutputDir     string `json:"output_dir,omitempty"`
	FileID        string `json:"file_id,omitempty" jsonschema:"Telegram file_id; required to download in bot mode"`
}

// actionMediaType maps a send action to the media kind it uploads.
var actionMediaType = map[string]string{
	"send_photo": "photo",
	"send_file":  "document",
	"send_voice": "voice",
	"send_video": "video",
}

var mediaActions = []string{"send_photo", "send_file", "send_voice", "send_video", "download"}

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

	return unknownAction(args.Action, mediaActions)
}
