package liveness

import (
	"errors"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess gives a running process.
const stillActive = 259

// open opens the process pid to query it; ok is false when there is no such
// process, err is set when it can't tell (e.g. access denied).
func open(pid int) (h windows.Handle, ok bool, err error) {
	h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return 0, false, nil // no process has this pid
	}
	if err != nil {
		return 0, false, err
	}
	return h, true, nil
}

func creationTime(h windows.Handle) uint64 {
	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &creation, &exit, &kernel, &user) != nil {
		return 0
	}
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
}

// Started is when the process pid was created, as a FILETIME; 0 when it
// can't be read.
func Started(pid int) uint64 {
	h, ok, err := open(pid)
	if !ok || err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	return creationTime(h)
}

// Parent is 0 on Windows: hooks run through Git Bash, whose processes sit
// between the hook and Claude Code, so the grandparent is no Agent.
// CLAUDE_PID names it instead.
func Parent(int) int { return 0 }

func gone(pid int, started uint64) bool {
	h, ok, err := open(pid)
	if err != nil {
		return false // it exists but isn't ours to query: not gone
	}
	if !ok {
		return true
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) == nil && code != stillActive {
		return true // exited; a handle somewhere keeps its pid
	}
	return started != 0 && creationTime(h) != started
}
