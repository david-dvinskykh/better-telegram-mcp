package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/david-dvinskykh/better-telegram-mcp/internal/telegram"
)

// The stub implements only the core Backend, which is what a backend missing a
// capability looks like -- so these tests are about what the caller is told
// when an action cannot be served, and about argument checking.

func result(t *testing.T, out Result) string {
	t.Helper()
	message, _ := out["error"].(string)
	return message
}

// An action that exists but needs the other mode must say which mode, so the
// caller can act on it instead of guessing.
func TestUnservedActionsNameTheModeThatServesThem(t *testing.T) {
	ctx := context.Background()
	backend := newStub()

	for _, call := range []struct {
		name string
		run  func() Result
	}{
		{"profile", func() Result {
			return HandleProfile(ctx, backend, ProfileArgs{Action: "me"})
		}},
		{"folder", func() Result {
			return HandleFolder(ctx, backend, FolderArgs{Action: "list"})
		}},
		{"message draft_list", func() Result {
			return HandleMessage(ctx, backend, MessageArgs{Action: "draft_list"})
		}},
		{"chat mute", func() Result {
			return HandleChat(ctx, backend, ChatArgs{Action: "mute", ChatID: 1})
		}},
		{"media gifs", func() Result {
			return HandleMedia(ctx, backend, MediaArgs{Action: "gifs"})
		}},
		{"contact blocked", func() Result {
			return HandleContact(ctx, backend, ContactArgs{Action: "blocked"})
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			message := result(t, call.run())
			if !strings.Contains(message, "user mode") {
				t.Errorf("expected the mode error, got %q", message)
			}
		})
	}
}

// A typo must be reported as a typo. It would otherwise come back as a mode
// problem, because the capability is what the dispatch reaches for first.
func TestMistypedActionsAreReportedAsTypos(t *testing.T) {
	ctx := context.Background()
	backend := newStub()

	for _, call := range []struct {
		name, typed, want string
		run               func(string) Result
	}{
		{"message", "draftlist", "draft_list", func(action string) Result {
			return HandleMessage(ctx, backend, MessageArgs{Action: action})
		}},
		{"chat", "achive", "archive", func(action string) Result {
			return HandleChat(ctx, backend, ChatArgs{Action: action})
		}},
		{"media", "stickerset", "sticker_sets", func(action string) Result {
			return HandleMedia(ctx, backend, MediaArgs{Action: action})
		}},
		{"contact", "aliaslist", "alias_list", func(action string) Result {
			return HandleContact(ctx, backend, ContactArgs{Action: action})
		}},
		{"profile", "privacyget", "privacy_get", func(action string) Result {
			return HandleProfile(ctx, backend, ProfileArgs{Action: action})
		}},
		{"folder", "addchat", "add_chat", func(action string) Result {
			return HandleFolder(ctx, backend, FolderArgs{Action: action})
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			message := result(t, call.run(call.typed))
			if !strings.Contains(message, "Unknown action") {
				t.Fatalf("expected an unknown-action error, got %q", message)
			}
			if !strings.Contains(message, call.want) {
				t.Errorf("expected %q suggested, got %q", call.want, message)
			}
		})
	}
}

// Every action names exactly what it is missing, so a caller fixes the call in
// one step rather than by trial.
//
// These run against the real bot backend rather than the stub: argument
// checking happens after the capability lookup, on purpose -- when a backend
// cannot serve an action at all, naming a missing field would send the caller
// to fix the wrong thing. So the cases here are actions a bot does serve, and
// no request leaves the process before the check fails.
func TestMissingArgumentsAreNamed(t *testing.T) {
	ctx := context.Background()
	backend := telegram.NewBotBackend("1:x", telegram.BotOptions{})

	for _, call := range []struct {
		name string
		want []string
		run  func() Result
	}{
		{"poll", []string{"question", "options"}, func() Result {
			return HandleMessage(ctx, backend, MessageArgs{Action: "poll", ChatID: 1})
		}},
		{"schedule", []string{"send_at"}, func() Result {
			return HandleMessage(ctx, backend,
				MessageArgs{Action: "schedule", ChatID: 1, Text: "hi"})
		}},
		{"permissions", []string{"permissions"}, func() Result {
			return HandleChat(ctx, backend, ChatArgs{Action: "permissions", ChatID: 1})
		}},
		{"send_album", []string{"files"}, func() Result {
			return HandleMedia(ctx, backend, MediaArgs{Action: "send_album", ChatID: 1})
		}},
		{"alias_set", []string{"alias", "chat_id"}, func() Result {
			return HandleContact(ctx, backend, ContactArgs{Action: "alias_set"})
		}},
		{"send_gif", []string{"document_id"}, func() Result {
			return HandleMedia(ctx, backend, MediaArgs{Action: "send_gif", ChatID: 1})
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			message := result(t, call.run())
			for _, fragment := range call.want {
				if !strings.Contains(message, fragment) {
					t.Errorf("the error should name %q, got %q", fragment, message)
				}
			}
		})
	}
}

// A time argument is rejected with the format spelled out; the alternative is a
// caller retrying "tomorrow at 9" until it gives up.
func TestBadTimesSayWhatAValidOneLooksLike(t *testing.T) {
	ctx := context.Background()
	// A poll is served in both modes, so the bad time is what fails here rather
	// than the backend.
	backend := telegram.NewBotBackend("1:x", telegram.BotOptions{})
	message := result(t, HandleMessage(ctx, backend, MessageArgs{
		Action: "poll", ChatID: 1, Question: "Which one?", Options: []string{"a", "b"},
		CloseAt: "tomorrow at 9",
	}))
	if !strings.Contains(message, "RFC3339") || !strings.Contains(message, "2026-01-31T09:00:00Z") {
		t.Errorf("expected the format shown, got %q", message)
	}
}

// The alias actions edit a local file, so they must not be gated behind a
// capability the backend does not have.
func TestAliasActionsWorkWithoutTheContactCapability(t *testing.T) {
	message := result(t, HandleContact(context.Background(), newStub(),
		ContactArgs{Action: "alias_list"}))
	if message != "" {
		t.Errorf("alias_list should not need a capability, got %q", message)
	}
}

// The Bot API serves part of every extended domain, so the real bot backend has
// to satisfy those interfaces -- otherwise the dispatch would answer "user
// mode" for actions a bot can genuinely do.
func TestTheBotBackendCarriesTheExtendedCapabilities(t *testing.T) {
	var backend telegram.Backend = telegram.NewBotBackend("1:x", telegram.BotOptions{})

	for name, ok := range map[string]bool{
		"messages": func() bool { _, err := telegram.Messages(backend); return err == nil }(),
		"chats":    func() bool { _, err := telegram.Chats(backend); return err == nil }(),
		"media":    func() bool { _, err := telegram.Media(backend); return err == nil }(),
		"contacts": func() bool { _, err := telegram.Contacts(backend); return err == nil }(),
	} {
		if !ok {
			t.Errorf("the bot backend does not implement the %s capability", name)
		}
	}

	// Profile and folders are the two it genuinely has none of.
	if _, err := telegram.Profile(backend); err == nil {
		t.Error("a bot has no profile of its own to edit")
	}
	if _, err := telegram.Folders(backend); err == nil {
		t.Error("chat folders do not exist in the Bot API")
	}
}
