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
	Action    string `json:"action" jsonschema:"send|edit|delete|forward|pin|react|search|history|callbacks|answer"`
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
	"send", "edit", "delete", "forward", "pin", "react",
	"search", "history", "callbacks", "answer",
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
		return unknownAction(args.Action, messageActions)
	}
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
