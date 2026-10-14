package daemon

import (
	"github.com/codino-labs/codino/internal/host"
)

// Op names what a Request asks for.
type Op string

const (
	OpEvents Op = "events" // apply Request.Events
	OpStatus Op = "status" // reply with the Status
)

// Request is one client call on the local socket: one JSON object per
// connection, newline-terminated, answered by one Response.
type Request struct {
	Op Op `json:"op"`
	// Events are applied in order for OpEvents.
	Events []host.Event `json:"events,omitempty"`
}

// Response answers a Request.
type Response struct {
	// Error is set when the request was refused.
	Error string `json:"error,omitempty"`
	// Retiring means the daemon is exiting and applied none of the events:
	// the client delivers them to a new Host Daemon.
	Retiring bool         `json:"retiring,omitempty"`
	Status   *host.Status `json:"status,omitempty"`
}
