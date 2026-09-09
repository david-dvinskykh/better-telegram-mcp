package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// A bot administers chats it has been made an admin of, so the moderation half
// of the chat surface works here. What it cannot do is anything belonging to a
// person's own chat list -- muting, archiving, folder membership -- or the
// global directory a user account can search.

func (b *BotBackend) BanUser(ctx context.Context, chatID any, userID int64, unban bool) (bool, error) {
	method := "banChatMember"
	params := Map{"chat_id": chatID, "user_id": userID}
	if unban {
		method = "unbanChatMember"
		// Without this an unban also adds the account back to the chat, which
		// is not what lifting a ban means.
		params["only_if_banned"] = true
	}
	return b.callBool(ctx, method, params)
}

func (b *BotBackend) SetPermissions(ctx context.Context, chatID any, permissions map[string]bool) (bool, error) {
	if len(permissions) == 0 {
		return false, fmt.Errorf("'permissions' requires at least one of: %s",
			strings.Join(PermissionNames(), ", "))
	}
	// The Bot API names its permissions differently from MTProto, so the tool's
	// vocabulary is translated here rather than leaking into the tool.
	botNames := map[string]string{
		"send_messages": "can_send_messages",
		"send_media":    "can_send_other_messages",
		"send_stickers": "can_send_other_messages",
		"send_gifs":     "can_send_other_messages",
		"send_polls":    "can_send_polls",
		"embed_links":   "can_add_web_page_previews",
		"change_info":   "can_change_info",
		"invite_users":  "can_invite_users",
		"pin_messages":  "can_pin_messages",
		"manage_topics": "can_manage_topics",
	}
	granted := Map{}
	for name, allowed := range permissions {
		key, ok := botNames[name]
		if !ok {
			return false, fmt.Errorf("Unknown permission %q. Valid: %s",
				name, strings.Join(PermissionNames(), "|"))
		}
		granted[key] = allowed
	}
	return b.callBool(ctx, "setChatPermissions", Map{
		"chat_id":     chatID,
		"permissions": granted,
	})
}

func (b *BotBackend) InviteLink(ctx context.Context, chatID any, revoke bool) (Map, error) {
	if revoke {
		// createChatInviteLink makes an additional link; only the primary one
		// can be replaced, and exportChatInviteLink is what replaces it.
		raw, err := b.call(ctx, "exportChatInviteLink", Map{"chat_id": chatID})
		if err != nil {
			return nil, err
		}
		var link string
		if err := json.Unmarshal(raw, &link); err != nil {
			return nil, err
		}
		return Map{"link": link, "revoked_previous": true}, nil
	}
	chat, err := b.callMap(ctx, "getChat", Map{"chat_id": chatID})
	if err != nil {
		return nil, err
	}
	if link, ok := chat["invite_link"].(string); ok && link != "" {
		return Map{"link": link, "revoked_previous": false}, nil
	}
	return nil, errors.New(
		"that chat has no invite link yet; call this action with revoke=true to " +
			"have the bot create one")
}

func (b *BotBackend) ListAdmins(ctx context.Context, chatID any, _ int) ([]Map, error) {
	raw, err := b.call(ctx, "getChatAdministrators", Map{"chat_id": chatID})
	if err != nil {
		return nil, err
	}
	var admins []struct {
		Status string `json:"status"`
		User   Map    `json:"user"`
	}
	if err := json.Unmarshal(raw, &admins); err != nil {
		return nil, err
	}
	out := make([]Map, 0, len(admins))
	for _, admin := range admins {
		entry := Map{"status": admin.Status}
		for key, value := range admin.User {
			entry[key] = value
		}
		out = append(out, entry)
	}
	return out, nil
}

func (b *BotBackend) SetChatPhoto(ctx context.Context, chatID any, path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return b.callBool(ctx, "deleteChatPhoto", Map{"chat_id": chatID})
	}
	return b.uploadChatPhoto(ctx, chatID, path)
}

func (b *BotBackend) FullChat(ctx context.Context, chatID any) (Map, error) {
	// getChat is already the full record on the Bot API side: there is no
	// shorter form to contrast it with.
	return b.callMap(ctx, "getChat", Map{"chat_id": chatID})
}

// The rest belongs to a person's account, not to a bot.

func (b *BotBackend) MuteChat(context.Context, any, bool) (bool, error) { return false, NeedsUser() }

func (b *BotBackend) ArchiveChat(context.Context, any, bool) (bool, error) {
	return false, NeedsUser()
}

func (b *BotBackend) ResolveUsername(context.Context, string) (Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) SearchPublic(context.Context, string, int) ([]Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) CommonChats(context.Context, int64, int) ([]Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) ReadBy(context.Context, any, int) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) ImportInvite(context.Context, string) (Map, error) {
	return nil, errors.New(
		"a bot cannot join a chat by invite link; an administrator has to add it")
}

func (b *BotBackend) InviteToChat(context.Context, any, []int64) (Map, error) {
	return nil, errors.New(
		"a bot cannot add members to a chat; share its invite link instead")
}

func (b *BotBackend) SetSlowMode(context.Context, any, int) (bool, error) {
	return false, NeedsUser()
}

func (b *BotBackend) ListBanned(context.Context, any, int) ([]Map, error) {
	return nil, NeedsUser()
}

func (b *BotBackend) RecentActions(context.Context, any, int) ([]Map, error) {
	return nil, NeedsUser()
}

// uploadChatPhoto posts a new picture for a chat the bot administers.
func (b *BotBackend) uploadChatPhoto(ctx context.Context, chatID any, path string) (bool, error) {
	filename, content, err := readUpload(path)
	if err != nil {
		return false, err
	}
	if _, err := b.callForm(ctx, "setChatPhoto", "photo", filename, content,
		Map{"chat_id": chatID}); err != nil {
		return false, err
	}
	return true, nil
}
