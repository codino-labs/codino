//go:build unix && !linux && !darwin

package liveness

// Started is 0: no start time is read on this Unix.
func Started(int) uint64 { return 0 }

// Parent is 0: no parent is read on this Unix.
func Parent(int) int { return 0 }

func gone(pid int, _ uint64) bool { return signalGone(pid) }
