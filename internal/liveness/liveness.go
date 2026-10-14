// Package liveness tells whether an Agent is still alive by its liveness
// handle: the pid of its process and, where the platform shows it, when that
// process started, so a pid reused after the Agent exited isn't taken for it.
//
// Start times are opaque values, comparable only on the Host that read them:
// the hook reads one (Started) while the Agent is certainly alive, being its
// ancestor, and the Host Daemon compares it later (Probe.Gone).
//
//   - Linux: /proc gives the start time, the parent, and zombies (a
//     kill -9'd Agent whose shell hasn't waited for it yet).
//   - Other Unix (macOS): signal 0 only, as no start time is readable without
//     cgo or a sysctl dependency; a reused pid keeps the session until the
//     Host's staleness fallback or the next SessionEnd.
//   - Windows: not probed yet; sessions are never reaped there.
package liveness

// Probe implements host.LivenessProbe for the processes of this Host.
type Probe struct{}

// Gone reports whether the Agent with this pid, started at started (0 when
// unknown), has exited: no such process, a zombie, or one started at another
// time (the pid was reused). False when it can't tell, and for pid 0.
func (Probe) Gone(pid int, started uint64) bool {
	if pid <= 0 {
		return false
	}
	return gone(pid, started)
}
