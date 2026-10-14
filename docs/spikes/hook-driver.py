#!/usr/bin/env python3
"""Drive a Claude Code session with the logging hooks installed (hook spike, issue #2).

Runs `claude` either in print mode (p) or interactively inside a pseudo-terminal (i),
and steps through a script that waits on events appearing in the hook log and sends
keystrokes or signals. Linux/macOS only (uses pty).

usage: SPIKE_DIR=/some/scratch hook-driver.py <name> <p|i> '<steps-json>' -- <claude args...>

  $SPIKE_DIR/proj   the scratch project Claude runs in (must exist, keep it outside any repo)
  $SPIKE_DIR/logs   <name>.jsonl (hook log) and <name>.screen (raw terminal output)
  $HOOK_SETTINGS    settings file to pass with --settings
                    (default: hook-logger-settings.json; hook-logger-settings-async.json
                    makes every hook async, as the plugin will)

steps (JSON list; keys are plain JSON strings, e.g. "\\r" Enter, "\\u001b" Esc, "\\u0003" Ctrl-C):
  ["wait", EVENT, timeout]          until EVENT appears in the hook log
  ["waitn", EVENT, count, timeout]  until EVENT appears count times
  ["waittext", "text", timeout]     until text appears in the terminal output
  ["send", "keys"]                  type keys
  ["sleep", seconds]
  ["signal", "INT"|"TERM"|"KILL"]   signal the claude process
  ["waitexit", timeout]

The first time Claude runs in a new folder it asks for workspace trust; the default
answer is "No, exit", so start interactive scripts with
  ["sleep",4],["send","\\u001b[B"],["sleep",0.5],["send","\\r"]
Example (plain turn):
  hook-driver.py plain i '[["sleep",4],["wait","Stop",60],["send","/exit"],["sleep",1],
                           ["send","\\r"],["waitexit",15]]' -- --model haiku "Reply with just ok."
"""
import json, os, pty, select, signal, subprocess, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
SPIKE = os.environ.get("SPIKE_DIR", os.getcwd())
SETTINGS = os.environ.get("HOOK_SETTINGS", f"{HERE}/hook-logger-settings.json")
if len(sys.argv) < 5 or "--" not in sys.argv or sys.argv[2] not in ("p", "i"):
    sys.exit(__doc__)
name, mode, steps = sys.argv[1], sys.argv[2], json.loads(sys.argv[3])
args = sys.argv[sys.argv.index("--") + 1:]
log = f"{SPIKE}/logs/{name}.jsonl"
screen = f"{SPIKE}/logs/{name}.screen"
for path in (log, screen):
    if os.path.exists(path):
        os.remove(path)
# drop CLAUDE* vars inherited from an outer Claude Code session
clean = {k: v for k, v in os.environ.items() if not k.startswith("CLAUDE")}
env = dict(clean, HOOK_LOGGER=f"{HERE}/hook-logger.py", HOOK_LOG=log)
cmd = ["claude"] + args + ["--settings", SETTINGS]
cwd = f"{SPIKE}/proj"


def events():
    """Event names logged so far; a line still being written is skipped."""
    try:
        with open(log) as f:
            return [json.loads(line)["event"] for line in f if line.endswith("\n")]
    except FileNotFoundError:
        return []


if mode == "p":
    t0 = time.time()
    try:
        r = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=300)
        open(screen, "w").write(r.stdout + "\n--stderr--\n" + r.stderr)
        print("rc", r.returncode, "secs", round(time.time() - t0, 1))
    except subprocess.TimeoutExpired:
        print("TIMEOUT after 300 s")
    print(" ".join(events()))
    sys.exit(0)

child, fd = pty.fork()
if child == 0:
    os.chdir(cwd)
    os.execvpe(cmd[0], cmd, env)

import fcntl, struct, termios
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 50, 160, 0, 0))
out = open(screen, "wb")
terminal_output = bytearray()
exited = False


def pump(seconds):
    """Read the child's terminal output for a while, answering terminal queries."""
    global exited
    end = time.time() + seconds
    while time.time() < end and not exited:
        readable, _, _ = select.select([fd], [], [], 0.1)
        if readable:
            try:
                chunk = os.read(fd, 65536)
            except OSError:
                chunk = b""
            if not chunk:
                exited = True
                break
            out.write(chunk)
            terminal_output.extend(chunk)
            if b"\x1b[6n" in chunk:  # cursor position query
                os.write(fd, b"\x1b[1;1R")
            if b"\x1b[c" in chunk:  # device attributes query
                os.write(fd, b"\x1b[?62c")
        if os.waitpid(child, os.WNOHANG)[0]:
            exited = True


def wait_until(cond, timeout, what):
    end = time.time() + timeout
    while time.time() < end and not exited:
        if cond():
            return
        pump(0.2)
    if not cond():
        print("TIMEOUT waiting for", what, "events:", " ".join(events()))


pump(1)
for step in steps:
    kind, *params = step
    print("step", step, flush=True)
    if kind == "wait":
        event, timeout = params
        wait_until(lambda: event in events(), timeout, event)
    elif kind == "waitn":
        event, count, timeout = params
        wait_until(lambda: events().count(event) >= count, timeout, step)
    elif kind == "waittext":
        text, timeout = params
        wait_until(lambda: text.encode() in terminal_output, timeout, text)
    elif kind == "send":
        os.write(fd, params[0].encode())
        pump(0.3)
    elif kind == "sleep":
        pump(params[0])
    elif kind == "signal":
        os.kill(child, getattr(signal, "SIG" + params[0]))
        pump(0.5)
    elif kind == "waitexit":
        wait_until(lambda: exited, params[0], "exit")
    else:
        sys.exit(f"unknown step {step}")
pump(2)
if not exited:
    print("still running; killing with SIGTERM")
    os.kill(child, signal.SIGTERM)
    pump(3)
    if not exited:
        os.kill(child, signal.SIGKILL)
print("events:", " ".join(events()))
