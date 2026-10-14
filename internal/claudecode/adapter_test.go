package claudecode_test

import (
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/claudecode"
	"github.com/codino-labs/codino/internal/host"
)

var receivedAt = time.UnixMilli(1760432400000)

func events(t *testing.T, input string, pid int) []host.Event {
	t.Helper()
	got, err := claudecode.Events(claudecode.Hook{Input: []byte(input), ReceivedAt: receivedAt, PID: pid})
	if err != nil {
		t.Fatalf("Events(%s): %v", input, err)
	}
	return got
}

func one(t *testing.T, input string) host.Event {
	t.Helper()
	got := events(t, input, 0)
	if len(got) != 1 {
		t.Fatalf("Events(%s) = %+v, want one event", input, got)
	}
	return got[0]
}

func TestAPromptTypedByTheHumanAcknowledgesTheSession(t *testing.T) {
	e := one(t, `{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"Reply with just the word ok."}`)

	if e.State != host.Working || !e.Acknowledge {
		t.Fatalf("got %+v, want Working with Acknowledge", e)
	}
}

func TestASubagentHandBackPromptIsWorkingButDoesNotAcknowledge(t *testing.T) {
	e := one(t, `{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"<agent-message from=\"a696c05b3115a8667\">\nhi\n</agent-message>"}`)

	if e.State != host.Working || e.Acknowledge {
		t.Fatalf("got %+v, want Working without Acknowledge", e)
	}
}

func TestOnlyPromptsAcknowledge(t *testing.T) {
	for _, input := range []string{
		`{"session_id":"s","hook_event_name":"SessionStart"}`,
		`{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Bash"}`,
		`{"session_id":"s","hook_event_name":"PermissionRequest","tool_name":"Bash"}`,
		`{"session_id":"s","hook_event_name":"Stop","background_tasks":[]}`,
	} {
		if e := one(t, input); e.Acknowledge {
			t.Errorf("Events(%s) Acknowledges", input)
		}
	}
}

func TestEventsCarryThePIDAndTranscriptOfTheSession(t *testing.T) {
	got := events(t, `{"session_id":"s","hook_event_name":"PreToolUse","cwd":"/tmp/spike/proj","transcript_path":"/home/user/.claude/projects/p/s.jsonl"}`, 139150)

	want := host.Event{SessionID: "s", State: host.Working, Project: "/tmp/spike/proj", At: receivedAt,
		PID: 139150, Transcript: "/home/user/.claude/projects/p/s.jsonl"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestEventsCarryWhenTheAgentStarted(t *testing.T) {
	// The liveness handle is the pid plus its start time, so a reused pid
	// isn't taken for the Agent.
	got, err := claudecode.Events(claudecode.Hook{Input: []byte(`{"session_id":"s","hook_event_name":"Stop"}`),
		ReceivedAt: receivedAt, PID: 139150, PIDStarted: 4821733})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PID != 139150 || got[0].PIDStarted != 4821733 {
		t.Fatalf("got %+v, want pid 139150 started at 4821733", got)
	}
}

func TestHookEventsThatChangeNoSessionStateGiveNoEvents(t *testing.T) {
	for _, name := range []string{"SubagentStop", "Notification", "MessageDisplay", "InstructionsLoaded", "PostModelSwitch", "SomeFutureHook"} {
		if got := events(t, `{"session_id":"s","hook_event_name":"`+name+`"}`, 1); len(got) != 0 {
			t.Errorf("%s gave %+v, want nothing", name, got)
		}
	}
}

func TestMalformedHookInputIsAnError(t *testing.T) {
	for _, input := range []string{``, `not json`, `{"hook_event_name":"Stop"}`} {
		if _, err := claudecode.Events(claudecode.Hook{Input: []byte(input), ReceivedAt: receivedAt}); err == nil {
			t.Errorf("Events(%q) gave no error", input)
		}
	}
}

func TestCompactionDoesNotMakeTheSessionIdle(t *testing.T) {
	// Auto-compaction can happen mid-turn and fires SessionStart again.
	if got := events(t, `{"session_id":"s","hook_event_name":"SessionStart","source":"compact"}`, 1); len(got) != 0 {
		t.Fatalf("got %+v, want nothing", got)
	}
	for _, source := range []string{"startup", "resume", "clear"} {
		if e := one(t, `{"session_id":"s","hook_event_name":"SessionStart","source":"`+source+`"}`); e.State != host.Idle {
			t.Errorf("SessionStart %s gave %+v, want Idle", source, e)
		}
	}
}

func TestMCPElicitationIsAwaitingInputUntilAnswered(t *testing.T) {
	if e := one(t, `{"session_id":"s","hook_event_name":"Elicitation"}`); e.State != host.AwaitingInput {
		t.Errorf("Elicitation gave %+v, want Awaiting Input", e)
	}
	if e := one(t, `{"session_id":"s","hook_event_name":"ElicitationResult"}`); e.State != host.Working {
		t.Errorf("ElicitationResult gave %+v, want Working", e)
	}
}

func TestEventsCarryAGuessedPIDAsGuessed(t *testing.T) {
	// Without CLAUDE_PID the hook guesses its grandparent: the Host must not
	// reap the session by it.
	got, err := claudecode.Events(claudecode.Hook{Input: []byte(`{"session_id":"s","hook_event_name":"Stop"}`),
		ReceivedAt: receivedAt, PID: 139150, PIDGuessed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PID != 139150 || !got[0].PIDGuessed {
		t.Fatalf("got %+v, want guessed pid 139150", got)
	}
}
