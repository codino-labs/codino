package daemon

import (
	"github.com/codino-labs/codino/internal/host"
)

// Request is one client call on the local socket: one JSON object per
// connection, newline-terminated, answered by one Response.
type Request struct {
	// Op is "events" or "status".
	Op string `json:"op"`
	// Events are applied in order for "events".
	Events []host.Event `json:"events,omitempty"`
}

// Response answers a Request.
type Response struct {
	// Error is set when the request was refused.
	Error  string       `json:"error,omitempty"`
	Status *host.Status `json:"status,omitempty"`
}
