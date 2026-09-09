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

	first, err := acquireSessionLock(sessionPath, false)
	if err != nil {
		t.Fatalf("the first holder should get the lock: %v", err)
	}
	defer first.release()

	_, err = acquireSessionLock(sessionPath, false)
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

	first, err := acquireSessionLock(sessionPath, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first.release()

	second, err := acquireSessionLock(sessionPath, false)
	if err != nil {
		t.Fatalf("the lock must be free once released: %v", err)
	}
	second.release()
}

// Two different session names are two different locks: that is the escape
// hatch the error message points at.
func TestSessionLockIsPerSession(t *testing.T) {
	dir := t.TempDir()

	first, err := acquireSessionLock(filepath.Join(dir, "one.session"), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer first.release()

	second, err := acquireSessionLock(filepath.Join(dir, "two.session"), false)
	if err != nil {
		t.Fatalf("a different session must lock independently: %v", err)
	}
	second.release()
}

// A supervisor that opens several connections to one stdio server starts one
// process per connection. Under the default exclusive lock all but the first
// are refused and exit, which the supervisor counts as crashes; the shared mode
// is the escape hatch for that topology, where every process is in one
// container and reaches Telegram from one address.
func TestSharedModeLetsSeveralProcessesHoldOneSession(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "shared.session")

	first, err := acquireSessionLock(sessionPath, true)
	if err != nil {
		t.Fatalf("the first shared lock failed: %v", err)
	}
	defer first.release()

	second, err := acquireSessionLock(sessionPath, true)
	if err != nil {
		t.Fatalf("a second shared lock should be granted, got %v", err)
	}
	second.release()
}

// Shared mode is opt-in per process, so a server left on the default still
// refuses to join one -- otherwise a misconfigured pair would share a session
// without anyone asking for it.
func TestAnExclusiveLockIsStillRefusedWhileSharedIsHeld(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "mixed.session")

	shared, err := acquireSessionLock(sessionPath, true)
	if err != nil {
		t.Fatalf("the shared lock failed: %v", err)
	}
	defer shared.release()

	if _, err := acquireSessionLock(sessionPath, false); err == nil {
		t.Error("an exclusive lock should not be granted while a shared one is held")
	}
}
