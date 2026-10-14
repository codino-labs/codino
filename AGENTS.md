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
- **Test at the seam.** Feed protocol lines (prefer the fixtures) and fake clock ticks; assert on `render()`'s pixel buffer with semantic checks ("the whole matrix is one lit color", "same frame as a lone Awaiting Input session"), not golden frames or private state. No test sleeps.
- **Names** follow `CONTEXT.md` (`SessionState::AwaitingInput`, `aggregateState`, `HardwareProfile`). The product name stays out of identifiers: the namespace is `device`.

A change is done when this exits 0 (needs `cmake` and a C++17 compiler):

```sh
make -C device test
```

Run a single test with `device/build/device_core_test -tc="*Aggregate State*"`.

Demo: pipe the fixtures into the terminal Virtual Device. The matrix goes dark → Awaiting Input → dark:

```sh
make -C device && cat protocol/fixtures/{hello,session-upsert,heartbeat,session-remove}.jsonl | device/build/virtual-device
```

## Agent skills

### Issue tracker

Issues and specs are tracked in GitHub Issues on `codino-labs/codino` (via `gh`). See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
