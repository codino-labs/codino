package claudecode

import (
	"time"

	"golang.org/x/sys/unix"
)

// birthTime is when the file at path was created; zero when the file system
// doesn't keep it.
func birthTime(path string) time.Time {
	var st unix.Statx_t
	if unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &st) != nil || st.Mask&unix.STATX_BTIME == 0 {
		return time.Time{}
	}
	return time.Unix(st.Btime.Sec, int64(st.Btime.Nsec))
}
