package telegram

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The lock is what keeps a second server off a session another one is using --
// the failure mode that turns every call in both processes into a connection
// error, because Telegram invalidates an auth key it sees twice.
func TestSessionLockRefusesASecondHolder(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "default.session")

	first, err := acquireSessionLock(sessionPath)
	if err != nil {
		t.Fatalf("the first holder should get the lock: %v", err)
	}
	defer first.release()

	_, err = acquireSessionLock(sessionPath)
	if err == nil {
		t.Fatal("a second holder must be refused")
	}
	var busy *SessionBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("expected a SessionBusyError, got %T: %v", err, err)
	}
	if busy.HolderPID != os.Getpid() {
		t.Errorf("the error should name the holding process, got pid %d", busy.HolderPID)
	}
	// The message has to be the one an operator reads at 3am.
	for _, want := range []string{"already in use", "TELEGRAM_SESSION_NAME", "deleting the file by hand"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message should mention %q: %s", want, err)
		}
	}
}

func TestSessionLockIsReusableAfterRelease(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "default.session")

	first, err := acquireSessionLock(sessionPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first.release()

	second, err := acquireSessionLock(sessionPath)
	if err != nil {
		t.Fatalf("the lock must be free once released: %v", err)
	}
	second.release()
}

// Two different session names are two different locks: that is the escape
// hatch the error message points at.
func TestSessionLockIsPerSession(t *testing.T) {
	dir := t.TempDir()

	first, err := acquireSessionLock(filepath.Join(dir, "one.session"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer first.release()

	second, err := acquireSessionLock(filepath.Join(dir, "two.session"))
	if err != nil {
		t.Fatalf("a different session must lock independently: %v", err)
	}
	second.release()
}
