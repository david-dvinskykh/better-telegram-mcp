package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func aliasFile(t *testing.T, entries map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aliases.json")
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A name only this machine knows must reach the Bot API as the id behind it,
// or every alias would be a "chat not found".
func TestBotResolvesAnAliasBeforeCallingTelegram(t *testing.T) {
	fake := newFakeBotAPI(t)
	fake.on("sendMessage", map[string]any{"message_id": 1})
	backend := fake.backend(t, BotOptions{
		AliasesPath: aliasFile(t, map[string]any{
			"андрей бекендер": map[string]any{"id": 375465077, "name": "Andrey"},
		}),
	})

	if _, err := backend.SendMessage(context.Background(),
		"Андрей Бекендер", "hi", SendOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	params := fake.callsTo("sendMessage")[0].Params
	if got := params["chat_id"]; got != float64(375465077) {
		t.Errorf("chat_id reached Telegram as %#v, not the aliased id", got)
	}
}

// An id or a @username is Telegram's to resolve; substituting for those would
// let a local file shadow a real account.
func TestBotLeavesIdsAndUsernamesAlone(t *testing.T) {
	fake := newFakeBotAPI(t)
	fake.on("sendMessage", map[string]any{"message_id": 1})
	backend := fake.backend(t, BotOptions{
		AliasesPath: aliasFile(t, map[string]any{
			"durov":   map[string]any{"id": 1},
			"1234567": map[string]any{"id": 2},
		}),
	})

	for _, chat := range []any{"@durov", "1234567"} {
		if _, err := backend.SendMessage(context.Background(), chat, "hi", SendOptions{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	calls := fake.callsTo("sendMessage")
	if got := calls[0].Params["chat_id"]; got != "@durov" {
		t.Errorf("a username was rewritten to %#v", got)
	}
	if got := calls[1].Params["chat_id"]; got != "1234567" {
		t.Errorf("a numeric id was rewritten to %#v", got)
	}
}

// An unknown name is passed through so Telegram's own error is what the caller
// sees, rather than a silent substitution.
func TestBotPassesAnUnknownNameThrough(t *testing.T) {
	fake := newFakeBotAPI(t)
	fake.on("sendMessage", map[string]any{"message_id": 1})
	backend := fake.backend(t, BotOptions{AliasesPath: aliasFile(t, map[string]any{})})

	if _, err := backend.SendMessage(context.Background(),
		"кто-то новый", "hi", SendOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fake.callsTo("sendMessage")[0].Params["chat_id"]; got != "кто-то новый" {
		t.Errorf("chat_id was rewritten to %#v", got)
	}
}

func TestLooksLikeHandle(t *testing.T) {
	for _, handle := range []string{"durov", "@durov", "some_channel", "abc12"} {
		if !looksLikeHandle(handle) {
			t.Errorf("%q should read as a handle Telegram can resolve", handle)
		}
	}
	for _, name := range []string{"андрей бекендер", "мама", "bob", "a b", ""} {
		if looksLikeHandle(name) {
			t.Errorf("%q should not read as a handle", name)
		}
	}
}

// A name nobody has explained has to come back as the question to ask, not as
// a dead end -- that instruction is the whole learning loop.
func TestUnknownReferenceAsksWhoThatIs(t *testing.T) {
	cause := errors.New("chat not found")

	answer := unknownReference("андрей бекендер", cause)
	for _, fragment := range []string{"alias_set", "андрей бекендер", "Ask who that is"} {
		if !strings.Contains(answer.Error(), fragment) {
			t.Errorf("the instruction should mention %q, got %s", fragment, answer)
		}
	}

	// A username that does not exist is Telegram's answer to give, not ours.
	if got := unknownReference("nosuchchannel", cause); got != cause {
		t.Errorf("a handle should keep Telegram's own error, got %v", got)
	}
}
