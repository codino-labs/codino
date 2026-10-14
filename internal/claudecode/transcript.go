package claudecode

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// Transcripts reads Claude Code transcripts for what no hook reports: Esc,
// Ctrl-C, a denied permission and a rejected plan fire no hook, but Claude
// Code writes a "[Request interrupted by user…]" user entry right away.
// It implements host.TranscriptProbe.
type Transcripts struct{}

// interruptMarker starts the text of the user entry Claude Code writes when
// the human interrupts a turn.
const interruptMarker = "[Request interrupted by user"

// Reading the transcript backwards from its end: first this many bytes,
// doubling until a conversation entry turns up, up to tailLimit.
const (
	tailStart = 64 << 10
	tailLimit = 4 << 20
)

// Interrupted reports whether the latest conversation entry (user or
// assistant; bookkeeping entries are skipped) is the interrupt marker, and
// when it was written.
func (Transcripts) Interrupted(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return time.Time{}, false
	}
	size := info.Size()
	for n := int64(tailStart); ; n *= 2 {
		n = min(n, size)
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, size-n); err != nil && err != io.EOF {
			return time.Time{}, false
		}
		if e, found := latestConversationEntry(buf, n == size); found {
			return e.interrupted()
		}
		if n == size || n >= tailLimit {
			return time.Time{}, false
		}
	}
}

// entry is a transcript line, with the fields the probe reads.
type entry struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// latestConversationEntry finds the last user or assistant entry among the
// complete lines of tail. whole says tail starts at the file's start, so its
// first line is complete too.
func latestConversationEntry(tail []byte, whole bool) (entry, bool) {
	lines := bytes.Split(tail, []byte("\n"))
	// The last element follows the last newline: empty, or a line still
	// being written.
	lines = lines[:len(lines)-1]
	if !whole && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		var e entry
		if json.Unmarshal(lines[i], &e) != nil {
			continue
		}
		if e.Type == "user" || e.Type == "assistant" {
			return e, true
		}
	}
	return entry{}, false
}

// interrupted reports whether e is the interrupt marker.
func (e entry) interrupted() (time.Time, bool) {
	if e.Type != "user" || e.Timestamp.IsZero() {
		return time.Time{}, false
	}
	var text string
	if json.Unmarshal(e.Message.Content, &text) == nil {
		if strings.HasPrefix(text, interruptMarker) {
			return e.Timestamp, true
		}
		return time.Time{}, false
	}
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return time.Time{}, false
	}
	for _, b := range blocks {
		if b.Type == "text" && strings.HasPrefix(b.Text, interruptMarker) {
			return e.Timestamp, true
		}
	}
	return time.Time{}, false
}
