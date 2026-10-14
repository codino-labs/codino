// Package claudecode is the Claude Code Adapter: it turns one Claude Code hook
// call into the session events of the shared internal API (host.Event), the
// same events `codino event` sends.
package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// Hook is one hook call as `codino hook` received it.
type Hook struct {
	// Input is the hook's JSON, exactly as Claude Code wrote it on stdin.
	Input []byte
	// ReceivedAt is when the hook process started. With async hooks, calls
	// can reach the Host Daemon out of order; this orders them.
	ReceivedAt time.Time
	// PID is the Claude Code process that owns the session (CLAUDE_PID), or 0
	// when unknown.
	PID int
}

// input holds the hook JSON fields the Adapter reads.
type input struct {
	SessionID      string `json:"session_id"`
	HookEventName  string `json:"hook_event_name"`
	Cwd            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	Prompt         string `json:"prompt"`
	// Source, on SessionStart: startup, resume, clear or compact.
	Source string `json:"source"`
	// BackgroundTasks, on Stop, lists the tasks still running.
	BackgroundTasks []backgroundTask `json:"background_tasks"`
}

type backgroundTask struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// subagentActive reports whether a background subagent is still running: its
// hand-back starts a new turn on its own, so the turn isn't Done.
func (in input) subagentActive() bool {
	for _, t := range in.BackgroundTasks {
		if t.Type == "subagent" && t.Status == "running" {
			return true
		}
	}
	return false
}

// Events translates a hook call. Hook events that change no Session State
// (SubagentStop, Notification, MessageDisplay, …) give no events.
//
// The mapping, confirmed by the hook spike (#2, docs/spikes/hooks.md):
//
//	Idle            SessionStart (not after compaction)
//	Working         UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure,
//	                PostToolBatch, SubagentStart, ElicitationResult; Stop while a
//	                background subagent still runs
//	Awaiting Input  PermissionRequest, Elicitation
//	Done            Stop
//	Error           StopFailure
//	removed         SessionEnd
//
// Subagent events carry their parent's session_id, so they fold into it. A
// prompt typed by the human Acknowledges the session. Esc, Ctrl-C, a denied
// permission and a rejected plan fire no hook: Transcripts catches them.
func Events(h Hook) ([]host.Event, error) {
	var in input
	if err := json.Unmarshal(h.Input, &in); err != nil {
		return nil, fmt.Errorf("hook input: %w", err)
	}
	if in.SessionID == "" {
		return nil, errors.New("hook input: no session_id")
	}
	e := host.Event{SessionID: in.SessionID, Project: in.Cwd, At: h.ReceivedAt, PID: h.PID, Transcript: in.TranscriptPath}
	switch in.HookEventName {
	case "SessionStart":
		if in.Source == "compact" {
			// Compaction, even in the middle of a turn: no new session.
			return nil, nil
		}
		e.State = host.Idle
	case "UserPromptSubmit":
		e.State = host.Working
		// A background subagent handing back fires a prompt no human typed.
		e.Acknowledge = !strings.HasPrefix(in.Prompt, "<agent-message from=")
	case "PreToolUse", "PostToolUse", "PostToolUseFailure", "PostToolBatch", "SubagentStart", "ElicitationResult":
		e.State = host.Working
	case "PermissionRequest", "Elicitation":
		// PermissionRequest covers tool permissions, AskUserQuestion and
		// ExitPlanMode. MCP elicitation is not recorded yet (#2).
		e.State = host.AwaitingInput
	case "Stop":
		e.State = host.Done
		if in.subagentActive() {
			e.State = host.Working
		}
	case "StopFailure":
		e.State = host.Error
	case "SessionEnd":
		e = host.Event{SessionID: in.SessionID, End: true, At: h.ReceivedAt}
	default:
		return nil, nil
	}
	return []host.Event{e}, nil
}
