// Package protocol holds the wire messages between the Host and a Device, as
// specified in protocol/README.md. Every message is one JSON object per line.
package protocol

import (
	"encoding/json"
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

// DecodeHelloReply parses one line as a hello_reply. It fails when the line is
// not JSON, is another message type, or lacks the fields the Host needs.
func DecodeHelloReply(line []byte) (HelloReply, error) {
	var raw struct {
		Type     *string `json:"type"`
		Protocol *int    `json:"protocol"`
		HelloReply
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return HelloReply{}, fmt.Errorf("hello_reply: %w", err)
	}
	if raw.Type == nil || *raw.Type != "hello_reply" {
		return HelloReply{}, fmt.Errorf("hello_reply: unexpected message %q", line)
	}
	if raw.Protocol == nil {
		return HelloReply{}, fmt.Errorf("hello_reply: missing protocol")
	}
	reply := raw.HelloReply
	reply.Type = *raw.Type
	reply.Protocol = *raw.Protocol
	return reply, nil
}
