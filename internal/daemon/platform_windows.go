//go:build windows

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

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// On Windows the local socket is a named pipe (pipe.go) that only this user
// can open, and the runtime directory is in the user's profile, which
// Windows already keeps to the user (and SYSTEM and Administrators).

func runtimeDir(name string) string {
	base, err := os.UserCacheDir() // %LocalAppData%
	if err != nil {
		base = os.TempDir() // per user too, under the profile
	}
	return filepath.Join(base, name, "run")
}

func socketPath(dir string) string { return pipeName(dir) }

// checkPrivateDir refuses a runtime directory that is a link (a junction or
// symlink could send the daemon's files elsewhere). Access is the profile's
// ACL; the pipe, which is what clients reach the daemon through, has its own.
func checkPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("runtime directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return fmt.Errorf("runtime directory %s is not a plain directory", dir)
	}
	return nil
}

// userSID is the SID of the user this process runs as.
func userSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("current user: %w", err)
	}
	return user.User.Sid.Copy()
}

// listenSocket creates the pipe with a user-only ACL. Creating it fails when
// the name exists, so a pipe another user made first is never served on.
// Message mode lets a client half-close its end (Show.Stop).
func listenSocket(path string) (net.Listener, error) {
	sid, err := userSID()
	if err != nil {
		return nil, err
	}
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor(sid.String()),
		MessageMode:        true,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	return ln, nil
}

// dialSocket connects to the pipe, and only to one this user owns: a pipe
// another user created under the name would otherwise read the events. It
// fails with errUnavailable when no daemon listens.
func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUnavailable, err)
	}
	if err := checkPipeOwner(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func checkPipeOwner(conn net.Conn) error {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("pipe: no handle to check its owner")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("pipe owner: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("pipe owner: %w", err)
	}
	me, err := userSID()
	if err != nil {
		return err
	}
	if !owner.Equals(me) {
		return fmt.Errorf("the Host Daemon's pipe is owned by %s, not this user", owner)
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
	var ol windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock: %w", err)
	}
	return f, nil
}

// startDetached starts a spawned daemon in a process group of its own with
// a hidden console (its own children, such as the Virtual Device, then
// open no window either), out of the hook's job object when the job allows
// it, so it outlives the hook that spawned it.
func startDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	const flags = windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW
	cmd := newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB, HideWindow: true}
	err := cmd.Start()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The job doesn't allow breaking away: stay in it.
		cmd = newCmd()
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		err = cmd.Start()
	}
	return cmd, err
}
