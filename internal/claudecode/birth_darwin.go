package claudecode

import (
	"os"
	"syscall"
	"time"
)

// birthTime is when the file at path was created; zero when unknown.
func birthTime(path string) time.Time {
	info, err := os.Lstat(path)
	if err != nil {
		return time.Time{}
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}
	}
	return time.Unix(st.Birthtimespec.Unix())
}
