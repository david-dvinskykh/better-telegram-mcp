package tools

import (
	"context"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// ChatArgs are the arguments of the `chat` tool.
type ChatArgs struct {
	Action      string `json:"action" jsonschema:"list|info|full|create|join|leave|invite|members|admins|banned|admin|ban|permissions|slow_mode|settings|photo|topics|mute|archive|resolve|search_public|common|read_by|invite_link|recent_actions"`
	ChatID      any    `json:"chat_id,omitempty" jsonschema:"chat id, @username or saved alias"`
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

	Username    string          `json:"username,omitempty" jsonschema:"@name to resolve"`
	Query       string          `json:"query,omitempty" jsonschema:"text to search the public directory for"`
	UserIDs     []int64         `json:"user_ids,omitempty" jsonschema:"accounts to invite"`
	MessageID   int             `json:"message_id,omitempty"`
	Unban       bool            `json:"unban,omitempty"`
	Mute        *bool           `json:"mute,omitempty" jsonschema:"silence the chat; defaults to true"`
	Archive     *bool           `json:"archive,omitempty" jsonschema:"move to the archive; defaults to true"`
	Revoke      bool            `json:"revoke,omitempty" jsonschema:"replace the existing invite link"`
	Seconds     int             `json:"seconds,omitempty" jsonschema:"slow-mode delay; 0 turns it off"`
	Permissions map[string]bool `json:"permissions,omitempty" jsonschema:"what ordinary members may do; anything not named is granted"`
	Path        string          `json:"path,omitempty" jsonschema:"chat photo file or URL; empty removes it"`
}

var chatActions = []string{
	"list", "info", "full", "create", "join", "leave", "invite",
	"members", "admins", "banned", "admin", "ban", "permissions", "slow_mode",
	"settings", "photo", "topics",
	"mute", "archive", "resolve", "search_public", "common", "read_by", "invite_link",
	"recent_actions",
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
		return handleChatExtra(ctx, backend, args)
	}
}

// handleChatExtra dispatches the actions that need a capability beyond the core
// Backend: chat-list state, the public directory, and moderation.
func handleChatExtra(ctx context.Context, backend telegram.Backend, args ChatArgs) Result {
	if !contains(chatActions, args.Action) {
		return unknownAction(args.Action, chatActions)
	}
	extras, err := telegram.Chats(backend)
	if err != nil {
		return SafeError(err)
	}

	switch args.Action {
	case "full":
		if isEmpty(args.ChatID) {
			return Err("'full' requires chat_id")
		}
		full, err := extras.FullChat(ctx, args.ChatID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(full)

	case "mute":
		if isEmpty(args.ChatID) {
			return Err("'mute' requires chat_id; pass mute=false to unmute")
		}
		mute := args.Mute == nil || *args.Mute
		changed, err := extras.MuteChat(ctx, args.ChatID, mute)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"muted": mute && changed, "changed": changed})

	case "archive":
		if isEmpty(args.ChatID) {
			return Err("'archive' requires chat_id; pass archive=false to unarchive")
		}
		archive := args.Archive == nil || *args.Archive
		changed, err := extras.ArchiveChat(ctx, args.ChatID, archive)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"archived": archive && changed, "changed": changed})

	case "resolve":
		if args.Username == "" {
			return Err("'resolve' requires username")
		}
		resolved, err := extras.ResolveUsername(ctx, args.Username)
		if err != nil {
			return SafeError(err)
		}
		return Ok(resolved)

	case "search_public":
		if args.Query == "" {
			return Err("'search_public' requires query")
		}
		found, err := extras.SearchPublic(ctx, args.Query, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"results": found, "count": len(found)})

	case "common":
		if args.UserID == 0 {
			return Err("'common' requires user_id")
		}
		chats, err := extras.CommonChats(ctx, args.UserID, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"chats": chats, "count": len(chats)})

	case "read_by":
		if isEmpty(args.ChatID) || args.MessageID == 0 {
			return Err("'read_by' requires chat_id and message_id")
		}
		readers, err := extras.ReadBy(ctx, args.ChatID, args.MessageID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"readers": readers, "count": len(readers)})

	case "invite_link":
		if isEmpty(args.ChatID) {
			return Err("'invite_link' requires chat_id")
		}
		link, err := extras.InviteLink(ctx, args.ChatID, args.Revoke)
		if err != nil {
			return SafeError(err)
		}
		return Ok(link)

	case "invite":
		if isEmpty(args.ChatID) || len(args.UserIDs) == 0 {
			return Err("'invite' requires chat_id and user_ids")
		}
		invited, err := extras.InviteToChat(ctx, args.ChatID, args.UserIDs)
		if err != nil {
			return SafeError(err)
		}
		return Ok(invited)

	case "ban":
		if isEmpty(args.ChatID) || args.UserID == 0 {
			return Err("'ban' requires chat_id and user_id; pass unban=true to lift it")
		}
		changed, err := extras.BanUser(ctx, args.ChatID, args.UserID, args.Unban)
		if err != nil {
			return SafeError(err)
		}
		word := "banned"
		if args.Unban {
			word = "unbanned"
		}
		return Ok(Result{word: changed})

	case "permissions":
		if isEmpty(args.ChatID) || len(args.Permissions) == 0 {
			return Err("'permissions' requires chat_id and permissions, e.g. " +
				`{"send_messages": true, "send_media": false}`)
		}
		set, err := extras.SetPermissions(ctx, args.ChatID, args.Permissions)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"updated": set, "permissions": args.Permissions})

	case "slow_mode":
		if isEmpty(args.ChatID) {
			return Err("'slow_mode' requires chat_id; seconds=0 turns it off")
		}
		set, err := extras.SetSlowMode(ctx, args.ChatID, args.Seconds)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"updated": set, "seconds": args.Seconds})

	case "admins":
		if isEmpty(args.ChatID) {
			return Err("'admins' requires chat_id")
		}
		admins, err := extras.ListAdmins(ctx, args.ChatID, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"admins": admins, "count": len(admins)})

	case "banned":
		if isEmpty(args.ChatID) {
			return Err("'banned' requires chat_id")
		}
		banned, err := extras.ListBanned(ctx, args.ChatID, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"banned": banned, "count": len(banned)})

	case "recent_actions":
		if isEmpty(args.ChatID) {
			return Err("'recent_actions' requires chat_id")
		}
		events, err := extras.RecentActions(ctx, args.ChatID, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"events": events, "count": len(events)})

	case "photo":
		if isEmpty(args.ChatID) {
			return Err("'photo' requires chat_id; an empty path removes the picture")
		}
		set, err := extras.SetChatPhoto(ctx, args.ChatID, args.Path)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"updated": set, "removed": args.Path == ""})

	default:
		return unknownAction(args.Action, chatActions)
	}
}
