#!/usr/bin/env python3
"""Drive a Claude Code session with the logging hooks installed (hook spike, issue #2).

Runs `claude` either in print mode (p) or interactively inside a pseudo-terminal (i),
and steps through a script that waits on events appearing in the hook log and sends
keystrokes or signals. Linux/macOS only (uses pty).

usage: SPIKE_DIR=/some/scratch hook-driver.py <name> <p|i> '<steps-json>' -- <claude args...>

  $SPIKE_DIR/proj   the scratch project Claude runs in (must exist, keep it outside any repo)
  $SPIKE_DIR/logs   <name>.jsonl (hook log) and <name>.screen (raw terminal output)

steps (JSON list):
  ["wait", EVENT, timeout]          until EVENT appears in the hook log
  ["waitn", EVENT, count, timeout]  until EVENT appears count times
  ["send", "text"]                  type text; backslash escapes allowed ("\\r", "\\x1b")
  ["sleep", seconds]
  ["signal", "INT"|"TERM"|"KILL"]   signal the claude process
  ["waitexit", timeout]

The first time Claude runs in a new folder it asks for workspace trust; the default
answer is "No, exit", so start interactive scripts with
  ["sleep",4],["send","\\x1b[B"],["sleep",0.5],["send","\\r"]
Example (plain turn):
  hook-driver.py plain i '[["sleep",4],["wait","Stop",60],["send","/exit"],["sleep",1],
                           ["send","\\r"],["waitexit",15]]' -- --model haiku "Reply with just ok."
"""
import json, os, pty, select, signal, subprocess, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
SPIKE = os.environ.get("SPIKE_DIR", os.getcwd())
name, mode, steps = sys.argv[1], sys.argv[2], json.loads(sys.argv[3])
args = sys.argv[sys.argv.index("--") + 1:]
log = f"{SPIKE}/logs/{name}.jsonl"
screen = f"{SPIKE}/logs/{name}.screen"
for p in (log, screen):
    if os.path.exists(p):
        os.remove(p)
# drop CLAUDE* vars inherited from an outer Claude Code session
clean = {k: v for k, v in os.environ.items() if not k.startswith("CLAUDE")}
env = dict(clean, HOOK_LOGGER=f"{HERE}/hook-logger.py", HOOK_LOG=log)
cmd = ["claude"] + args + ["--settings", f"{HERE}/hook-logger-settings.json"]
cwd = f"{SPIKE}/proj"


def events():
    try:
        with open(log) as f:
            return [json.loads(l)["event"] for l in f if l.strip()]
    except FileNotFoundError:
        return []


if mode == "p":
    t0 = time.time()
    r = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=300)
    open(screen, "w").write(r.stdout + "\n--stderr--\n" + r.stderr)
    print("rc", r.returncode, "secs", round(time.time() - t0, 1))
    print(" ".join(events()))
    sys.exit(0)

pid, fd = pty.fork()
if pid == 0:
    os.chdir(cwd)
    os.execvpe(cmd[0], cmd, env)

import fcntl, struct, termios
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 50, 160, 0, 0))
out = open(screen, "wb")
buf = bytearray()
exited = False


def pump(t):
    global exited
    end = time.time() + t
    while time.time() < end and not exited:
        r, _, _ = select.select([fd], [], [], 0.1)
        if r:
            try:
                d = os.read(fd, 65536)
            except OSError:
                d = b""
            if not d:
                exited = True
                break
            out.write(d); buf.extend(d)
            # answer terminal queries (cursor position / device attributes)
            if b"\x1b[6n" in d:
                os.write(fd, b"\x1b[1;1R")
            if b"\x1b[c" in d:
                os.write(fd, b"\x1b[?62c")
        wp, st = os.waitpid(pid, os.WNOHANG)
        if wp:
            exited = True


def wait_until(cond, timeout, what):
    end = time.time() + timeout
    while time.time() < end and not exited:
        if cond():
            return True
        pump(0.2)
    ok = cond()
    if not ok:
        print("TIMEOUT waiting for", what, "events:", " ".join(events()))
    return ok


pump(1)
for st in steps:
    k = st[0]
    print("step", st, flush=True)
    if k == "wait":
        wait_until(lambda: st[1] in events(), st[2], st[1])
    elif k == "waitn":
        wait_until(lambda: events().count(st[1]) >= st[2], st[3], st)
    elif k == "waittext":
        wait_until(lambda: st[1].encode() in buf, st[2], st[1])
    elif k == "send":
        os.write(fd, st[1].encode().decode("unicode_escape").encode())
        pump(0.3)
    elif k == "sleep":
        pump(st[1])
    elif k == "clearbuf":
        buf.clear()
    elif k == "signal":
        os.kill(pid, getattr(signal, "SIG" + st[1]))
        pump(0.5)
    elif k == "waitexit":
        end = time.time() + st[1]
        while not exited and time.time() < end:
            pump(0.2)
pump(2)
if not exited:
    print("still running; killing with SIGTERM")
    os.kill(pid, signal.SIGTERM)
    pump(3)
    if not exited:
        os.kill(pid, signal.SIGKILL)
print("events:", " ".join(events()))
