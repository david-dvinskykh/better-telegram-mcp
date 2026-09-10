//go:build !unix

package telegram

import (
	"fmt"
	"os"
	"strconv"
)

// sessionLock on a platform without flock. The claim is best-effort: the file
// is opened for exclusive use so a second process on the same machine is still
// refused, but there is no kernel-released lock to fall back on.
type sessionLock struct {
	file *os.File
	path string
}

func acquireSessionLock(sessionPath string, shared bool) (*sessionLock, error) {
	path := sessionPath + ".lock"
	if shared {
		// There is no shared mode without flock, and refusing would be worse
		// than the guard is worth on a platform this server is not deployed on.
		return &sessionLock{}, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, &SessionBusyError{SessionPath: sessionPath, LockPath: path}
		}
		return nil, fmt.Errorf("cannot open the session lock at %s: %w", path, err)
	}
	_, _ = file.WriteString(strconv.Itoa(os.Getpid()))
	return &sessionLock{file: file, path: path}, nil
}

// release drops the claim. Here the lock file IS the claim, so it has to go;
// a process killed before this runs leaves a stale file the operator deletes.
func (l *sessionLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = l.file.Close()
	_ = os.Remove(l.path)
	l.file = nil
}

// withFileLock has nothing to lock with on a platform without flock, so it just
// runs fn. The window it leaves open is the same one the sessionLock above
// already accepts there.
func withFileLock(_ string, _ bool, fn func() error) error { return fn() }
