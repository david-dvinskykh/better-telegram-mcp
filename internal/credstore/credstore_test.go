package credstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CREDENTIAL_SECRET", "test-secret")
	store := New(dir)

	if err := store.Save(map[string]string{"TELEGRAM_BOT_TOKEN": "123:abc"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded["TELEGRAM_BOT_TOKEN"] != "123:abc" {
		t.Errorf("expected the token back, got %#v", loaded)
	}
}

func TestLoadReturnsNothingWhenNothingWasSaved(t *testing.T) {
	t.Setenv("CREDENTIAL_SECRET", "test-secret")
	loaded, err := New(t.TempDir()).Load()
	if err != nil {
		t.Fatalf("an empty store is not an error: %v", err)
	}
	if loaded != nil {
		t.Errorf("expected nothing, got %#v", loaded)
	}
}

// The point of the store is that the token is not sitting in plain text.
func TestSavedBlobDoesNotContainThePlaintext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CREDENTIAL_SECRET", "test-secret")

	if err := New(dir).Save(map[string]string{"TELEGRAM_BOT_TOKEN": "123:supersecret"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, configFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "supersecret") {
		t.Fatal("the credential is stored in the clear")
	}
}

func TestSavedBlobIsPrivateToTheOwner(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CREDENTIAL_SECRET", "test-secret")
	if err := New(dir).Save(map[string]string{"TELEGRAM_PHONE": "+15551234567"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, configFile))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected 0600 on the credential blob, got %o", mode)
	}
}

func TestADifferentSecretCannotReadTheBlob(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CREDENTIAL_SECRET", "first-secret")
	if err := New(dir).Save(map[string]string{"TELEGRAM_BOT_TOKEN": "123:abc"}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CREDENTIAL_SECRET", "second-secret")
	_, err := New(dir).Load()
	if err == nil {
		t.Fatal("a blob written under another secret must not decrypt")
	}
	if !strings.Contains(err.Error(), "logout") {
		t.Errorf("the error should say how to recover: %v", err)
	}
}

// With no operator secret the store generates a machine key, so `auth` works
// out of the box and still survives a restart.
func TestMachineKeyIsGeneratedAndReused(t *testing.T) {
	dir := t.TempDir()
	for _, key := range []string{"CREDENTIAL_SECRET", "MCP_DCR_SERVER_SECRET", "DCR_SERVER_SECRET", "MASTER_SECRET"} {
		t.Setenv(key, "")
	}

	store := New(dir)
	if err := store.Save(map[string]string{"TELEGRAM_PHONE": "+15551234567"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(dir).Load()
	if err != nil {
		t.Fatalf("a second process on the same machine must be able to read it: %v", err)
	}
	if loaded["TELEGRAM_PHONE"] != "+15551234567" {
		t.Errorf("expected the phone back, got %#v", loaded)
	}

	info, err := os.Stat(filepath.Join(dir, secretFile))
	if err != nil {
		t.Fatalf("the machine key should have been written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected 0600 on the machine key, got %o", mode)
	}
}

func TestClearIsSafeToRunTwice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CREDENTIAL_SECRET", "test-secret")
	store := New(dir)

	if err := store.Save(map[string]string{"TELEGRAM_BOT_TOKEN": "123:abc"}); err != nil {
		t.Fatal(err)
	}
	if !store.Exists() {
		t.Fatal("expected the blob to exist after a save")
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("clearing an empty store is not an error: %v", err)
	}
	if store.Exists() {
		t.Error("the blob should be gone")
	}
}
