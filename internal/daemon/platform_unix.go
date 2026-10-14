//go:build unix

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func runtimeDir(name string) string {
	if runtime := os.Getenv("XDG_RUNTIME_DIR"); runtime != "" {
		return filepath.Join(runtime, name)
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", name, os.Getuid()))
}

func socketPath(dir string) string { return filepath.Join(dir, "host.sock") }

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

// listenSocket opens the Unix socket, owner-only. The caller holds the
// daemon lock, so any socket file left over is stale.
func listenSocket(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	// The directory is already owner-only; the socket is too, in case it moves.
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("socket permissions: %w", err)
	}
	return ln, nil
}

// dialSocket connects to the Unix socket. It fails with errUnavailable when
// no daemon listens.
func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUnavailable, err)
	}
	return conn, nil
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

// startDetached starts a spawned daemon in a session of its own, so it
// outlives the command that spawned it and leaves the caller's terminal.
func startDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, cmd.Start()
}
