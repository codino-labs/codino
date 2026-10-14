# Protocol v0

The wire protocol between the Host Daemon and a Device (or a Virtual Device). It is semantic (ADR-0001): the Host says what state each Agent Session is in, and the Device decides how that looks.

v0 covers the handshake, session upsert, session remove, heartbeat and Disconnected. Later kinds from the v1 spec (Preferences push, acknowledge, Raw Mode, Theme and Sprite Pack upload, preview-state override) will be added here with their own fixtures.

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
| `state_entered` | integer | Host time the session entered `state`. |
| `tile` | integer | Tile slot assigned by the Host, starting at 0. |

A v0 Device needs `id` (a non-empty string) and a valid `state`.

### `session_remove`

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Agent Session id. Removing an unknown id is a no-op. |

### `heartbeat`

Tells the Device the Host is alive. The Host sends one at least every second while it drives the Device.

| Field | Type | Meaning |
|---|---|---|
| `host_time` | integer | Host time when sent. |

## Disconnected

Every message the Device can use (`hello`, `heartbeat`, a valid `session_upsert` or `session_remove`) tells it the Host is alive; lines it ignores don't. When nothing usable has arrived for the Disconnected timeout, the Device shows Disconnected. It counts from boot too, so a Device that never heard from a Host shows it as well.

- The timeout is 5 seconds for now. It becomes a Preference, carried by the Preferences push.
- Disconnected is not a Session State. It hides the sessions, because the Device no longer knows their state, and it never looks like Idle: v0 draws a dim diagonal line on a dark matrix.
- The next usable message ends it. A heartbeat brings back the sessions the Device holds; a `hello` replaces them.
- A Host Daemon that exits stops the heartbeats too, so the Device shows Disconnected until the next Host Daemon says `hello`.

## Rendering in v0

The Device shows the Aggregate State as one solid color across the whole matrix. The most urgent Session State wins: Awaiting Input > Error > Done > Working > Idle. With no sessions the matrix is dark. Themes, Layouts with Tiles, Escalation and enforcing the Hardware Profile's `brightness_cap` come later.

## Fixtures

`fixtures/` holds one example per message kind, one message per `.jsonl` file. They are the contract between the two sides: Host core tests assert that they produce these messages, and Device core tests assert that they accept them (and, for `hello_reply`, that they produce it).

| Fixture | Direction |
|---|---|
| `hello.jsonl` | Host → Device |
| `hello-reply.jsonl` | Device → Host |
| `session-upsert.jsonl` | Host → Device |
| `session-remove.jsonl` | Host → Device |
| `heartbeat.jsonl` | Host → Device |

A change to the protocol changes the fixtures in the same commit.
