---
name: codino
description: Show or change the Codino Preferences (brightness, Layout, rotation, Quiet Mode, Done decay, Escalation thresholds, Disconnected timeout, pinned Project Colors) through `codino get/set/unset`. Use when the user asks to change how their Codino LED matrix looks or behaves.
argument-hint: "[KEY [VALUE]] | unset KEY | what you want changed"
allowed-tools: Bash(codino get:*), Bash(codino set:*), Bash(codino unset:*)
---

# Codino Preferences

The Preferences live in one config file, which the `codino` CLI reads and edits. The Host Daemon applies a change to the Device within a second. Never edit the config file yourself: always go through `codino get`, `codino set` and `codino unset`, which validate every value and keep the user's comments.

The current Preferences:

!`codino get`

Arguments: `$ARGUMENTS`

- No arguments: show the Preferences above as a short table, and say how to change one (`/codino KEY VALUE`).
- `KEY`: run `codino get KEY` and show the value.
- `KEY VALUE`: run `codino set KEY VALUE`.
- `unset KEY`: run `codino unset KEY`, which returns that Preference to its default.
- Anything else ("make it dimmer", "quiet mode on", "pin this project to orange"): work out the key and value from the list below and run `codino set`. Ask only if the request is ambiguous.

Keys and values:

| Key | Values |
|---|---|
| `theme` | `default` (more Themes come later) |
| `brightness` | 1-100: percent of the Device's brightness cap, which the Device always enforces |
| `layout` | `adaptive` (one Tile per session) or `aggregate` (the Aggregate State on the whole matrix) |
| `rotation` | `0`, `90`, `180` or `270` degrees clockwise |
| `quiet_mode` | `true` or `false`: show nothing but Awaiting Input (Escalation included) |
| `done_decay` | a duration such as `10m`: how long Done shows before it decays to Idle |
| `disconnected_timeout` | `2s` to `1h`: how long the Host may be silent before the Device shows Disconnected |
| `escalation.faster` | a duration such as `30s`: Awaiting Input pulses faster and brighter |
| `escalation.takeover` | a duration such as `2m`: the waiting session takes the whole matrix over |
| `escalation.steady` | a duration such as `10m`: Escalation settles into a steady glow |
| `project_colors.PATH` | `#rrggbb` pinned to the git repo PATH is in; `project_colors..` is the current project |

The Escalation steps must stay in order (faster ≤ takeover ≤ steady). If `codino set` refuses a value, show its error and suggest a valid value. After a change, report the new value in one line.
