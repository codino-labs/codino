package liveness

import "golang.org/x/sys/unix"

// macOS has no /proc: the kern.proc.pid sysctl gives the start time, the
// parent and zombies, without cgo.

// szomb is a zombie's p_stat (sys/proc.h).
const szomb = 5

func kinfo(pid int) (*unix.KinfoProc, bool) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(k.Proc.P_pid) != pid {
		return nil, false
	}
	return k, true
}

// Started is when the process pid started, in microseconds since the
// epoch; 0 when it can't be read.
func Started(pid int) uint64 {
	k, ok := kinfo(pid)
	if !ok {
		return 0
	}
	t := k.Proc.P_starttime
	return uint64(t.Sec)*1_000_000 + uint64(t.Usec)
}

// Parent is the parent of the process pid; 0 when it can't be read.
func Parent(pid int) int {
	k, ok := kinfo(pid)
	if !ok {
		return 0
	}
	return int(k.Eproc.Ppid)
}

func gone(pid int, started uint64) bool {
	k, ok := kinfo(pid)
	if !ok {
		return signalGone(pid)
	}
	if k.Proc.P_stat == szomb {
		return true
	}
	t := k.Proc.P_starttime
	return started != 0 && uint64(t.Sec)*1_000_000+uint64(t.Usec) != started
}
