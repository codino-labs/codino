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
cmd/codino/          the one binary: CLI (`event`, `show`, `get`/`set`/`unset`, `status`, `theme`, `ui`), the Host Daemon (`daemon`) and `hook` (internal, run by the plugin)
internal/claudecode/ Claude Code Adapter: hook JSON → host.Events; transcript probe (interrupts, last write)
internal/host/       Host core: Session Tracker, Aggregate State, Done decay, Acknowledge, Device Link driving, heartbeat, exit grace, stuck-session cleanup; clock and probes injected
internal/gitrepo/    repo root of a project directory (worktrees share the main repo's), read from git's files without running git
internal/preferences/ Preferences Store: the config file (a TOML subset, parsed and edited line by line), validation, get/set/unset
internal/picture/    PNG/JPEG/GIF → Raw Mode frames for a matrix size (fit, box filter, GIF disposal and delays), stdlib only
internal/liveness/   liveness probe: is the Agent's pid still its process (signal 0; /proc start time on Linux)
internal/devicelink/ handshake and claim; Discoverer seam (VirtualDevice now, USB serial later)
internal/daemon/     local Unix socket: server, client (`Client.Deliver` spawns the daemon on demand)
internal/webui/      the web UI (`codino ui`): HTTP server on 127.0.0.1, the page and the browser Virtual Device in assets/ (embedded)
internal/protocol/   wire messages, as in protocol/README.md
```

Every Agent's signals become `host.Event`s delivered with `daemon.Client.Deliver`: `codino event` does it from flags, `codino hook` from hook JSON through the Claude Code Adapter. Events carry the time the Agent signalled them (`At`); the Session Tracker drops one older than the last applied to its session, because async hooks race. Host core tests (`internal/host`) drive events and fake clock ticks in and assert on the protocol lines a fake Device receives, including the shared fixtures; `claudecode_replay_test.go` replays the hook spike recordings in `fixtures/hooks/claude-code/` through the Adapter.

Stuck sessions (#8): once a second the Host probes, outside its lock, the liveness of every session with a pid (`LivenessProbe`) and the transcript of every Working/Awaiting Input session (`TranscriptProbe`). A session whose Agent is gone is removed, as if it had ended (kill -9 fires no SessionEnd); an interrupted turn goes back to Idle; a Working session with no hook and no transcript write for `host.StaleAfter` (15 min) falls back to Idle. The hook reads the Agent's start time (`liveness.Started`) while the Agent is certainly alive, so a reused pid reads as gone. A session without a pid is never reaped, only judged stale. Host core tests fake both probes (`fakeLiveness`, `fakeTranscripts`, `quietTranscript`). The product name stays in `cmd/codino` (runtime directory name, `CODINO_VIRTUAL_DEVICE`, `CODINO_EXIT_GRACE`).

Done decay and Acknowledge (#9): the Session Tracker returns a session Done for the Done decay time (the `done_decay` Preference, 10 min by default) to Idle on a Tick. An event with `Acknowledge` (a prompt the human typed, `codino event --acknowledge`) clears that session's Done and restarts its Escalation; an `acknowledge` from the Device (`devicelink.Device.Acknowledged`) does it for every session. Escalation itself is the Device's (ADR-0001): the Host only tells it when a waiting session was Acknowledged, and replays that after the upserts when it claims a Device. `internal/host/acknowledge_test.go` drives the Device side through the fake Device's `toHost`.

Layout and Project Colors (#10): the Host's Project Color Allocator (`project_color.go`) gives a session a pinned color (the `project_colors` Preferences), else the color of an active session of the same repo root (a session without a project is a project of its own), else the hue of `host.ProjectColors` its repo root hashes to (FNV-1a), bumped to the next hue no active session holds. A session keeps it for its lifetime. Repo roots come from the injected `host.ProjectRootProbe` (`gitrepo.Roots` in the daemon; tests use `fakeRoots`), resolved outside the Host's lock. Tile slots (`assignTiles`) follow the adaptive Layout's room (1, 2, 4, 8, then 7 plus the Overflow Tile from slot 7): a session keeps a slot that fits; newcomers and sessions that no longer fit take the first free slots by session start time, and the Host upserts every session that moved. `project_color_test.go` and `tile_test.go` assert on the upserts the fake Device receives.

Preferences (#11): the config file is canonical: `$CODINO_CONFIG`, else `$XDG_CONFIG_HOME/codino/config.toml`, else `~/.config/codino/config.toml`; no file means the defaults. `internal/preferences` reads it as a small TOML subset (strings, integers, booleans, tables, dotted and quoted keys, comments) with a hand-written line parser rather than a TOML module, because `codino set` edits it line by line and must keep the user's comments and layout, which no round-tripping library does; it validates every value (an unknown key is an error, so a typo is reported), refuses an edit that leaves the file invalid, writes atomically (temp file + rename) and through a symlink (dotfiles). The Host takes a `host.PreferencesSource` (`host.WithPreferences`; tests use `fakeConfig`), reads it when created and every `PreferencesPollInterval` (1 s) outside its lock, ignores an invalid file (logged once per problem, shown by `codino status`), and keeps the Done decay, the pinned Project Colors (pinning recolors the running sessions of that repo root) and the Disconnected timeout (for Raw Mode's re-entry). The rest goes to the Device in a `preferences` push after every `hello` and on every change. The CLI resolves `project_colors.PATH` to the repo root of PATH (`.` works). `/codino` is the plugin's skill (`plugin/skills/codino/SKILL.md`), which calls `codino get/set/unset` (on the Bash tool's `PATH` from the plugin's `bin/`). The staleness threshold (`host.StaleAfter`) and the exit grace stay in code: they aren't Preferences in the spec. `preferences_test.go` in both packages.

Raw Mode (#16): `codino show IMAGE` sends the file to the Host Daemon (`daemon.Client.Show`, op `show`) on a connection it keeps open until Ctrl-C; the daemon holds `host.Show`'s `*Showing` until that connection closes, a newer show replaces it, or the daemon stops, then `Stop` sends `raw_mode_exit`. The Host scales the picture to the claimed Device's matrix (`picture.Frames`, cached per size and rendered ahead of `h.mu` for the claimed or claiming Device; only a claim racing a new show scales under it), re-enters Raw Mode on every claim and after a silence the Device took for Disconnected, sends the frame due on each Tick and again with each heartbeat (the daemon ticks every 10 ms during an animation), and a show holds the exit grace. The binary frame is `protocol.RawFrame` (one line: `0xFF`, RGB, `\n`; no pixel byte is a newline). `show_test.go` asserts on the bytes the fake Device receives, `raw-frame.bin` included.

Web UI (#15): `codino ui` asks the Host Daemon (op `ui`, spawning it if needed) to open its web UI and opens the URL it returns in the browser (`--no-browser` prints it), then exits. The daemon's `webui.Launcher` serves `webui.Server` on a free port of 127.0.0.1 and holds the Host open (`host.Hold`) until the page has gone: no stream open and no heartbeat (the page sends one every 5 s) for `webui.KeepAlive`, nor an unused URL younger than `webui.FirstVisitWait`; then it stops the server, ends the preview and releases the hold. Only the human who ran `codino ui` gets in: the URL carries a one-time ticket (a browser's command line is readable by other local users) that `/open` swaps for an HttpOnly, SameSite=Strict cookie; every request must name the server's own `127.0.0.1:PORT` as Host (DNS rebinding), and a POST must be JSON from the page's origin (`Origin`, `Sec-Fetch-Site`). Preferences go through `preferences.File.Set/Unset`, as `codino set`, then `Host.ReloadPreferences` pushes them at once. The preview is the Device core built for the browser (`assets/device.wasm`, below) fed what the Device is sent: `host.Watch` returns a `Watcher` that starts with a Device of its own (the claimed Device's Hardware Profile, else `host.PreviewProfile`), the Host's copies of the installed Themes and Sprite Packs, a `hello` and the claim's greeting, then mirrors every line, heartbeats included even without a Device; a claim starts it over. The Device's flash can't be read back, so the daemon keeps a copy of everything it installs (`host.ThemeCopies`, in `<user cache dir>/codino/themes/`). `/api/stream` sends it as Server-Sent Events (`device`, `line`, `frame`). The preview controls send `preview_enter`/`preview_exit` (`host.Preview`, `EndPreview`; replayed on claim and after a silence). Tests: `internal/webui` (HTTP seam with a real Host and a Watcher capturing the protocol lines, a fake clock for the keep-alive), `internal/host/preview_test.go`, `watch_test.go`.

### Demo: `codino event` drives the Virtual Device

Real USB discovery isn't built yet, so the Host Daemon drives the terminal Virtual Device: it spawns it as a subprocess and speaks the protocol on its stdin/stdout. The matrix goes to the daemon's stderr. Run the daemon in the foreground in one terminal to watch the matrix:

```sh
make -C device && go build -o codino ./cmd/codino
./codino daemon --virtual-device device/build/virtual-device
```

and send events, or a picture, from another:

```sh
./codino event --session a --state working --project .
./codino event --session b --state awaiting_input
./codino status
./codino event --session b --acknowledge   # restarts b's Escalation
./codino event --session b --end
./codino show some.gif                     # Raw Mode until Ctrl-C; Awaiting Input shows over it
./codino set layout aggregate              # the daemon pushes it within a second
./codino set brightness 30; ./codino get
./codino ui                                # the web UI: Preferences, Themes, preview each Session State
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

`plugin/` is the Claude Code plugin: `.claude-plugin/plugin.json`, the `/codino` skill (`skills/codino/SKILL.md`) and `hooks/hooks.json`, where every hook is `async` and runs `"${CLAUDE_PLUGIN_ROOT}/bin/codino" hook`. `codino hook` reads the hook JSON on stdin, forwards it (spawning the Host Daemon unless the session is ending) and always exits 0. Nothing is ever written into the plugin root, which changes on every update: persistent Host state belongs in `$CLAUDE_PLUGIN_DATA`, runtime files in the runtime directory above. The hook→Session State mapping is documented on `claudecode.Events`.

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
                     (theme.*: the Theme interpreter; device_core_themes.cpp: install, uninstall, selecting;
                     builtin_themes.cpp: the built-in Themes as data)
  test/              native Device core tests (doctest); golden/: Theme snapshot golden frames
  virtual/terminal/  terminal Virtual Device (stdin/stdout protocol, matrix on stderr)
  virtual/browser/   browser Virtual Device: the core as wasm32-wasi with a C ABI, for the web UI
  third_party/       vendored single headers (ArduinoJson, doctest), see its README
```

- **The Device core stays portable.** C++17 and the standard library plus ArduinoJson only: no Arduino core, FastLED or OS calls, because the same code builds for the firmware, the terminal and the browser. Hardware, time and I/O come in through the constructor (`HardwareProfile`, `Clock`) or `receive()`.
- **Test at the seam.** Feed protocol lines (prefer the fixtures), `acknowledge()` presses and fake clock ticks; assert on `render()`'s pixel buffer with semantic checks ("the whole matrix is one lit color", "same frame as a lone Awaiting Input session"), not golden frames or private state. No test sleeps. The one exception is `theme_snapshot_test.cpp`: golden frames of every built-in Theme × Session State (whole matrix 8×8 and 16×16, and a Tile) in `device/test/golden/`. When a change is meant to change how a built-in Theme looks, regenerate them with `make -C device update-goldens` and review the diff; never to make a failing test pass otherwise.
- **Names** follow `CONTEXT.md` (`SessionState::AwaitingInput`, `aggregateState`, `HardwareProfile`). The product name stays out of identifiers: the namespace is `device`.

A change is done when this exits 0 (needs `cmake` and a C++17 compiler):

```sh
make -C device test
```

Run a single test with `device/build/device_core_test -tc="*Aggregate State*"`.

The Layout (#10) is a Preference (`device::Preferences::layout`), adaptive by default; the terminal Virtual Device boots with `--layout aggregate` until a push says otherwise. One session, or the Aggregate Layout, shows the Aggregate State on the whole matrix; several get Tiles where their `tile` says (halves, quadrants, columns, then the Overflow Tile), each in its Project Color and animated by its Session State; a Takeover takes the whole matrix in either Layout. Layout tests assert on regions (`isFilled(frame, kTopLeft, kGreen)`, `isLitIn`, `column(i)`); multi-session tests about the Aggregate State use the `AggregateDevice8x8` fixture.

Raw Mode (#16): `raw_mode_enter`, the binary frame (`DeviceCore::kRawFrameStart`, then RGB; `receive()` takes it like any line) and `raw_mode_exit`. Any session Awaiting Input pre-empts the frame (normal rendering, Escalation and Takeover included) and the frame shows again after; Disconnected and `hello` end Raw Mode. Demo, a frame pre-empted by Awaiting Input and back:

```sh
(cat protocol/fixtures/{raw-mode-enter.jsonl,raw-frame.bin}; sleep 1; echo '{"type":"session_upsert","id":"a","state":"awaiting_input"}'; sleep 2; echo '{"type":"session_remove","id":"a"}'; sleep 1) | device/build/virtual-device
```

Escalation (#9) lives in the Device core: per session, from `state_entered` (related to the Device clock through `host_time`) or the latest Acknowledge. Its thresholds are Preferences (`device::Preferences::escalation`). Escalation tests send `heartbeat(clock)`, whose Host time tracks the fake clock, and compare with frames of a fresh Device (`awaitingInputFor(ms)`) or watch a stretch of frames (`watch`: pulses, brightness, colors).

Preferences (#11): `device::Preferences` (constructor defaults) and the `preferences` push, which replaces them whole (a missing field is the default; an invalid one voids the message). `render()` lays the frame out in the rotated shape, then `present()` turns it onto the matrix and scales every channel to `brightnessCap × brightness / 100` (a lit channel stays ≥ 1): the cap holds for every state, Escalation step, Takeover, Disconnected and Raw Mode frame. Quiet Mode darkens all but Awaiting Input; Escalation, Takeover, Disconnected and Raw Mode are unchanged. Caching is the shell's: the core takes a `device::PreferencesCache*`, loads it once in the constructor (boot) and `save()`s the canonical `preferences` line when a push changes them; the terminal Virtual Device persists it with `--preferences-file PATH` (the daemon passes one in the runtime directory). Tests: the `// --- Preferences` sections of `device_core_test.cpp` (`FakeCache`, `brightestUnder`, `cappedProfile`).

Themes (#12): per-state appearance is data, interpreted by the Device core (`theme.h`; format in `protocol/themes.md`). The `theme` Preference names one: the built-in `default` (the Character Theme with Nino, the original mascot, frames in the built-in Sprite Pack `nino`) or `minimal` (abstract), or one installed with an `install` message (`codino theme install FILE`). A pose shows only on the whole matrix; Tiles, and a pose whose Sprite Pack is missing, use the abstract Theme's effect for the state. Escalation dims what the Theme draws for Awaiting Input; Takeover, Quiet Mode, Disconnected, Raw Mode, the cap and rotation are the Theme's frame. Storage is the shell's: the core takes a `device::ThemeStore*` (RAM-only `MemoryThemeStore` by default) and lists, loads, saves and erases one `install` line per Theme or Sprite Pack; the terminal Virtual Device persists them with `--store-dir DIR` (the daemon passes one in the runtime directory). On the Host, `host.Install`/`Uninstall` send one at a time and wait for the Device's `install_reply` without `h.mu`; `codino theme list|install|uninstall` go through the daemon (ops `install`, `uninstall`) and wait for it to claim a Device. Most `device_core_test.cpp` tests render with `minimal` (`abstractTheme()`), whose fills read as Session States; `theme_test.cpp` covers the Character Theme, installing and the effects. Clawd is never shipped (ADR 0003): `protocol/themes.md` tells users how to load a pack they make, and `themes/examples/` (tested to install) is the starting point.

Preview (#15): `preview_enter` shows a lone session in a Session State (escalating from its `elapsed_ms`) in place of the sessions and Raw Mode, until `preview_exit`, Disconnected or `hello`; the sessions are swapped out while rendering (`previewed_`), not lost. Tests: `// --- Preview` in `device_core_test.cpp`.

Browser build (#15): `make -C device web` builds the core with a pinned wasi-sdk (fetched once into `~/.cache/codino-toolchain`, SHA-256 checked; nothing system-wide) into `internal/webui/assets/device.wasm`, with the hashes of its sources in `device.wasm.sources`. Both are committed, so building the Go binary needs no C++ toolchain. After changing anything in `device/core/`, run `make -C device web` and commit the result: `go test ./internal/webui` fails while the committed build is stale.

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
