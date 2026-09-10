package telegram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/gotd/td/session"
)

// sessionStorage is the MTProto session on disk, written so that two processes
// sharing it cannot destroy it.
//
// gotd's own FileStorage writes with os.WriteFile: the file is truncated and
// then filled, and the only guard is a mutex private to one process. That is
// fine for one client and wrong for the arrangement TELEGRAM_SESSION_LOCK=shared
// exists to support, where a supervisor runs a process per connection against
// one session file. There, a reader lands in the truncated window and gets a
// session that will not parse; gotd reads that as "no session", generates a
// brand-new auth key, and does it again on the next process. An account that
// asks Telegram for a new auth key hundreds of times an hour stops getting
// answers, and every connection then hangs in the handshake -- which is exactly
// how the user-mode servers went dark while the bot, which uses plain HTTPS,
// kept working.
//
// So a write goes to a temporary file and is renamed into place, which is
// atomic, and both reads and writes are serialized across processes by a lock
// file that survives the rename.
type sessionStorage struct {
	path string
	mu   sync.Mutex
}

func newSessionStorage(path string) *sessionStorage {
	return &sessionStorage{path: path}
}

// lockPath is the guard file. It is not the session file and not the
// whole-process claim in sessionlock_unix.go: that one is held for the life of
// the process, and taking it again here would deadlock against itself.
func (s *sessionStorage) lockPath() string { return s.path + ".rw" }

func (s *sessionStorage) LoadSession(context.Context) ([]byte, error) {
	if s == nil {
		return nil, errors.New("nil session storage is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var data []byte
	err := withFileLock(s.lockPath(), false, func() error {
		read, err := os.ReadFile(s.path)
		if err != nil {
			if os.IsNotExist(err) {
				return session.ErrNotFound
			}
			return err
		}
		data = read
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The server pre-creates the file 0600 before anything is written to it, so
	// an empty one means "no session yet" rather than a session that failed to
	// parse. Saying so plainly is what lets gotd start a fresh sign-in instead
	// of failing on an unmarshal error.
	if len(data) == 0 {
		return nil, session.ErrNotFound
	}
	return data, nil
}

func (s *sessionStorage) StoreSession(_ context.Context, data []byte) error {
	if s == nil {
		return errors.New("nil session storage is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	return withFileLock(s.lockPath(), true, func() error {
		dir := filepath.Dir(s.path)
		temp, err := os.CreateTemp(dir, filepath.Base(s.path)+".*.tmp")
		if err != nil {
			return err
		}
		name := temp.Name()
		defer func() { _ = os.Remove(name) }()

		if err := temp.Chmod(0o600); err != nil {
			_ = temp.Close()
			return err
		}
		if _, err := temp.Write(data); err != nil {
			_ = temp.Close()
			return err
		}
		// Flush before the rename: a rename that lands ahead of the data leaves
		// an empty session file behind a power cut, which reads as a lost
		// account.
		if err := temp.Sync(); err != nil {
			_ = temp.Close()
			return err
		}
		if err := temp.Close(); err != nil {
			return err
		}
		return os.Rename(name, s.path)
	})
}
