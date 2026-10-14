// Package daemon is the Host Daemon's local socket: the listening end that
// feeds a host.Host, and the Client that `codino event` (and later the Claude
// Code Adapter) use to reach it, spawning the daemon on demand.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths are the files of one user's Host Daemon, all in one owner-only
// directory.
type Paths struct {
	Dir    string
	Socket string // the local socket clients connect to: a Unix socket, or a named pipe on Windows
	Lock   string // held by the running daemon, so only one runs
	Log    string // the spawned daemon's stdout and stderr
}

// PathsIn lays the daemon files out in dir.
func PathsIn(dir string) Paths {
	return Paths{
		Dir:    dir,
		Socket: socketPath(dir),
		Lock:   filepath.Join(dir, "host.lock"),
		Log:    filepath.Join(dir, "host.log"),
	}
}

// DefaultPaths puts the daemon files in the user's runtime directory:
// $XDG_RUNTIME_DIR/<name>, or a per-user directory under the system
// temporary directory when there is none; on Windows
// %LocalAppData%\<name>\run, inside the user's profile.
func DefaultPaths(name string) Paths {
	return PathsIn(runtimeDir(name))
}

// ensureDir creates the directory owner-only, and refuses one that another
// user owns or that others can access (on Windows: one that is a link; the
// profile's ACL keeps the directory to the user).
func (p Paths) ensureDir() error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("runtime directory: %w", err)
	}
	return checkPrivateDir(p.Dir)
}
