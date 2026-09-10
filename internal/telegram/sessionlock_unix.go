//go:build unix

package telegram

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// sessionLock is a whole-process claim on one MTProto session file.
//
// Two clients running on the same auth key is not a file-corruption problem
// that a retry fixes: Telegram invalidates the key when it sees the same
// session from two connections, and every call after that fails. So the second
// process has to be refused at startup, loudly, instead of quietly fighting for
// the session.
//
// The claim is a kernel advisory lock (flock), not a PID file. That is the
// whole point: the kernel drops the lock when the holder exits for any reason,
// including a kill -9 or a container stop, so a crash can never leave a stale
// lock that has to be deleted by hand.
type sessionLock struct {
	file *os.File
	path string
}

// acquireSessionLock claims the session, or reports who holds it.
//
// shared takes the lock in shared mode instead, letting any number of processes
// on this host use one session. That is safe only when they all reach Telegram
// from the same address -- which is what a supervisor spawning several
// connections to one stdio server inside one container does, and is the reason
// the option exists at all.
func acquireSessionLock(sessionPath string, shared bool) (*sessionLock, error) {
	path := sessionPath + ".lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open the session lock at %s: %w", path, err)
	}

	mode := syscall.LOCK_EX
	if shared {
		mode = syscall.LOCK_SH
	}
	if err := syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB); err != nil {
		holder := readLockHolder(file)
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &SessionBusyError{
				SessionPath: sessionPath,
				LockPath:    path,
				HolderPID:   holder,
			}
		}
		return nil, fmt.Errorf("cannot lock the session at %s: %w", path, err)
	}

	// Record who holds it, purely so the next process can name the culprit.
	// Under a shared lock several processes hold it at once, so the pid would
	// be a lie: whoever wrote last is not the only holder.
	if !shared {
		if err := file.Truncate(0); err == nil {
			_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
			_ = file.Sync()
		}
	}
	return &sessionLock{file: file, path: path}, nil
}

// release drops the claim. The lock file itself is left in place: unlinking it
// would race another process that has already opened it and is about to lock.
func (l *sessionLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

func readLockHolder(file *os.File) int {
	buf := make([]byte, 32)
	n, err := file.ReadAt(buf, 0)
	if n == 0 || (err != nil && n == 0) {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		return 0
	}
	return pid
}

// withFileLock runs fn while holding a kernel advisory lock on path, creating
// the file if it is not there.
//
// It is a different file from the session itself on purpose: the session file
// is replaced by a rename on every write, and a lock taken on the old inode
// would guard nothing once the new one is in place.
func withFileLock(path string, exclusive bool, fn func() error) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("cannot open the session write lock at %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(file.Fd()), mode); err != nil {
		return fmt.Errorf("cannot take the session write lock at %s: %w", path, err)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()

	return fn()
}
