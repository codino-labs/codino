// Package protocol holds the wire messages between the Host and a Device, as
// specified in protocol/README.md. Every message is one JSON object per line.
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

// NewHello and the other constructors fill in the message type, so callers
// can't send a message with the wrong one.
func NewHello(hostTime int64) Hello {
	return Hello{Type: "hello", Protocol: Version, HostTime: hostTime}
}

func NewSessionUpsert(id, state, projectColor string, stateEntered int64, tile int) SessionUpsert {
	return SessionUpsert{Type: "session_upsert", ID: id, State: state, ProjectColor: projectColor, StateEntered: stateEntered, Tile: tile}
}

func NewSessionRemove(id string) SessionRemove { return SessionRemove{Type: "session_remove", ID: id} }

func NewHeartbeat(hostTime int64) Heartbeat { return Heartbeat{Type: "heartbeat", HostTime: hostTime} }

// Encode renders a message as one protocol line, newline included.
func Encode(msg any) ([]byte, error) {
	line, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode %T: %w", msg, err)
	}
	return append(line, '\n'), nil
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
	}, nil
}
