package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// On Windows the local socket is a named pipe with a user-only ACL (spec #1).
// The pipe's name and security descriptor are worked out here, on every OS,
// so they are tested everywhere; platform_windows.go opens the pipe.

// pipeName is the named pipe of the Host Daemon whose files are in dir. Pipe
// names are global to the machine, so it is derived from the (per-user)
// runtime directory: each user, and each test's directory, gets its own.
// Windows paths are case-insensitive, so one directory always gives one name.
func pipeName(dir string) string {
	key := strings.ToLower(strings.ReplaceAll(dir, "/", `\`))
	key = strings.TrimRight(key, `\`)
	sum := sha256.Sum256([]byte(key))
	return `\\.\pipe\host-` + hex.EncodeToString(sum[:12])
}

// pipeSecurityDescriptor is the SDDL for the Host Daemon's pipe: owned by the
// user, with a protected DACL (nothing inherited) whose one entry allows the
// user full access. Nobody else is listed, so nobody else can connect, not
// even Administrators or SYSTEM. Clients check the owner before they send
// anything, so a pipe another user created first is never trusted.
func pipeSecurityDescriptor(userSID string) string {
	return "O:" + userSID + "D:P(A;;GA;;;" + userSID + ")"
}
