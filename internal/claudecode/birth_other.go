//go:build !linux && !darwin

package claudecode

import "time"

// birthTime is zero: this platform's creation time is not read (Windows:
// where Claude Code keeps its tool output is unverified), so no approved tool
// is seen starting.
func birthTime(string) time.Time { return time.Time{} }
