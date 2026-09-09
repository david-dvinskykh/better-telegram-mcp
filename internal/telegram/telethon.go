package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gotd/td/session"
)

// A Telethon session string is the credential the Python telegram servers
// carry: base64 of the datacenter, its address, and the 256-byte auth key.
// Adopting one is what lets this server take over an account that is already
// signed in, instead of asking the operator for another code by SMS.
//
// The auth key is the whole account. Telegram invalidates a key it sees on two
// connections at once, so importing a string that another server is still
// running would take that server down and this one with it: the string has to
// be moved, not copied.

// importTelethonSession writes the account behind a Telethon session string
// into this server's session file, unless the file already holds one.
//
// It is a no-op once the file has content, so leaving the string in the
// environment is safe: it seeds a fresh deployment and is ignored afterwards,
// which is what makes the same config work on a restart.
func (u *UserBackend) importTelethonSession(ctx context.Context) error {
	if u.sessionString == "" {
		return nil
	}

	storage := &session.FileStorage{Path: u.sessionPath}
	if existing, err := storage.LoadSession(ctx); err == nil && len(existing) > 0 {
		return nil
	}

	data, err := session.TelethonSession(strings.TrimSpace(u.sessionString))
	if err != nil {
		return fmt.Errorf(
			"TELEGRAM_SESSION_STRING is not a Telethon session string: %w", err)
	}

	loader := session.Loader{Storage: storage}
	if err := loader.Save(ctx, data); err != nil {
		return err
	}
	u.secureSessionFile()
	return nil
}

// ImportTelethonSessionFile writes a Telethon session string into a session
// file for the CLI, so an operator can adopt an account without putting the
// string in the environment.
func ImportTelethonSessionFile(ctx context.Context, sessionPath, sessionString string) error {
	trimmed := strings.TrimSpace(sessionString)
	if trimmed == "" {
		return errors.New("a Telethon session string is required")
	}
	data, err := session.TelethonSession(trimmed)
	if err != nil {
		return fmt.Errorf("that is not a Telethon session string: %w", err)
	}

	loader := session.Loader{Storage: &session.FileStorage{Path: sessionPath}}
	if err := loader.Save(ctx, data); err != nil {
		return err
	}
	if err := os.Chmod(sessionPath, 0o600); err != nil {
		return err
	}
	return nil
}
