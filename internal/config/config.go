// Package config resolves the server's runtime settings from the TELEGRAM_*
// environment and from the encrypted single-user config written by the `auth`
// CLI subcommand.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Telegram's own public desktop api_id/api_hash pair
// (https://core.telegram.org/api/obtaining_api_id). Bundling it gives
// zero-config user mode: the operator only has to supply a phone number.
// TELEGRAM_API_ID / TELEGRAM_API_HASH override it.
const (
	bundledAPIID   = 37984984
	bundledAPIHash = "2f5f4c76c4de7c07302380c788390100" // gitleaks:allow
)

// Mode is the Telegram API a backend talks: the Bot API or MTProto as a user.
type Mode string

const (
	ModeBot  Mode = "bot"
	ModeUser Mode = "user"
)

// Settings is the resolved configuration of one server process.
type Settings struct {
	BotToken string
	APIID    int
	APIHash  string
	Phone    string

	SessionName string
	DataDir     string

	// Guardrails for message(action="callbacks"): who may press a button and
	// what the callback data must look like. Empty means no filter.
	AllowedCallbackSenders []int64
	CallbackDataPattern    string

	// Set when another process owns this bot's getUpdates stream and queues
	// the presses as JSONL for this server to read.
	CallbackQueueFile string
	// Where message_id -> Claude session registrations are appended.
	ResumeMapFile string

	// AliasesFile is the local map from the words people use for a contact to
	// the id behind them. Empty means the default under DataDir.
	AliasesFile string

	// APIBase overrides https://api.telegram.org for bot mode. Telegram
	// supports self-hosted Bot API servers
	// (https://core.telegram.org/bots/api#using-a-local-bot-api-server), and
	// pointing at one is also how this server is exercised end to end without
	// touching the real Telegram.
	APIBase string

	Mode Mode
}

// Load reads the TELEGRAM_* environment into Settings. Values that are unset
// (or empty, which is how plugin.json spells "unset") fall back to defaults.
func Load() (*Settings, error) {
	s := &Settings{
		BotToken:            env("TELEGRAM_BOT_TOKEN"),
		APIID:               bundledAPIID,
		APIHash:             bundledAPIHash,
		Phone:               env("TELEGRAM_PHONE"),
		SessionName:         "default",
		CallbackDataPattern: env("TELEGRAM_CALLBACK_DATA_PATTERN"),
		CallbackQueueFile:   env("TELEGRAM_CALLBACK_QUEUE_FILE"),
		ResumeMapFile:       env("TELEGRAM_RESUME_MAP_FILE"),
		AliasesFile:         env("TELEGRAM_ALIASES_FILE"),
		APIBase:             strings.TrimRight(env("TELEGRAM_API_BASE"), "/"),
		Mode:                ModeBot,
	}

	if raw := env("TELEGRAM_API_ID"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("TELEGRAM_API_ID must be an integer, got %q", raw)
		}
		s.APIID = id
	}
	if raw := env("TELEGRAM_API_HASH"); raw != "" {
		s.APIHash = raw
	}
	if raw := env("TELEGRAM_SESSION_NAME"); raw != "" {
		s.SessionName = raw
	}

	dataDir := env("TELEGRAM_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot resolve home directory: %w", err)
		}
		dataDir = filepath.Join(home, ".better-telegram-mcp")
	}
	s.DataDir = dataDir

	senders, err := parseSenders(env("TELEGRAM_ALLOWED_CALLBACK_SENDERS"))
	if err != nil {
		return nil, err
	}
	s.AllowedCallbackSenders = senders

	s.detectMode()
	return s, nil
}

// ApplySaved merges the credentials persisted by `auth` into the settings.
// The environment wins: an operator who exports TELEGRAM_BOT_TOKEN expects that
// token, not a stale one saved months ago.
func (s *Settings) ApplySaved(saved map[string]string) {
	if s.BotToken == "" {
		s.BotToken = saved["TELEGRAM_BOT_TOKEN"]
	}
	if s.Phone == "" {
		s.Phone = saved["TELEGRAM_PHONE"]
	}
	if env("TELEGRAM_API_ID") == "" {
		if raw := saved["TELEGRAM_API_ID"]; raw != "" {
			if id, err := strconv.Atoi(raw); err == nil {
				s.APIID = id
			}
		}
	}
	if env("TELEGRAM_API_HASH") == "" {
		if raw := saved["TELEGRAM_API_HASH"]; raw != "" {
			s.APIHash = raw
		}
	}
	s.detectMode()
}

// detectMode picks the backend the credentials describe. A bot token wins:
// it is the cheaper, stateless mode, and it is what an operator who sets both
// is asking for.
func (s *Settings) detectMode() {
	switch {
	case s.BotToken != "":
		s.Mode = ModeBot
	case s.Phone != "" && s.APIID != 0 && s.APIHash != "":
		s.Mode = ModeUser
	}
}

// IsConfigured reports whether any usable Telegram credential is present.
func (s *Settings) IsConfigured() bool {
	return s.BotToken != "" || (s.Phone != "" && s.APIID != 0 && s.APIHash != "")
}

// SessionPath is the on-disk MTProto session for user mode.
func (s *Settings) SessionPath() string {
	return filepath.Join(s.DataDir, s.SessionName+".session")
}

// CallbackCursorPath is where the last processed callback update_id is kept, so
// a restart cannot re-deliver a decision that was already acted on.
func (s *Settings) CallbackCursorPath() string {
	return filepath.Join(s.DataDir, s.SessionName+".callbacks.json")
}

// AliasesPath is where the contact aliases live. They are keyed by person, not
// by session, so every session on this machine shares one file.
func (s *Settings) AliasesPath() string {
	if s.AliasesFile != "" {
		return s.AliasesFile
	}
	return filepath.Join(s.DataDir, "aliases.json")
}

func parseSenders(raw string) ([]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf(
				"TELEGRAM_ALLOWED_CALLBACK_SENDERS must be a comma-separated list "+
					"of numeric Telegram user IDs, got %q", part)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// env reads a variable, treating a whitespace-only value as unset.
func env(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}
