package config

import (
	"path/filepath"
	"testing"
)

func TestLoadDefaultsToBundledUserCredentials(t *testing.T) {
	t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())

	settings, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settings.APIID != bundledAPIID || settings.APIHash != bundledAPIHash {
		t.Error("user mode should work with no api_id/api_hash of the operator's own")
	}
	if settings.IsConfigured() {
		t.Error("bundled app credentials alone are not a login")
	}
}

func TestModeFollowsTheCredentials(t *testing.T) {
	t.Run("bot token", func(t *testing.T) {
		t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())
		t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
		settings, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if settings.Mode != ModeBot || !settings.IsConfigured() {
			t.Errorf("expected configured bot mode, got %s / %v", settings.Mode, settings.IsConfigured())
		}
	})

	t.Run("phone", func(t *testing.T) {
		t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())
		t.Setenv("TELEGRAM_PHONE", "+15551234567")
		settings, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if settings.Mode != ModeUser || !settings.IsConfigured() {
			t.Errorf("expected configured user mode, got %s / %v", settings.Mode, settings.IsConfigured())
		}
	})

	t.Run("both prefers bot", func(t *testing.T) {
		t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())
		t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
		t.Setenv("TELEGRAM_PHONE", "+15551234567")
		settings, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if settings.Mode != ModeBot {
			t.Errorf("a bot token is the cheaper mode and should win, got %s", settings.Mode)
		}
	})
}

// plugin.json spells "unset" as an empty string, which must not read as a
// credential.
func TestEmptyEnvValuesAreTreatedAsUnset(t *testing.T) {
	t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())
	t.Setenv("TELEGRAM_BOT_TOKEN", "   ")
	t.Setenv("TELEGRAM_PHONE", "")

	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.IsConfigured() {
		t.Error("blank env values are not credentials")
	}
}

func TestApplySavedDoesNotOverrideTheEnvironment(t *testing.T) {
	t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())
	t.Setenv("TELEGRAM_BOT_TOKEN", "env:token")

	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	settings.ApplySaved(map[string]string{"TELEGRAM_BOT_TOKEN": "saved:token"})

	if settings.BotToken != "env:token" {
		t.Errorf("an exported token is what the operator asked for, got %q", settings.BotToken)
	}
}

func TestApplySavedFillsWhatTheEnvironmentLeftEmpty(t *testing.T) {
	t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())

	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	settings.ApplySaved(map[string]string{"TELEGRAM_PHONE": "+15551234567"})

	if settings.Mode != ModeUser || !settings.IsConfigured() {
		t.Errorf("saved credentials should configure the server, got %s", settings.Mode)
	}
}

func TestParseAllowedCallbackSenders(t *testing.T) {
	t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())
	t.Setenv("TELEGRAM_ALLOWED_CALLBACK_SENDERS", " 111 , 222 ,")

	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.AllowedCallbackSenders) != 2 ||
		settings.AllowedCallbackSenders[0] != 111 || settings.AllowedCallbackSenders[1] != 222 {
		t.Errorf("expected [111 222], got %v", settings.AllowedCallbackSenders)
	}
}

func TestParseAllowedCallbackSendersRejectsNonNumeric(t *testing.T) {
	t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())
	t.Setenv("TELEGRAM_ALLOWED_CALLBACK_SENDERS", "111,@david")

	if _, err := Load(); err == nil {
		t.Fatal("a username is not a user id and should be rejected at startup")
	}
}

func TestPathsHangOffTheSessionName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TELEGRAM_DATA_DIR", dir)
	t.Setenv("TELEGRAM_SESSION_NAME", "work")

	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.SessionPath() != filepath.Join(dir, "work.session") {
		t.Errorf("unexpected session path: %s", settings.SessionPath())
	}
	if settings.CallbackCursorPath() != filepath.Join(dir, "work.callbacks.json") {
		t.Errorf("unexpected cursor path: %s", settings.CallbackCursorPath())
	}
}

// A session string is an account that is already signed in, so it starts user
// mode on its own -- the phone number is only how a fresh sign-in begins.
func TestASessionStringAloneConfiguresUserMode(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_PHONE", "")
	t.Setenv("TELEGRAM_SESSION_STRING", "1AsCoAAEBu2Fh")
	t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())

	settings, err := Load()
	if err != nil {
		t.Fatalf("could not load settings: %v", err)
	}
	if !settings.IsConfigured() {
		t.Error("a session string should count as configured")
	}
	if settings.Mode != ModeUser {
		t.Errorf("expected user mode, got %q", settings.Mode)
	}
}

// A bot token still wins: it is the cheaper mode, and it is what an operator
// who sets both is asking for.
func TestABotTokenStillWinsOverASessionString(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:token")
	t.Setenv("TELEGRAM_SESSION_STRING", "1AsCoAAEBu2Fh")
	t.Setenv("TELEGRAM_DATA_DIR", t.TempDir())

	settings, err := Load()
	if err != nil {
		t.Fatalf("could not load settings: %v", err)
	}
	if settings.Mode != ModeBot {
		t.Errorf("expected bot mode, got %q", settings.Mode)
	}
}
