# Codino

Codino is a small USB LED matrix for your desk. It shows the state of every Claude Code session on the computer it is plugged into: Idle, Working, Awaiting Input, Done or Error. When a session sits blocked on a permission prompt or a question, you see it without switching windows. Works with Claude Code on Linux, macOS and Windows.

## Install

Four steps: buy a board, flash it, plug it in, install the plugin. There is nothing to configure.

### 1. Buy

The reference board is the **Waveshare ESP32-S3-Matrix**. It has an 8×8 RGB LED matrix built in, so there is no soldering and no wiring. Any ESP32-S3 board with native USB also works, wired to a separate 8×8 or 16×16 WS2812B panel with three Dupont wires (5V, GND, data).

Use a USB cable that carries data, not a charge-only one.

### 2. Flash

You flash the firmware from a web page in Chrome or Edge, with no toolchain to install. Choose your board's Hardware Profile there; the reference board's is preselected.

> **Coming soon.** The web flasher isn't published yet. This section will link to it when it is.

### 3. Plug in

Plug the board into any USB port. The Host finds it on its own, and only a board that answers Codino's handshake is claimed. Another board that doesn't answer the handshake is left alone until you replug it, or for a minute.

- **Linux**: your user needs permission to open the board's serial port. See [Linux: serial port permissions](#linux-serial-port-permissions).
- **macOS**: nothing to do. The board shows up as `/dev/cu.usbmodem…`.
- **Windows** (10 or later): nothing to do. Windows' built-in USB serial driver handles the board, which shows up as a COM port.

### 4. Install the Claude Code plugin

The plugin carries everything else: the hooks, the `codino` command and the Host Daemon, which hooks start on their own.

> **Coming soon.** Installing straight from GitHub needs the prebuilt binaries, which ship with the first release. Until then, install from a clone, as described below.

From a clone of this repository (this needs Go and `make`):

```sh
git clone https://github.com/codino-labs/codino.git
cd codino
make plugin-all                                # builds bin/codino-<os>-<arch> for every OS
claude plugin marketplace add "$PWD"
claude plugin install codino@codino
```

Start a Claude Code session and the Device lights up. Run `/codino` inside Claude Code to see its status or change Preferences.

**Windows:** Claude Code runs the plugin's hooks through Git Bash, so install [Git for Windows](https://git-scm.com/download/win). Without it, Claude Code falls back to PowerShell, which can't run the plugin's launcher, and the Device stays dark.

## Linux: serial port permissions

On most distributions, serial ports belong to the `dialout` group (`uucp` on Arch). Your user is often not in it. Pick one of the following fixes.

**Install the udev rule (recommended).** It gives the logged-in user access to the board's port without a group, and it stops ModemManager from probing the board:

```sh
sudo cp packaging/linux/70-codino.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

Then unplug the board and plug it back in.

**Or join the group,** then log out and back in:

```sh
sudo usermod -aG dialout "$USER"    # Arch: uucp
```

If the port isn't readable, `codino status` (or `/codino` in Claude Code) says so: `Device: not connected: open /dev/ttyACM0: permission denied (…)`.

## Where Codino keeps its files

| What | Where |
|---|---|
| Preferences (your config file) | `$CODINO_CONFIG`, else `$XDG_CONFIG_HOME/codino/config.toml`, else `~/.config/codino/config.toml` (on Windows, `%AppData%\codino\config.toml`) |
| Host Daemon socket, lock and log | Linux: `$XDG_RUNTIME_DIR/codino/`. macOS: `$TMPDIR/codino-<uid>/`. Both are owner-only. Windows: `%LocalAppData%\codino\run\`, and the socket is a named pipe that only your user can open |
| Host state (copies of installed Themes) | The plugin's data directory, `~/.claude/plugins/data/codino-codino/`, or `<user cache dir>/codino/` for a Host Daemon started outside Claude Code |

Nothing is written into the plugin's own directory, which changes on every update.

## Try it without hardware

The Virtual Device is the Device core, built for your terminal. See *Demo* in [AGENTS.md](AGENTS.md).

## Contributing

[AGENTS.md](AGENTS.md) has the conventions, the layout and the done-checks. [CONTEXT.md](CONTEXT.md) holds the vocabulary, and [docs/adr/](docs/adr/) the decisions. The wire protocol is in [protocol/README.md](protocol/README.md).
