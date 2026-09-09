package telegram

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/session"
)

// telethonString builds a session string in Telethon's own format: a version
// byte, then base64 of dc id, IPv4 address, port and the 256-byte auth key.
func telethonString(t *testing.T, dc byte) string {
	t.Helper()
	packed := make([]byte, 0, 1+4+2+256)
	packed = append(packed, dc)
	packed = append(packed, 192, 168, 0, 1)
	packed = binary.BigEndian.AppendUint16(packed, 443)
	key := make([]byte, 256)
	for i := range key {
		key[i] = byte(i)
	}
	packed = append(packed, key...)
	return "1" + base64.URLEncoding.EncodeToString(packed)
}

// Adopting an account that is already signed in is what makes replacing the
// Python server a config change rather than another round of SMS codes.
func TestImportingATelethonSessionWritesAUsableSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adopted.session")
	ctx := context.Background()

	if err := ImportTelethonSessionFile(ctx, path, telethonString(t, 2)); err != nil {
		t.Fatalf("import failed: %v", err)
	}

	loader := session.Loader{Storage: &session.FileStorage{Path: path}}
	data, err := loader.Load(ctx)
	if err != nil {
		t.Fatalf("the written session does not load back: %v", err)
	}
	if data.DC != 2 || data.Addr != "192.168.0.1:443" {
		t.Errorf("unexpected session data: DC=%d Addr=%s", data.DC, data.Addr)
	}
	if len(data.AuthKey) != 256 {
		t.Errorf("expected a 256-byte auth key, got %d", len(data.AuthKey))
	}

	// The auth key is the whole account, so the file must not be readable by
	// anyone else.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected 0600, got %o", mode)
	}
}

func TestImportingRubbishSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adopted.session")
	err := ImportTelethonSessionFile(context.Background(), path, "not-a-session")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "session string") {
		t.Errorf("the error should say what was expected, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a rejected string should not leave a session file behind")
	}
}

// The string seeds an empty session and is ignored afterwards, so leaving it in
// the environment survives a restart instead of overwriting what the sign-in
// produced.
func TestAnExistingSessionIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.session")
	ctx := context.Background()

	if err := ImportTelethonSessionFile(ctx, path, telethonString(t, 4)); err != nil {
		t.Fatalf("seeding failed: %v", err)
	}

	backend := NewUserBackend(UserOptions{
		SessionPath:   path,
		SessionString: telethonString(t, 2),
	})
	if err := backend.importTelethonSession(ctx); err != nil {
		t.Fatalf("import failed: %v", err)
	}

	loader := session.Loader{Storage: &session.FileStorage{Path: path}}
	data, err := loader.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if data.DC != 4 {
		t.Errorf("the existing session was replaced: DC=%d", data.DC)
	}
}

// A server with no string configured must not touch the session file at all.
func TestNoStringIsANoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.session")
	backend := NewUserBackend(UserOptions{SessionPath: path})
	if err := backend.importTelethonSession(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("no session file should have been created")
	}
}
