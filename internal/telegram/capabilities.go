package telegram

import (
	"context"
	"time"
)

// The interfaces below carve the surface a Telegram account exposes into the
// domains the MCP tools are grouped by. They are deliberately optional: the Bot
// API serves only part of each one, so a backend implements what it can and the
// dispatch layer answers the rest with the ModeError that says which mode would.
//
// The alternative -- one Backend interface with every method on it -- would put
// several dozen stub methods on BotBackend whose only job is to return the same
// error, which buries the handful of actions the Bot API genuinely does serve.

// ProfileBackend covers the account behind the session: who it is, how it looks
// to others, and what it discloses.
type ProfileBackend interface {
	GetMe(ctx context.Context) (Map, error)
	UpdateProfile(ctx context.Context, firstName, lastName, about *string) (Map, error)
	SetProfilePhoto(ctx context.Context, path string) (Map, error)
	DeleteProfilePhoto(ctx context.Context) (bool, error)
	GetPrivacy(ctx context.Context, key string) (Map, error)
	SetPrivacy(ctx context.Context, key, rule string) (Map, error)
	GetUserInfo(ctx context.Context, user any) (Map, error)
	GetUserPhotos(ctx context.Context, user any, limit int) ([]Map, error)
}

// FolderBackend covers the chat folders (dialog filters) a user account keeps.
// The Bot API has no equivalent at all.
type FolderBackend interface {
	ListFolders(ctx context.Context) ([]Map, error)
	GetFolder(ctx context.Context, id int) (Map, error)
	CreateFolder(ctx context.Context, title string, chats []any) (Map, error)
	DeleteFolder(ctx context.Context, id int) (bool, error)
	SetFolderChat(ctx context.Context, id int, chat any, remove bool) (Map, error)
	ReorderFolders(ctx context.Context, order []int) (bool, error)
}

// MessageExtras is everything the `message` tool does beyond the send/edit/
// delete core that both modes share.
type MessageExtras interface {
	UnpinMessage(ctx context.Context, chatID any, messageID int) (bool, error)
	UnpinAllMessages(ctx context.Context, chatID any) (bool, error)
	MarkRead(ctx context.Context, chatID any, maxID int) (bool, error)
	ListPinned(ctx context.Context, chatID any, limit int) ([]Map, error)
	MessageContext(ctx context.Context, chatID any, messageID, around int) ([]Map, error)
	MessageLink(ctx context.Context, chatID any, messageID int) (Map, error)
	SearchGlobal(ctx context.Context, query string, limit int) ([]Map, error)
	ListReactions(ctx context.Context, chatID any, messageID, limit int) ([]Map, error)
	DeleteMessages(ctx context.Context, chatID any, ids []int) (int, error)
	PurgeHistory(ctx context.Context, chatID any, revoke bool) (Map, error)
	SendPoll(ctx context.Context, chatID any, question string, options []string, opts PollOptions) (Map, error)
	SaveDraft(ctx context.Context, chatID any, text string) (bool, error)
	ListDrafts(ctx context.Context) ([]Map, error)
	ScheduleMessage(ctx context.Context, chatID any, text string, at time.Time) (Map, error)
	ListScheduled(ctx context.Context, chatID any) ([]Map, error)
	CancelScheduled(ctx context.Context, chatID any, ids []int) (bool, error)
	ForwardMessages(ctx context.Context, fromChat, toChat any, ids []int) ([]Map, error)
}

// ChatExtras is everything the `chat` tool does beyond the list/info/membership
// core.
type ChatExtras interface {
	MuteChat(ctx context.Context, chatID any, mute bool) (bool, error)
	ArchiveChat(ctx context.Context, chatID any, archive bool) (bool, error)
	ResolveUsername(ctx context.Context, username string) (Map, error)
	SearchPublic(ctx context.Context, query string, limit int) ([]Map, error)
	FullChat(ctx context.Context, chatID any) (Map, error)
	CommonChats(ctx context.Context, userID int64, limit int) ([]Map, error)
	ReadBy(ctx context.Context, chatID any, messageID int) ([]Map, error)
	InviteLink(ctx context.Context, chatID any, revoke bool) (Map, error)
	ImportInvite(ctx context.Context, link string) (Map, error)
	InviteToChat(ctx context.Context, chatID any, userIDs []int64) (Map, error)
	BanUser(ctx context.Context, chatID any, userID int64, unban bool) (bool, error)
	SetPermissions(ctx context.Context, chatID any, permissions map[string]bool) (bool, error)
	SetSlowMode(ctx context.Context, chatID any, seconds int) (bool, error)
	ListAdmins(ctx context.Context, chatID any, limit int) ([]Map, error)
	ListBanned(ctx context.Context, chatID any, limit int) ([]Map, error)
	RecentActions(ctx context.Context, chatID any, limit int) ([]Map, error)
	SetChatPhoto(ctx context.Context, chatID any, path string) (bool, error)
}

// MediaExtras is everything the `media` tool does beyond send/download.
type MediaExtras interface {
	SendAlbum(ctx context.Context, chatID any, paths []string, caption string) ([]Map, error)
	SendVoice(ctx context.Context, chatID any, path string) (Map, error)
	SendSticker(ctx context.Context, chatID any, sticker string) (Map, error)
	ListStickerSets(ctx context.Context) ([]Map, error)
	SearchGIFs(ctx context.Context, query string, limit int) ([]Map, error)
	SendGIF(ctx context.Context, chatID any, gif, caption string) (Map, error)
	MediaInfo(ctx context.Context, chatID any, messageID int) (Map, error)
	ListPhotos(ctx context.Context, chatID any, limit int) ([]Map, error)
}

// ContactExtras is everything the `contact` tool does beyond list/search/add/
// block.
type ContactExtras interface {
	DeleteContact(ctx context.Context, userID int64) (bool, error)
	ImportContacts(ctx context.Context, entries []ContactEntry) (Map, error)
	ExportContacts(ctx context.Context) ([]Map, error)
	ListBlocked(ctx context.Context, limit int) ([]Map, error)
	LastInteraction(ctx context.Context, userID int64) (Map, error)
	SendContactCard(ctx context.Context, chatID any, userID int64) (Map, error)
	DirectChat(ctx context.Context, query string) (Map, error)
}

// PollOptions carries the optional arguments of a poll.
type PollOptions struct {
	MultipleChoice bool
	Anonymous      bool
	Quiz           bool
	CorrectOption  int
}

// ContactEntry is one row of a bulk contact import.
type ContactEntry struct {
	Phone     string `json:"phone"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
}

// The capability accessors below turn "this backend cannot do that" into the
// ModeError a caller can act on, so every dispatch site reads the same way.

func Profile(backend Backend) (ProfileBackend, error) {
	if typed, ok := backend.(ProfileBackend); ok {
		return typed, nil
	}
	return nil, NeedsUser()
}

func Folders(backend Backend) (FolderBackend, error) {
	if typed, ok := backend.(FolderBackend); ok {
		return typed, nil
	}
	return nil, NeedsUser()
}

func Messages(backend Backend) (MessageExtras, error) {
	if typed, ok := backend.(MessageExtras); ok {
		return typed, nil
	}
	return nil, NeedsUser()
}

func Chats(backend Backend) (ChatExtras, error) {
	if typed, ok := backend.(ChatExtras); ok {
		return typed, nil
	}
	return nil, NeedsUser()
}

func Media(backend Backend) (MediaExtras, error) {
	if typed, ok := backend.(MediaExtras); ok {
		return typed, nil
	}
	return nil, NeedsUser()
}

func Contacts(backend Backend) (ContactExtras, error) {
	if typed, ok := backend.(ContactExtras); ok {
		return typed, nil
	}
	return nil, NeedsUser()
}
