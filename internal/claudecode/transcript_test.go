package claudecode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/claudecode"
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
			at, ok := claudecode.Transcripts{}.Interrupted(writeTranscript(t, content))
			if !ok || !at.Equal(markerAt) {
				t.Fatalf("Interrupted = %v, %v; want %v, true", at, ok, markerAt)
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
			if at, ok := (claudecode.Transcripts{}).Interrupted(writeTranscript(t, content)); ok {
				t.Fatalf("Interrupted = %v, true; want false", at)
			}
		})
	}
	if _, ok := (claudecode.Transcripts{}).Interrupted(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Fatal("a missing transcript shows an interrupt")
	}
}

func TestTranscriptInterruptFoundBeforeALargeBookkeepingEntry(t *testing.T) {
	big := `{"type":"file-history-snapshot","snapshot":"` + strings.Repeat("x", 300_000) + `"}`
	at, ok := claudecode.Transcripts{}.Interrupted(writeTranscript(t, lines(promptLine, toolUseMarker, big)))
	if !ok || !at.Equal(markerAt) {
		t.Fatalf("Interrupted = %v, %v; want %v, true", at, ok, markerAt)
	}
}
