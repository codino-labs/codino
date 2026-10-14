# Protocol v0

The wire protocol between the Host Daemon and a Device (or a Virtual Device). It is semantic (ADR-0001): the Host says what state each Agent Session is in, and the Device decides how that looks.

v0 covers the handshake, session upsert, session remove, heartbeat, acknowledge and Disconnected. Later kinds from the v1 spec (Preferences push, Raw Mode, Theme and Sprite Pack upload, preview-state override) will be added here with their own fixtures.

## Framing

- Transport: USB CDC serial on a Device, stdin/stdout on the terminal Virtual Device.
- Every message is one JSON object on one line, UTF-8, terminated by `\n` (newline-delimited JSON). A trailing `\r` is tolerated.
- Every message has a string field `type`.
- Receivers ignore unknown fields, unknown `type`s and lines that are not valid JSON. Senders always send every field listed for a message. A receiver ignores a message as a whole when a field it needs is missing or invalid; each message below says which fields a v0 Device needs. This keeps an older Device usable by a newer Host for everything the Device already understands.
- Times are Unix milliseconds on the Host clock. The Device has no wall clock; it relates Host times to its own clock through `host_time` in `hello` and `heartbeat`.

## Handshake

The Host opens the link with `hello`. The Device answers with `hello_reply`. The Host claims a port only after a valid reply, and refuses to drive a Device whose `protocol` differs from its own (it surfaces "update firmware" instead).

### `hello` (Host → Device)

A Device answers every `hello`, whatever its fields, so that a Host speaking any protocol version learns which version the Device speaks.

A `hello` starts a new link: the Device forgets every Agent Session it holds, and the Host follows a successful handshake with a `session_upsert` for every session it knows. Sessions from an earlier Host Daemon never linger.

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

Hardware Profile fields:

| Field | Type | Meaning |
|---|---|---|
| `width`, `height` | integer | Matrix size in pixels. |
| `data_pin` | integer | GPIO driving the LED data line. |
| `serpentine` | bool | `true` when alternate rows run in reverse (zig-zag wiring). |
| `color_order` | string | LED channel order, e.g. `"GRB"`. |
| `brightness_cap` | integer 0–255 | Maximum brightness, enforced whatever the Preferences say. |
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

The Host's Project Color Allocator picks `project_color`: a pinned color, else a stable hash of the git repo root of the session's project (the directory itself outside a repo; every worktree of a repo shares its root) onto a palette of 8 well-separated hues. When an active session of another project already holds that hue, the newcomer takes the next free one, and keeps it for its lifetime. Sessions of the same project share a color.

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

Escalation is the Device's (ADR-0001): it renders it from `state_entered`, the latest `acknowledge` and its own clock, relating Host times to its clock through the `host_time` of `hello` and `heartbeat`. A Host time after the Device's estimate of now counts as now. Defaults, until Preferences carry them: a gentle pulse for 30 s, faster and brighter until 2 min, a Takeover of the whole matrix in the waiting session's `project_color` until 10 min (cycling between the Project Colors of every session in Takeover, one a second), then a steady glow. Where the whole matrix shows the Aggregate State (below), it shows the most insistent step any Awaiting Input session has reached; a Tile shows its own session's step: Takeover, faster, gentle, steady glow (a session that has just started to wait outranks one that settled).

Done decay is the Host's: the Session Tracker returns a session that stayed Done for the Done decay time (10 min by default) to Idle, as an Acknowledge of that session only, and sends its `session_upsert` (`state_entered` is when the decay was due). The Host is canonical for Session States, so `codino status` and the Device never disagree about a decayed session.

## Disconnected

Every message the Device can use (`hello`, `heartbeat`, a valid `session_upsert`, `session_remove` or `acknowledge`) tells it the Host is alive; lines it ignores don't. When nothing usable has arrived for the Disconnected timeout, the Device shows Disconnected. It counts from boot too, so a Device that never heard from a Host shows it as well.

- The timeout is 5 seconds for now. It becomes a Preference, carried by the Preferences push.
- Disconnected is not a Session State. It hides the sessions, because the Device no longer knows their state, and it never looks like Idle: v0 draws a dim diagonal line on a dark matrix.
- The next usable message ends it. A heartbeat brings back the sessions the Device holds; a `hello` replaces them.
- A Host Daemon that exits stops the heartbeats too, so the Device shows Disconnected until the next Host Daemon says `hello`.

## Layout and Tiles

How the matrix is used is the Device's Layout, a Device-side setting until the Preferences push carries it (#11):

- **Adaptive** (the default): one Tile per Agent Session. One session has the whole matrix, two get halves (left, right), three or four get quadrants (top-left, top-right, bottom-left, bottom-right), five to eight get one column each (an eighth of the width, from the left). With nine or more, seven sessions get columns and the eighth column is the Overflow Tile, which shows the Aggregate State of the rest.
- **Aggregate**: the whole matrix shows the Aggregate State, however many sessions there are.

Tile slots are the Host's. Tile slot `i` is the `i`th region in the order above; slots from 7 up, with nine or more sessions, are the Overflow Tile. The room the adaptive Layout has is 1, 2, 4 or 8 Tiles for 1, 2, 3–4 and 5–8 sessions, and 7 plus the Overflow Tile beyond. The Host keeps a session's slot for its whole life while it fits that room. When a session starts or ends, the sessions without a slot that fits (a newcomer, a session whose slot the shrunken Layout lost, a session in the Overflow Tile) take the first free slots in order of session start time; any left over keep, or take, a slot from 7 up. The Host sends a `session_upsert` for every session whose slot changed, after the `session_upsert` or `session_remove` that changed the number of sessions.

The Device places each session at its slot when the slot fits the room and no other session holds it. Between those messages (a session removed, the moved sessions not upserted yet) slots can disagree with the room: the sessions that don't fit take the free Tiles in slot order, and those left over go to the Overflow Tile.

## Rendering in v0

With one session, or in the Aggregate Layout, the Device shows the Aggregate State as one solid color across the whole matrix. The most urgent Session State wins: Awaiting Input > Error > Done > Working > Idle. Awaiting Input escalates as above; the other states are steady. With no sessions the matrix is dark.

With several sessions in the adaptive Layout each Tile is lit in its session's Project Color and animated by its Session State: Idle is a dim steady glow, Working a bright row running down a dimmer Tile, Done steadily lit, Error blinking, Awaiting Input pulsing with its own Escalation step (a gentle pulse, then a faster and brighter one, then a steady glow). Free Tiles are dark. The Overflow Tile is animated the same way by the Aggregate State of its sessions, in that Session State's own color, since it stands for several projects. A Takeover takes the whole matrix in either Layout.

Themes and enforcing the Hardware Profile's `brightness_cap` come later: Escalation's brightness steps are fractions of full brightness, which the cap will scale.

## Fixtures

`fixtures/` holds one example per message kind, one message per `.jsonl` file. They are the contract between the two sides: Host core tests assert that they produce the Host → Device messages and accept the Device → Host ones, and Device core tests the other way round.

| Fixture | Direction |
|---|---|
| `hello.jsonl` | Host → Device |
| `hello-reply.jsonl` | Device → Host |
| `session-upsert.jsonl` | Host → Device |
| `session-remove.jsonl` | Host → Device |
| `heartbeat.jsonl` | Host → Device |
| `acknowledge.jsonl` | Host → Device |
| `device-acknowledge.jsonl` | Device → Host |

A change to the protocol changes the fixtures in the same commit.
