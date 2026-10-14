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

## Agent skills

### Issue tracker

Issues and specs are tracked in GitHub Issues on `codino-labs/codino` (via `gh`). See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
