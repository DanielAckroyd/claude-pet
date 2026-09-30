#!/usr/bin/env python3
"""Statusline for claude-pet. Never crashes.
  (no args)       the pet, the model, and context %
  --segment       only the pet, for splicing into your own statusline script
  --wrap 'CMD'    run your existing statusline CMD and put the pet in front of its output"""
import os, sys, json, subprocess

raw = ""
try:
    raw = sys.stdin.read()
    data = json.loads(raw)
except Exception:
    data = {}
data = data if isinstance(data, dict) else {}
args = sys.argv[1:]

pet = ""
try:
    sys.path.insert(0, os.path.dirname(os.path.realpath(__file__)))
    import petlib
    pet = petlib.segment(data, {"good": "\033[38;5;42m", "meh": "\033[38;5;179m",
                                "bad": "\033[38;5;203m", "reset": "\033[0m"}) or ""
except Exception:
    pass

out = pet
if "--wrap" in args:
    theirs = ""
    try:
        cmd = args[args.index("--wrap") + 1]
        theirs = subprocess.run(cmd, shell=True, input=raw, capture_output=True, text=True, timeout=10).stdout
    except Exception:
        pass
    theirs = theirs.rstrip("\n")
    if pet and theirs:
        out = f"{pet} {theirs}"
    else:
        out = pet or theirs
elif "--segment" not in args:
    segs = [pet] if pet else []
    try:
        model = (data.get("model") or {}).get("display_name")
        if model:
            segs.append(model)
        pct = (data.get("context_window") or {}).get("used_percentage")
        if pct is not None:
            segs.append(f"ctx {round(float(pct))}%")
    except Exception:
        pass
    out = " │ ".join(segs)
sys.stdout.write(out)
