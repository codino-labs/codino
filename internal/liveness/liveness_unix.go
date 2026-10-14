//go:build unix && !linux

package liveness

// Started is 0: without /proc, no start time is readable without cgo.
func Started(int) uint64 { return 0 }

// Parent is 0: without /proc, no parent is readable without cgo.
func Parent(int) int { return 0 }

func gone(pid int, _ uint64) bool { return signalGone(pid) }
