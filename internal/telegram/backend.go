// Package telegram holds the two backends the server can talk through: the
// Bot API over HTTP and a user account over MTProto.
package telegram

import (
	"context"
	"fmt"
)

// Mode is the API a backend speaks.
type Mode string

const (
	ModeBot  Mode = "bot"
	ModeUser Mode = "user"
)

// ModeError is returned when an action exists only in the other mode. Its text
// says how to get that mode rather than only what is missing.
type ModeError struct{ Required Mode }

func (e *ModeError) Error() string {
	switch e.Required {
	case ModeUser:
		return "This action requires user mode. " +
			"Set TELEGRAM_API_ID + TELEGRAM_API_HASH + TELEGRAM_PHONE."
	case ModeBot:
		return "This action requires bot mode. Set TELEGRAM_BOT_TOKEN " +
			"(inline buttons and callback queries exist only in the Bot API)."
	default:
		return fmt.Sprintf("This action requires %s mode.", e.Required)
	}
}

// NeedsUser and NeedsBot build the two ModeErrors the backends raise.
func NeedsUser() error { return &ModeError{Required: ModeUser} }
func NeedsBot() error  { return &ModeError{Required: ModeBot} }

// Map is one serialized Telegram object on its way into a tool result.
type Map = map[string]any

// Backend is everything the MCP tools need from Telegram. A method an
// implementation cannot serve returns a ModeError rather than a zero value, so
// the caller is told which mode would serve it.
type Backend interface {
	Mode() Mode

	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	IsConnected() bool
	IsAuthorized(ctx context.Context) bool
	ClearCache(ctx context.Context) error

	// Messages
	SendMessage(ctx context.Context, chatID any, text string, opts SendOptions) (Map, error)
	EditMessage(ctx context.Context, chatID any, messageID int, text string, opts EditOptions) (Map, error)
	DeleteMessage(ctx context.Context, chatID any, messageID int) (bool, error)
	ForwardMessage(ctx context.Context, fromChat, toChat any, messageID int) (Map, error)
	PinMessage(ctx context.Context, chatID any, messageID int) (bool, error)
	ReactToMessage(ctx context.Context, chatID any, messageID int, emoji string) (bool, error)
	SearchMessages(ctx context.Context, query string, chatID any, limit int) ([]Map, error)
	GetHistory(ctx context.Context, chatID any, limit int, offsetID int) ([]Map, error)

	// Inline buttons and callback queries (bot mode only)
	EditMessageButtons(ctx context.Context, chatID any, messageID int, buttons any) (Map, error)
	GetCallbackQueries(ctx context.Context, sinceID *int, limit int, consume bool) ([]Map, error)
	AnswerCallbackQuery(ctx context.Context, queryID string, text string, showAlert bool) (bool, error)
	RecordResumeSession(ctx context.Context, chatID any, messageID int, sessionID string) (bool, error)
	LookupResumeSession(ctx context.Context, chatID any, messageID int) (string, error)

	// Chats
	ListChats(ctx context.Context, limit int) ([]Map, error)
	GetChatInfo(ctx context.Context, chatID any) (Map, error)
	CreateChat(ctx context.Context, title string, isChannel bool) (Map, error)
	JoinChat(ctx context.Context, linkOrHash string) (bool, error)
	LeaveChat(ctx context.Context, chatID any) (bool, error)
	GetMembers(ctx context.Context, chatID any, limit int) ([]Map, error)
	PromoteAdmin(ctx context.Context, chatID any, userID int64, demote bool) (bool, error)
	UpdateChatSettings(ctx context.Context, chatID any, title, description *string) (bool, error)
	ManageTopics(ctx context.Context, chatID any, action string, opts TopicOptions) (Map, error)

	// Media
	SendMedia(ctx context.Context, chatID any, mediaType, pathOrURL string, caption string) (Map, error)
	DownloadMedia(ctx context.Context, chatID any, messageID int, fileID, outputDir string) (string, error)

	// Contacts
	ListContacts(ctx context.Context) ([]Map, error)
	SearchContacts(ctx context.Context, query string) ([]Map, error)
	AddContact(ctx context.Context, phone, firstName, lastName string) (bool, error)
	BlockUser(ctx context.Context, userID int64, unblock bool) (bool, error)
}

// SendOptions carries the optional arguments of a send.
type SendOptions struct {
	ReplyTo   int
	ParseMode string
	// Buttons is the compact inline-keyboard shape, or nil when the caller
	// sent none. A nil keeps the request identical to a plain text send.
	Buttons any
}

// EditOptions carries the optional arguments of an edit.
type EditOptions struct {
	ParseMode string
	Buttons   any
}

// TopicOptions carries the arguments of the forum-topic actions.
type TopicOptions struct {
	TopicID int
	Name    string
	Limit   int
}

// Authenticator is implemented by a backend whose credentials are established
// interactively: MTProto needs a code, and possibly a 2FA password.
type Authenticator interface {
	SendCode(ctx context.Context, phone string) error
	SignIn(ctx context.Context, phone, code, password string) (Map, error)
	LogOut(ctx context.Context) (bool, error)
}
