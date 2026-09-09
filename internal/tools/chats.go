package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// ChatArgs are the arguments of the `chat` tool.
type ChatArgs struct {
	Action      string `json:"action" jsonschema:"list|info|create|join|leave|members|admin|settings|topics"`
	ChatID      any    `json:"chat_id,omitempty" jsonschema:"chat id or @username"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	IsChannel   bool   `json:"is_channel,omitempty"`
	LinkOrHash  string `json:"link_or_hash,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
	Demote      bool   `json:"demote,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	TopicAction string `json:"topic_action,omitempty" jsonschema:"list|create|close"`
	TopicID     int    `json:"topic_id,omitempty"`
	TopicName   string `json:"topic_name,omitempty"`
}

var chatActions = []string{
	"list", "info", "create", "join", "leave", "members", "admin", "settings", "topics",
}

// HandleChat dispatches one call of the `chat` tool.
func HandleChat(ctx context.Context, backend telegram.Backend, args ChatArgs) Result {
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}

	switch args.Action {
	case "list":
		chats, err := backend.ListChats(ctx, limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"chats": chats, "count": len(chats)})

	case "info":
		if isEmpty(args.ChatID) {
			return Err("'info' requires chat_id. Use positive int (user), negative int " +
				"(group/supergroup), or @username (public).")
		}
		info, err := backend.GetChatInfo(ctx, args.ChatID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(info)

	case "create":
		if args.Title == "" {
			return Err("'create' requires title")
		}
		created, err := backend.CreateChat(ctx, args.Title, args.IsChannel)
		if err != nil {
			return SafeError(err)
		}
		return Ok(created)

	case "join":
		if args.LinkOrHash == "" {
			return Err("'join' requires link_or_hash")
		}
		joined, err := backend.JoinChat(ctx, args.LinkOrHash)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"joined": joined})

	case "leave":
		if isEmpty(args.ChatID) {
			return Err("'leave' requires chat_id")
		}
		left, err := backend.LeaveChat(ctx, args.ChatID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"left": left})

	case "members":
		if isEmpty(args.ChatID) {
			return Err("'members' requires chat_id")
		}
		members, err := backend.GetMembers(ctx, args.ChatID, limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"members": members, "count": len(members)})

	case "admin":
		if isEmpty(args.ChatID) || args.UserID == 0 {
			return Err("'admin' requires chat_id and user_id")
		}
		changed, err := backend.PromoteAdmin(ctx, args.ChatID, args.UserID, args.Demote)
		if err != nil {
			return SafeError(err)
		}
		word := "promoted"
		if args.Demote {
			word = "demoted"
		}
		return Ok(Result{word: changed})

	case "settings":
		if isEmpty(args.ChatID) {
			return Err("'settings' requires chat_id")
		}
		var title, description *string
		if args.Title != "" {
			title = &args.Title
		}
		if args.Description != "" {
			description = &args.Description
		}
		if title == nil && description == nil {
			return Err("'settings' requires at least one of: title, description")
		}
		updated, err := backend.UpdateChatSettings(ctx, args.ChatID, title, description)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"updated": updated})

	case "topics":
		if isEmpty(args.ChatID) {
			return Err("'topics' requires chat_id")
		}
		if args.TopicAction == "" {
			return Err("'topics' requires topic_action")
		}
		result, err := backend.ManageTopics(ctx, args.ChatID, args.TopicAction, telegram.TopicOptions{
			TopicID: args.TopicID,
			Name:    args.TopicName,
			Limit:   args.Limit,
		})
		if err != nil {
			return SafeError(err)
		}
		return Ok(result)

	default:
		return unknownAction(args.Action, chatActions)
	}
}
