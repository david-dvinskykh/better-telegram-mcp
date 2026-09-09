package telegram

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// archiveFolder is the id of the "Archived" pseudo-folder every account has.
// Folder 0 is the ordinary chat list.
const archiveFolder = 1

// MuteChat silences a chat's notifications, or turns them back on.
func (u *UserBackend) MuteChat(ctx context.Context, chatID any, mute bool) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	settings := tg.InputPeerNotifySettings{}
	// Telegram spells "muted" as a mute-until far in the future, and "unmuted"
	// as one in the past; there is no boolean for it.
	if mute {
		settings.SetMuteUntil(int(time.Now().Add(365 * 24 * time.Hour).Unix()))
	} else {
		settings.SetMuteUntil(0)
	}
	return api.AccountUpdateNotifySettings(ctx, &tg.AccountUpdateNotifySettingsRequest{
		Peer:     &tg.InputNotifyPeer{Peer: peer},
		Settings: settings,
	})
}

// ArchiveChat moves a chat into the archive, or back out of it.
func (u *UserBackend) ArchiveChat(ctx context.Context, chatID any, archive bool) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	folder := 0
	if archive {
		folder = archiveFolder
	}
	if _, err := api.FoldersEditPeerFolders(ctx, []tg.InputFolderPeer{{
		Peer:     peer,
		FolderID: folder,
	}}); err != nil {
		return false, err
	}
	return true, nil
}

// ResolveUsername turns a @name into the chat behind it, without joining or
// messaging anything.
func (u *UserBackend) ResolveUsername(ctx context.Context, username string) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(strings.TrimSpace(username), "@")
	if name == "" {
		return nil, errors.New("'resolve' requires username")
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: name,
	})
	if err != nil {
		return nil, err
	}
	u.rememberPeers(ctx, resolved.Users, resolved.Chats)

	out := Map{"username": name, "id": peerID(resolved.Peer)}
	for _, item := range resolved.Users {
		if user, ok := item.(*tg.User); ok && user.ID == peerID(resolved.Peer) {
			out["type"] = "user"
			if user.Bot {
				out["type"] = "bot"
			}
			out["name"] = strings.TrimSpace(user.FirstName + " " + user.LastName)
		}
	}
	for _, item := range resolved.Chats {
		switch chat := item.(type) {
		case *tg.Chat:
			out["type"] = "group"
			out["name"] = chat.Title
		case *tg.Channel:
			out["type"] = "channel"
			if chat.Megagroup {
				out["type"] = "supergroup"
			}
			out["name"] = chat.Title
			out["participants"] = chat.ParticipantsCount
		}
	}
	return out, nil
}

// SearchPublic finds public chats, channels and users by name.
func (u *UserBackend) SearchPublic(ctx context.Context, query string, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("'search_public' requires query")
	}
	found, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{
		Q:     query,
		Limit: limitOr(limit, 20, 50),
	})
	if err != nil {
		return nil, err
	}

	u.rememberPeers(ctx, found.Users, found.Chats)

	out := []Map{}
	for _, item := range found.Users {
		if user, ok := item.(*tg.User); ok {
			entry := serializeUser(user)
			entry["type"] = "user"
			if user.Bot {
				entry["type"] = "bot"
			}
			out = append(out, entry)
		}
	}
	for _, item := range found.Chats {
		switch chat := item.(type) {
		case *tg.Chat:
			out = append(out, Map{"id": -chat.ID, "title": chat.Title, "type": "group"})
		case *tg.Channel:
			kind := "channel"
			if chat.Megagroup {
				kind = "supergroup"
			}
			out = append(out, Map{
				"id":       -1_000_000_000_000 - chat.ID,
				"title":    chat.Title,
				"username": emptyToNil(chat.Username),
				"type":     kind,
			})
		}
	}
	return out, nil
}

// FullChat returns the detail a chat list entry leaves out: the description,
// the member count, and the pinned message.
func (u *UserBackend) FullChat(ctx context.Context, chatID any) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}

	out := Map{}
	switch typed := peer.(type) {
	case *tg.InputPeerChannel:
		full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{
			ChannelID: typed.ChannelID, AccessHash: typed.AccessHash,
		})
		if err != nil {
			return nil, err
		}
		info, ok := full.FullChat.(*tg.ChannelFull)
		if !ok {
			return nil, errors.New("Telegram returned an unexpected chat type.")
		}
		out["id"] = -1_000_000_000_000 - info.ID
		out["about"] = emptyToNil(info.About)
		out["members"] = info.ParticipantsCount
		out["admins"] = info.AdminsCount
		out["online"] = info.OnlineCount
		out["slow_mode_seconds"] = info.SlowmodeSeconds
		if pinned, ok := info.GetPinnedMsgID(); ok {
			out["pinned_message_id"] = pinned
		}
		out["title"] = chatTitle(full.Chats, info.ID)

	case *tg.InputPeerChat:
		full, err := api.MessagesGetFullChat(ctx, typed.ChatID)
		if err != nil {
			return nil, err
		}
		info, ok := full.FullChat.(*tg.ChatFull)
		if !ok {
			return nil, errors.New("Telegram returned an unexpected chat type.")
		}
		out["id"] = -info.ID
		out["about"] = emptyToNil(info.About)
		if participants, ok := info.Participants.(*tg.ChatParticipants); ok {
			out["members"] = len(participants.Participants)
		}
		if pinned, ok := info.GetPinnedMsgID(); ok {
			out["pinned_message_id"] = pinned
		}
		out["title"] = chatTitle(full.Chats, info.ID)

	default:
		// A one-to-one conversation has no chat record; the person is the chat.
		return u.GetUserInfo(ctx, chatID)
	}
	return out, nil
}

func chatTitle(chats []tg.ChatClass, id int64) string {
	for _, item := range chats {
		switch chat := item.(type) {
		case *tg.Chat:
			if chat.ID == id {
				return chat.Title
			}
		case *tg.Channel:
			if chat.ID == id {
				return chat.Title
			}
		}
	}
	return ""
}

// CommonChats lists the groups and channels this account shares with someone.
func (u *UserBackend) CommonChats(ctx context.Context, userID int64, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	user, err := u.inputUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesGetCommonChats(ctx, &tg.MessagesGetCommonChatsRequest{
		UserID: user,
		Limit:  limitOr(limit, 50, 100),
	})
	if err != nil {
		return nil, err
	}

	u.rememberPeers(ctx, nil, chatsOf(result))

	out := []Map{}
	for _, item := range chatsOf(result) {
		switch chat := item.(type) {
		case *tg.Chat:
			out = append(out, Map{"id": -chat.ID, "title": chat.Title, "type": "group"})
		case *tg.Channel:
			kind := "channel"
			if chat.Megagroup {
				kind = "supergroup"
			}
			out = append(out, Map{
				"id": -1_000_000_000_000 - chat.ID, "title": chat.Title, "type": kind,
			})
		}
	}
	return out, nil
}

func chatsOf(result tg.MessagesChatsClass) []tg.ChatClass {
	switch typed := result.(type) {
	case *tg.MessagesChats:
		return typed.Chats
	case *tg.MessagesChatsSlice:
		return typed.Chats
	default:
		return nil
	}
}

// ReadBy reports which members have read a message. Telegram only answers this
// for small groups and only for recent messages.
func (u *UserBackend) ReadBy(ctx context.Context, chatID any, messageID int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	participants, err := api.MessagesGetMessageReadParticipants(ctx,
		&tg.MessagesGetMessageReadParticipantsRequest{Peer: peer, MsgID: messageID})
	if err != nil {
		return nil, err
	}
	out := []Map{}
	for _, participant := range participants {
		out = append(out, Map{
			"user_id": participant.UserID,
			"date":    time.Unix(int64(participant.Date), 0).UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// InviteLink returns the chat's primary invite link, optionally replacing it.
// Revoking is how a link that has leaked is taken out of circulation.
func (u *UserBackend) InviteLink(ctx context.Context, chatID any, revoke bool) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	request := &tg.MessagesExportChatInviteRequest{Peer: peer}
	request.LegacyRevokePermanent = revoke
	exported, err := api.MessagesExportChatInvite(ctx, request)
	if err != nil {
		return nil, err
	}
	invite, ok := exported.(*tg.ChatInviteExported)
	if !ok {
		return nil, errors.New("Telegram did not return an invite link for that chat.")
	}
	return Map{"link": invite.Link, "revoked_previous": revoke}, nil
}

// ImportInvite joins a chat through an invite link.
func (u *UserBackend) ImportInvite(ctx context.Context, link string) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	hash := inviteHash(link)
	if hash == "" {
		return nil, errors.New("that does not look like a Telegram invite link")
	}
	result, err := api.MessagesImportChatInvite(ctx, hash)
	if err != nil {
		return nil, err
	}
	joined, ok := result.(*tg.MessagesChatInviteJoinResultOk)
	if !ok {
		// The other shape asks the joiner to complete a bot web view first,
		// which is a browser flow this server cannot drive.
		return nil, errors.New(
			"that invite needs to be accepted in a Telegram client: it opens a " +
				"bot's web form rather than joining directly")
	}
	return firstChat(joined.Updates, ""), nil
}

// InviteToChat adds people to a group or channel.
func (u *UserBackend) InviteToChat(ctx context.Context, chatID any, userIDs []int64) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return nil, errors.New("'invite' requires user_ids")
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}

	users := make([]tg.InputUserClass, 0, len(userIDs))
	for _, id := range userIDs {
		user, err := u.inputUser(ctx, id)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}

	switch typed := peer.(type) {
	case *tg.InputPeerChannel:
		if _, err := api.ChannelsInviteToChannel(ctx, &tg.ChannelsInviteToChannelRequest{
			Channel: &tg.InputChannel{ChannelID: typed.ChannelID, AccessHash: typed.AccessHash},
			Users:   users,
		}); err != nil {
			return nil, err
		}
	case *tg.InputPeerChat:
		for _, user := range users {
			if _, err := api.MessagesAddChatUser(ctx, &tg.MessagesAddChatUserRequest{
				ChatID: typed.ChatID,
				UserID: user,
				// No back history is shared: a person added to a group should
				// not silently receive what was said before they arrived.
				FwdLimit: 0,
			}); err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("only a group or channel can have people invited to it")
	}
	return Map{"invited": len(users)}, nil
}

// BanUser removes someone from a chat and keeps them out, or lifts that ban.
func (u *UserBackend) BanUser(ctx context.Context, chatID any, userID int64, unban bool) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	channel, err := u.inputChannel(ctx, chatID)
	if err != nil {
		return false, err
	}
	participant, err := u.resolvePeer(ctx, userID)
	if err != nil {
		return false, err
	}
	// ViewMessages is the flag that means "banned": with it set the account
	// cannot even see the chat. Clearing every flag is how a ban is lifted.
	rights := tg.ChatBannedRights{ViewMessages: !unban}
	if _, err := api.ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{
		Channel:      channel,
		Participant:  participant,
		BannedRights: rights,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// permissionFlags maps the names a caller uses to the restriction each one
// lifts. The MTProto field is a ban, so a permission being granted means the
// corresponding flag is cleared.
var permissionFlags = map[string]func(*tg.ChatBannedRights, bool){
	"send_messages": func(r *tg.ChatBannedRights, banned bool) { r.SendMessages = banned },
	"send_media":    func(r *tg.ChatBannedRights, banned bool) { r.SendMedia = banned },
	"send_stickers": func(r *tg.ChatBannedRights, banned bool) { r.SendStickers = banned },
	"send_gifs":     func(r *tg.ChatBannedRights, banned bool) { r.SendGifs = banned },
	"send_polls":    func(r *tg.ChatBannedRights, banned bool) { r.SendPolls = banned },
	"embed_links":   func(r *tg.ChatBannedRights, banned bool) { r.EmbedLinks = banned },
	"change_info":   func(r *tg.ChatBannedRights, banned bool) { r.ChangeInfo = banned },
	"invite_users":  func(r *tg.ChatBannedRights, banned bool) { r.InviteUsers = banned },
	"pin_messages":  func(r *tg.ChatBannedRights, banned bool) { r.PinMessages = banned },
	"manage_topics": func(r *tg.ChatBannedRights, banned bool) { r.ManageTopics = banned },
}

// PermissionNames lists the permissions SetPermissions understands.
func PermissionNames() []string {
	out := make([]string, 0, len(permissionFlags))
	for name := range permissionFlags {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// SetPermissions replaces what ordinary members of a chat may do. Every
// permission the caller does not name is granted, so the map read as a whole is
// the chat's new rule rather than a patch on the old one.
func (u *UserBackend) SetPermissions(ctx context.Context, chatID any, permissions map[string]bool) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	if len(permissions) == 0 {
		return false, fmt.Errorf("'permissions' requires at least one of: %s",
			strings.Join(PermissionNames(), ", "))
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}

	rights := tg.ChatBannedRights{}
	for name, allowed := range permissions {
		apply, ok := permissionFlags[name]
		if !ok {
			return false, fmt.Errorf("Unknown permission %q. Valid: %s",
				name, strings.Join(PermissionNames(), "|"))
		}
		apply(&rights, !allowed)
	}
	if _, err := api.MessagesEditChatDefaultBannedRights(ctx,
		&tg.MessagesEditChatDefaultBannedRightsRequest{Peer: peer, BannedRights: rights}); err != nil {
		return false, err
	}
	return true, nil
}

// SetSlowMode limits how often a member may post. Zero turns it off.
func (u *UserBackend) SetSlowMode(ctx context.Context, chatID any, seconds int) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	channel, err := u.inputChannel(ctx, chatID)
	if err != nil {
		return false, err
	}
	if _, err := api.ChannelsToggleSlowMode(ctx, &tg.ChannelsToggleSlowModeRequest{
		Channel: channel,
		Seconds: seconds,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// ListAdmins returns the administrators of a chat.
func (u *UserBackend) ListAdmins(ctx context.Context, chatID any, limit int) ([]Map, error) {
	return u.listParticipants(ctx, chatID, &tg.ChannelParticipantsAdmins{}, limit)
}

// ListBanned returns the accounts kept out of a chat.
func (u *UserBackend) ListBanned(ctx context.Context, chatID any, limit int) ([]Map, error) {
	return u.listParticipants(ctx, chatID, &tg.ChannelParticipantsKicked{}, limit)
}

func (u *UserBackend) listParticipants(ctx context.Context, chatID any, filter tg.ChannelParticipantsFilterClass, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	channel, err := u.inputChannel(ctx, chatID)
	if err != nil {
		return nil, err
	}
	result, err := api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
		Channel: channel,
		Filter:  filter,
		Limit:   limitOr(limit, 50, 200),
	})
	if err != nil {
		return nil, err
	}
	participants, ok := result.(*tg.ChannelsChannelParticipants)
	if !ok {
		return []Map{}, nil
	}
	u.rememberPeers(ctx, participants.Users, participants.Chats)
	return serializeUsers(participants.Users), nil
}

// RecentActions returns the administrative log of a chat: who was promoted,
// banned, or edited what, and when.
func (u *UserBackend) RecentActions(ctx context.Context, chatID any, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	channel, err := u.inputChannel(ctx, chatID)
	if err != nil {
		return nil, err
	}
	log, err := api.ChannelsGetAdminLog(ctx, &tg.ChannelsGetAdminLogRequest{
		Channel: channel,
		Limit:   limitOr(limit, 50, 100),
	})
	if err != nil {
		return nil, err
	}

	u.rememberPeers(ctx, log.Users, log.Chats)

	names := map[int64]string{}
	for _, item := range log.Users {
		if user, ok := item.(*tg.User); ok {
			names[user.ID] = strings.TrimSpace(user.FirstName + " " + user.LastName)
		}
	}
	out := []Map{}
	for _, event := range log.Events {
		out = append(out, Map{
			"id":     event.ID,
			"date":   time.Unix(int64(event.Date), 0).UTC().Format(time.RFC3339),
			"by_id":  event.UserID,
			"by":     emptyToNil(names[event.UserID]),
			"action": adminActionName(event.Action),
		})
	}
	return out, nil
}

// adminActionName renders the event type as a readable name. The MTProto
// constructors are named ChannelAdminLogEventAction<Something>, so the suffix
// is the answer.
func adminActionName(action tg.ChannelAdminLogEventActionClass) string {
	if action == nil {
		return "unknown"
	}
	name := fmt.Sprintf("%T", action)
	if index := strings.LastIndex(name, "ChannelAdminLogEventAction"); index >= 0 {
		name = name[index+len("ChannelAdminLogEventAction"):]
	}
	return snakeCase(name)
}

// snakeCase turns a Go type name into the spelling the rest of a result uses.
func snakeCase(name string) string {
	var out strings.Builder
	for index, r := range name {
		if r >= 'A' && r <= 'Z' {
			if index > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(r - 'A' + 'a')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// SetChatPhoto replaces a chat's picture, or removes it when path is empty.
func (u *UserBackend) SetChatPhoto(ctx context.Context, chatID any, path string) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}

	var photo tg.InputChatPhotoClass = &tg.InputChatPhotoEmpty{}
	if strings.TrimSpace(path) != "" {
		file, _, err := u.uploadLocal(ctx, path)
		if err != nil {
			return false, err
		}
		photo = &tg.InputChatUploadedPhoto{File: file}
	}

	switch typed := peer.(type) {
	case *tg.InputPeerChannel:
		_, err = api.ChannelsEditPhoto(ctx, &tg.ChannelsEditPhotoRequest{
			Channel: &tg.InputChannel{ChannelID: typed.ChannelID, AccessHash: typed.AccessHash},
			Photo:   photo,
		})
	case *tg.InputPeerChat:
		_, err = api.MessagesEditChatPhoto(ctx, &tg.MessagesEditChatPhotoRequest{
			ChatID: typed.ChatID,
			Photo:  photo,
		})
	default:
		return false, errors.New("only a group or channel has a chat photo")
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// inviteHash pulls the invite code out of the several shapes a link takes.
func inviteHash(link string) string {
	trimmed := strings.TrimSpace(link)
	for _, prefix := range []string{
		"https://t.me/joinchat/", "http://t.me/joinchat/", "t.me/joinchat/",
		"https://t.me/+", "http://t.me/+", "t.me/+", "+",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimPrefix(trimmed, prefix)
		}
	}
	// A bare hash is accepted as it is, which is what the join action already
	// takes elsewhere.
	if !strings.Contains(trimmed, "/") && trimmed != "" {
		return trimmed
	}
	return ""
}

// knownPermission reports whether a name is one SetPermissions understands.
func knownPermission(name string) bool {
	_, ok := permissionFlags[name]
	return ok
}
