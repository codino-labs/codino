package liveness

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// procStat is the part of /proc/<pid>/stat this package reads.
type procStat struct {
	state   byte
	ppid    int
	started uint64
}

// readStat reads /proc/<pid>/stat.
func readStat(pid int) (procStat, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procStat{}, err
	}
	// pid (comm) state ppid … starttime (field 22) …; comm may contain
	// spaces and parentheses.
	stat := string(raw)
	fields := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
	if len(fields) < 20 || len(fields[0]) != 1 {
		return procStat{}, fmt.Errorf("/proc/%d/stat: unexpected format", pid)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return procStat{}, fmt.Errorf("/proc/%d/stat: ppid: %w", pid, err)
	}
	started, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("/proc/%d/stat: starttime: %w", pid, err)
	}
	return procStat{state: fields[0][0], ppid: ppid, started: started}, nil
}

// Started is when the process pid started, in clock ticks since boot; 0 when
// it can't be read.
func Started(pid int) uint64 {
	st, err := readStat(pid)
	if err != nil {
		return 0
	}
	return st.started
}

// Parent is the parent of the process pid; 0 when it can't be read.
func Parent(pid int) int {
	st, err := readStat(pid)
	if err != nil {
		return 0
	}
	return st.ppid
}

func gone(pid int, started uint64) bool {
	st, err := readStat(pid)
	if err != nil {
		// No /proc entry, or one hidden from us (hidepid): ask the kernel.
		return signalGone(pid)
	}
	if st.state == 'Z' || st.state == 'X' {
		return true
	}
	return started != 0 && st.started != started
}
