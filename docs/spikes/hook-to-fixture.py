#!/usr/bin/env python3
"""Turn a hook-logger.py log into a replay fixture (hook spike, issue #2).

usage: hook-to-fixture.py <log.jsonl> <out.jsonl> <scenario> <description> <how> [note]

The fixture's first line is {"meta": {...}}; every other line is one hook call:
  {"t": seconds since the first hook, "event": ..., "liveness": {...}, "input": {...}}
Machine-specific paths are rewritten to stable placeholders.
Run it right after recording: claude_code_version is read from `claude --version` now.
"""
import json
import os
import re
import subprocess
import sys
import time

log, out, scenario, description, how = sys.argv[1:6]
note = sys.argv[6] if len(sys.argv) > 6 else (
    "hooks ran synchronously (not async), so lines are in the order Claude Code fired them")
version = subprocess.run(["claude", "--version"], capture_output=True, text=True).stdout.split()[0]
home = os.path.expanduser("~")
records = [json.loads(line) for line in open(log) if line.strip()]
project_dir = records[0]["env"].get("CLAUDE_PROJECT_DIR", "")
project_slug = project_dir.replace("/", "-").replace("_", "-").replace(".", "-")


def scrub(s):
    if project_dir:
        s = s.replace(project_slug, "-tmp-spike-proj")
        s = s.replace(project_dir, "/tmp/spike/proj")
        s = s.replace(os.path.dirname(project_dir), "/tmp/spike")
    s = s.replace(f"/tmp/claude-{os.getuid()}/", "/tmp/claude-uid/")  # Claude Code's Linux temp dir
    s = s.replace(home, "/home/user")
    user = os.environ.get("USER")
    return re.sub(rf"\b{re.escape(user)}\b", "user", s) if user else s


lines = [{"meta": {
    "scenario": scenario,
    "description": description,
    "how": how,
    "claude_code_version": version,
    "recorded": time.strftime("%Y-%m-%d", time.localtime(records[0]["ts"])),
    "os": sys.platform,
    "note": note,
}}]
t0 = records[0]["ts"]
for r in records:
    lines.append({
        "t": round(r["ts"] - t0, 3),
        "event": r["event"],
        "liveness": {
            "CLAUDE_PID": int(r["env"]["CLAUDE_PID"]) if "CLAUDE_PID" in r["env"] else None,
            "hook_ppid_chain": [a["comm"] for a in r["ancestry"][:2]],
            "grandparent_is_claude_pid": len(r["ancestry"]) > 1
                and str(r["ancestry"][1]["pid"]) == r["env"].get("CLAUDE_PID"),
        },
        "input": r["input"],
    })
with open(out, "w") as f:
    for line in lines:
        f.write(scrub(json.dumps(line)) + "\n")
