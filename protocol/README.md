# Protocol v0

The wire protocol between the Host Daemon and a Device (or a Virtual Device). It is semantic (ADR-0001): the Host says what state each Agent Session is in, and the Device decides how that looks.

v0 covers the handshake, session upsert, session remove, heartbeat, acknowledge, Disconnected, Raw Mode, the preview-state override, the Preferences push and installing Themes and Sprite Packs. The Theme and Sprite Pack format is in [themes.md](themes.md).

## Framing

- Transport: USB CDC serial on a Device, stdin/stdout on the terminal Virtual Device.
- Every message is one line terminated by `\n`: one JSON object, UTF-8 (newline-delimited JSON; a trailing `\r` is tolerated), or a Raw Mode frame, the one binary message (below). A Device's line buffer holds 8192 bytes, newline included: it ignores a longer line. An `install` is the longest message. A frame's first byte is `0xFF`, which is never valid UTF-8, so a receiver tells the two apart by the first byte of the line.
- Every JSON message has a string field `type`.
- Receivers ignore unknown fields, unknown `type`s and lines that are not valid JSON. Senders always send every field listed for a message. A receiver ignores a message as a whole when a field it needs is missing or invalid; each message below says which fields a v0 Device needs. This keeps an older Device usable by a newer Host for everything the Device already understands.
- Times are Unix milliseconds on the Host clock. The Device has no wall clock; it relates Host times to its own clock through `host_time` in `hello` and `heartbeat`.

## Handshake

The Host opens the link with `hello`. The Device answers with `hello_reply`. The Host claims a port only after a valid reply, and refuses to drive a Device whose `protocol` differs from its own (it surfaces "update firmware" instead).

### `hello` (Host → Device)

A Device answers every `hello`, whatever its fields, so that a Host speaking any protocol version learns which version the Device speaks.

A `hello` starts a new link: the Device forgets every Agent Session it holds (it keeps its Preferences), and the Host follows a successful handshake with a `preferences` push, then a `session_upsert` for every session it knows. Sessions from an earlier Host Daemon never linger.

| Field | Type | Meaning |
|---|---|---|
| `protocol` | integer | Protocol version the Host speaks. `0` for this document. |
| `host_time` | integer | Host time when sent. |

### `hello_reply` (Device → Host)

| Field | Type | Meaning |
|---|---|---|
| `protocol` | integer | Protocol version the Device speaks. |
| `firmware` | string | Firmware version (semver). |
| `hardware_profile` | object | The Device's Hardware Profile, below. |
| `themes` | array of strings | The Themes the Device has: its built-in ones first, then the installed ones (Themes and Sprite Packs, below). |
| `sprite_packs` | array of strings | The Sprite Packs it has, likewise. |

Hardware Profile fields:

| Field | Type | Meaning |
|---|---|---|
| `width`, `height` | integer | Matrix size in pixels. |
| `data_pin` | integer | GPIO driving the LED data line. |
| `serpentine` | bool | `true` when alternate rows run in reverse (zig-zag wiring). |
| `color_order` | string | LED channel order, e.g. `"GRB"`. |
| `brightness_cap` | integer 0–255 | Maximum brightness of any LED channel, enforced on everything the Device shows whatever the Preferences say (below). |
| `current_cap_ma` | integer | Current budget for the matrix, in mA. |
| `button`, `imu` | bool | Which inputs the build has. |

## Session messages (Host → Device)

The protocol is keyed by session id, so several Hosts could drive one Device later.

### `session_upsert`

Creates the Agent Session if the id is new, otherwise replaces its fields.

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Agent Session id. |
| `state` | string | Session State: `idle`, `working`, `awaiting_input`, `done` or `error`. |
| `project_color` | string | Project Color as `#rrggbb`. |
| `state_entered` | integer | Host time the session entered `state`. Escalation counts from it, so a Device claimed again shows how long a session has really been waiting. |
| `tile` | integer | Tile slot assigned by the Host, starting at 0: where the session goes in the adaptive Layout (below). |

A v0 Device needs `id` (a non-empty string) and a valid `state`. Without a valid `project_color` it uses the Session State's own color; without a valid `tile` the session takes the first free Tile.

The Host's Project Color Allocator picks `project_color`: a pinned color, else a stable hash of the git repo root of the session's project (the directory itself outside a repo; every worktree of a repo shares its root) onto a palette of 8 well-separated hues. When an active session of another project already holds that hue, the newcomer takes the next free one, and keeps it for its lifetime. Sessions of the same project share a color; a session without a project is a project of its own.

### `session_remove`

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Agent Session id. Removing an unknown id is a no-op. |

### `heartbeat`

Tells the Device the Host is alive. The Host sends one at least every second while it drives the Device.

| Field | Type | Meaning |
|---|---|---|
| `host_time` | integer | Host time when sent. |

## Acknowledge (both directions)

Acknowledge is the human having seen Agent Sessions. It clears Done (the session becomes Idle) and restarts Escalation; it never hides Awaiting Input. The Host is canonical for Session States and keeps when each waiting session was last Acknowledged.

### `acknowledge` (Device → Host)

The human pressed the button or double-tapped the Device (a key press on the Virtual Device). It is for every session, so it carries no other field. The Device applies it at once (Done sessions show as Idle, every Escalation restarts) and the Host follows: it sends a `session_upsert` with `idle` for each Done session and remembers the Acknowledge for each Awaiting Input one. It sends no `acknowledge` back.

### `acknowledge` (Host → Device)

The Host tells the Device when the human Acknowledged one waiting session elsewhere (a `codino event --acknowledge`), and replays it after the `session_upsert` of each Awaiting Input session it Acknowledged when it claims a Device. The Host clears Done with the session's `session_upsert` with `idle`, and a new prompt typed into a session makes it Working with a `session_upsert`; it sends no `acknowledge` for those. A Device still clears Done on any `acknowledge` it receives, as Acknowledge always does.

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Agent Session id. Unknown ids are a no-op. |
| `host_time` | integer | Host time of the Acknowledge. Escalation restarts from it. |

A Device needs nothing but the `type`: without `id` the Acknowledge is for every session, and without `host_time` it happened now. An `id` that is present but not a non-empty string makes the message ignored.

## Escalation and Done decay

Escalation is the Device's (ADR-0001): it renders it from `state_entered`, the latest `acknowledge` and its own clock, relating Host times to its clock through the `host_time` of `hello` and `heartbeat`. A Host time after the Device's estimate of now counts as now. The thresholds are Preferences (below); by default: a gentle pulse for 30 s, faster and brighter until 2 min, a Takeover of the whole matrix in the waiting session's `project_color` until 10 min (cycling between the Project Colors of every session in Takeover, one a second), then a steady glow. Where the whole matrix shows the Aggregate State (below), it shows the most insistent step any Awaiting Input session has reached; a Tile shows its own session's step: Takeover, faster, gentle, steady glow (a session that has just started to wait outranks one that settled).

Done decay is the Host's: the Session Tracker returns a session that stayed Done for the Done decay time (a Preference, 10 min by default) to Idle, as an Acknowledge of that session only, and sends its `session_upsert` (`state_entered` is when the decay was due). The Host is canonical for Session States, so `codino status` and the Device never disagree about a decayed session.

## Disconnected

Every message the Device can use (`hello`, `heartbeat`, a valid `session_upsert`, `session_remove`, `acknowledge` or `preferences`, `raw_mode_enter`, `raw_mode_exit`, a Raw Mode frame it shows, a valid `preview_enter`, `preview_exit`, an `install` or `uninstall` it answers) tells it the Host is alive; lines it ignores don't. When nothing usable has arrived for the Disconnected timeout, the Device shows Disconnected. It counts from boot too, so a Device that never heard from a Host shows it as well.

- The timeout is a Preference (`disconnected_timeout_ms`, 5 seconds by default), which a Device boots with from its cache, so it applies before any Host connects.
- Disconnected is not a Session State. It hides the sessions, because the Device no longer knows their state, and it never looks like Idle: v0 draws a dim diagonal line on a dark matrix. It shows in Quiet Mode too.
- The next usable message ends it. A heartbeat brings back the sessions the Device holds; a `hello` replaces them.
- Disconnected ends Raw Mode (above) and the preview (below).
- A Host Daemon that exits stops the heartbeats too, so the Device shows Disconnected until the next Host Daemon says `hello`.

## Raw Mode (Host → Device)

Raw Mode is the escape hatch for custom pixels (`codino show`): the Host streams frames and the Device shows them instead of its own rendering. Awaiting Input always pre-empts it (ADR-0001).

### `raw_mode_enter`

No other field. The Device enters Raw Mode with a dark matrix until the first frame. Entering again while in Raw Mode clears the matrix too.

### `raw_mode_exit`

No other field. The Device returns to its own rendering. Ignored outside Raw Mode.

### Raw Mode frame (binary)

The pixels of one frame, as one line of bytes:

| Bytes | Value |
|---|---|
| 1 | `0xFF`: a frame starts here. No JSON line starts with it. |
| 3 × width × height | One LED after another, row-major from the top-left of the logical matrix (the Device maps it to its wiring, serpentine and color order), each as R, G, B, 0–255. No byte is `0x0A`: the Host sends 11 for a channel at 10, an invisible difference. |
| 1 | `0x0A` (`\n`). |

The length is not sent: width and height are the Device's, from the `hardware_profile` of its `hello_reply`, swapped while the `rotation` Preference is 90 or 270 (the picture is seen turned sideways), and the Host scales every picture to them. The Device turns the frame by the rotation and scales it under its brightness cap like everything it shows. A Device uses a frame only in Raw Mode and only when it holds exactly `1 + 3 × width × height` bytes before the newline; it ignores any other frame as a whole, like an unusable JSON message. Transports must pass a frame's bytes unchanged: a trailing `\r` is not stripped from a frame.

Why one line: because no pixel byte is a newline, a frame is framed like any JSON message, and every receiver resynchronises the same way: at the next `\n`. A frame cut short (a Host that died mid-frame) runs into the next line and is ignored for its wrong length, losing that line too (should the two add up to a frame's length exactly, they show as noise until the next frame); the Host's next message, at the latest its next heartbeat a second later, arrives whole.

Size: a 16×16 matrix makes 770-byte lines (8×8: 194), so a Device's line buffer holds `2 + 3 × width × height` bytes, and its Raw Mode keeps one frame (768 bytes for 16×16) apart from the rendered one, to show again after a pre-emption.

### Rules

- **Pre-emption.** While any session is Awaiting Input the Device renders as if not in Raw Mode: the Aggregate State or Tiles, with Escalation and Takeover going on as usual. It stays in Raw Mode meanwhile: once no session is Awaiting Input, the latest frame shows again. The Host keeps streaming either way.
- **The Host gone.** Disconnected ends Raw Mode, and so does `hello`: the Host that streamed the pixels is gone. After Disconnected the next heartbeat brings back the Device's own rendering, not the old pixels; a Host that still has a picture to show sends `raw_mode_enter` again after the `session_upsert`s of a new handshake.
- `raw_mode_enter`, `raw_mode_exit` and a frame the Device uses all tell the Device the Host is alive (Disconnected, below).
- Quiet Mode doesn't hide Raw Mode: the human asked for those pixels (`codino show`).

### What the Host sends (`codino show`)

`codino show IMAGE` hands a PNG, JPEG or GIF to the Host Daemon, which keeps the show while the command runs:

1. On start, or on claiming a Device during a show (after the `session_upsert`s and replayed `acknowledge`s): `raw_mode_enter`, then the current frame. Also after the Host itself went silent for the Disconnected timeout (a laptop asleep), since the Device left Raw Mode meanwhile.
2. Each picture is fitted into the Device's matrix: its aspect ratio kept, centered on unlit LEDs, each LED the average of the pixels it covers (the nearest one when scaling up), transparency as unlit.
3. An animated GIF loops forever by its frame delays (its loop count is ignored), its frames composed by their disposal methods; delays under 20 ms play at 100 ms, as browsers do. A frame is sent when the one due changes, and again with every heartbeat, so a frame lost or garbled on the way is good again a second later.
4. On Ctrl-C (or SIGTERM, or the command's connection to the daemon closing for any reason): `raw_mode_exit`. A newer `codino show` replaces the picture (a new `raw_mode_enter` and frame) and ends the older command.

A show keeps the Host Daemon from retiring, as sessions do.

## Preview (Host → Device)

The web UI (`codino ui`) lets the human see what each Session State looks like without waiting for an Agent: it forces the Device, and the UI's in-browser Virtual Device, into one state, then restores them.

### `preview_enter`

| Field | Type | Meaning |
|---|---|---|
| `state` | string | The Session State to show. |
| `elapsed_ms` | integer ≥ 0 | How long the previewed session has been in it: for Awaiting Input, which Escalation step shows (e.g. the `escalation_takeover_ms` Preference for the Takeover). |

The Device shows what it would with a lone session in `state` that entered it `elapsed_ms` ago, without a Project Color (so in the Session State's own color, a Takeover included), animated and escalating from there by its clock: the Theme, brightness, rotation and Quiet Mode apply as usual. The preview shows in place of every session the Device holds and of Raw Mode; the Device keeps them meanwhile (upserts, removes, Acknowledges and frames still apply) and shows them again once the preview ends. A new `preview_enter` replaces the preview.

A v0 Device needs a valid `state`; without `elapsed_ms` the session has just entered it, and an `elapsed_ms` that is present but not an integer from 0 to 2³²−1 makes the message ignored.

### `preview_exit`

No other field. The Device returns to its sessions (or Raw Mode). Ignored without a preview.

### Rules

- **The Host gone.** Disconnected and `hello` end the preview, as they end Raw Mode. The Host sends `preview_enter` again, its `elapsed_ms` grown by the time passed, after the `session_upsert`s (and Raw Mode) of a new handshake, and after a silence the Device took for Disconnected.
- **Bounded.** The Host ends the preview (`preview_exit`) when the web UI closes, so a preview never outlives the page that asked for it.
- `preview_enter` and `preview_exit` tell the Device the Host is alive (Disconnected, above).

## Layout and Tiles

How the matrix is used is the Device's Layout, the `layout` Preference:

- **Adaptive** (the default): one Tile per Agent Session. One session has the whole matrix, two get halves (left, right), three or four get quadrants (top-left, top-right, bottom-left, bottom-right), five to eight get one column each (an eighth of the width, from the left). With nine or more, seven sessions get columns and the eighth column is the Overflow Tile, which shows the Aggregate State of the rest.
- **Aggregate**: the whole matrix shows the Aggregate State, however many sessions there are.

Tile slots are the Host's. Tile slot `i` is the `i`th region in the order above; slots from 7 up, with nine or more sessions, are the Overflow Tile. The room the adaptive Layout has is 1, 2, 4 or 8 Tiles for 1, 2, 3–4 and 5–8 sessions, and 7 plus the Overflow Tile beyond. The Host keeps a session's slot for its whole life while it fits that room. When a session starts or ends, the sessions without a slot that fits (a newcomer, a session whose slot the shrunken Layout lost, a session in the Overflow Tile) take the first free slots in order of session start time; any left over keep, or take, a slot from 7 up. The Host sends a `session_upsert` for every session whose slot changed, after the `session_upsert` or `session_remove` that changed the number of sessions.

The Device places each session at its slot when the slot fits the room and no other session holds it. Between those messages (a session removed, the moved sessions not upserted yet) slots can disagree with the room: the sessions that don't fit take the free Tiles in slot order, and those left over go to the Overflow Tile.

## Rendering

The Device renders by the Theme the `theme` Preference names ([themes.md](themes.md)): `default`, the Character Theme with the mascot, unless the Preferences say otherwise.

With one session, or in the Aggregate Layout, the whole matrix shows the Aggregate State as the Theme draws it: a pose of the mascot, or an effect in the Session State's own color. The most urgent Session State wins: Awaiting Input > Error > Done > Working > Idle. Awaiting Input escalates as above, Escalation dimming and brightening what the Theme draws. With no sessions the matrix is dark.

With several sessions in the adaptive Layout each Tile is lit in its session's Project Color and animated by its Session State's effect in the Theme; a pose needs the whole matrix, so under a Character Theme Tiles use the effects of the abstract Theme (`minimal`): Idle is a dim steady glow, Working a bright row running down a dimmer Tile, Done steadily lit, Error blinking, Awaiting Input pulsing with its own Escalation step (a gentle pulse, then a faster and brighter one, then a steady glow). Free Tiles are dark. The Overflow Tile is animated the same way by the Aggregate State of its sessions, in that Session State's own color (the first of its palette in the Theme), since it stands for several projects. A Takeover takes the whole matrix in either Layout.

Escalation's brightness steps are fractions of full brightness, which the brightness cap scales like every other pixel (below). In Quiet Mode the matrix shows only Awaiting Input: the Aggregate State is dark unless it is Awaiting Input, and every Tile but those of sessions Awaiting Input (and an Overflow Tile holding one) is dark. Escalation and Takeover go on as usual.

## Preferences (Host → Device)

The user's Preferences live in a config file on the Host, which is canonical (`codino get/set`). The Host pushes the ones the Device renders by; it keeps the others (Done decay, pinned Project Colors) to itself.

### `preferences`

Sent after `hello_reply`, before the `session_upsert`s, on every claim, and again whenever the config file changes what it carries.

| Field | Type | Meaning |
|---|---|---|
| `theme` | string | The name of the Theme to render by: 1–32 of `a-z`, `0-9`, `-`, `_`. A Theme the Device doesn't have shows as `default`; the Device keeps the name, so the Theme shows once it is installed. |
| `brightness` | integer 1–100 | Percent of the Hardware Profile's `brightness_cap`. |
| `layout` | string | `adaptive` or `aggregate` (Layout and Tiles, above). |
| `rotation` | integer | `0`, `90`, `180` or `270`: degrees the picture is turned clockwise on the matrix. |
| `quiet_mode` | bool | Show nothing but Awaiting Input (Rendering, above). |
| `disconnected_timeout_ms` | integer ≥ 1000 | Host silence before Disconnected. The Host's config file takes 2 s at least, so one late heartbeat never shows Disconnected; the Device accepts any timeout a heartbeat a second keeps away. |
| `escalation_faster_ms`, `escalation_takeover_ms`, `escalation_steady_ms` | integer | When each Escalation step starts (Escalation, above), in order: faster ≤ takeover ≤ steady (two at the same time skip the first of them). |

A push is the whole set: a field the Device doesn't find takes the Device's default (`default`, 100, `adaptive`, 0, `false`, 5000, 30000, 120000, 600000, as the Host's defaults), not its previous value. A field present with an invalid value makes the Device ignore the whole message, which then doesn't count as hearing from the Host either.

- **Brightness.** Every LED channel the Device shows (Session States, Escalation's steps, Takeover, Disconnected, Raw Mode frames) is scaled to `brightness_cap × brightness / 100` of full; a lit channel stays at least 1, so dim patterns never go dark on a dim Device. Whatever the Preferences say, nothing exceeds the cap.
- **Rotation.** The Device lays the picture out in the turned shape (a 16×8 matrix turned by 90 shows an 8×16 picture) and turns it onto the LEDs, Raw Mode frames included.
- **Cached.** The Device keeps the last Preferences it was pushed (in flash; the terminal Virtual Device in its `--preferences-file`) and boots with them, so the Disconnected timeout, brightness and rotation apply before any Host connects. It writes the cache only when a push changes them.

## Themes and Sprite Packs (Host → Device, reply Device → Host)

Themes and Sprite Packs install on the Device without reflashing ([themes.md](themes.md) has their format). The Device keeps them in its flash (the terminal Virtual Device in its `--store-dir`), at most 8 of each besides its built-in ones, and boots with them. The Host doesn't keep them: what a Device has, it says in its `hello_reply` and in every `install_reply`.

### `install`

A Theme or Sprite Pack file, as one line, with `"type":"install"` added: its `kind` (`theme` or `sprite_pack`), its `name`, and the fields of its kind. The Device validates the whole of it; one it refuses changes nothing. Installing under a name the Device has replaces it; a built-in name is refused. Installing the selected Theme, or a Sprite Pack it uses, shows at once.

### `uninstall`

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | `theme` or `sprite_pack`. |
| `name` | string | What to remove. Built-in ones can't be removed. Uninstalling the selected Theme shows `default`; uninstalling a Sprite Pack the selected Theme uses shows its abstract effects instead of those poses. |

### `install_reply`

The Device answers every `install` and `uninstall` with one, which also counts as hearing from the Host. The Host sends one install or uninstall at a time and waits for its reply.

| Field | Type | Meaning |
|---|---|---|
| `kind`, `name` | string | As in the message answered. |
| `ok` | bool | Whether it was done. |
| `error` | string | Why not, when `ok` is false (e.g. `states.idle: palette must hold 1 to 16 colors`), for the CLI to show. |
| `themes`, `sprite_packs` | array of strings | What the Device has now, as in `hello_reply`. |

## Fixtures

`fixtures/` holds one example per message kind, one message per `.jsonl` file, and the binary Raw Mode frame in `raw-frame.bin`. They are the contract between the two sides: Host core tests assert that they produce the Host → Device messages and accept the Device → Host ones, and Device core tests the other way round.

| Fixture | Direction |
|---|---|
| `hello.jsonl` | Host → Device |
| `hello-reply.jsonl` | Device → Host |
| `session-upsert.jsonl` | Host → Device |
| `session-remove.jsonl` | Host → Device |
| `heartbeat.jsonl` | Host → Device |
| `acknowledge.jsonl` | Host → Device |
| `device-acknowledge.jsonl` | Device → Host |
| `raw-mode-enter.jsonl` | Host → Device |
| `raw-mode-exit.jsonl` | Host → Device |
| `raw-frame.bin` | Host → Device: a frame for the 8×8 reference build, the LED at (x, y) in R = 32x, G = 32y, B = 128 |
| `preferences.jsonl` | Host → Device: the `minimal` Theme, brightness 50, the Aggregate Layout, turned 180°, Disconnected after 10 s, Escalation at 20 s, 1 min, 5 min |
| `install-sprite-pack.jsonl` | Host → Device: the Sprite Pack `dots`, two 4×4 frames |
| `install-theme.jsonl` | Host → Device: the Theme `ember`, with every effect but `pulse` and a pose of `dots` for Done |
| `install-reply.jsonl` | Device → Host: the reply to `install-theme.jsonl` on the reference build that has `dots` |
| `uninstall.jsonl` | Host → Device: uninstall the Theme `ember` |
| `preview-enter.jsonl` | Host → Device: preview Awaiting Input 2 min 30 s in, a Takeover by the default Escalation |
| `preview-exit.jsonl` | Host → Device |

A change to the protocol changes the fixtures in the same commit.
