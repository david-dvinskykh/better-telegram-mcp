package telegram

import "fmt"

// SessionBusyError says another process already owns this MTProto session.
//
// The message is deliberately long: the failure it replaces ("Not connected")
// sent an operator hunting through container logs for a cause that was two
// servers pointed at one session file all along.
type SessionBusyError struct {
	SessionPath string
	LockPath    string
	// HolderPID is the process that holds the lock, or 0 when it could not be
	// read (an older holder, or a platform without the recorded pid).
	HolderPID int
}

func (e *SessionBusyError) Error() string {
	holder := "another better-telegram-mcp process"
	if e.HolderPID != 0 {
		holder = fmt.Sprintf("process %d", e.HolderPID)
	}
	return fmt.Sprintf(
		"The Telegram session %s is already in use by %s. "+
			"Two clients on one session make Telegram invalidate the auth key, so "+
			"this server refuses to start a second one. Stop the other process, or "+
			"give this one its own session with TELEGRAM_SESSION_NAME. "+
			"The lock at %s is held by the kernel and is released automatically when "+
			"that process exits -- deleting the file by hand is never the fix.",
		e.SessionPath, holder, e.LockPath)
}
