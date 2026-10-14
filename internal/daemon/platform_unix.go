//go:build unix

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func checkPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("runtime directory: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("runtime directory %s is not a directory owned by this user", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("runtime directory %s is accessible to other users (mode %v)", dir, info.Mode().Perm())
	}
	return nil
}

// lockFile takes the daemon lock without waiting. It fails with
// ErrAlreadyRunning when another daemon holds it.
func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock: %w", err)
	}
	return f, nil
}

// detach makes a spawned daemon outlive the command that spawned it and
// leaves the caller's terminal session.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
