// Package protocol holds the wire messages between the Host and a Device, as
// specified in protocol/README.md. Every message is one line: a JSON object,
// or a Raw Mode frame (RawFrame).
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the protocol version this Host speaks.
const Version = 0

// Hello opens the handshake (Host → Device).
type Hello struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	HostTime int64  `json:"host_time"`
}

// HardwareProfile describes the Device's physical build, as reported in the
// handshake reply.
type HardwareProfile struct {
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	DataPin       int    `json:"data_pin"`
	Serpentine    bool   `json:"serpentine"`
	ColorOrder    string `json:"color_order"`
	BrightnessCap int    `json:"brightness_cap"`
	CurrentCapMA  int    `json:"current_cap_ma"`
	Button        bool   `json:"button"`
	IMU           bool   `json:"imu"`
}

// HelloReply answers Hello (Device → Host).
type HelloReply struct {
	Type            string          `json:"type"`
	Protocol        int             `json:"protocol"`
	Firmware        string          `json:"firmware"`
	HardwareProfile HardwareProfile `json:"hardware_profile"`
	// Themes and SpritePacks the Device has, built-in ones first; empty
	// from a Device that predates Themes.
	Themes      []string `json:"themes,omitempty"`
	SpritePacks []string `json:"sprite_packs,omitempty"`
}

// SessionUpsert creates or replaces one Agent Session on the Device.
type SessionUpsert struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	State        string `json:"state"`
	ProjectColor string `json:"project_color"`
	StateEntered int64  `json:"state_entered"`
	Tile         int    `json:"tile"`
}

// SessionRemove removes one Agent Session from the Device.
type SessionRemove struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Heartbeat tells the Device the Host is alive.
type Heartbeat struct {
	Type     string `json:"type"`
	HostTime int64  `json:"host_time"`
}

// Acknowledge is the human having seen Agent Sessions. Host → Device it names
// one session and when it was Acknowledged; Device → Host it carries only its
// type and stands for every session (a button press or double tap).
type Acknowledge struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	HostTime int64  `json:"host_time"`
}

// Preferences pushes the Preferences the Device renders by (Host → Device),
// on claiming a Device and whenever they change. The Device caches them.
type Preferences struct {
	Type string `json:"type"`
	// Theme is the Theme's name.
	Theme string `json:"theme"`
	// Brightness is a percentage (1-100) of the Hardware Profile's
	// brightness cap.
	Brightness int    `json:"brightness"`
	Layout     string `json:"layout"`
	// Rotation is 0, 90, 180 or 270 degrees clockwise.
	Rotation              int   `json:"rotation"`
	QuietMode             bool  `json:"quiet_mode"`
	DisconnectedTimeoutMS int64 `json:"disconnected_timeout_ms"`
	EscalationFasterMS    int64 `json:"escalation_faster_ms"`
	EscalationTakeoverMS  int64 `json:"escalation_takeover_ms"`
	EscalationSteadyMS    int64 `json:"escalation_steady_ms"`
}

// RawModeEnter puts the Device in Raw Mode: it shows the RawFrames that
// follow instead of its own rendering, except while a session is Awaiting
// Input.
type RawModeEnter struct {
	Type string `json:"type"`
}

// RawModeExit returns the Device to its own rendering.
type RawModeExit struct {
	Type string `json:"type"`
}

// RawFrameStart is the first byte of a RawFrame: never valid UTF-8, so no
// JSON line starts with it.
const RawFrameStart = 0xFF

// RawFrame is the one binary message: the pixels of one frame in Raw Mode,
// sent as RawFrameStart, then 3 bytes (R, G, B) per LED, row-major from the
// top-left, then a newline. No pixel byte is a newline: NewRawFrame raises
// every channel at 10 to 11, so a frame is one line however its pixels read.
type RawFrame struct {
	line []byte
}

// NewRawFrame quantises RGB bytes (3 per LED, row-major from the top-left)
// for the wire.
func NewRawFrame(rgb []byte) RawFrame {
	line := make([]byte, 0, len(rgb)+2)
	line = append(line, RawFrameStart)
	for _, c := range rgb {
		if c == '\n' {
			c++
		}
		line = append(line, c)
	}
	return RawFrame{line: append(line, '\n')}
}

// NewHello and the other constructors fill in the message type, so callers
// can't send a message with the wrong one.
func NewHello(hostTime int64) Hello {
	return Hello{Type: "hello", Protocol: Version, HostTime: hostTime}
}

func NewSessionUpsert(id, state, projectColor string, stateEntered int64, tile int) SessionUpsert {
	return SessionUpsert{Type: "session_upsert", ID: id, State: state, ProjectColor: projectColor, StateEntered: stateEntered, Tile: tile}
}

func NewSessionRemove(id string) SessionRemove { return SessionRemove{Type: "session_remove", ID: id} }

func NewAcknowledge(id string, hostTime int64) Acknowledge {
	return Acknowledge{Type: "acknowledge", ID: id, HostTime: hostTime}
}

func NewHeartbeat(hostTime int64) Heartbeat { return Heartbeat{Type: "heartbeat", HostTime: hostTime} }

// NewPreferences fills in the message type of p.
func NewPreferences(p Preferences) Preferences {
	p.Type = "preferences"
	return p
}

func NewRawModeEnter() RawModeEnter { return RawModeEnter{Type: "raw_mode_enter"} }

func NewRawModeExit() RawModeExit { return RawModeExit{Type: "raw_mode_exit"} }

// Encode renders a message as one protocol line, newline included.
func Encode(msg any) ([]byte, error) {
	switch m := msg.(type) {
	case RawFrame:
		return m.line, nil
	case Install:
		return m.line, nil
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode %T: %w", msg, err)
	}
	return append(line, '\n'), nil
}

// IsAcknowledge reports whether a line from the Device is an acknowledge.
// Its other fields don't matter: a Device Acknowledges every session.
func IsAcknowledge(line []byte) bool {
	var m struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(line, &m) == nil && m.Type == "acknowledge"
}

// ErrNotHelloReply marks a line that is not a hello_reply at all: not JSON, or
// another message type. Receivers ignore such lines; a Device may print boot
// noise before it answers.
var ErrNotHelloReply = errors.New("not a hello_reply")

// DecodeHelloReply parses one line as a hello_reply. It fails with
// ErrNotHelloReply for a line to ignore, and with another error for a
// hello_reply that lacks a field the Host needs (protocol, firmware,
// hardware_profile).
func DecodeHelloReply(line []byte) (HelloReply, error) {
	var raw struct {
		Type            *string          `json:"type"`
		Protocol        *int             `json:"protocol"`
		Firmware        *string          `json:"firmware"`
		HardwareProfile *HardwareProfile `json:"hardware_profile"`
		Themes          []string         `json:"themes"`
		SpritePacks     []string         `json:"sprite_packs"`
	}
	if err := json.Unmarshal(line, &raw); err != nil || raw.Type == nil || *raw.Type != "hello_reply" {
		return HelloReply{}, ErrNotHelloReply
	}
	if raw.Protocol == nil || raw.Firmware == nil || raw.HardwareProfile == nil {
		return HelloReply{}, fmt.Errorf("hello_reply: missing protocol, firmware or hardware_profile in %q", line)
	}
	return HelloReply{
		Type:            *raw.Type,
		Protocol:        *raw.Protocol,
		Firmware:        *raw.Firmware,
		HardwareProfile: *raw.HardwareProfile,
		Themes:          raw.Themes,
		SpritePacks:     raw.SpritePacks,
	}, nil
}
