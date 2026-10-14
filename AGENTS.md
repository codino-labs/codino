# Codino

USB LED matrix that shows the state of the AI agent sessions on the host. Domain vocabulary is in `CONTEXT.md`; decisions in `docs/adr/`.

## Go

The host side (hook binary + Host Daemon) is Go (ADR-0002). The user installs the plugin and nothing else, so every change keeps that true:

- **Pure Go, `CGO_ENABLED=0`.** The binary cross-compiles for Linux, macOS and Windows from one machine. A dependency that needs cgo needs an ADR.
- **Standard library first.** Add a module only when the stdlib has no answer (e.g. serial port access); say why in the commit message.
- **Hooks stay fast and harmless.** The hook binary forwards the event and exits 0, even when the daemon or Device is unreachable. A Codino failure never blocks or fails an Agent Session.
- **Name code with the glossary.** Types, functions and test names use `CONTEXT.md` terms: `SessionState`, `AggregateState`, `HostDaemon`, `Acknowledge`. The glossary's _Avoid_ words stay out of identifiers too.

Layout: binaries in `cmd/<name>/`, packages in `internal/`. Errors are wrapped with `fmt.Errorf("...: %w", err)`; logging uses `log/slog`.

### Tests

Test against the Virtual Device, so the suite runs without hardware. Table-driven tests for state logic (Aggregate State precedence, Escalation, Done decay); inject the clock so timers are deterministic.

A change is done when this exits 0:

```sh
test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...
```

`-race` matters: the daemon serves many hook connections and one serial port concurrently.

Run a single test with `go test -race ./internal/host -run 'TestReplug'`.

### Packages

```
cmd/codino/          the one binary: CLI (`event`, `status`), the Host Daemon (`daemon`) and `hook` (internal, run by the plugin)
internal/claudecode/ Claude Code Adapter: hook JSON → host.Events; transcript probe (interrupts, last write)
internal/host/       Host core: Session Tracker, Aggregate State, Done decay, Acknowledge, Device Link driving, heartbeat, exit grace, stuck-session cleanup; clock and probes injected
internal/gitrepo/    repo root of a project directory (worktrees share the main repo's), read from git's files without running git
internal/liveness/   liveness probe: is the Agent's pid still its process (signal 0; /proc start time on Linux)
internal/devicelink/ handshake and claim; Discoverer seam (VirtualDevice now, USB serial later)
internal/daemon/     local Unix socket: server, client (`Client.Deliver` spawns the daemon on demand)
internal/protocol/   wire messages, as in protocol/README.md
```

Every Agent's signals become `host.Event`s delivered with `daemon.Client.Deliver`: `codino event` does it from flags, `codino hook` from hook JSON through the Claude Code Adapter. Events carry the time the Agent signalled them (`At`); the Session Tracker drops one older than the last applied to its session, because async hooks race. Host core tests (`internal/host`) drive events and fake clock ticks in and assert on the protocol lines a fake Device receives, including the shared fixtures; `claudecode_replay_test.go` replays the hook spike recordings in `fixtures/hooks/claude-code/` through the Adapter.

Stuck sessions (#8): once a second the Host probes, outside its lock, the liveness of every session with a pid (`LivenessProbe`) and the transcript of every Working/Awaiting Input session (`TranscriptProbe`). A session whose Agent is gone is removed, as if it had ended (kill -9 fires no SessionEnd); an interrupted turn goes back to Idle; a Working session with no hook and no transcript write for `host.StaleAfter` (15 min) falls back to Idle. The hook reads the Agent's start time (`liveness.Started`) while the Agent is certainly alive, so a reused pid reads as gone. A session without a pid is never reaped, only judged stale. Host core tests fake both probes (`fakeLiveness`, `fakeTranscripts`, `quietTranscript`). The product name stays in `cmd/codino` (runtime directory name, `CODINO_VIRTUAL_DEVICE`, `CODINO_EXIT_GRACE`).

Done decay and Acknowledge (#9): the Session Tracker returns a session Done for `host.DefaultDoneDecay` (10 min, `host.WithDoneDecay`) to Idle on a Tick. An event with `Acknowledge` (a prompt the human typed, `codino event --acknowledge`) clears that session's Done and restarts its Escalation; an `acknowledge` from the Device (`devicelink.Device.Acknowledged`) does it for every session. Escalation itself is the Device's (ADR-0001): the Host only tells it when a waiting session was Acknowledged, and replays that after the upserts when it claims a Device. `internal/host/acknowledge_test.go` drives the Device side through the fake Device's `toHost`.

Layout and Project Colors (#10): the Host's Project Color Allocator (`project_color.go`) gives a session a pinned color (`host.WithPinnedProjectColors`, until Preferences carry pins in #11), else the color of an active session of the same repo root, else the hue of `host.ProjectColors` its repo root hashes to (FNV-1a), bumped to the next hue no active session holds. A session keeps it for its lifetime. Repo roots come from the injected `host.ProjectRootProbe` (`gitrepo.Roots` in the daemon; tests use `fakeRoots`), resolved outside the Host's lock. Tile slots (`assignTiles`) follow the adaptive Layout's room (1, 2, 4, 8, then 7 plus the Overflow Tile from slot 7): a session keeps a slot that fits; newcomers and sessions that no longer fit take the first free slots by session start time, and the Host upserts every session that moved. `project_color_test.go` and `tile_test.go` assert on the upserts the fake Device receives.

### Demo: `codino event` drives the Virtual Device

Real USB discovery isn't built yet, so the Host Daemon drives the terminal Virtual Device: it spawns it as a subprocess and speaks the protocol on its stdin/stdout. The matrix goes to the daemon's stderr. Run the daemon in the foreground in one terminal to watch the matrix:

```sh
make -C device && go build -o codino ./cmd/codino
./codino daemon --virtual-device device/build/virtual-device
```

and send events from another:

```sh
./codino event --session a --state working --project .
./codino event --session b --state awaiting_input
./codino status
./codino event --session b --acknowledge   # restarts b's Escalation
./codino event --session b --end
```

Awaiting Input escalates on the matrix: a gentle pulse, faster and brighter after 30 s, a Takeover in the Project Color after 2 min, a steady glow after 10 min. Press Enter in the daemon's terminal to Acknowledge on the Virtual Device: it reads keys from `/dev/tty` only when its matrix goes to a terminal.

Without a running daemon, `codino event` spawns one in the background. It reads the Virtual Device path from `CODINO_VIRTUAL_DEVICE` (use an absolute path) and writes its output, matrix included, to `host.log` next to the socket:

```sh
export CODINO_VIRTUAL_DEVICE=$PWD/device/build/virtual-device
./codino event --session a --state awaiting_input
tail -f "$XDG_RUNTIME_DIR/codino/host.log"
```

The socket, lock and log live in `$XDG_RUNTIME_DIR/codino/` (owner-only), or `$TMPDIR/codino-<uid>/` when `XDG_RUNTIME_DIR` is unset.

The daemon exits after 5 minutes with no Agent Sessions (`--exit-grace`, or `CODINO_EXIT_GRACE=10s` so a spawned daemon inherits it); the next `codino event` spawns a new one. An exiting daemon refuses events with a `retiring` reply, so the client spawns the next daemon instead of losing them, and a new daemon waits for the old one to release the lock. Stop it early with Ctrl-C or `pkill -f 'codino daemon'`.

### Claude Code plugin

`plugin/` is the Claude Code plugin: `.claude-plugin/plugin.json` and `hooks/hooks.json`, where every hook is `async` and runs `"${CLAUDE_PLUGIN_ROOT}/bin/codino" hook`. `codino hook` reads the hook JSON on stdin, forwards it (spawning the Host Daemon unless the session is ending) and always exits 0. Nothing is ever written into the plugin root, which changes on every update: persistent Host state belongs in `$CLAUDE_PLUGIN_DATA`, runtime files in the runtime directory above. The hook→Session State mapping is documented on `claudecode.Events`.

`make plugin` builds the Linux binary into `plugin/bin/` (git-ignored; per-OS packaging is #17). Try it in a scratch directory outside the repo, without touching `~/.claude`:

```sh
make plugin && make -C device
export CODINO_VIRTUAL_DEVICE=$PWD/device/build/virtual-device
cd "$(mktemp -d)" && claude --plugin-dir ~/code/claudino/plugin   # or: -p "say hi" --model haiku
# elsewhere: codino status; tail -f "$XDG_RUNTIME_DIR/codino/host.log"
```

## Protocol

`protocol/README.md` is the wire protocol spec (currently v0); `protocol/fixtures/` holds one example `.jsonl` per message kind. Both sides test against the fixtures: Host tests produce them, Device tests accept them. A protocol change updates the spec and the fixtures in the same commit.

## Device (C++)

```
device/
  core/              Device core: protocol messages + Clock in, PixelBuffer out
  test/              native Device core tests (doctest)
  virtual/terminal/  terminal Virtual Device (stdin/stdout protocol, matrix on stderr)
  third_party/       vendored single headers (ArduinoJson, doctest), see its README
```

- **The Device core stays portable.** C++17 and the standard library plus ArduinoJson only: no Arduino core, FastLED or OS calls, because the same code builds for the firmware, the terminal and the browser. Hardware, time and I/O come in through the constructor (`HardwareProfile`, `Clock`) or `receive()`.
- **Test at the seam.** Feed protocol lines (prefer the fixtures), `acknowledge()` presses and fake clock ticks; assert on `render()`'s pixel buffer with semantic checks ("the whole matrix is one lit color", "same frame as a lone Awaiting Input session"), not golden frames or private state. No test sleeps.
- **Names** follow `CONTEXT.md` (`SessionState::AwaitingInput`, `aggregateState`, `HardwareProfile`). The product name stays out of identifiers: the namespace is `device`.

A change is done when this exits 0 (needs `cmake` and a C++17 compiler):

```sh
make -C device test
```

Run a single test with `device/build/device_core_test -tc="*Aggregate State*"`.

The Layout (#10) is a `device::Layout` constructor parameter, adaptive by default, until the Preferences push carries it (#11); the terminal Virtual Device takes `--layout aggregate`. One session, or the Aggregate Layout, shows the Aggregate State on the whole matrix; several get Tiles where their `tile` says (halves, quadrants, columns, then the Overflow Tile), each in its Project Color and animated by its Session State; a Takeover takes the whole matrix in either Layout. Layout tests assert on regions (`isFilled(frame, kTopLeft, kGreen)`, `isLitIn`, `column(i)`); multi-session tests about the Aggregate State use the `AggregateDevice8x8` fixture.

Escalation (#9) lives in the Device core: per session, from `state_entered` (related to the Device clock through `host_time`) or the latest Acknowledge. Its thresholds are a `device::Escalation` constructor parameter, with defaults until they become Preferences. Escalation tests send `heartbeat(clock)`, whose Host time tracks the fake clock, and compare with frames of a fresh Device (`awaitingInputFor(ms)`) or watch a stretch of frames (`watch`: pulses, brightness, colors).

The terminal Virtual Device renders every 100 ms even without input, so Disconnected shows 5 s after the Host goes silent:

```sh
(cat protocol/fixtures/{hello,session-upsert}.jsonl; sleep 6; cat protocol/fixtures/heartbeat.jsonl) | device/build/virtual-device
```

Demo: pipe the fixtures into the terminal Virtual Device. The matrix goes dark → Awaiting Input → dark:

```sh
make -C device && cat protocol/fixtures/{hello,session-upsert,heartbeat,session-remove}.jsonl | device/build/virtual-device
```

To see every Session State win the Aggregate State in turn (Idle → Working → Done → Error → Awaiting Input):

```sh
for s in idle working done error awaiting_input; do echo "{\"type\":\"session_upsert\",\"id\":\"$s\",\"state\":\"$s\"}"; done | device/build/virtual-device --layout aggregate
```

The same sessions in the adaptive Layout, one column each:

```sh
i=0; for s in idle working done error awaiting_input; do echo "{\"type\":\"session_upsert\",\"id\":\"$s\",\"state\":\"$s\",\"project_color\":\"#3a86ff\",\"tile\":$i}"; i=$((i+1)); done | device/build/virtual-device
```

## Agent skills

### Issue tracker

Issues and specs are tracked in GitHub Issues on `codino-labs/codino` (via `gh`). See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
