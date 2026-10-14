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
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, attrs.CreationTime.Nanoseconds())
}
