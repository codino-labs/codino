# Codino

A USB-attached LED matrix that shows, at a glance, the state of the AI agents running on the computer it is plugged into.

## Language

### Agents and sessions

**Agent**:
A kind of AI coding tool running on the host (e.g. Claude Code). The Device never knows which Agent it is displaying.
_Avoid_: AI, bot, assistant

**Agent Session**:
One running conversation of an Agent (one terminal, one worktree). Subagents belong to their parent Agent Session and are never sessions of their own.
_Avoid_: instance, process, conversation

**Adapter**:
The host-side translator that turns one Agent's native signals (e.g. Claude Code hooks) into Session State changes.
_Avoid_: plugin, integration, driver

### States

**Session State**:
The status of one Agent Session at a moment: exactly one of Idle, Working, Awaiting Input, Done, Error.
_Avoid_: status, mode

**Idle**:
The session is open and nothing is happening.

**Working**:
The Agent is thinking or running tools.
_Avoid_: busy, running

**Awaiting Input**:
The Agent is blocked on the human: a permission prompt, a question or a plan approval.
_Avoid_: waiting, blocked, needs attention

**Done**:
The Agent finished a turn and the human has not acknowledged it yet. Decays to Idle.
_Avoid_: finished, complete

**Error**:
The session failed (crash, API error, rate limit).

**Acknowledge**:
The human signalling they have seen a session: submitting a new prompt to it, pressing or double-tapping the Device, or the Done decay timer expiring. Clears every Done session and resets Escalation, but never hides Awaiting Input.
_Avoid_: dismiss, clear, read

**Escalation**:
Awaiting Input growing more insistent the longer it goes unacknowledged.
_Avoid_: alarm, nagging

**Takeover**:
The last step of Escalation: the whole matrix shows the waiting session, whatever the Layout.

**Aggregate State**:
The single state that represents all Agent Sessions together. The most urgent one wins: Awaiting Input > Error > Done > Working > Idle.
_Avoid_: overall status, global state

### Hardware and host

**Device**:
The physical Codino: a microcontroller with native USB plus a WS2812B matrix. It renders states with its own animations and keeps its configuration across reboots.
_Avoid_: board, gadget, lamp

**Hardware Profile**:
The description of a physical build (matrix size, wiring, color order, brightness cap, available inputs). Stored on the Device and chosen when the firmware is flashed.
_Avoid_: board config, device config

**Preferences**:
The user's choices about how states look and behave (Theme, Layout, brightness, Quiet Mode…). Stored on the Host, which is canonical, and cached on the Device.
_Avoid_: settings, config (ambiguous with Hardware Profile)

**Virtual Device**:
A simulated Device that speaks the same protocol and renders the same Themes on screen instead of on LEDs.
_Avoid_: emulator, mock device

**Host**:
The computer the Device is plugged into.

**Host Daemon**:
The single host process that owns the USB link to the Device, tracks every Agent Session and computes the Aggregate State.
_Avoid_: server, service, bridge

**Exit Grace**:
How long the Host Daemon stays up with no Agent Sessions before it retires (exits). The next event spawns a new one.
_Avoid_: timeout, idle timeout (Idle is a Session State)

**Layout**:
How the matrix area is used: the Aggregate Layout (one Aggregate State for the whole matrix) or the adaptive Layout (one Tile per Agent Session, split by how many there are).
_Avoid_: view, screen, mode

**Tile**:
The region of the matrix given to one Agent Session in the per-session Layout. A session keeps its Tile for its whole life. The Tile's color is the session's Project Color and its animation is the Session State.
_Avoid_: cell, zone, slot

**Overflow Tile**:
The Tile that stands for every session beyond the ones that fit, showing their Aggregate State.

**Project Color**:
The color that identifies an Agent Session by the project it runs in, so the human can tell which session wants them.
_Avoid_: session color, tag

**Theme**:
A named, declarative description of the colors and animations for each Session State, interpreted by the Device and the Virtual Device alike.
_Avoid_: skin, style, preset

**Sprite Pack**:
A loadable bundle of pixel-art frames that a Theme can reference, installed on the Device without reflashing.
_Avoid_: asset pack, skin

**Character Theme**:
A Theme in which a pixel-art mascot strikes one pose per Session State. Needs a full matrix; Tiles fall back to abstract animation.
_Avoid_: mascot mode, sprite theme

**Nino**:
The original mascot of the default Character Theme: a round mint creature with a pink ponytail ("codino"), one pose per Session State. Codino's own; unrelated to Clawd.

**Clawd**:
The Claude Code pixel mascot. Available only as a separately loaded sprite pack, never distributed with the project, pending permission from Anthropic.

**Quiet Mode**:
A setting under which the Device shows nothing except Awaiting Input.
_Avoid_: do not disturb, focus mode

**Disconnected**:
What the Device shows when the Host has gone silent. It is not a Session State: the Device simply doesn't know.
_Avoid_: offline, idle

**Raw Mode**:
A Device mode where the Host streams pixels directly, bypassing the Device's own state rendering. Awaiting Input always pre-empts it.
_Avoid_: passthrough, direct mode
