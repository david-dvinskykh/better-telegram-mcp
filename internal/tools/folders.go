package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// FolderArgs are the arguments of the `folder` tool.
type FolderArgs struct {
	Action string `json:"action" jsonschema:"list|get|create|delete|add_chat|remove_chat|reorder"`

	FolderID int    `json:"folder_id,omitempty"`
	Title    string `json:"title,omitempty"`
	ChatID   any    `json:"chat_id,omitempty" jsonschema:"chat id, @username or saved alias"`
	Chats    []any  `json:"chats,omitempty" jsonschema:"chats to put in a new folder"`
	Order    []int  `json:"order,omitempty" jsonschema:"folder ids in the order the tabs should appear"`
}

var folderActions = []string{
	"add_chat", "create", "delete", "get", "list", "remove_chat", "reorder",
}

// HandleFolder dispatches one call of the `folder` tool.
func HandleFolder(ctx context.Context, backend telegram.Backend, args FolderArgs) Result {
	// A mistyped action is a caller error and must read as one; asking the
	// backend first would report it as a mode problem.
	if !contains(folderActions, args.Action) {
		return unknownAction(args.Action, folderActions)
	}
	folders, err := telegram.Folders(backend)
	if err != nil {
		return SafeError(err)
	}

	switch args.Action {
	case "list":
		all, err := folders.ListFolders(ctx)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"folders": all, "count": len(all)})

	case "get":
		if args.FolderID == 0 {
			return Err("'get' requires folder_id")
		}
		folder, err := folders.GetFolder(ctx, args.FolderID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(folder)

	case "create":
		if args.Title == "" {
			return Err("'create' requires title")
		}
		folder, err := folders.CreateFolder(ctx, args.Title, args.Chats)
		if err != nil {
			return SafeError(err)
		}
		return Ok(folder)

	case "delete":
		if args.FolderID == 0 {
			return Err("'delete' requires folder_id")
		}
		deleted, err := folders.DeleteFolder(ctx, args.FolderID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"deleted": deleted})

	case "add_chat", "remove_chat":
		if args.FolderID == 0 || args.ChatID == nil {
			return Err("'%s' requires folder_id and chat_id", args.Action)
		}
		folder, err := folders.SetFolderChat(ctx, args.FolderID, args.ChatID, args.Action == "remove_chat")
		if err != nil {
			return SafeError(err)
		}
		return Ok(folder)

	case "reorder":
		if len(args.Order) == 0 {
			return Err("'reorder' requires order")
		}
		reordered, err := folders.ReorderFolders(ctx, args.Order)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"reordered": reordered})

	default:
		return unknownAction(args.Action, folderActions)
	}
}
