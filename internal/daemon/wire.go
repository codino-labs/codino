package daemon

import (
	"github.com/codino-labs/codino/internal/host"
)

// Op names what a Request asks for.
type Op string

const (
	OpEvents Op = "events" // apply Request.Events
	OpStatus Op = "status" // reply with the Status
	// OpShow shows Request.Picture on the Device in Raw Mode for as long as
	// the client keeps the connection open: the reply comes once the show
	// started, and the daemon closes the connection once it ended (the
	// client half-closed it, a newer show replaced it, or the daemon exits).
	OpShow Op = "show"
)

// MaxPicture bounds the encoded picture an OpShow request carries.
const MaxPicture = 16 << 20

// Request is one client call on the local socket: one JSON object per
// connection, newline-terminated, answered by one Response.
type Request struct {
	Op Op `json:"op"`
	// Events are applied in order for OpEvents.
	Events []host.Event `json:"events,omitempty"`
	// Picture is an encoded PNG, JPEG or GIF for OpShow.
	Picture []byte `json:"picture,omitempty"`
}

// Response answers a Request.
type Response struct {
	// Error is set when the request was refused.
	Error string `json:"error,omitempty"`
	// Retiring means the daemon is exiting: the client delivers the events
	// to a new Host Daemon. An event applied before the refusal can only
	// have ended an unknown session, so delivering it again is harmless.
	Retiring bool         `json:"retiring,omitempty"`
	Status   *host.Status `json:"status,omitempty"`
}
