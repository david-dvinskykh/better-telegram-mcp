package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/aliases"
	"github.com/david-dvinskykh/better-telegram-mcp/internal/security"
)

const (
	// DefaultAPIBase is Telegram's own Bot API host. A self-hosted Bot API
	// server (https://core.telegram.org/bots/api#using-a-local-bot-api-server)
	// replaces it via BotOptions.APIBase.
	DefaultAPIBase = "https://api.telegram.org"

	apiPath  = "%s/bot%s/"
	filePath = "%s/file/bot%s/"

	// getUpdates caps limit at 100 (https://core.telegram.org/bots/api#getupdates).
	maxUpdatesPerCall = 100
	// Bot API upload/download ceiling for a bot.
	maxFileBytes = 50 * 1024 * 1024
	// Keep the resume map bounded; only the newest entry per message wins.
	maxResumeEntries = 500
)

// sessionIDRe is the only shape accepted into the resume map: the value is
// handed to a process that resumes that Claude session.
var sessionIDRe = regexp.MustCompile(`^(session|cse)_[A-Za-z0-9]{10,48}$`)

// APIError is a Bot API call that came back with ok:false, or a transport
// failure. The bot token is redacted on the way in: httpx-style transport
// errors quote the request URL, which carries it.
type APIError struct {
	Description string
	Code        int
}

func (e *APIError) Error() string { return e.Description }

func newAPIError(description string, code int) *APIError {
	return &APIError{Description: security.RedactBotToken(description), Code: code}
}

// BotBackend talks to the Telegram Bot API over HTTP.
type BotBackend struct {
	token       string
	baseURL     string
	fileBaseURL string
	client      *http.Client
	connected   bool
	botInfo     Map

	cursorPath    string
	queuePath     string
	resumeMapPath string
	aliases       *aliases.Store

	mu             sync.Mutex
	callbackOffset *int
	cursorLoaded   bool
}

// BotOptions configures where the callback cursor, the press queue and the
// resume map live, and which Bot API host to talk to. All are optional.
type BotOptions struct {
	CursorPath    string
	QueuePath     string
	ResumeMapPath string
	AliasesPath   string
	// APIBase overrides DefaultAPIBase, e.g. to reach a self-hosted Bot API
	// server. Empty means Telegram's own host.
	APIBase string
}

// NewBotBackend builds a Bot API backend for the given token.
func NewBotBackend(token string, opts BotOptions) *BotBackend {
	host := opts.APIBase
	if host == "" {
		host = DefaultAPIBase
	}
	host = strings.TrimRight(host, "/")
	return &BotBackend{
		token:         token,
		baseURL:       fmt.Sprintf(apiPath, host, token),
		fileBaseURL:   fmt.Sprintf(filePath, host, token),
		client:        &http.Client{Timeout: 30 * time.Second},
		cursorPath:    opts.CursorPath,
		queuePath:     opts.QueuePath,
		resumeMapPath: opts.ResumeMapPath,
		aliases:       aliases.New(opts.AliasesPath),
	}
}

// Aliases exposes the local name map so the contact tool can edit it.
func (b *BotBackend) Aliases() *aliases.Store { return b.aliases }

func (b *BotBackend) Mode() Mode { return ModeBot }

// --- transport ---

// call posts a Bot API method with a JSON body, dropping nil parameters so a
// method sees exactly the fields the caller set.
func (b *BotBackend) call(ctx context.Context, method string, params Map) (json.RawMessage, error) {
	body := Map{}
	for key, value := range params {
		if value == nil {
			continue
		}
		// A chat named the way a person says it resolves from the local alias
		// map. Doing it here covers every method at once, and the Bot API sees
		// only ids and usernames, which is all it understands.
		if key == "chat_id" || key == "from_chat_id" {
			value = b.applyAlias(value)
		}
		body[key] = value
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+method, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return b.do(req)
}

// callForm posts a Bot API method as multipart, which is how a file is uploaded.
func (b *BotBackend) callForm(
	ctx context.Context, method string, field, filename string, content []byte, params Map,
) (json.RawMessage, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range params {
		if value == nil {
			continue
		}
		if key == "chat_id" || key == "from_chat_id" {
			value = b.applyAlias(value)
		}
		if err := writer.WriteField(key, fmt.Sprint(value)); err != nil {
			return nil, err
		}
	}
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(content); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+method, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return b.do(req)
}

// uploadedFile is one attachment of a multi-file form.
type uploadedFile struct {
	Field    string
	Filename string
	Content  []byte
}

// callFormFiles posts a Bot API method with several attachments at once, which
// is what sendMediaGroup needs: the media array references each part by the
// field name it was uploaded under.
func (b *BotBackend) callFormFiles(
	ctx context.Context, method string, files []uploadedFile, params Map,
) (json.RawMessage, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range params {
		if value == nil {
			continue
		}
		if key == "chat_id" || key == "from_chat_id" {
			value = b.applyAlias(value)
		}
		if err := writer.WriteField(key, fmt.Sprint(value)); err != nil {
			return nil, err
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile(file.Field, file.Filename)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(file.Content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+method, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return b.do(req)
}

func (b *BotBackend) do(req *http.Request) (json.RawMessage, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		// The URL in a transport error carries the bot token.
		return nil, newAPIError(err.Error(), 0)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newAPIError(err.Error(), resp.StatusCode)
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, newAPIError("Unexpected Bot API response", resp.StatusCode)
	}
	if !envelope.OK {
		description := envelope.Description
		if description == "" {
			description = "Unknown error"
		}
		code := envelope.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return nil, newAPIError(description, code)
	}
	return envelope.Result, nil
}

// callMap runs a method and decodes its result as an object.
func (b *BotBackend) callMap(ctx context.Context, method string, params Map) (Map, error) {
	raw, err := b.call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	var out Map
	if len(raw) == 0 {
		return Map{}, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Map{}, nil
	}
	return out, nil
}

// callBool runs a method whose result is a plain true.
func (b *BotBackend) callBool(ctx context.Context, method string, params Map) (bool, error) {
	raw, err := b.call(ctx, method, params)
	if err != nil {
		return false, err
	}
	var ok bool
	if err := json.Unmarshal(raw, &ok); err != nil {
		// A method that answers with an object (sendMessage-shaped) still
		// succeeded; the caller only asked whether it worked.
		return true, nil
	}
	return ok, nil
}

// --- connection ---

func (b *BotBackend) Connect(ctx context.Context) error {
	info, err := b.callMap(ctx, "getMe", nil)
	if err != nil {
		if strings.Contains(err.Error(), "Unauthorized") {
			return newAPIError(
				"Invalid bot token. Get a new one from @BotFather: https://t.me/BotFather", 401)
		}
		return fmt.Errorf("failed to connect to Bot API: %w", err)
	}
	b.botInfo = info
	b.connected = true
	return nil
}

func (b *BotBackend) Disconnect(context.Context) error {
	b.client.CloseIdleConnections()
	b.connected = false
	return nil
}

func (b *BotBackend) IsConnected() bool                 { return b.connected }
func (b *BotBackend) IsAuthorized(context.Context) bool { return b.connected }
func (b *BotBackend) ClearCache(context.Context) error  { return nil } // the Bot API is stateless
func (b *BotBackend) BotInfo() Map                      { return b.botInfo }

// --- messages ---

func (b *BotBackend) SendMessage(ctx context.Context, chatID any, text string, opts SendOptions) (Map, error) {
	markup, err := optionalKeyboard(opts.Buttons)
	if err != nil {
		return nil, err
	}
	return b.callMap(ctx, "sendMessage", Map{
		"chat_id":             chatID,
		"text":                text,
		"reply_to_message_id": optionalInt(opts.ReplyTo),
		"parse_mode":          optionalString(opts.ParseMode),
		"reply_markup":        markup,
	})
}

func (b *BotBackend) EditMessage(ctx context.Context, chatID any, messageID int, text string, opts EditOptions) (Map, error) {
	markup, err := optionalKeyboard(opts.Buttons)
	if err != nil {
		return nil, err
	}
	return b.callMap(ctx, "editMessageText", Map{
		"chat_id":      chatID,
		"message_id":   messageID,
		"text":         text,
		"parse_mode":   optionalString(opts.ParseMode),
		"reply_markup": markup,
	})
}

func (b *BotBackend) DeleteMessage(ctx context.Context, chatID any, messageID int) (bool, error) {
	return b.callBool(ctx, "deleteMessage", Map{"chat_id": chatID, "message_id": messageID})
}

func (b *BotBackend) ForwardMessage(ctx context.Context, fromChat, toChat any, messageID int) (Map, error) {
	return b.callMap(ctx, "forwardMessage", Map{
		"chat_id":      toChat,
		"from_chat_id": fromChat,
		"message_id":   messageID,
	})
}

func (b *BotBackend) PinMessage(ctx context.Context, chatID any, messageID int) (bool, error) {
	return b.callBool(ctx, "pinChatMessage", Map{"chat_id": chatID, "message_id": messageID})
}

func (b *BotBackend) ReactToMessage(ctx context.Context, chatID any, messageID int, emoji string) (bool, error) {
	return b.callBool(ctx, "setMessageReaction", Map{
		"chat_id":    chatID,
		"message_id": messageID,
		"reaction":   []Map{{"type": "emoji", "emoji": emoji}},
	})
}

// SearchMessages has no Bot API equivalent: a bot cannot search a chat it does
// not own the history of.
func (b *BotBackend) SearchMessages(context.Context, string, any, int) ([]Map, error) {
	return nil, NeedsUser()
}

// GetHistory returns nothing rather than an error: the Bot API genuinely has no
// history to read, and a caller browsing a chat learns that from the empty list.
func (b *BotBackend) GetHistory(context.Context, any, int, int) ([]Map, error) {
	return []Map{}, nil
}

// --- inline buttons and callback queries ---

func (b *BotBackend) EditMessageButtons(ctx context.Context, chatID any, messageID int, buttons any) (Map, error) {
	if buttons == nil {
		buttons = []any{}
	}
	keyboard, err := BuildInlineKeyboard(buttons)
	if err != nil {
		return nil, err
	}
	return b.callMap(ctx, "editMessageReplyMarkup", Map{
		"chat_id":      chatID,
		"message_id":   messageID,
		"reply_markup": keyboard,
	})
}

// GetCallbackQueries returns pending presses, oldest first.
//
// The backend owns the cursor: an update handed out here is confirmed with
// Telegram and never returned again, so a repeated call cannot replay a
// decision. With consume=false the cursor is left alone, so a watcher can look
// without taking the press away from whoever acts on it.
func (b *BotBackend) GetCallbackQueries(ctx context.Context, sinceID *int, limit int, consume bool) ([]Map, error) {
	limit = clamp(limit, 1, maxUpdatesPerCall)

	// One poll at a time: two concurrent getUpdates calls would hand the same
	// press to both callers (and Telegram rejects them anyway).
	b.mu.Lock()
	defer b.mu.Unlock()

	b.loadCursor()
	offset := b.callbackOffset
	if sinceID != nil {
		// A caller-supplied cursor may only move forward: an older since_id
		// must not replay decisions already handed out.
		next := *sinceID + 1
		if offset == nil || next > *offset {
			offset = &next
		}
	}

	if b.queuePath != "" {
		return b.readQueue(offset, limit, consume)
	}

	raw, err := b.call(ctx, "getUpdates", Map{
		"offset":          offsetValue(offset),
		"limit":           limit,
		"allowed_updates": []string{"callback_query"},
		"timeout":         0,
	})
	if err != nil {
		return nil, webhookHint(err)
	}
	var updates []Map
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, newAPIError("Unexpected getUpdates response", 0)
	}
	if len(updates) == 0 {
		return nil, nil
	}

	highest := -1
	for _, update := range updates {
		if id, ok := asInt(update["update_id"]); ok && id > highest {
			highest = id
		}
	}
	// Telegram drops an update only once a higher offset is confirmed, so
	// skipping that call is exactly what makes this read a peek.
	if consume && highest >= 0 {
		b.confirmUpdates(ctx, highest+1)
	}
	return onlyCallbacks(updates), nil
}

// readQueue reads presses from the JSONL queue written by whoever owns this
// bot's getUpdates stream (Telegram allows only one reader). Each line is a Bot
// API update object; the cursor is this backend's own, so a line is handed out
// only once.
func (b *BotBackend) readQueue(offset *int, limit int, consume bool) ([]Map, error) {
	raw, err := os.ReadFile(b.queuePath)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing queued yet: an empty queue, not an error.
		return nil, nil
	}
	if err != nil {
		return nil, newAPIError(
			fmt.Sprintf("Cannot read the callback queue at %s: %v", b.queuePath, err), 0)
	}

	var updates []Map
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var update Map
		if err := json.Unmarshal([]byte(line), &update); err != nil {
			slog.Warn("skipping malformed line in the callback queue")
			continue
		}
		id, ok := asInt(update["update_id"])
		if !ok || id < offsetValueInt(offset) {
			continue
		}
		updates = append(updates, update)
	}

	sort.Slice(updates, func(i, j int) bool {
		left, _ := asInt(updates[i]["update_id"])
		right, _ := asInt(updates[j]["update_id"])
		return left < right
	})
	if len(updates) > limit {
		updates = updates[:limit]
	}
	if len(updates) > 0 && consume {
		last, _ := asInt(updates[len(updates)-1]["update_id"])
		b.storeCursor(last + 1)
	}
	return onlyCallbacks(updates), nil
}

func (b *BotBackend) AnswerCallbackQuery(ctx context.Context, queryID, text string, showAlert bool) (bool, error) {
	return b.callBool(ctx, "answerCallbackQuery", Map{
		"callback_query_id": queryID,
		"text":              optionalString(text),
		"show_alert":        showAlert,
	})
}

// RecordResumeSession remembers which Claude session owns the buttons on a
// message, so whoever delivers the press can continue that session instead of
// starting a new one. It reports false when no map file is configured.
func (b *BotBackend) RecordResumeSession(_ context.Context, chatID any, messageID int, sessionID string) (bool, error) {
	if !sessionIDRe.MatchString(sessionID) {
		return false, fmt.Errorf(
			"resume_session must be a Claude session id like 'session_01AbCd...', got %q", sessionID)
	}
	if b.resumeMapPath == "" {
		return false, nil
	}
	entry, err := json.Marshal(Map{
		"chat_id":    chatID,
		"message_id": messageID,
		"session_id": sessionID,
	})
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(b.resumeMapPath), 0o700); err != nil {
		return false, err
	}
	file, err := os.OpenFile(b.resumeMapPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	if _, err := file.Write(append(entry, '\n')); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	b.trimResumeMap()
	return true, nil
}

func (b *BotBackend) trimResumeMap() {
	raw, err := os.ReadFile(b.resumeMapPath)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) <= maxResumeEntries {
		return
	}
	kept := strings.Join(lines[len(lines)-maxResumeEntries:], "\n") + "\n"
	if err := os.WriteFile(b.resumeMapPath, []byte(kept), 0o600); err != nil {
		slog.Warn("could not trim the resume map", "error", err)
	}
}

// LookupResumeSession returns the session registered for a message; the newest
// registration wins.
func (b *BotBackend) LookupResumeSession(_ context.Context, chatID any, messageID int) (string, error) {
	if b.resumeMapPath == "" {
		return "", nil
	}
	raw, err := os.ReadFile(b.resumeMapPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		slog.Warn("cannot read the resume map", "error", err)
		return "", nil
	}

	found := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry Map
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if fmt.Sprint(entry["chat_id"]) != fmt.Sprint(chatID) {
			continue
		}
		if id, ok := asInt(entry["message_id"]); !ok || id != messageID {
			continue
		}
		if sessionID, ok := entry["session_id"].(string); ok && sessionIDRe.MatchString(sessionID) {
			found = sessionID
		}
	}
	return found, nil
}

// webhookHint turns the two ways getUpdates can be taken away from this server
// into messages that name the cause. Both otherwise surface as an opaque Bot
// API error, and both mean the same thing: someone else owns the update stream.
func webhookHint(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	description := strings.ToLower(apiErr.Description)

	if strings.Contains(description, "webhook") {
		return newAPIError(
			"Cannot read callback queries while a webhook is active: "+apiErr.Description+
				". Delete the webhook (Bot API deleteWebhook) so this server can poll "+
				"getUpdates, or have the webhook receiver queue callback_query updates itself.",
			apiErr.Code)
	}
	// 409: Telegram allows one getUpdates consumer per bot, so a second server
	// (or a relay) on the same token silently takes every press.
	if apiErr.Code == 409 || strings.Contains(description, "terminated by other getupdates") {
		return newAPIError(
			"Another process is already polling this bot: "+apiErr.Description+
				". Telegram allows one getUpdates reader per token. Stop the other "+
				"reader, or have it queue presses to a JSONL file and point "+
				"TELEGRAM_CALLBACK_QUEUE_FILE at it so both can see them.",
			apiErr.Code)
	}
	return err
}

// confirmUpdates persists the cursor, then tells Telegram to drop the confirmed
// updates. The local cursor is written first: if the confirming call fails, the
// already-returned presses are still filtered out on the next poll.
func (b *BotBackend) confirmUpdates(ctx context.Context, nextOffset int) {
	b.storeCursor(nextOffset)
	if _, err := b.call(ctx, "getUpdates", Map{
		"offset":          nextOffset,
		"limit":           1,
		"allowed_updates": []string{"callback_query"},
		"timeout":         0,
	}); err != nil {
		slog.Debug("failed to confirm callback offset", "offset", nextOffset, "error", err)
	}
}

func (b *BotBackend) loadCursor() {
	if b.cursorLoaded {
		return
	}
	b.cursorLoaded = true
	if b.cursorPath == "" {
		return
	}
	raw, err := os.ReadFile(b.cursorPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("ignoring unreadable callback cursor file", "error", err)
		}
		return
	}
	var stored struct {
		Offset *int `json:"offset"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		slog.Warn("ignoring unreadable callback cursor file", "error", err)
		return
	}
	b.callbackOffset = stored.Offset
}

func (b *BotBackend) storeCursor(offset int) {
	b.callbackOffset = &offset
	if b.cursorPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.cursorPath), 0o700); err != nil {
		slog.Warn("failed to persist callback cursor", "error", err)
		return
	}
	encoded, err := json.Marshal(Map{"offset": offset})
	if err != nil {
		return
	}
	tmp := b.cursorPath + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o600); err != nil {
		slog.Warn("failed to persist callback cursor", "error", err)
		return
	}
	if err := os.Rename(tmp, b.cursorPath); err != nil {
		slog.Warn("failed to persist callback cursor", "error", err)
	}
}

// --- chats ---

func (b *BotBackend) ListChats(context.Context, int) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) GetChatInfo(ctx context.Context, chatID any) (Map, error) {
	return b.callMap(ctx, "getChat", Map{"chat_id": chatID})
}

func (b *BotBackend) CreateChat(context.Context, string, bool) (Map, error) { return nil, NeedsUser() }
func (b *BotBackend) JoinChat(context.Context, string) (bool, error)        { return false, NeedsUser() }

func (b *BotBackend) LeaveChat(ctx context.Context, chatID any) (bool, error) {
	return b.callBool(ctx, "leaveChat", Map{"chat_id": chatID})
}

// GetMembers lists the administrators: a bot cannot enumerate every member of
// a chat, only the admins.
func (b *BotBackend) GetMembers(ctx context.Context, chatID any, _ int) ([]Map, error) {
	raw, err := b.call(ctx, "getChatAdministrators", Map{"chat_id": chatID})
	if err != nil {
		return nil, err
	}
	var members []Map
	if err := json.Unmarshal(raw, &members); err != nil {
		return []Map{}, nil
	}
	return members, nil
}

func (b *BotBackend) PromoteAdmin(ctx context.Context, chatID any, userID int64, demote bool) (bool, error) {
	rights := !demote
	return b.callBool(ctx, "promoteChatMember", Map{
		"chat_id":             chatID,
		"user_id":             userID,
		"can_manage_chat":     rights,
		"can_post_messages":   rights,
		"can_edit_messages":   rights,
		"can_delete_messages": rights,
	})
}

func (b *BotBackend) UpdateChatSettings(ctx context.Context, chatID any, title, description *string) (bool, error) {
	if title != nil {
		if _, err := b.call(ctx, "setChatTitle", Map{"chat_id": chatID, "title": *title}); err != nil {
			return false, err
		}
	}
	if description != nil {
		if _, err := b.call(ctx, "setChatDescription", Map{
			"chat_id": chatID, "description": *description,
		}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (b *BotBackend) ManageTopics(ctx context.Context, chatID any, action string, opts TopicOptions) (Map, error) {
	switch action {
	case "list":
		return Map{"error": "Bot API does not support listing forum topics. " +
			"Use user mode for full topic access."}, nil
	case "create":
		name := opts.Name
		if name == "" {
			name = "Topic"
		}
		return b.callMap(ctx, "createForumTopic", Map{"chat_id": chatID, "name": name})
	case "close":
		if _, err := b.call(ctx, "closeForumTopic", Map{
			"chat_id": chatID, "message_thread_id": opts.TopicID,
		}); err != nil {
			return nil, err
		}
		return Map{"closed": true}, nil
	default:
		return Map{"error": fmt.Sprintf("Unknown topic action: %s", action)}, nil
	}
}

// --- media ---

var botMediaMethods = map[string]string{
	"photo":    "sendPhoto",
	"document": "sendDocument",
	"voice":    "sendVoice",
	"video":    "sendVideo",
}

func (b *BotBackend) SendMedia(ctx context.Context, chatID any, mediaType, pathOrURL, caption string) (Map, error) {
	method, ok := botMediaMethods[mediaType]
	if !ok {
		method = "sendDocument"
		mediaType = "document"
	}
	params := Map{"chat_id": chatID, "caption": optionalString(caption)}

	filename, content, err := readUpload(pathOrURL)
	if err != nil {
		return nil, err
	}
	return b.decodeForm(b.callForm(ctx, method, mediaType, filename, content, params))
}

// readUpload turns a local path or an http(s) URL into the bytes to post,
// through the same security checks either way.
func readUpload(pathOrURL string) (string, []byte, error) {
	trimmed := strings.TrimSpace(pathOrURL)
	if isHTTPURL(trimmed) {
		content, err := security.FetchURL(trimmed, 30*time.Second)
		if err != nil {
			return "", nil, err
		}
		filename := filepath.Base(trimmed)
		if filename == "" || filename == "." || filename == "/" {
			filename = "file"
		}
		return filename, content, nil
	}

	path, err := security.ValidateFilePath(pathOrURL)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, fmt.Errorf("File not found: %s", pathOrURL)
	}
	if err != nil {
		return "", nil, err
	}
	if info.Size() > maxFileBytes {
		return "", nil, fmt.Errorf("File size exceeds maximum allowed (%d bytes)", maxFileBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	return filepath.Base(path), content, nil
}

func (b *BotBackend) decodeForm(raw json.RawMessage, err error) (Map, error) {
	if err != nil {
		return nil, err
	}
	var out Map
	if err := json.Unmarshal(raw, &out); err != nil {
		return Map{}, nil
	}
	return out, nil
}

func (b *BotBackend) DownloadMedia(ctx context.Context, _ any, _ int, fileID, outputDir string) (string, error) {
	if fileID == "" {
		return "", errors.New(
			"file_id is required in bot mode: the Bot API cannot look up a message's " +
				"media by (chat_id, message_id). Get file_id from a message this bot " +
				"received (message tool results include it), then call " +
				"media(action='download', file_id=...).")
	}
	info, err := b.callMap(ctx, "getFile", Map{"file_id": fileID})
	if err != nil {
		return "", err
	}
	remotePath, _ := info["file_path"].(string)
	if remotePath == "" {
		return "", newAPIError(
			"getFile returned no file_path (file may exceed bot API 20MB limit)", 0)
	}

	dir := outputDir
	if dir == "" {
		dir = os.TempDir()
	}
	targetDir, err := security.ValidateOutputDir(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(targetDir, filepath.Base(remotePath))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.fileBaseURL+remotePath, nil)
	if err != nil {
		return "", err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return "", newAPIError(err.Error(), 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", newAPIError(fmt.Sprintf("Downloading the file returned HTTP %d", resp.StatusCode), resp.StatusCode)
	}

	file, err := os.Create(target)
	if err != nil {
		return "", err
	}
	written, err := io.Copy(file, io.LimitReader(resp.Body, maxFileBytes+1))
	closeErr := file.Close()
	if err == nil && written > maxFileBytes {
		err = fmt.Errorf("File size exceeds maximum allowed (%d bytes)", maxFileBytes)
	}
	if err == nil {
		err = closeErr
	}
	if err != nil {
		// Leave nothing half-written behind.
		_ = os.Remove(target)
		return "", err
	}
	return target, nil
}

// --- contacts (user mode only) ---

func (b *BotBackend) ListContacts(context.Context) ([]Map, error)           { return nil, NeedsUser() }
func (b *BotBackend) SearchContacts(context.Context, string) ([]Map, error) { return nil, NeedsUser() }

func (b *BotBackend) AddContact(context.Context, string, string, string) (bool, error) {
	return false, NeedsUser()
}

func (b *BotBackend) BlockUser(context.Context, int64, bool) (bool, error) {
	return false, NeedsUser()
}

// --- helpers ---

func optionalKeyboard(buttons any) (any, error) {
	if buttons == nil {
		return nil, nil
	}
	return BuildInlineKeyboard(buttons)
}

func optionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func optionalInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func offsetValue(offset *int) any {
	if offset == nil {
		return nil
	}
	return *offset
}

func offsetValueInt(offset *int) int {
	if offset == nil {
		return 0
	}
	return *offset
}

func onlyCallbacks(updates []Map) []Map {
	var out []Map
	for _, update := range updates {
		if update["callback_query"] != nil {
			out = append(out, update)
		}
	}
	return out
}

func asInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed), true
		}
	case string:
		if parsed, err := strconv.Atoi(typed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func isHTTPURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// applyAlias substitutes a saved alias for a chat reference Telegram itself
// cannot resolve. Anything that is already an id or a username is left alone.
func (b *BotBackend) applyAlias(value any) any {
	text, ok := value.(string)
	if !ok || b.aliases == nil {
		return value
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || looksLikeHandle(trimmed) {
		return value
	}
	if _, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return value
	}
	if entry, found := b.aliases.Lookup(trimmed); found {
		return entry.ID
	}
	return value
}
