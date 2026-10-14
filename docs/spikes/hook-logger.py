#!/usr/bin/env python3
"""Throwaway Claude Code hook logger (hook spike, issue #2).

Wire it to every hook event (see hook-logger-settings.json). Each call appends
one JSON line to $HOOK_LOG (default: ./hook-log.jsonl) with:

  ts         wall-clock seconds (float) when the hook process started
  event      hook_event_name from the payload
  hook_pid   this process; ppid and ancestry = the process chain above it,
             used to find the Claude Code process (the liveness handle)
  env        names of CLAUDE* env vars, values only for a safe allowlist
  input      the stdin JSON exactly as Claude Code sent it

It never blocks or fails the hook: it prints nothing and exits 0.
"""
import json
import os
import sys
import time

SAFE_ENV_VALUES = {"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_PROJECT_DIR",
                   "CLAUDE_EFFORT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_PID",
                   "CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_CHILD_SESSION"}


def proc_info(pid):
    try:
        with open(f"/proc/{pid}/stat") as f:
            stat = f.read()
        # comm is in parentheses and may contain spaces
        comm = stat[stat.index("(") + 1:stat.rindex(")")]
        ppid = int(stat[stat.rindex(")") + 2:].split()[1])
        with open(f"/proc/{pid}/cmdline", "rb") as f:
            argv = f.read().split(b"\0")[:3]
        return {"pid": pid, "comm": comm,
                "argv0": [a.decode(errors="replace")[:80] for a in argv if a]}, ppid
    except (OSError, ValueError):
        return None, 0


def ancestry(pid, depth=6):
    chain = []
    while pid > 1 and len(chain) < depth:
        info, pid = proc_info(pid)
        if info is None:
            break
        chain.append(info)
    return chain


def main():
    ts = time.time()
    raw = sys.stdin.read()
    try:
        payload = json.loads(raw)
    except ValueError:
        payload = {"_unparsed": raw}
    env = {k: (os.environ[k] if k in SAFE_ENV_VALUES else "<set>")
           for k in sorted(os.environ) if k.startswith("CLAUDE")}
    record = {
        "ts": ts,
        "event": payload.get("hook_event_name"),
        "hook_pid": os.getpid(),
        "ppid": os.getppid(),
        "ancestry": ancestry(os.getppid()),
        "env": env,
        "input": payload,
    }
    path = os.environ.get("HOOK_LOG", "hook-log.jsonl")
    with open(path, "a") as f:
        f.write(json.dumps(record) + "\n")


if __name__ == "__main__":
    try:
        main()
    except Exception:  # never break the session under test
        pass
    sys.exit(0)
