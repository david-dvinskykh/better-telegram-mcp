package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/security"
)

// UserBackend talks to Telegram as a user account over MTProto.
//
// gotd drives its connection from inside a Run callback, so Connect starts that
// callback in a goroutine and blocks until the client is up. Every method then
// uses the API handle the callback published; Disconnect cancels the context,
// which is what ends Run.
type UserBackend struct {
	apiID       int
	apiHash     string
	sessionPath string

	client   *telegram.Client
	api      *tg.Client
	sender   *message.Sender
	peers    *peers.Manager
	auth     *auth.Client
	uploader *uploader.Uploader

	lock    *sessionLock
	stop    context.CancelFunc
	done    chan struct{}
	started bool
	mu      sync.Mutex

	// runErr is why the client stopped, kept so a call made afterwards can say
	// what actually happened instead of a bare "not connected". It has its own
	// mutex: the goroutine that sets it does so while Disconnect holds the main
	// one and waits for that same goroutine to finish.
	errMu  sync.Mutex
	runErr error

	// codeHash is the handle Telegram returns from SendCode and requires back
	// on SignIn, so it has to survive between the two tool calls.
	codeHash string
}

// UserOptions configures a user-mode backend.
type UserOptions struct {
	APIID       int
	APIHash     string
	SessionPath string
}

// NewUserBackend builds a user-mode backend. The session file is created on
// Connect, not here.
func NewUserBackend(opts UserOptions) *UserBackend {
	return &UserBackend{
		apiID:       opts.APIID,
		apiHash:     opts.APIHash,
		sessionPath: opts.SessionPath,
	}
}

func (u *UserBackend) Mode() Mode { return ModeUser }

// Connect starts the MTProto client and waits for it to be usable. An
// unauthorized session still connects: the caller finds out from IsAuthorized
// and can run the code/password flow.
func (u *UserBackend) Connect(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.started {
		return nil
	}

	if err := u.prepareSessionFile(); err != nil {
		return err
	}

	// Claim the session before opening it. Two clients sharing one auth key
	// make Telegram invalidate it, which is how a second server turns every
	// call in the first one into a connection failure.
	lock, err := acquireSessionLock(u.sessionPath)
	if err != nil {
		return err
	}

	client := telegram.NewClient(u.apiID, u.apiHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: u.sessionPath},
	})
	u.client = client

	// Clear the reason the previous connection died before starting a new one,
	// so a reconnect cannot report a stale cause.
	u.errMu.Lock()
	u.runErr = nil
	u.errMu.Unlock()

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	ready := make(chan error, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		err := client.Run(runCtx, func(runCtx context.Context) error {
			ready <- nil
			// Hold the connection open until Disconnect cancels the context;
			// every tool call runs against it in the meantime.
			<-runCtx.Done()
			return nil
		})
		if err != nil {
			// Report a failure that happened before the callback ran, so
			// Connect does not wait for a client that will never come up.
			select {
			case ready <- err:
			default:
			}
		}
		u.errMu.Lock()
		u.runErr = err
		u.errMu.Unlock()
	}()

	select {
	case err := <-ready:
		if err != nil {
			cancel()
			lock.release()
			return fmt.Errorf("failed to connect to Telegram: %w", err)
		}
	case <-time.After(60 * time.Second):
		cancel()
		lock.release()
		return errors.New("timed out connecting to Telegram")
	case <-ctx.Done():
		cancel()
		lock.release()
		return ctx.Err()
	}

	u.lock = lock
	u.stop = cancel
	u.done = done
	u.started = true
	u.api = client.API()
	u.sender = message.NewSender(u.api)
	u.peers = peers.Options{}.Build(u.api)
	u.auth = client.Auth()
	u.uploader = uploader.NewUploader(u.api)

	// The session file is created by gotd on first write; tighten it now that
	// it exists so an auth key is never briefly world-readable.
	u.secureSessionFile()
	return nil
}

func (u *UserBackend) Disconnect(context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.started {
		return nil
	}
	u.stop()
	select {
	case <-u.done:
	case <-time.After(10 * time.Second):
	}
	u.started = false
	u.api = nil
	u.lock.release()
	u.lock = nil
	return nil
}

func (u *UserBackend) IsConnected() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.started
}

func (u *UserBackend) IsAuthorized(ctx context.Context) bool {
	if !u.IsConnected() {
		return false
	}
	status, err := u.auth.Status(ctx)
	return err == nil && status.Authorized
}

// ClearCache drops the resolved-peer cache. The MTProto session itself stays:
// clearing it would mean logging out.
func (u *UserBackend) ClearCache(context.Context) error {
	if u.api != nil {
		u.peers = peers.Options{}.Build(u.api)
	}
	return nil
}

func (u *UserBackend) prepareSessionFile() error {
	dir := filepath.Dir(u.sessionPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Pre-create with 0600 so the auth key is never written into a file that
	// was briefly created with a laxer default.
	file, err := os.OpenFile(u.sessionPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_ = file.Close()
	}
	return nil
}

func (u *UserBackend) secureSessionFile() {
	if _, err := os.Stat(u.sessionPath); err == nil {
		_ = os.Chmod(u.sessionPath, 0o600)
	}
}

// ensure returns the live API handle, or an error that says why there is none.
//
// "Not connected" on its own is what made a real outage hard to diagnose, so a
// connection that died reports the reason gotd gave for dying.
func (u *UserBackend) ensure() (*tg.Client, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.started && u.api != nil {
		return u.api, nil
	}
	u.errMu.Lock()
	runErr := u.runErr
	u.errMu.Unlock()
	if runErr != nil {
		return nil, fmt.Errorf(
			"the Telegram connection is down (%v). If another better-telegram-mcp "+
				"process is running against the same session, stop it or give this "+
				"one its own TELEGRAM_SESSION_NAME", runErr)
	}
	return nil, errors.New(
		"Not connected to Telegram. The server starts the connection at startup; " +
			"if it never came up, check the server log for the reason.")
}

// --- auth ---

// SendCode asks Telegram to deliver a login code and remembers the hash the
// sign-in step needs.
func (u *UserBackend) SendCode(ctx context.Context, phone string) error {
	if _, err := u.ensure(); err != nil {
		return err
	}
	sent, err := u.auth.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return err
	}
	code, ok := sent.(*tg.AuthSentCode)
	if !ok {
		return errors.New("Telegram did not send a login code; try again")
	}
	u.codeHash = code.PhoneCodeHash
	return nil
}

// SignIn completes the login. A 2FA-protected account needs the password too;
// when it is not supplied the caller gets ErrPasswordAuthNeeded and can ask.
func (u *UserBackend) SignIn(ctx context.Context, phone, code, password string) (Map, error) {
	if _, err := u.ensure(); err != nil {
		return nil, err
	}
	if u.codeHash == "" {
		return nil, errors.New("no login code was requested; call send_code first")
	}

	_, err := u.auth.SignIn(ctx, phone, code, u.codeHash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		if password == "" {
			return nil, auth.ErrPasswordAuthNeeded
		}
		if _, err = u.auth.Password(ctx, password); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	u.secureSessionFile()
	self, err := u.peers.Self(ctx)
	if err != nil {
		return Map{"authenticated_as": "", "username": nil}, nil
	}
	username, _ := self.Username()
	return Map{
		"authenticated_as": self.VisibleName(),
		"username":         emptyToNil(username),
	}, nil
}

// LogOut revokes the session with Telegram and removes the local session file.
func (u *UserBackend) LogOut(ctx context.Context) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	if _, err := api.AuthLogOut(ctx); err != nil {
		return false, err
	}
	_ = os.Remove(u.sessionPath)
	return true, nil
}

// --- peer resolution ---

// resolvePeer turns the chat_id a tool was given into an input peer.
//
// It accepts what the Bot API accepts, because that is what callers already
// have: "@username", a positive user id, and the negative forms of a group
// (-123) or a channel (-100123).
func (u *UserBackend) resolvePeer(ctx context.Context, chatID any) (tg.InputPeerClass, error) {
	if chatID == nil {
		return nil, errors.New("chat_id is required")
	}
	if _, err := u.ensure(); err != nil {
		return nil, err
	}

	switch value := chatID.(type) {
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, errors.New("chat_id is required")
		}
		if id, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return u.resolveNumeric(ctx, id)
		}
		if strings.HasPrefix(trimmed, "+") {
			user, err := u.peers.ResolvePhone(ctx, trimmed)
			if err != nil {
				return nil, err
			}
			return user.InputPeer(), nil
		}
		peer, err := u.peers.Resolve(ctx, strings.TrimPrefix(trimmed, "@"))
		if err != nil {
			return nil, err
		}
		return peer.InputPeer(), nil
	case int:
		return u.resolveNumeric(ctx, int64(value))
	case int64:
		return u.resolveNumeric(ctx, value)
	case float64:
		return u.resolveNumeric(ctx, int64(value))
	default:
		return nil, fmt.Errorf("chat_id must be a username or a numeric id, got %T", chatID)
	}
}

func (u *UserBackend) resolveNumeric(ctx context.Context, id int64) (tg.InputPeerClass, error) {
	switch {
	case id > 0:
		user, err := u.peers.ResolveUserID(ctx, id)
		if err != nil {
			return nil, err
		}
		return user.InputPeer(), nil
	case id > -1_000_000_000_000:
		// A plain negative id is a legacy group.
		chat, err := u.peers.ResolveChatID(ctx, -id)
		if err != nil {
			return nil, err
		}
		return chat.InputPeer(), nil
	default:
		// -100<channel_id> is the Bot API spelling of a channel/supergroup.
		channel, err := u.peers.ResolveChannelID(ctx, -id-1_000_000_000_000)
		if err != nil {
			return nil, err
		}
		return channel.InputPeer(), nil
	}
}

func (u *UserBackend) inputChannel(ctx context.Context, chatID any) (tg.InputChannelClass, error) {
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	channel, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, errors.New("this action only applies to a channel or supergroup")
	}
	return &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}, nil
}

func (u *UserBackend) inputUser(ctx context.Context, userID int64) (tg.InputUserClass, error) {
	user, err := u.peers.ResolveUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return user.InputUser(), nil
}

// --- messages ---

func (u *UserBackend) SendMessage(ctx context.Context, chatID any, text string, opts SendOptions) (Map, error) {
	if opts.Buttons != nil {
		// Inline keyboards are a bot-only feature of MTProto; a user account
		// cannot attach one to its own message.
		return nil, NeedsBot()
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	builder := &u.sender.To(peer).Builder
	if opts.ReplyTo != 0 {
		builder = builder.Reply(opts.ReplyTo)
	}
	updates, err := builder.Text(ctx, text)
	if err != nil {
		return nil, err
	}
	return firstMessage(updates), nil
}

func (u *UserBackend) EditMessage(ctx context.Context, chatID any, messageID int, text string, opts EditOptions) (Map, error) {
	if opts.Buttons != nil {
		return nil, NeedsBot()
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	updates, err := u.sender.To(peer).Edit(messageID).Text(ctx, text)
	if err != nil {
		return nil, err
	}
	return firstMessage(updates), nil
}

func (u *UserBackend) DeleteMessage(ctx context.Context, chatID any, messageID int) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	if channel, ok := peer.(*tg.InputPeerChannel); ok {
		_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			ID:      []int{messageID},
		})
		return err == nil, err
	}
	_, err = api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
		Revoke: true,
		ID:     []int{messageID},
	})
	return err == nil, err
}

func (u *UserBackend) ForwardMessage(ctx context.Context, fromChat, toChat any, messageID int) (Map, error) {
	from, err := u.resolvePeer(ctx, fromChat)
	if err != nil {
		return nil, err
	}
	to, err := u.resolvePeer(ctx, toChat)
	if err != nil {
		return nil, err
	}
	updates, err := u.sender.To(to).ForwardIDs(from, messageID).Send(ctx)
	if err != nil {
		return nil, err
	}
	return firstMessage(updates), nil
}

func (u *UserBackend) PinMessage(ctx context.Context, chatID any, messageID int) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	_, err = api.MessagesUpdatePinnedMessage(ctx, &tg.MessagesUpdatePinnedMessageRequest{
		Peer: peer,
		ID:   messageID,
	})
	return err == nil, err
}

func (u *UserBackend) ReactToMessage(ctx context.Context, chatID any, messageID int, emoji string) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}
	request := &tg.MessagesSendReactionRequest{
		Peer:  peer,
		MsgID: messageID,
	}
	request.SetReaction([]tg.ReactionClass{&tg.ReactionEmoji{Emoticon: emoji}})
	_, err = api.MessagesSendReaction(ctx, request)
	return err == nil, err
}

func (u *UserBackend) SearchMessages(ctx context.Context, query string, chatID any, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	limit = clamp(limit, 1, 100)

	if chatID == nil {
		result, err := api.MessagesSearchGlobal(ctx, &tg.MessagesSearchGlobalRequest{
			Q:          query,
			Filter:     &tg.InputMessagesFilterEmpty{},
			Limit:      limit,
			OffsetPeer: &tg.InputPeerEmpty{},
		})
		if err != nil {
			return nil, err
		}
		return serializeMessages(result), nil
	}

	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:   peer,
		Q:      query,
		Filter: &tg.InputMessagesFilterEmpty{},
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	return serializeMessages(result), nil
}

func (u *UserBackend) GetHistory(ctx context.Context, chatID any, limit, offsetID int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     peer,
		Limit:    clamp(limit, 1, 100),
		OffsetID: offsetID,
	})
	if err != nil {
		return nil, err
	}
	return serializeMessages(result), nil
}

// --- inline buttons (bot mode only) ---

func (u *UserBackend) EditMessageButtons(context.Context, any, int, any) (Map, error) {
	return nil, NeedsBot()
}

func (u *UserBackend) GetCallbackQueries(context.Context, *int, int, bool) ([]Map, error) {
	return nil, NeedsBot()
}

func (u *UserBackend) AnswerCallbackQuery(context.Context, string, string, bool) (bool, error) {
	return false, NeedsBot()
}

func (u *UserBackend) RecordResumeSession(context.Context, any, int, string) (bool, error) {
	return false, NeedsBot()
}

func (u *UserBackend) LookupResumeSession(context.Context, any, int) (string, error) {
	return "", NeedsBot()
}

// --- chats ---

func (u *UserBackend) ListChats(ctx context.Context, limit int) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	result, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      clamp(limit, 1, 100),
	})
	if err != nil {
		return nil, err
	}

	var dialogs []tg.DialogClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch typed := result.(type) {
	case *tg.MessagesDialogs:
		dialogs, users, chats = typed.Dialogs, typed.Users, typed.Chats
	case *tg.MessagesDialogsSlice:
		dialogs, users, chats = typed.Dialogs, typed.Users, typed.Chats
	default:
		return []Map{}, nil
	}

	titles := titleIndex(users, chats)
	out := make([]Map, 0, len(dialogs))
	for _, dialog := range dialogs {
		entry, ok := dialog.(*tg.Dialog)
		if !ok {
			continue
		}
		id := peerID(entry.Peer)
		out = append(out, Map{
			"id":           id,
			"title":        titles[id],
			"unread_count": entry.UnreadCount,
		})
	}
	return out, nil
}

func (u *UserBackend) GetChatInfo(ctx context.Context, chatID any) (Map, error) {
	if _, err := u.ensure(); err != nil {
		return nil, err
	}
	input, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	peer, err := u.peers.FromInputPeer(ctx, input)
	if err != nil {
		return nil, err
	}

	info := Map{"id": peer.ID()}
	switch typed := peer.(type) {
	case peers.User:
		raw := typed.Raw()
		if raw != nil {
			info["first_name"] = raw.FirstName
			info["last_name"] = raw.LastName
			info["username"] = emptyToNil(raw.Username)
		}
	default:
		info["title"] = peer.VisibleName()
		if username, ok := peer.Username(); ok {
			info["username"] = username
		}
	}
	return info, nil
}

func (u *UserBackend) CreateChat(ctx context.Context, title string, isChannel bool) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	if isChannel {
		updates, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
			Broadcast: true,
			Title:     title,
		})
		if err != nil {
			return nil, err
		}
		return firstChat(updates, title), nil
	}
	invited, err := api.MessagesCreateChat(ctx, &tg.MessagesCreateChatRequest{
		Title: title,
		Users: []tg.InputUserClass{},
	})
	if err != nil {
		return nil, err
	}
	return firstChat(invited.Updates, title), nil
}

func (u *UserBackend) JoinChat(ctx context.Context, linkOrHash string) (bool, error) {
	if _, err := u.ensure(); err != nil {
		return false, err
	}
	trimmed := strings.TrimSpace(linkOrHash)
	if strings.Contains(trimmed, "joinchat/") || strings.Contains(trimmed, "/+") ||
		strings.HasPrefix(trimmed, "+") {
		if _, err := u.peers.JoinLink(ctx, trimmed); err != nil {
			return false, err
		}
		return true, nil
	}
	// A public @username or t.me/name: resolve it, then join the channel.
	peer, err := u.peers.Resolve(ctx, strings.TrimPrefix(trimmed, "@"))
	if err != nil {
		return false, err
	}
	if _, err := u.sender.To(peer.InputPeer()).Join(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (u *UserBackend) LeaveChat(ctx context.Context, chatID any) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return false, err
	}

	switch typed := peer.(type) {
	case *tg.InputPeerChannel:
		_, err = api.ChannelsLeaveChannel(ctx, &tg.InputChannel{
			ChannelID: typed.ChannelID, AccessHash: typed.AccessHash,
		})
		return err == nil, err
	case *tg.InputPeerChat:
		self, err := u.peers.Self(ctx)
		if err != nil {
			return false, err
		}
		_, err = api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{
			ChatID: typed.ChatID,
			UserID: self.InputUser(),
		})
		return err == nil, err
	default:
		return false, errors.New("only a group, supergroup or channel can be left")
	}
}

func (u *UserBackend) GetMembers(ctx context.Context, chatID any, limit int) ([]Map, error) {
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
		Filter:  &tg.ChannelParticipantsRecent{},
		Limit:   clamp(limit, 1, 200),
		Hash:    0,
	})
	if err != nil {
		return nil, err
	}
	participants, ok := result.(*tg.ChannelsChannelParticipants)
	if !ok {
		return []Map{}, nil
	}
	out := make([]Map, 0, len(participants.Users))
	for _, user := range participants.Users {
		if typed, ok := user.(*tg.User); ok {
			out = append(out, serializeUser(typed))
		}
	}
	return out, nil
}

func (u *UserBackend) PromoteAdmin(ctx context.Context, chatID any, userID int64, demote bool) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	channel, err := u.inputChannel(ctx, chatID)
	if err != nil {
		return false, err
	}
	user, err := u.inputUser(ctx, userID)
	if err != nil {
		return false, err
	}

	rights := tg.ChatAdminRights{}
	if !demote {
		rights = tg.ChatAdminRights{
			PostMessages:   true,
			EditMessages:   true,
			DeleteMessages: true,
			BanUsers:       true,
			InviteUsers:    true,
			PinMessages:    true,
			ManageCall:     true,
		}
	}
	_, err = api.ChannelsEditAdmin(ctx, &tg.ChannelsEditAdminRequest{
		Channel:     channel,
		UserID:      user,
		AdminRights: rights,
		Rank:        "",
	})
	return err == nil, err
}

func (u *UserBackend) UpdateChatSettings(ctx context.Context, chatID any, title, description *string) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	if title != nil {
		channel, err := u.inputChannel(ctx, chatID)
		if err != nil {
			return false, err
		}
		if _, err := api.ChannelsEditTitle(ctx, &tg.ChannelsEditTitleRequest{
			Channel: channel, Title: *title,
		}); err != nil {
			return false, err
		}
	}
	if description != nil {
		peer, err := u.resolvePeer(ctx, chatID)
		if err != nil {
			return false, err
		}
		if _, err := api.MessagesEditChatAbout(ctx, &tg.MessagesEditChatAboutRequest{
			Peer: peer, About: *description,
		}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (u *UserBackend) ManageTopics(ctx context.Context, chatID any, action string, opts TopicOptions) (Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}

	switch action {
	case "list":
		limit := opts.Limit
		if limit == 0 {
			limit = 100
		}
		result, err := api.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{
			Peer:  peer,
			Limit: limit,
		})
		if err != nil {
			return nil, err
		}
		topics := make([]Map, 0, len(result.Topics))
		for _, topic := range result.Topics {
			entry, ok := topic.(*tg.ForumTopic)
			if !ok {
				continue
			}
			item := Map{"id": entry.ID, "title": entry.Title}
			if emojiID, ok := entry.GetIconEmojiID(); ok {
				item["icon_emoji_id"] = emojiID
			} else {
				item["icon_emoji_id"] = nil
			}
			topics = append(topics, item)
		}
		return Map{"topics": topics, "count": len(topics)}, nil

	case "create":
		name := opts.Name
		if name == "" {
			name = "Topic"
		}
		updates, err := api.MessagesCreateForumTopic(ctx, &tg.MessagesCreateForumTopicRequest{
			Peer:     peer,
			Title:    name,
			RandomID: randomID(),
		})
		if err != nil {
			return nil, err
		}
		created := firstMessage(updates)
		return Map{"topic_id": created["message_id"]}, nil

	case "close":
		request := &tg.MessagesEditForumTopicRequest{
			Peer:    peer,
			TopicID: opts.TopicID,
		}
		request.SetClosed(true)
		if _, err := api.MessagesEditForumTopic(ctx, request); err != nil {
			return nil, err
		}
		return Map{"closed": true}, nil

	default:
		return Map{"error": fmt.Sprintf("Unknown topic action: %s", action)}, nil
	}
}

// --- media ---

func (u *UserBackend) SendMedia(ctx context.Context, chatID any, mediaType, pathOrURL, caption string) (Map, error) {
	if _, err := u.ensure(); err != nil {
		return nil, err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, err
	}

	var (
		name    string
		content []byte
	)
	trimmed := strings.TrimSpace(pathOrURL)
	if isHTTPURL(trimmed) {
		content, err = security.FetchURL(trimmed, 60*time.Second)
		if err != nil {
			return nil, err
		}
		name = filepath.Base(trimmed)
		if name == "" || name == "." || name == "/" {
			name = "file"
		}
	} else {
		path, err := security.ValidateFilePath(pathOrURL)
		if err != nil {
			return nil, err
		}
		content, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name = filepath.Base(path)
	}

	file, err := u.uploader.FromBytes(ctx, name, content)
	if err != nil {
		return nil, err
	}

	builder := u.sender.To(peer)
	var captions []message.StyledTextOption
	if caption != "" {
		captions = append(captions, styling.Plain(caption))
	}

	var updates tg.UpdatesClass
	switch mediaType {
	case "photo":
		updates, err = builder.UploadedPhoto(ctx, file, captions...)
	case "voice":
		updates, err = builder.Voice(ctx, file)
	case "video":
		updates, err = builder.Video(ctx, file, captions...)
	default:
		updates, err = builder.File(ctx, file, captions...)
	}
	if err != nil {
		return nil, err
	}
	return firstMessage(updates), nil
}

// DownloadMedia saves the media of one message. fileID is a bot-mode concept
// and is ignored here: a user account resolves the media from the message.
func (u *UserBackend) DownloadMedia(ctx context.Context, chatID any, messageID int, _ string, outputDir string) (string, error) {
	api, err := u.ensure()
	if err != nil {
		return "", err
	}
	peer, err := u.resolvePeer(ctx, chatID)
	if err != nil {
		return "", err
	}

	msg, err := u.fetchMessage(ctx, peer, messageID)
	if err != nil {
		return "", err
	}
	media, ok := msg.GetMedia()
	if !ok {
		return "", errors.New("Message has no media to download.")
	}

	location, name, err := mediaLocation(media)
	if err != nil {
		return "", err
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
	target := filepath.Join(targetDir, name)

	if _, err := downloader.NewDownloader().Download(api, location).ToPath(ctx, target); err != nil {
		_ = os.Remove(target)
		return "", err
	}
	return target, nil
}

func (u *UserBackend) fetchMessage(ctx context.Context, peer tg.InputPeerClass, messageID int) (*tg.Message, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}

	var result tg.MessagesMessagesClass
	if channel, ok := peer.(*tg.InputPeerChannel); ok {
		result, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
		})
	} else {
		result, err = api.MessagesGetMessages(ctx, []tg.InputMessageClass{
			&tg.InputMessageID{ID: messageID},
		})
	}
	if err != nil {
		return nil, err
	}

	for _, raw := range messagesOf(result) {
		if msg, ok := raw.(*tg.Message); ok && msg.ID == messageID {
			return msg, nil
		}
	}
	return nil, fmt.Errorf("message %d not found", messageID)
}

// --- contacts ---

func (u *UserBackend) ListContacts(ctx context.Context) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	result, err := api.ContactsGetContacts(ctx, 0)
	if err != nil {
		return nil, err
	}
	contacts, ok := result.(*tg.ContactsContacts)
	if !ok {
		return []Map{}, nil
	}
	return serializeUsers(contacts.Users), nil
}

func (u *UserBackend) SearchContacts(ctx context.Context, query string) ([]Map, error) {
	api, err := u.ensure()
	if err != nil {
		return nil, err
	}
	found, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: query, Limit: 50})
	if err != nil {
		return nil, err
	}
	return serializeUsers(found.Users), nil
}

func (u *UserBackend) AddContact(ctx context.Context, phone, firstName, lastName string) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	user, err := u.peers.ResolvePhone(ctx, phone)
	if err != nil {
		return false, err
	}
	_, err = api.ContactsAddContact(ctx, &tg.ContactsAddContactRequest{
		ID:        user.InputUser(),
		FirstName: firstName,
		LastName:  lastName,
		Phone:     phone,
	})
	return err == nil, err
}

func (u *UserBackend) BlockUser(ctx context.Context, userID int64, unblock bool) (bool, error) {
	api, err := u.ensure()
	if err != nil {
		return false, err
	}
	user, err := u.peers.ResolveUserID(ctx, userID)
	if err != nil {
		return false, err
	}
	peer := user.InputPeer()
	if unblock {
		return api.ContactsUnblock(ctx, &tg.ContactsUnblockRequest{ID: peer})
	}
	return api.ContactsBlock(ctx, &tg.ContactsBlockRequest{ID: peer})
}

func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
