//go:build windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
)

// The Windows local socket is a named pipe with a user-only ACL (spec #1);
// it is not built yet.
var errWindows = errors.New("the Host Daemon is not supported on Windows yet")

func checkPrivateDir(string) error      { return errWindows }
func lockFile(string) (*os.File, error) { return nil, errWindows }
func detach(*exec.Cmd)                  {}
