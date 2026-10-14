# Hook spike: real Claude Code event sequences

Issue #2, parent spec #1. Recorded on 2026-10-14 with **Claude Code 2.1.299** on Linux, model `haiku`.

Recordings are in [`fixtures/hooks/claude-code/`](../../fixtures/hooks/claude-code/), one `.jsonl` file per scenario. They are meant as replay fixtures for the Host core tests.

## How the recordings were made

- A logging hook ([`hook-logger.py`](hook-logger.py)) is installed for every hook event through [`hook-logger-settings.json`](hook-logger-settings.json), passed with `--settings`. The global `~/.claude` settings were not touched. `WorktreeCreate`/`WorktreeRemove` are left out, because a `WorktreeCreate` hook replaces git's own worktree creation.
- Each session ran in a scratch project outside the repo.
- [`hook-driver.py`](hook-driver.py) runs `claude` either in print mode (`-p`) or as the real interactive TUI inside a pseudo-terminal. It waits for events to appear in the hook log, then types keys (Enter, Esc, Ctrl-C, `/exit`) or sends signals (`kill -9`, SIGTERM). Every interactive case below therefore went through the real TUI dialogs, not a print-mode approximation.
- [`hook-to-fixture.py`](hook-to-fixture.py) turns a log into a fixture and rewrites machine paths to placeholders (`/tmp/spike/proj`, `/home/user`).
- Hooks ran synchronously, except in `question-tool-async-hooks`, where every hook had `async: true` as the plugin will ([`hook-logger-settings-async.json`](hook-logger-settings-async.json), selected with `HOOK_SETTINGS=`).
- `--permission-mode manual` on the command line shows up as `"permission_mode": "default"` in hook payloads. With no flag, sessions on this account started in `auto`.

To re-record a case (for example on macOS, or one listed as unverified):

```sh
mkdir -p /tmp/spike/proj /tmp/spike/logs
SPIKE_DIR=/tmp/spike docs/spikes/hook-driver.py my-case i '[...steps...]' -- --model haiku "prompt"
docs/spikes/hook-to-fixture.py /tmp/spike/logs/my-case.jsonl fixtures/hooks/claude-code/my-case.jsonl my-case "what" "how"
```

### Fixture format

Line 1 is `{"meta": {scenario, description, how, claude_code_version, recorded, os, note}}`. Every other line is one hook call:

```json
{"t": 2.07, "event": "PermissionRequest",
 "liveness": {"CLAUDE_PID": 139150, "hook_ppid_chain": ["sh", "claude"], "grandparent_is_claude_pid": true},
 "input": { ...stdin JSON exactly as Claude Code sent it... }}
```

`t` is the number of seconds since the first hook. `input` is what the Adapter will receive.

## Scenarios

| Fixture | Sequence (MessageDisplay and InstructionsLoaded left out) |
|---|---|
| `plain-turn-done` | SessionStart → UserPromptSubmit → Stop → SessionEnd(prompt_input_exit) |
| `print-plain-turn-done` | same, SessionEnd reason `other` |
| `auto-approved-tool` | … PreToolUse Bash → PostToolUse → PostToolBatch → Stop → SubagentStop(agent_type "") → *60 s* → Notification idle_prompt |
| `permission-approved` | PreToolUse Bash → PermissionRequest (+50 ms) → Notification permission_prompt (+6.0 s) → *approved* → PostToolUse (only when the tool finishes) → … → Stop |
| `permission-denied` / `permission-denied-esc` | PreToolUse → PermissionRequest → Notification permission_prompt → *denied* → **nothing** until SessionEnd 76 s later |
| `print-permission-denied` | PreToolUse → PermissionRequest → PostToolBatch (auto-denied, nobody to ask) → Stop |
| `question-tool` | PreToolUse AskUserQuestion → PermissionRequest AskUserQuestion (+40 ms) → Notification permission_prompt (+6.0 s) → *answered* → PostToolUse → Stop |
| `question-tool-async-hooks` | same, PreToolUse → PermissionRequest only 7 ms apart |
| `plan-approval` | PreToolUse ExitPlanMode → PermissionRequest ExitPlanMode → Notification permission_prompt "…approval for the plan" → *approved* → PostToolUse, PostModelSwitch, permission_mode plan → auto → … → Stop |
| `plan-rejected-esc` | … PermissionRequest ExitPlanMode → Notification → *Esc* → **nothing** until SessionEnd |
| `esc-interrupt-tool` | PreToolUse Bash → *Esc* → **nothing** → next UserPromptSubmit 15 s later |
| `esc-interrupt-streaming` | UserPromptSubmit → *Esc while text streams* → **nothing** → next UserPromptSubmit |
| `ctrl-c-interrupt-tool` | same as `esc-interrupt-tool` |
| `subagent-stop-before-handback` | PreToolUse Agent → SubagentStart → PostToolUse Agent → **Stop with `background_tasks` = [running subagent]** → SubagentStop → PreToolUse/PostToolUse SubagentHandback (with `agent_id`) → **synthetic UserPromptSubmit** (`<agent-message from=…>`) → Stop |
| `subagents-parallel` | 2 (then 3) subagents. Their hand-backs (PreToolUse/PostToolUse `SubagentHandback`, with `agent_id`) arrive *before* the parent's Stop, followed by three synthetic UserPromptSubmits in a row, then one Stop |
| `stopfailure-model-not-found` | UserPromptSubmit → StopFailure `error: "model_not_found"`, no Stop |
| `print-stopfailure-model-not-found` | same, headless |
| `killed-working-sigkill` | … PreToolUse Bash → *kill -9* → **no SessionEnd** |
| `killed-idle-sigkill` | … Stop → *kill -9* → **no SessionEnd** |
| `sigterm-idle` | … Stop → *SIGTERM* → SessionEnd(other) |

## Answers to the ticket's questions

**Do the question tool and plan approval produce PreToolUse?** Yes. `PreToolUse` fires with `tool_name` `AskUserQuestion` or `ExitPlanMode`. More useful: `PermissionRequest` for the same tool follows 7–47 ms later, even in `auto` and `plan` permission mode. The Adapter does not need a PreToolUse special case. `PermissionRequest` alone covers permission prompts, questions and plan approvals.

**Does Esc-interrupt fire Stop?** No. Esc, Ctrl-C, denying a permission prompt and rejecting a plan all fire **no hook at all**: no Stop and no PostToolUseFailure. The two permission-denied sessions then went 76 s with no hook at all, not even the `idle_prompt` Notification that follows a normal Stop after 60 s. The next hook is the next `UserPromptSubmit` or `SessionEnd`. Claude Code does write the interruption to the transcript (`transcript_path`) right away, as a user entry whose text is `[Request interrupted by user]` (while streaming) or `[Request interrupted by user for tool use]` (during a tool call, a permission prompt or a plan approval). This was checked in the transcripts of all five interrupt/deny recordings, but the transcripts themselves are not committed, because they hold the whole conversation. The entry looks like this (trimmed):

```json
{"type": "user", "timestamp": "2026-10-14T08:44:41.599Z",
 "message": {"role": "user", "content": [{"type": "text", "text": "[Request interrupted by user for tool use]"}]}}
```

**Does PermissionRequest fire for calls that auto mode approves?** No. In `auto-approved-tool`, an auto-approved Bash call produced only PreToolUse → PostToolUse. The exceptions are AskUserQuestion and ExitPlanMode, which always fire PermissionRequest because they always need the human. In print mode, a call that would need asking still fires PermissionRequest, then is denied automatically and the turn carries on (`print-permission-denied`).

**In which order do PermissionRequest and the permission_prompt Notification arrive?** PermissionRequest first. The `permission_prompt` Notification follows **6.0 s later** in every recording (6.01–6.05 s). Its `message` is the only thing that tells the kinds apart: "Claude needs your permission" for tools and questions, "Claude Code needs your approval for the plan" for plans. Print mode sends no Notification.

**Liveness handle.** Every hook gets **`CLAUDE_PID`** in its environment: the pid of the Claude Code process that owns the session. It held in all 233 hook calls in the fixtures, in both print and interactive mode. The process tree confirms it: hook → `/bin/sh` → `claude` (pid = `CLAUDE_PID`). `CLAUDE_CODE_SESSION_ID` (= `session_id`) is in the environment too. `CLAUDE_PID` is not in the hooks documentation, so the hook binary should fall back to its grandparent (parent of the `sh` parent) when the variable is missing. (Implemented that way, the guess is only shown: the Host reaps only by `CLAUDE_PID`, since a guess can name a wrapper or another shell, and a session with a guessed pid relies on the staleness fallback.) To guard against pid reuse, the Session Tracker should store the pid together with the process start time and treat a mismatch as "process gone". `kill -9` gives no SessionEnd, so this is the only way to reap such sessions. SIGTERM and `/exit` do fire SessionEnd.

## Hook → Session State mapping (confirmed / corrected)

| Session State | Entered on |
|---|---|
| Idle | `SessionStart`; Done decay; staleness fallback; **new:** the transcript's last entry is a `[Request interrupted by user…]` marker while the session is Working or Awaiting Input |
| Working | `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `SubagentStart`; **add** `PostToolUseFailure` and `PostToolBatch` (the only signal after an automatic denial in print mode) |
| Awaiting Input | `PermissionRequest`, which covers tool permissions, AskUserQuestion and ExitPlanMode. **Drop** the PreToolUse special case for the question and plan tools. MCP elicitation: unverified |
| Done | `Stop` with an empty `background_tasks`. **Correction:** `Stop` with running `background_tasks` (background subagents) should stay Working, because the hand-back starts a new turn on its own |
| Error | `StopFailure` (`error` field; observed: `model_not_found`). No Stop follows it |
| session removed | `SessionEnd` (`prompt_input_exit` on /exit; `other` on SIGTERM and at the end of print mode); `CLAUDE_PID` gone |

Ignore: `SubagentStop`, `Notification` (the permission_prompt one only repeats PermissionRequest 6 s late), `MessageDisplay`, `InstructionsLoaded`, `PostModelSwitch`.

Other things the Adapter must handle:

- **Synthetic UserPromptSubmit.** When a background subagent hands back, Claude Code fires `UserPromptSubmit` with a `prompt` starting `<agent-message from="…">`. It means Working, but it must **not** Acknowledge, because no human typed it.
- **Stray SubagentStop.** After most turns, Claude Code fires a `SubagentStop` with `agent_type: ""` and no matching SubagentStart. This is an internal helper agent. Ignoring SubagentStop covers it.
- **Subagent events fold into the parent.** Confirmed: they carry the parent's `session_id` plus an `agent_id`.
- **Approval has no hook.** After a permission prompt is approved, the next hook is `PostToolUse`, which fires only when the tool *finishes*. A long tool approved by the human stays Awaiting Input until it completes. For questions and plan approvals the gap is negligible, because those tools finish as soon as the human answers. No hook closes the gap for long tools; the Adapter ticket should accept it or look for another signal.
- **Async ordering.** With `async: true`, PreToolUse and PermissionRequest started 7 ms apart. The order held in the one recording, but nothing in the payload sequences events. The hook binary should stamp each event with its start time, and the Session Tracker should drop an event older than the last one applied to that session. Otherwise a late PreToolUse could overwrite Awaiting Input.

## Unverified

- **StopFailure types other than `model_not_found`.** `rate_limit`, `overloaded`, `billing_error`, `authentication_failed` and `server_error` can't be provoked on demand. A bogus `ANTHROPIC_API_KEY` would probably give `authentication_failed`, but it wasn't tried, so as not to disturb the account's auth state. The payload shape (`error`, `last_assistant_message`) is assumed to be the same.
- **`PermissionDenied`** (auto mode refusing a call) did not occur. Provoking it means asking for something dangerous.
- **MCP elicitation** (`Elicitation` hook, `elicitation_dialog` Notification): no MCP server was set up.
- **A real SIGINT signal.** Ctrl-C was sent as the 0x03 byte through the terminal, which is what a human keypress does. `kill -INT` was not tried.
- **macOS and Windows.** Only Linux was recorded. `CLAUDE_PID` is probably cross-platform, but the process-tree fallback was only checked through `/proc`.
- **Async ordering under load.** Only one async recording was made.
