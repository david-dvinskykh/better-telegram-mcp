package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeBotAPI stands in for api.telegram.org: it records the calls a test makes
// and answers each method with a canned result.
type fakeBotAPI struct {
	server *httptest.Server

	mu       sync.Mutex
	calls    []recordedCall
	handlers map[string]any
}

type recordedCall struct {
	Method string
	Params map[string]any
}

func newFakeBotAPI(t *testing.T) *fakeBotAPI {
	t.Helper()
	fake := &fakeBotAPI{handlers: map[string]any{}}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/")
		body, _ := io.ReadAll(r.Body)
		params := map[string]any{}
		_ = json.Unmarshal(body, &params)

		fake.mu.Lock()
		fake.calls = append(fake.calls, recordedCall{Method: method, Params: params})
		result, ok := fake.handlers[method]
		fake.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": false, "description": "Unknown method: " + method, "error_code": 400,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeBotAPI) on(method string, result any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = result
}

func (f *fakeBotAPI) callsTo(method string) []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedCall
	for _, call := range f.calls {
		if call.Method == method {
			out = append(out, call)
		}
	}
	return out
}

func (f *fakeBotAPI) backend(t *testing.T, opts BotOptions) *BotBackend {
	t.Helper()
	backend := NewBotBackend("123:token", opts)
	backend.baseURL = f.server.URL + "/"
	backend.fileBaseURL = f.server.URL + "/file/"
	return backend
}

func callbackUpdate(updateID, messageID int, data string, fromID int64) map[string]any {
	return map[string]any{
		"update_id": updateID,
		"callback_query": map[string]any{
			"id":   "q" + data,
			"data": data,
			"from": map[string]any{"id": fromID, "username": "presser"},
			"message": map[string]any{
				"message_id": messageID,
				"date":       1700000000,
				"chat":       map[string]any{"id": -100123},
			},
		},
	}
}

func TestSendMessageOmitsUnsetOptionalFields(t *testing.T) {
	fake := newFakeBotAPI(t)
	fake.on("sendMessage", map[string]any{"message_id": 7})
	backend := fake.backend(t, BotOptions{})

	if _, err := backend.SendMessage(context.Background(), "@chat", "hi", SendOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	params := fake.callsTo("sendMessage")[0].Params
	for _, key := range []string{"reply_to_message_id", "parse_mode", "reply_markup"} {
		if _, present := params[key]; present {
			t.Errorf("%s must be omitted when unset, got %#v", key, params[key])
		}
	}
}

func TestSendMessageRejectsBadButtonsBeforeCalling(t *testing.T) {
	fake := newFakeBotAPI(t)
	fake.on("sendMessage", map[string]any{"message_id": 7})
	backend := fake.backend(t, BotOptions{})

	_, err := backend.SendMessage(context.Background(), "@chat", "hi", SendOptions{
		Buttons: []any{map[string]any{"text": "Yes"}},
	})
	if err == nil {
		t.Fatal("expected the malformed keyboard to be rejected")
	}
	if calls := fake.callsTo("sendMessage"); len(calls) != 0 {
		t.Errorf("a rejected keyboard must not reach Telegram, got %d call(s)", len(calls))
	}
}

func TestGetCallbackQueriesConsumesAndPersistsCursor(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "default.callbacks.json")

	fake := newFakeBotAPI(t)
	fake.on("getUpdates", []any{callbackUpdate(41, 5, "DEC-1:yes", 99)})
	backend := fake.backend(t, BotOptions{CursorPath: cursorPath})

	updates, err := backend.GetCallbackQueries(context.Background(), nil, 20, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected one press, got %d", len(updates))
	}

	// The cursor must be on disk, so a restart cannot replay the decision.
	raw, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("cursor was not persisted: %v", err)
	}
	var stored struct {
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("cursor file is not readable: %v", err)
	}
	if stored.Offset != 42 {
		t.Errorf("cursor should sit one past the press, got %d", stored.Offset)
	}

	// The confirming call is what makes Telegram drop the update.
	calls := fake.callsTo("getUpdates")
	if len(calls) != 2 {
		t.Fatalf("expected a poll and a confirm, got %d call(s)", len(calls))
	}
	if got := calls[1].Params["offset"]; got != float64(42) {
		t.Errorf("confirm should use offset 42, got %v", got)
	}
}

func TestGetCallbackQueriesPeekDoesNotConsume(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "default.callbacks.json")

	fake := newFakeBotAPI(t)
	fake.on("getUpdates", []any{callbackUpdate(41, 5, "DEC-1:yes", 99)})
	backend := fake.backend(t, BotOptions{CursorPath: cursorPath})

	if _, err := backend.GetCallbackQueries(context.Background(), nil, 20, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(cursorPath); err == nil {
		t.Error("a peek must not move the persisted cursor")
	}
	if calls := fake.callsTo("getUpdates"); len(calls) != 1 {
		t.Errorf("a peek must not confirm the update, got %d call(s)", len(calls))
	}
}

func TestGetCallbackQueriesSinceIDOnlyMovesForward(t *testing.T) {
	fake := newFakeBotAPI(t)
	fake.on("getUpdates", []any{})
	backend := fake.backend(t, BotOptions{})
	backend.cursorLoaded = true
	offset := 100
	backend.callbackOffset = &offset

	stale := 5
	if _, err := backend.GetCallbackQueries(context.Background(), &stale, 20, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fake.callsTo("getUpdates")[0].Params["offset"]; got != float64(100) {
		t.Errorf("an older since_id must not rewind the cursor, offset was %v", got)
	}
}

func TestReadQueueHandsOutEachPressOnce(t *testing.T) {
	dir := t.TempDir()
	queuePath := filepath.Join(dir, "queue.jsonl")

	var lines []string
	for _, update := range []map[string]any{
		callbackUpdate(1, 5, "a", 99),
		callbackUpdate(2, 5, "b", 99),
	} {
		encoded, _ := json.Marshal(update)
		lines = append(lines, string(encoded))
	}
	// A malformed line must be skipped rather than stopping the read.
	lines = append(lines, "{not json")
	if err := os.WriteFile(queuePath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := newFakeBotAPI(t)
	backend := fake.backend(t, BotOptions{
		QueuePath:  queuePath,
		CursorPath: filepath.Join(dir, "cursor.json"),
	})

	first, err := backend.GetCallbackQueries(context.Background(), nil, 20, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("expected both queued presses, got %d", len(first))
	}

	second, err := backend.GetCallbackQueries(context.Background(), nil, 20, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("a consumed press must not come back, got %d", len(second))
	}
	if calls := fake.callsTo("getUpdates"); len(calls) != 0 {
		t.Errorf("queue mode must not poll Telegram, got %d call(s)", len(calls))
	}
}

func TestReadQueueMissingFileIsEmptyNotAnError(t *testing.T) {
	fake := newFakeBotAPI(t)
	backend := fake.backend(t, BotOptions{QueuePath: filepath.Join(t.TempDir(), "absent.jsonl")})

	updates, err := backend.GetCallbackQueries(context.Background(), nil, 20, true)
	if err != nil {
		t.Fatalf("an empty queue is not an error: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("expected no presses, got %d", len(updates))
	}
}

func TestResumeSessionRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.jsonl")
	fake := newFakeBotAPI(t)
	backend := fake.backend(t, BotOptions{ResumeMapPath: path})
	ctx := context.Background()

	registered, err := backend.RecordResumeSession(ctx, -100123, 5, "session_01AbCdEfGhIjKl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !registered {
		t.Fatal("expected the registration to be reported as stored")
	}

	// The newest registration for a message wins.
	if _, err := backend.RecordResumeSession(ctx, -100123, 5, "session_02NewerSession"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found, err := backend.LookupResumeSession(ctx, -100123, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found != "session_02NewerSession" {
		t.Errorf("expected the newest session, got %q", found)
	}

	missing, err := backend.LookupResumeSession(ctx, -100123, 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if missing != "" {
		t.Errorf("an unregistered message must resolve to nothing, got %q", missing)
	}
}

func TestRecordResumeSessionRejectsForeignIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.jsonl")
	fake := newFakeBotAPI(t)
	backend := fake.backend(t, BotOptions{ResumeMapPath: path})

	if _, err := backend.RecordResumeSession(context.Background(), 1, 2, "../etc/passwd"); err == nil {
		t.Fatal("expected a non-session-id value to be rejected")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a rejected value must not be written to the map")
	}
}

func TestRecordResumeSessionWithoutMapReportsFalse(t *testing.T) {
	fake := newFakeBotAPI(t)
	backend := fake.backend(t, BotOptions{})

	registered, err := backend.RecordResumeSession(context.Background(), 1, 2, "session_01AbCdEfGhIjKl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if registered {
		t.Error("with no map file configured the registration cannot be stored")
	}
}

func TestUserOnlyActionsReportTheModeTheyNeed(t *testing.T) {
	fake := newFakeBotAPI(t)
	backend := fake.backend(t, BotOptions{})
	ctx := context.Background()

	if _, err := backend.ListChats(ctx, 10); err == nil ||
		!strings.Contains(err.Error(), "requires user mode") {
		t.Errorf("list_chats should ask for user mode, got %v", err)
	}
	if _, err := backend.SearchMessages(ctx, "x", nil, 10); err == nil ||
		!strings.Contains(err.Error(), "requires user mode") {
		t.Errorf("search should ask for user mode, got %v", err)
	}
	// History is genuinely empty for a bot rather than an error.
	messages, err := backend.GetHistory(ctx, 1, 10, 0)
	if err != nil || len(messages) != 0 {
		t.Errorf("history should be empty without an error, got %v / %v", messages, err)
	}
}

func TestDownloadMediaRequiresFileID(t *testing.T) {
	fake := newFakeBotAPI(t)
	backend := fake.backend(t, BotOptions{})

	_, err := backend.DownloadMedia(context.Background(), 1, 2, "", "")
	if err == nil || !strings.Contains(err.Error(), "file_id is required") {
		t.Fatalf("expected the file_id requirement to be explained, got %v", err)
	}
}

func TestAPIErrorsRedactTheBotToken(t *testing.T) {
	err := newAPIError(
		"Post \"https://api.telegram.org/bot123456:AAHfyourrealsecrettokenvaluegoeshere1234/getMe\": timeout", 0)
	if strings.Contains(err.Error(), "AAHfyourrealsecrettokenvaluegoeshere1234") {
		t.Fatalf("the token leaked into the error: %s", err)
	}
	if !strings.Contains(err.Error(), "123456:<redacted>") {
		t.Errorf("the bot id should survive for debuggability: %s", err)
	}
}

func TestWebhookAndPollingConflictsExplainThemselves(t *testing.T) {
	webhook := webhookHint(newAPIError(
		"Conflict: can't use getUpdates method while webhook is active", 409))
	if !strings.Contains(webhook.Error(), "deleteWebhook") {
		t.Errorf("the webhook conflict should name the fix: %s", webhook)
	}

	polling := webhookHint(newAPIError(
		"Conflict: terminated by other getUpdates request", 409))
	if !strings.Contains(polling.Error(), "TELEGRAM_CALLBACK_QUEUE_FILE") {
		t.Errorf("the polling conflict should point at the queue file: %s", polling)
	}
}
