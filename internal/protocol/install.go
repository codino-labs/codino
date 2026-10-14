package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// Kind is what an install carries: a Theme, or a Sprite Pack a Theme draws
// from (protocol/themes.md).
type Kind string

const (
	KindTheme      Kind = "theme"
	KindSpritePack Kind = "sprite_pack"
)

// MaxLine is the longest protocol line a Device takes, its newline included:
// an install of a Sprite Pack is the longest message.
const MaxLine = 8192

// validName is a Theme, Sprite Pack or frame name: 1-32 of a-z, 0-9, - and _.
var validName = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// ValidName reports whether name is a valid Theme or Sprite Pack name.
func ValidName(name string) bool { return validName.MatchString(name) }

// Install installs a Theme or Sprite Pack on the Device (Host → Device). It
// is the file the user wrote, with its type added; the Device validates the
// rest and answers with an InstallReply.
type Install struct {
	Kind Kind
	Name string
	line []byte
}

// NewInstall makes the install message of a Theme or Sprite Pack file: a
// JSON object with a kind ("theme" or "sprite_pack") and a name. It fails
// on a file that can't be one, or that is too long for a Device's line.
func NewInstall(file []byte) (Install, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(file, &fields); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return Install{}, fmt.Errorf("not JSON: %w", err)
		}
		return Install{}, errors.New("want a JSON object")
	}
	if fields == nil {
		return Install{}, errors.New("want a JSON object")
	}
	var kind Kind
	if json.Unmarshal(fields["kind"], &kind) != nil || (kind != KindTheme && kind != KindSpritePack) {
		return Install{}, errors.New(`kind must be "theme" or "sprite_pack"`)
	}
	var name string
	if json.Unmarshal(fields["name"], &name) != nil || !ValidName(name) {
		return Install{}, errors.New("name must be 1-32 of a-z, 0-9, - and _")
	}
	fields["type"] = json.RawMessage(`"install"`)
	line, err := json.Marshal(fields) // compact, so one line
	if err != nil {
		return Install{}, fmt.Errorf("encode install: %w", err)
	}
	line = append(line, '\n')
	if len(line) > MaxLine {
		return Install{}, fmt.Errorf("%d bytes as a protocol line: a Device takes at most %d", len(line), MaxLine)
	}
	return Install{Kind: kind, Name: name, line: line}, nil
}

// Uninstall removes an installed Theme or Sprite Pack (Host → Device); the
// Device answers with an InstallReply.
type Uninstall struct {
	Type string `json:"type"`
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
}

func NewUninstall(kind Kind, name string) Uninstall {
	return Uninstall{Type: "uninstall", Kind: kind, Name: name}
}

// InstallReply answers an Install or Uninstall (Device → Host): whether it
// worked, why not, and what the Device has now.
type InstallReply struct {
	Type  string `json:"type"`
	Kind  Kind   `json:"kind"`
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Themes and SpritePacks the Device has, built-in ones first.
	Themes      []string `json:"themes"`
	SpritePacks []string `json:"sprite_packs"`
}

// ErrNotInstallReply marks a line that is not an install_reply.
var ErrNotInstallReply = errors.New("not an install_reply")

// DecodeInstallReply parses one line from the Device as an install_reply.
func DecodeInstallReply(line []byte) (InstallReply, error) {
	var reply InstallReply
	if err := json.Unmarshal(line, &reply); err != nil || reply.Type != "install_reply" {
		return InstallReply{}, ErrNotInstallReply
	}
	return reply, nil
}
