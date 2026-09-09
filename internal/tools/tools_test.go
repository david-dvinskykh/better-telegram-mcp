package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

func TestChatActionsValidateTheirArguments(t *testing.T) {
	tests := []struct {
		name  string
		args  ChatArgs
		wants string
	}{
		{"info without chat", ChatArgs{Action: "info"}, "'info' requires chat_id"},
		{"create without title", ChatArgs{Action: "create"}, "'create' requires title"},
		{"join without link", ChatArgs{Action: "join"}, "'join' requires link_or_hash"},
		{"admin without user", ChatArgs{Action: "admin", ChatID: 1}, "requires chat_id and user_id"},
		{"settings with nothing to set", ChatArgs{Action: "settings", ChatID: 1},
			"at least one of: title, description"},
		{"topics without action", ChatArgs{Action: "topics", ChatID: 1}, "requires topic_action"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := HandleChat(context.Background(), newStub(), test.args)
			if !strings.Contains(errorText(t, result), test.wants) {
				t.Errorf("unexpected message: %s", errorText(t, result))
			}
		})
	}
}

func TestChatAdminReportsTheDirection(t *testing.T) {
	promoted := HandleChat(context.Background(), newStub(), ChatArgs{
		Action: "admin", ChatID: 1, UserID: 7,
	})
	if promoted["promoted"] != true {
		t.Errorf("expected a promoted flag, got %#v", promoted)
	}

	demoted := HandleChat(context.Background(), newStub(), ChatArgs{
		Action: "admin", ChatID: 1, UserID: 7, Demote: true,
	})
	if demoted["demoted"] != true {
		t.Errorf("expected a demoted flag, got %#v", demoted)
	}
}

func TestChatListReportsACount(t *testing.T) {
	result := HandleChat(context.Background(), newStub(), ChatArgs{Action: "list"})
	if result["count"] != 1 {
		t.Errorf("expected a count alongside the chats, got %#v", result)
	}
}

func TestChatUnknownActionIsExplained(t *testing.T) {
	result := HandleChat(context.Background(), newStub(), ChatArgs{Action: "lst"})
	if !strings.Contains(errorText(t, result), "Did you mean 'list'?") {
		t.Errorf("expected a suggestion, got %s", errorText(t, result))
	}
}

func TestMediaSendActionsMapToTheirType(t *testing.T) {
	for action, mediaType := range map[string]string{
		"send_photo": "photo",
		"send_file":  "document",
		"send_voice": "voice",
		"send_video": "video",
	} {
		result := HandleMedia(context.Background(), newStub(), MediaArgs{
			Action: action, ChatID: 1, FilePathOrURL: "/tmp/x",
		})
		if result["media_type"] != mediaType {
			t.Errorf("%s should upload a %s, got %#v", action, mediaType, result)
		}
	}
}

func TestMediaSendRequiresATarget(t *testing.T) {
	result := HandleMedia(context.Background(), newStub(), MediaArgs{Action: "send_photo"})
	if !strings.Contains(errorText(t, result), "requires chat_id and file_path_or_url") {
		t.Errorf("unexpected message: %s", errorText(t, result))
	}
}

func TestMediaDownloadReturnsThePath(t *testing.T) {
	result := HandleMedia(context.Background(), newStub(), MediaArgs{
		Action: "download", ChatID: 1, MessageID: 2,
	})
	if result["path"] != "/tmp/file.jpg" {
		t.Errorf("expected the saved path, got %#v", result)
	}
}

func TestContactActionsValidateTheirArguments(t *testing.T) {
	tests := []struct {
		name  string
		args  ContactArgs
		wants string
	}{
		{"search without query", ContactArgs{Action: "search"}, "'search' requires query"},
		{"add without phone", ContactArgs{Action: "add", FirstName: "D"}, "requires phone and first_name"},
		{"block without user", ContactArgs{Action: "block"}, "'block' requires user_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := HandleContact(context.Background(), newStub(), test.args)
			if !strings.Contains(errorText(t, result), test.wants) {
				t.Errorf("unexpected message: %s", errorText(t, result))
			}
		})
	}
}

func TestContactBlockReportsTheDirection(t *testing.T) {
	blocked := HandleContact(context.Background(), newStub(), ContactArgs{Action: "block", UserID: 7})
	if blocked["blocked"] != true {
		t.Errorf("expected a blocked flag, got %#v", blocked)
	}
	unblocked := HandleContact(context.Background(), newStub(), ContactArgs{
		Action: "block", UserID: 7, Unblock: true,
	})
	if unblocked["unblocked"] != true {
		t.Errorf("expected an unblocked flag, got %#v", unblocked)
	}
}

// Contact actions only exist for a user account, and a bot backend says so.
func TestContactsOnABotSayWhichModeIsNeeded(t *testing.T) {
	stub := newStub()
	stub.err = telegram.NeedsUser()

	result := HandleContact(context.Background(), stub, ContactArgs{Action: "list"})
	if !strings.Contains(errorText(t, result), "requires user mode") {
		t.Errorf("unexpected message: %s", errorText(t, result))
	}
}

func TestHelpServesATopicAndTheWhole(t *testing.T) {
	messages := HandleHelp("messages")
	if !strings.Contains(messages, "# Telegram Messages") {
		t.Errorf("expected the messages document, got %.60s", messages)
	}

	everything := HandleHelp("")
	for _, heading := range []string{"# Telegram Messages", "# Telegram Chats", "# Telegram Media"} {
		if !strings.Contains(everything, heading) {
			t.Errorf("the combined document is missing %q", heading)
		}
	}
}

func TestHelpUnknownTopicSuggests(t *testing.T) {
	result := HandleHelp("message")
	if !strings.Contains(result, "Did you mean 'messages'?") {
		t.Errorf("expected a suggestion, got %s", result)
	}
}

// --- config ---

type fakeState struct {
	backend     telegram.Backend
	configured  bool
	pendingAuth bool
	runtime     map[string]int
	reset       bool
	refreshed   bool
}

func newFakeState() *fakeState {
	return &fakeState{runtime: map[string]int{"message_limit": 20, "timeout": 30}}
}

func (f *fakeState) Backend() telegram.Backend { return f.backend }
func (f *fakeState) Configured() bool          { return f.configured }
func (f *fakeState) PendingAuth() bool         { return f.pendingAuth }
func (f *fakeState) CredentialState() string {
	if f.configured {
		return "configured"
	}
	return "awaiting_setup"
}
func (f *fakeState) RuntimeConfig() map[string]int { return f.runtime }
func (f *fakeState) SetRuntimeValue(key string, value int) {
	f.runtime[key] = value
}
func (f *fakeState) ResetCredentials() error   { f.reset = true; return nil }
func (f *fakeState) RefreshCredentials() error { f.refreshed = true; return nil }

func TestConfigStatusOnAConnectedServer(t *testing.T) {
	state := newFakeState()
	state.backend = newStub()
	state.configured = true

	result := HandleConfig(context.Background(), state, ConfigArgs{Action: "status"})
	if result["mode"] != "bot" || result["connected"] != true || result["authorized"] != true {
		t.Errorf("unexpected status: %#v", result)
	}
}

func TestConfigStatusWithoutCredentialsExplainsSetup(t *testing.T) {
	result := HandleConfig(context.Background(), newFakeState(), ConfigArgs{Action: "status"})
	if result["configured"] != false {
		t.Fatalf("expected an unconfigured status, got %#v", result)
	}
	setup, ok := result["setup"].(Result)
	if !ok {
		t.Fatalf("expected setup instructions, got %#v", result["setup"])
	}
	bot := setup["bot_mode"].(Result)
	if !strings.Contains(bot["command"].(string), "auth --bot-token") {
		t.Errorf("the instructions should name the local command: %#v", bot)
	}
}

func TestConfigSetAcceptsBothForms(t *testing.T) {
	state := newFakeState()
	state.backend = newStub()
	state.configured = true

	generic := HandleConfig(context.Background(), state, ConfigArgs{
		Action: "set", Key: "message_limit", Value: "50",
	})
	if updated := generic["updated"].(map[string]int); updated["message_limit"] != 50 {
		t.Errorf("the generic form did not apply: %#v", generic)
	}

	timeout := 90
	typed := HandleConfig(context.Background(), state, ConfigArgs{Action: "set", Timeout: &timeout})
	if updated := typed["updated"].(map[string]int); updated["timeout"] != 90 {
		t.Errorf("the typed form did not apply: %#v", typed)
	}
}

func TestConfigSetRejectsUnknownKeysAndNonIntegers(t *testing.T) {
	state := newFakeState()
	state.backend = newStub()
	state.configured = true

	unknown := HandleConfig(context.Background(), state, ConfigArgs{
		Action: "set", Key: "message_limits", Value: "5",
	})
	if !strings.Contains(errorText(t, unknown), "Did you mean 'message_limit'?") {
		t.Errorf("unexpected message: %s", errorText(t, unknown))
	}

	notAnInt := HandleConfig(context.Background(), state, ConfigArgs{
		Action: "set", Key: "timeout", Value: "soon",
	})
	if !strings.Contains(errorText(t, notAnInt), "must be an integer") {
		t.Errorf("unexpected message: %s", errorText(t, notAnInt))
	}

	nothing := HandleConfig(context.Background(), state, ConfigArgs{Action: "set"})
	if !strings.Contains(errorText(t, nothing), "at least one of") {
		t.Errorf("unexpected message: %s", errorText(t, nothing))
	}
}

// The setup actions have to work while the server has no credentials at all --
// that is the state they exist to get out of.
func TestSetupActionsRunWithoutCredentials(t *testing.T) {
	state := newFakeState()

	status := HandleConfig(context.Background(), state, ConfigArgs{Action: "setup_status"})
	if status["state"] != "awaiting_setup" {
		t.Errorf("unexpected state: %#v", status)
	}

	start := HandleConfig(context.Background(), state, ConfigArgs{Action: "setup_start"})
	if start["status"] != "cli_setup_required" {
		t.Errorf("stdio setup is a local command, got %#v", start)
	}

	if reset := HandleConfig(context.Background(), state, ConfigArgs{Action: "setup_reset"}); !state.reset {
		t.Errorf("setup_reset should clear the credentials, got %#v", reset)
	}
	if done := HandleConfig(context.Background(), state, ConfigArgs{Action: "setup_complete"}); !state.refreshed {
		t.Errorf("setup_complete should re-read the credentials, got %#v", done)
	}
}

func TestSetupStartOnAConfiguredServerNeedsForce(t *testing.T) {
	state := newFakeState()
	state.configured = true

	guarded := HandleConfig(context.Background(), state, ConfigArgs{Action: "setup_start"})
	if guarded["status"] != "already_configured" {
		t.Errorf("expected the guard, got %#v", guarded)
	}

	forced := HandleConfig(context.Background(), state, ConfigArgs{Action: "setup_start", Key: "force"})
	if forced["status"] != "cli_setup_required" {
		t.Errorf("force should get past the guard, got %#v", forced)
	}
}

func TestNotReadyDependsOnWhatIsMissing(t *testing.T) {
	unconfigured := NotReady(newFakeState())
	if unconfigured["error"] != "Not configured" {
		t.Errorf("unexpected result: %#v", unconfigured)
	}

	state := newFakeState()
	state.configured = true
	unauthenticated := NotReady(state)
	if !strings.Contains(errorText(t, unauthenticated), "not authenticated") {
		t.Errorf("unexpected message: %s", errorText(t, unauthenticated))
	}
}

func TestOpenRelayPointsAtTheLocalCommand(t *testing.T) {
	result := OpenRelay()
	if result["status"] != "stdio_only" {
		t.Errorf("unexpected status: %#v", result)
	}
	if !strings.Contains(result["message"].(string), "auth --bot-token") {
		t.Errorf("the message should name the command that replaces the relay: %#v", result)
	}
}
