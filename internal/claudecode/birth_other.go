//go:build !linux && !darwin && !windows

package claudecode

import "time"

// birthTime is zero: this platform's creation time is not read, so no
// approved tool is seen starting.
func birthTime(string) time.Time { return time.Time{} }
