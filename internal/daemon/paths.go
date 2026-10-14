// Package daemon is the Host Daemon's local socket: the server that feeds a
// host.Host, and the client that `codino event` (and later the Claude Code
// Adapter) use to reach it, spawning the daemon on demand.
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
	Socket string // the Unix socket clients connect to
	Lock   string // held by the running daemon, so only one runs
	Log    string // the spawned daemon's stdout and stderr
}

// PathsIn lays the daemon files out in dir.
func PathsIn(dir string) Paths {
	return Paths{
		Dir:    dir,
		Socket: filepath.Join(dir, "host.sock"),
		Lock:   filepath.Join(dir, "host.lock"),
		Log:    filepath.Join(dir, "host.log"),
	}
}

// DefaultPaths puts the daemon files in the user's runtime directory
// ($XDG_RUNTIME_DIR/<name>), or in a per-user directory under the system
// temporary directory when there is none.
func DefaultPaths(name string) Paths {
	if runtime := os.Getenv("XDG_RUNTIME_DIR"); runtime != "" {
		return PathsIn(filepath.Join(runtime, name))
	}
	return PathsIn(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", name, os.Getuid())))
}

// ensureDir creates the directory owner-only, and refuses one that another
// user owns or that others can access.
func (p Paths) ensureDir() error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("runtime directory: %w", err)
	}
	return checkPrivateDir(p.Dir)
}
