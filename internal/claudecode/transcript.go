package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// Transcripts reads Claude Code transcripts for what no hook reports: Esc,
// Ctrl-C, a denied permission and a rejected plan fire no hook, but Claude
// Code writes a "[Request interrupted by user…]" user entry right away; and
// when the transcript was last written, so the Host can tell a session that
// went quiet. Approving a permission prompt fires no hook either, and writes
// nothing to the transcript until the tool finishes; but the tool's output
// file appears when it starts, so it also reports when the latest running
// tool started. It implements host.TranscriptProbe.
type Transcripts struct {
	// TempDirs are where Claude Code may keep its temporary files (its
	// claude-<uid> directories); nil means claude-<uid> in the system's
	// temporary directory and in /tmp, where Claude Code 2.1 keeps them on
	// Linux.
	TempDirs []string
}

// interruptMarker starts the text of the user entry Claude Code writes when
// the human interrupts a turn.
const interruptMarker = "[Request interrupted by user"

// Reading the transcript backwards from its end: first this many bytes,
// doubling until a message entry turns up, up to tailLimit.
const (
	tailStart = 64 << 10
	tailLimit = 4 << 20
)

// Read reports when the transcript was last written and, when its latest
// message entry (user or assistant; bookkeeping entries are skipped) is the
// interrupt marker, when the human interrupted the turn.
func (t Transcripts) Read(path string) host.Transcript {
	f, err := os.Open(path)
	if err != nil {
		return host.Transcript{}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return host.Transcript{}
	}
	tr := host.Transcript{Modified: info.ModTime(), ToolStarted: t.toolStarted(path)}
	if at, ok := interrupted(f, info.Size()); ok {
		tr.Interrupted = at
	}
	return tr
}

// toolStarted is when the newest output file of a tool in progress in the
// session of the transcript at path was created; zero when there is none, or the
// platform keeps no creation time. Claude Code writes the output of every
// Bash command it runs, foreground or background, to
// <project>/<session>/tasks/<id>.output in its temporary directory,
// <project> named as the transcript's directory is, from when the command
// starts; a foreground command's file goes when it ends. (Claude Code
// 2.1.299 on Linux; see docs/spikes/hooks.md.)
func (t Transcripts) toolStarted(path string) time.Time {
	project := filepath.Base(filepath.Dir(path))
	session := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	var newest time.Time
	for _, dir := range t.tempDirs() {
		tasks := filepath.Join(dir, project, session, "tasks")
		entries, err := os.ReadDir(tasks)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".output") {
				continue
			}
			if born := birthTime(filepath.Join(tasks, e.Name())); born.After(newest) {
				newest = born
			}
		}
	}
	return newest
}

// tempDirs are TempDirs, else claude-<uid> in the system's temporary
// directory and in /tmp (none on Windows, which has no uid).
func (t Transcripts) tempDirs() []string {
	if t.TempDirs != nil {
		return t.TempDirs
	}
	uid := os.Getuid()
	if uid < 0 {
		return nil
	}
	name := fmt.Sprintf("claude-%d", uid)
	dirs := []string{filepath.Join(os.TempDir(), name)}
	if tmp := filepath.Join("/tmp", name); tmp != dirs[0] {
		dirs = append(dirs, tmp)
	}
	return dirs
}

// interrupted reports whether the latest message entry of the transcript f,
// size bytes long, is the interrupt marker, and when it was written.
func interrupted(f *os.File, size int64) (time.Time, bool) {
	for n := int64(tailStart); ; n *= 2 {
		n = min(n, size)
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, size-n); err != nil && err != io.EOF {
			return time.Time{}, false
		}
		if e, found := latestMessageEntry(buf, n == size); found {
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

// latestMessageEntry finds the last user or assistant entry among the
// complete lines of tail. whole says tail starts at the file's start, so its
// first line is complete too.
func latestMessageEntry(tail []byte, whole bool) (entry, bool) {
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
