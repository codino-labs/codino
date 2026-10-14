//go:build unix

package liveness

import (
	"errors"
	"syscall"
)

// signalGone reports whether no process has this pid, by sending it signal 0.
// EPERM means one exists but belongs to another user: not gone.
func signalGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
