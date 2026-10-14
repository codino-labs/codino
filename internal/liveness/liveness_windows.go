package liveness

// Started is 0: not read on Windows yet.
func Started(int) uint64 { return 0 }

// Parent is 0: not read on Windows yet.
func Parent(int) int { return 0 }

// gone can't tell on Windows yet, so sessions are never reaped there.
func gone(int, uint64) bool { return false }
