package tools

import (
	"context"
	"log/slog"
	"regexp"
	"time"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// MaxAnswerText is the Bot API's cap on answerCallbackQuery notification text.
const MaxAnswerText = 200

// MessageArgs are the arguments of the `message` tool. Every action reads a
// subset; the required ones are checked per action so the error names exactly
// what is missing.
type MessageArgs struct {
	Action    string `json:"action" jsonschema:"send|edit|delete|delete_bulk|purge|forward|forward_bulk|pin|unpin|unpin_all|pinned|react|reactions|read|search|history|context|link|poll|draft_save|draft_list|schedule|schedule_list|schedule_cancel|callbacks|answer"`
	ChatID    any    `json:"chat_id,omitempty" jsonschema:"chat id or @username"`
	Text      string `json:"text,omitempty"`
	MessageID int    `json:"message_id,omitempty"`
	ReplyTo   int    `json:"reply_to,omitempty"`
	ParseMode string `json:"parse_mode,omitempty" jsonschema:"HTML, MarkdownV2 or Markdown"`
	// These carry a jsonschema description for the same reason chat_id does,
	// and not only as documentation: a bare `any` infers to the boolean schema
	// `true`, which is valid JSON Schema but is rejected by clients whose
	// validator expects every property to be an object -- MetaMCP among them,
	// where it made the whole tool list fail to load.
	FromChat any    `json:"from_chat,omitempty" jsonschema:"source chat id or @username"`
	ToChat   any    `json:"to_chat,omitempty" jsonschema:"destination chat id or @username"`
	Emoji    string `json:"emoji,omitempty"`
	Query    string `json:"query,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	OffsetID int    `json:"offset_id,omitempty"`

	// Bulk and window actions
	MessageIDs []int `json:"message_ids,omitempty" jsonschema:"message ids for delete_bulk, forward_bulk and schedule_cancel"`
	Around     int   `json:"around,omitempty" jsonschema:"messages to show either side of message_id in context (default 5)"`
	Revoke     bool  `json:"revoke,omitempty" jsonschema:"purge for everyone, not just this account -- irreversible"`

	// Polls
	Question       string   `json:"question,omitempty"`
	Options        []string `json:"options,omitempty" jsonschema:"2 to 10 poll answers"`
	MultipleChoice bool     `json:"multiple_choice,omitempty"`
	Anonymous      *bool    `json:"anonymous,omitempty" jsonschema:"hide who voted; defaults to true"`
	Quiz           bool     `json:"quiz,omitempty"`
	CloseAt        string   `json:"close_at,omitempty" jsonschema:"RFC3339 time the poll closes"`

	// Scheduling
	SendAt string `json:"send_at,omitempty" jsonschema:"RFC3339 time to send the message"`

	// Inline buttons and callback queries (bot mode)
	Buttons         []any   `json:"buttons,omitempty" jsonschema:"rows of {text, data} callback buttons"`
	SinceID         *int    `json:"since_id,omitempty"`
	CallbackQueryID string  `json:"callback_query_id,omitempty"`
	AnswerText      string  `json:"answer_text,omitempty"`
	ShowAlert       bool    `json:"show_alert,omitempty"`
	AutoAnswer      *bool   `json:"auto_answer,omitempty" jsonschema:"acknowledge each press; defaults to true"`
	AllowedFromIDs  []int64 `json:"allowed_from_ids,omitempty"`
	DataPattern     string  `json:"data_pattern,omitempty"`
	ResumeSession   string  `json:"resume_session,omitempty"`
	Peek            bool    `json:"peek,omitempty"`
}

func (a MessageArgs) limitOr(fallback int) int {
	if a.Limit <= 0 {
		return fallback
	}
	return a.Limit
}

func (a MessageArgs) autoAnswer() bool {
	return a.AutoAnswer == nil || *a.AutoAnswer
}

var messageActions = []string{
	"send", "edit", "delete", "delete_bulk", "purge",
	"forward", "forward_bulk", "pin", "unpin", "unpin_all", "pinned",
	"react", "reactions", "read", "search", "history", "context", "link",
	"poll", "draft_save", "draft_list",
	"schedule", "schedule_list", "schedule_cancel",
	"callbacks", "answer",
}

// HandleMessage dispatches one call of the `message` tool.
func HandleMessage(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	switch args.Action {
	case "send":
		return handleSend(ctx, backend, args)
	case "edit":
		return handleEdit(ctx, backend, args)
	case "delete":
		return handleDelete(ctx, backend, args)
	case "forward":
		return handleForward(ctx, backend, args)
	case "pin":
		return handlePin(ctx, backend, args)
	case "react":
		return handleReact(ctx, backend, args)
	case "search":
		return handleSearch(ctx, backend, args)
	case "history":
		return handleHistory(ctx, backend, args)
	case "callbacks":
		return handleCallbacks(ctx, backend, args)
	case "answer":
		return handleAnswer(ctx, backend, args)
	default:
		return handleMessageExtra(ctx, backend, args)
	}
}

// handleMessageExtra dispatches the actions that need a capability beyond the
// core Backend, so the switch above stays the shape it had.
func handleMessageExtra(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	// A mistyped action is a caller error and must be reported as one, so the
	// action is checked before the backend is asked whether it can serve it --
	// otherwise "sned" would come back as a mode problem.
	if !contains(messageActions, args.Action) {
		return unknownAction(args.Action, messageActions)
	}
	extras, err := telegram.Messages(backend)
	if err != nil {
		return SafeError(err)
	}

	switch args.Action {
	case "unpin":
		if isEmpty(args.ChatID) || args.MessageID == 0 {
			return Err("'unpin' requires chat_id and message_id")
		}
		unpinned, err := extras.UnpinMessage(ctx, args.ChatID, args.MessageID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"unpinned": unpinned})

	case "unpin_all":
		if isEmpty(args.ChatID) {
			return Err("'unpin_all' requires chat_id")
		}
		unpinned, err := extras.UnpinAllMessages(ctx, args.ChatID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"unpinned_all": unpinned})

	case "pinned":
		if isEmpty(args.ChatID) {
			return Err("'pinned' requires chat_id")
		}
		messages, err := extras.ListPinned(ctx, args.ChatID, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"messages": messages, "count": len(messages)})

	case "read":
		if isEmpty(args.ChatID) {
			return Err("'read' requires chat_id")
		}
		read, err := extras.MarkRead(ctx, args.ChatID, args.MessageID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"marked_read": read})

	case "context":
		if isEmpty(args.ChatID) || args.MessageID == 0 {
			return Err("'context' requires chat_id and message_id")
		}
		messages, err := extras.MessageContext(ctx, args.ChatID, args.MessageID, args.Around)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"messages": messages, "count": len(messages)})

	case "link":
		if isEmpty(args.ChatID) || args.MessageID == 0 {
			return Err("'link' requires chat_id and message_id")
		}
		link, err := extras.MessageLink(ctx, args.ChatID, args.MessageID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(link)

	case "reactions":
		if isEmpty(args.ChatID) || args.MessageID == 0 {
			return Err("'reactions' requires chat_id and message_id")
		}
		reactions, err := extras.ListReactions(ctx, args.ChatID, args.MessageID, args.Limit)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"reactions": reactions, "count": len(reactions)})

	case "delete_bulk":
		if isEmpty(args.ChatID) || len(args.MessageIDs) == 0 {
			return Err("'delete_bulk' requires chat_id and message_ids")
		}
		deleted, err := extras.DeleteMessages(ctx, args.ChatID, args.MessageIDs)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"deleted": deleted, "requested": len(args.MessageIDs)})

	case "purge":
		if isEmpty(args.ChatID) {
			return Err("'purge' requires chat_id")
		}
		purged, err := extras.PurgeHistory(ctx, args.ChatID, args.Revoke)
		if err != nil {
			return SafeError(err)
		}
		return Ok(purged)

	case "forward_bulk":
		if isEmpty(args.FromChat) || isEmpty(args.ToChat) || len(args.MessageIDs) == 0 {
			return Err("'forward_bulk' requires from_chat, to_chat and message_ids")
		}
		messages, err := extras.ForwardMessages(ctx, args.FromChat, args.ToChat, args.MessageIDs)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"messages": messages, "count": len(messages)})

	case "poll":
		return handlePoll(ctx, extras, args)

	case "draft_save":
		if isEmpty(args.ChatID) {
			return Err("'draft_save' requires chat_id; an empty text clears the draft")
		}
		saved, err := extras.SaveDraft(ctx, args.ChatID, args.Text)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"saved": saved, "cleared": args.Text == ""})

	case "draft_list":
		drafts, err := extras.ListDrafts(ctx)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"drafts": drafts, "count": len(drafts)})

	case "schedule":
		if isEmpty(args.ChatID) || args.Text == "" || args.SendAt == "" {
			return Err("'schedule' requires chat_id, text and send_at (RFC3339)")
		}
		at, err := time.Parse(time.RFC3339, args.SendAt)
		if err != nil {
			return Err("send_at must be an RFC3339 time such as 2026-01-31T09:00:00Z, got %q", args.SendAt)
		}
		scheduled, err := extras.ScheduleMessage(ctx, args.ChatID, args.Text, at)
		if err != nil {
			return SafeError(err)
		}
		return Ok(scheduled)

	case "schedule_list":
		if isEmpty(args.ChatID) {
			return Err("'schedule_list' requires chat_id")
		}
		messages, err := extras.ListScheduled(ctx, args.ChatID)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"messages": messages, "count": len(messages)})

	case "schedule_cancel":
		if isEmpty(args.ChatID) || len(args.MessageIDs) == 0 {
			return Err("'schedule_cancel' requires chat_id and message_ids")
		}
		cancelled, err := extras.CancelScheduled(ctx, args.ChatID, args.MessageIDs)
		if err != nil {
			return SafeError(err)
		}
		return Ok(Result{"cancelled": cancelled, "count": len(args.MessageIDs)})

	default:
		return unknownAction(args.Action, messageActions)
	}
}

// handlePoll is split out because a poll has more of its own arguments than
// every other action put together.
func handlePoll(ctx context.Context, extras telegram.MessageExtras, args MessageArgs) Result {
	if isEmpty(args.ChatID) || args.Question == "" || len(args.Options) < 2 {
		return Err("'poll' requires chat_id, question and at least 2 options")
	}
	opts := telegram.PollOptions{
		MultipleChoice: args.MultipleChoice,
		// A poll is anonymous unless the caller says otherwise, which is what
		// Telegram's own compose screen defaults to.
		Anonymous: args.Anonymous == nil || *args.Anonymous,
		Quiz:      args.Quiz,
	}
	if args.CloseAt != "" {
		at, err := time.Parse(time.RFC3339, args.CloseAt)
		if err != nil {
			return Err("close_at must be an RFC3339 time such as 2026-01-31T09:00:00Z, got %q", args.CloseAt)
		}
		opts.CloseAt = at
	}
	poll, err := extras.SendPoll(ctx, args.ChatID, args.Question, args.Options, opts)
	if err != nil {
		return SafeError(err)
	}
	return Ok(poll)
}

func handleSend(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if isEmpty(args.ChatID) || args.Text == "" {
		return Err("'send' requires chat_id and text. " +
			"chat_id: positive int (user), negative int (group), or @username. " +
			"Optional parse_mode: HTML, MarkdownV2, or Markdown. " +
			`Optional buttons: [[{"text": "Yes", "data": "DEC-12:yes"}]].`)
	}
	result, err := backend.SendMessage(ctx, args.ChatID, args.Text, telegram.SendOptions{
		ReplyTo:   args.ReplyTo,
		ParseMode: args.ParseMode,
		Buttons:   optionalButtons(args.Buttons),
	})
	if err != nil {
		return SafeError(err)
	}
	if args.ResumeSession == "" {
		return Ok(result)
	}

	// Tie the buttons to the asking session, so a press can be delivered back
	// into it instead of starting a fresh one.
	messageID, ok := asInt(result["message_id"])
	if !ok {
		result["resume_registered"] = false
		return Ok(result)
	}
	registered, err := backend.RecordResumeSession(ctx, args.ChatID, messageID, args.ResumeSession)
	if err != nil {
		return SafeError(err)
	}
	result["resume_registered"] = registered
	return Ok(result)
}

func handleEdit(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if isEmpty(args.ChatID) || args.MessageID == 0 || (args.Text == "" && args.Buttons == nil) {
		return Err("'edit' requires chat_id, message_id, and text and/or buttons. " +
			"Optional parse_mode: HTML, MarkdownV2, or Markdown. " +
			"buttons=[] strips the inline keyboard from the message.")
	}
	if args.Text == "" {
		result, err := backend.EditMessageButtons(ctx, args.ChatID, args.MessageID, anySlice(args.Buttons))
		if err != nil {
			return SafeError(err)
		}
		return Ok(result)
	}
	result, err := backend.EditMessage(ctx, args.ChatID, args.MessageID, args.Text, telegram.EditOptions{
		ParseMode: args.ParseMode,
		Buttons:   optionalButtons(args.Buttons),
	})
	if err != nil {
		return SafeError(err)
	}
	return Ok(result)
}

func handleDelete(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if isEmpty(args.ChatID) || args.MessageID == 0 {
		return Err("'delete' requires chat_id and message_id")
	}
	deleted, err := backend.DeleteMessage(ctx, args.ChatID, args.MessageID)
	if err != nil {
		return SafeError(err)
	}
	return Ok(Result{"deleted": deleted})
}

func handleForward(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if isEmpty(args.FromChat) || isEmpty(args.ToChat) || args.MessageID == 0 {
		return Err("'forward' requires from_chat, to_chat, and message_id")
	}
	result, err := backend.ForwardMessage(ctx, args.FromChat, args.ToChat, args.MessageID)
	if err != nil {
		return SafeError(err)
	}
	return Ok(result)
}

func handlePin(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if isEmpty(args.ChatID) || args.MessageID == 0 {
		return Err("'pin' requires chat_id and message_id")
	}
	pinned, err := backend.PinMessage(ctx, args.ChatID, args.MessageID)
	if err != nil {
		return SafeError(err)
	}
	return Ok(Result{"pinned": pinned})
}

func handleReact(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if isEmpty(args.ChatID) || args.MessageID == 0 || args.Emoji == "" {
		return Err("'react' requires chat_id, message_id, and emoji")
	}
	reacted, err := backend.ReactToMessage(ctx, args.ChatID, args.MessageID, args.Emoji)
	if err != nil {
		return SafeError(err)
	}
	return Ok(Result{"reacted": reacted})
}

func handleSearch(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if args.Query == "" {
		return Err("'search' requires query")
	}
	messages, err := backend.SearchMessages(ctx, args.Query, args.ChatID, args.limitOr(20))
	if err != nil {
		return SafeError(err)
	}
	return Ok(Result{"messages": messages, "count": len(messages)})
}

func handleHistory(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if isEmpty(args.ChatID) {
		return Err("'history' requires chat_id")
	}
	messages, err := backend.GetHistory(ctx, args.ChatID, args.limitOr(20), args.OffsetID)
	if err != nil {
		return SafeError(err)
	}
	return Ok(Result{"messages": messages, "count": len(messages)})
}

// handleCallbacks reads inline-button presses (bot mode) and acknowledges them.
func handleCallbacks(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if len([]rune(args.AnswerText)) > MaxAnswerText {
		return Err("answer_text exceeds %d characters", MaxAnswerText)
	}

	var pattern *regexp.Regexp
	if args.DataPattern != "" {
		compiled, err := regexp.Compile(args.DataPattern)
		if err != nil {
			return Err("Invalid data_pattern regex: %v", err)
		}
		pattern = compiled
	}

	if args.MessageID != 0 && !args.Peek {
		return Err("'callbacks' with message_id only makes sense together with " +
			"peek=true: a filtered consuming read would drop every other press " +
			"instead of leaving it for whoever acts on it.")
	}

	allowed := map[int64]bool{}
	for _, id := range args.AllowedFromIDs {
		allowed[id] = true
	}

	updates, err := backend.GetCallbackQueries(ctx, args.SinceID, args.limitOr(20), !args.Peek)
	if err != nil {
		return SafeError(err)
	}
	polledAt := time.Now().UTC().Format(time.RFC3339)

	accepted := []Result{}
	ignored := []Result{}
	cursor := args.SinceID

	for _, update := range updates {
		entry := normalizeCallback(update, polledAt)
		if id, ok := asInt(entry["update_id"]); ok {
			if cursor == nil || id > *cursor {
				next := id
				cursor = &next
			}
		}

		// A watcher waiting on one question ignores presses on the others.
		if args.MessageID != 0 {
			if id, ok := asInt(entry["message_id"]); !ok || id != args.MessageID {
				continue
			}
		}

		reason := rejectionReason(entry, allowed, pattern)
		if reason != "" {
			slog.Warn("ignoring callback_query",
				"update_id", entry["update_id"], "from_id", entry["from_id"], "reason", reason)
			rejected := cloneResult(entry)
			rejected["reason"] = reason
			ignored = append(ignored, rejected)
			continue
		}

		if args.autoAnswer() && !args.Peek && entry["answered"] != true {
			// An unanswered query leaves a spinner on the button in every
			// client, so acknowledge before handing the press to the caller.
			queryID, _ := entry["callback_query_id"].(string)
			if _, err := backend.AnswerCallbackQuery(ctx, queryID, args.AnswerText, args.ShowAlert); err != nil {
				// A stale query id must not swallow the decision itself.
				slog.Warn("answerCallbackQuery failed",
					"update_id", entry["update_id"], "error", err)
			} else {
				entry["answered"] = true
			}
		}

		// Which session asked, when the sender registered one at send time.
		if entry["chat_id"] != nil && entry["message_id"] != nil {
			if messageID, ok := asInt(entry["message_id"]); ok {
				sessionID, err := backend.LookupResumeSession(ctx, entry["chat_id"], messageID)
				if err == nil && sessionID != "" {
					entry["session_id"] = sessionID
				} else {
					entry["session_id"] = nil
				}
			}
		}
		accepted = append(accepted, entry)
	}

	return Ok(Result{
		"callbacks":     accepted,
		"count":         len(accepted),
		"ignored":       ignored,
		"ignored_count": len(ignored),
		// A peek leaves every press pending, so the reader knows nothing was
		// taken from whoever acts on it.
		"peek": args.Peek,
		// Pass this back as since_id on the next call; the server keeps the
		// same cursor itself, so a repeated call never replays a press.
		"cursor": intOrNil(cursor),
	})
}

func rejectionReason(entry Result, allowed map[int64]bool, pattern *regexp.Regexp) string {
	queryID, hasID := entry["callback_query_id"].(string)
	data, hasData := entry["data"].(string)
	if !hasID || queryID == "" || !hasData {
		return "malformed"
	}
	if len(allowed) > 0 {
		from, ok := asInt64(entry["from_id"])
		if !ok || !allowed[from] {
			return "unauthorized_sender"
		}
	}
	if pattern != nil {
		if match := pattern.FindString(data); match != data {
			return "data_pattern_mismatch"
		}
	}
	return ""
}

// normalizeCallback flattens a raw Bot API update into the entry a caller acts
// on, so nobody downstream has to walk the nested update shape.
func normalizeCallback(update telegram.Map, polledAt string) Result {
	query, _ := update["callback_query"].(map[string]any)
	sender, _ := query["from"].(map[string]any)
	msg, _ := query["message"].(map[string]any)
	chat, _ := msg["chat"].(map[string]any)

	// The ids arrive as whatever JSON decoding produced (a float64, in
	// practice). Normalising them here keeps the tool result stable and lets a
	// caller compare an id without knowing how it was decoded.
	return Result{
		"update_id":         numeric(update["update_id"]),
		"callback_query_id": query["id"],
		"data":              query["data"],
		"from_id":           numeric(sender["id"]),
		"from_username":     sender["username"],
		"chat_id":           numeric(chat["id"]),
		"message_id":        numeric(msg["message_id"]),
		// The Bot API carries no press timestamp: message_date is when the
		// question was posted, received_at when this server read it.
		"message_date": isoUTC(msg["date"]),
		"received_at":  polledAt,
		// True when whoever queued this press already acknowledged it (queue
		// mode); such a callback_query_id is spent and must not be answered again.
		"answered": update["answered"] == true,
	}
}

func handleAnswer(ctx context.Context, backend telegram.Backend, args MessageArgs) Result {
	if args.CallbackQueryID == "" {
		return Err("'answer' requires callback_query_id (from a 'callbacks' result). " +
			"Optional answer_text (<=200 chars) and show_alert.")
	}
	if len([]rune(args.AnswerText)) > MaxAnswerText {
		return Err("answer_text exceeds %d characters", MaxAnswerText)
	}
	answered, err := backend.AnswerCallbackQuery(ctx, args.CallbackQueryID, args.AnswerText, args.ShowAlert)
	if err != nil {
		return SafeError(err)
	}
	return Ok(Result{"answered": answered})
}

// numeric renders an id as an integer, or leaves it alone when it is not one
// (a missing field stays nil rather than becoming a misleading zero).
func numeric(value any) any {
	if value == nil {
		return nil
	}
	if parsed, ok := asInt64(value); ok {
		return parsed
	}
	return value
}

// isoUTC formats a Bot API unix timestamp the way the rest of the results
// spell a time.
func isoUTC(value any) any {
	seconds, ok := asInt64(value)
	if !ok {
		return nil
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

// optionalButtons keeps the "no buttons" case indistinguishable from a plain
// send: only a payload the caller actually passed reaches the backend.
func optionalButtons(buttons []any) any {
	if buttons == nil {
		return nil
	}
	return anySlice(buttons)
}

func anySlice(buttons []any) any {
	out := make([]any, len(buttons))
	copy(out, buttons)
	return out
}

func cloneResult(entry Result) Result {
	out := make(Result, len(entry)+1)
	for key, value := range entry {
		out[key] = value
	}
	return out
}

func intOrNil(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
