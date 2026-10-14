package claudecode_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/claudecode"
	"github.com/codino-labs/codino/internal/host"
)

// Transcript lines as Claude Code writes them (trimmed).
const (
	promptLine      = `{"type":"user","timestamp":"2026-10-14T08:44:30.000Z","message":{"role":"user","content":"Use the Bash tool to run sleep 30"}}`
	assistantLine   = `{"type":"assistant","timestamp":"2026-10-14T08:44:31.000Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}}`
	toolResultLine  = `{"type":"user","timestamp":"2026-10-14T08:44:41.598Z","message":{"role":"user","content":[{"type":"tool_result","content":"The user doesn't want to proceed with this tool use.","is_error":true}]}}`
	toolUseMarker   = `{"type":"user","timestamp":"2026-10-14T08:44:41.599Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	streamingMarker = `{"type":"user","timestamp":"2026-10-14T08:44:41.599Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	turnDuration    = `{"type":"system","subtype":"turn_duration","timestamp":"2026-10-14T08:44:41.602Z"}`
	costState       = `{"type":"cost-state"}`
	snapshot        = `{"type":"file-history-snapshot","snapshot":{}}`
	nextPrompt      = `{"type":"user","timestamp":"2026-10-14T08:44:55.000Z","message":{"role":"user","content":"Reply with just the word ok."}}`
)

var markerAt = time.Date(2026, 10, 14, 8, 44, 41, 599_000_000, time.UTC)

func writeTranscript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func TestTranscriptShowsTheHumanInterruptingTheLatestTurn(t *testing.T) {
	for name, content := range map[string]string{
		"during a tool call":             lines(promptLine, assistantLine, toolResultLine, toolUseMarker),
		"while streaming":                lines(promptLine, streamingMarker),
		"followed by bookkeeping":        lines(promptLine, assistantLine, toolResultLine, toolUseMarker, turnDuration, costState, snapshot),
		"with a line still being copied": lines(promptLine, toolUseMarker) + `{"type":"cost-st`,
	} {
		t.Run(name, func(t *testing.T) {
			if at := (claudecode.Transcripts{}).Read(writeTranscript(t, content)).Interrupted; !at.Equal(markerAt) {
				t.Fatalf("Interrupted = %v, want %v", at, markerAt)
			}
		})
	}
}

func TestTranscriptWithoutAnInterruptedLatestTurn(t *testing.T) {
	for name, content := range map[string]string{
		"turn in progress":           lines(promptLine, assistantLine),
		"new prompt after interrupt": lines(promptLine, toolUseMarker, snapshot, nextPrompt),
		"empty":                      "",
		"not JSON":                   "garbage\n",
	} {
		t.Run(name, func(t *testing.T) {
			if at := (claudecode.Transcripts{}).Read(writeTranscript(t, content)).Interrupted; !at.IsZero() {
				t.Fatalf("Interrupted = %v, want none", at)
			}
		})
	}
	if got := (claudecode.Transcripts{}).Read(filepath.Join(t.TempDir(), "missing.jsonl")); got != (host.Transcript{}) {
		t.Fatalf("a missing transcript reads %+v, want nothing", got)
	}
}

func TestTranscriptInterruptFoundBeforeALargeBookkeepingEntry(t *testing.T) {
	big := `{"type":"file-history-snapshot","snapshot":"` + strings.Repeat("x", 300_000) + `"}`
	if at := (claudecode.Transcripts{}).Read(writeTranscript(t, lines(promptLine, toolUseMarker, big))).Interrupted; !at.Equal(markerAt) {
		t.Fatalf("Interrupted = %v, want %v", at, markerAt)
	}
}

func TestTranscriptShowsWhenItWasLastWritten(t *testing.T) {
	path := writeTranscript(t, lines(promptLine, assistantLine))
	written := time.Date(2026, 10, 14, 8, 50, 0, 0, time.UTC)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}

	if got := (claudecode.Transcripts{}).Read(path).Modified; !got.Equal(written) {
		t.Fatalf("Modified = %v, want %v", got, written)
	}
}

// claudeDirs lays out a session as Claude Code does: its transcript under
// projects/ and the output of each tool it runs under the temporary
// directory, both in a directory named after the project.
func claudeDirs(t *testing.T) (transcript, tasks, tmp string) {
	t.Helper()
	tmp = t.TempDir()
	transcript = filepath.Join(t.TempDir(), "projects", "-tmp-spike-proj", "0f8e-session.jsonl")
	tasks = filepath.Join(tmp, "-tmp-spike-proj", "0f8e-session", "tasks")
	for _, dir := range []string{filepath.Dir(transcript), tasks} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(transcript, []byte(lines(promptLine, assistantLine)), 0o600); err != nil {
		t.Fatal(err)
	}
	return transcript, tasks, tmp
}

func startTool(t *testing.T, tasks, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tasks, name+".output"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptShowsWhenTheAgentsToolInProgressStarted(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" { // birth_other.go
		t.Skip("no file birth time on", runtime.GOOS)
	}
	transcript, tasks, tmp := claudeDirs(t)
	// File times come from a coarser clock than time.Now.
	before := time.Now().Add(-time.Second)
	startTool(t, tasks, "bcoog5cpt")
	after := time.Now().Add(time.Second)

	got := claudecode.Transcripts{TempDirs: []string{tmp}}.Read(transcript).ToolStarted

	if got.Before(before) || got.After(after) {
		t.Fatalf("ToolStarted = %v, want between %v and %v", got, before, after)
	}
}

func TestTranscriptWithNoToolInProgress(t *testing.T) {
	transcript, _, tmp := claudeDirs(t)
	// Another session of the same project runs one.
	other := filepath.Join(tmp, "-tmp-spike-proj", "other-session", "tasks")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	startTool(t, other, "b1")

	if got := (claudecode.Transcripts{TempDirs: []string{tmp}}).Read(transcript).ToolStarted; !got.IsZero() {
		t.Fatalf("ToolStarted = %v with no tool in progress, want zero", got)
	}
}
