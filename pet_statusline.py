#!/usr/bin/env python3
"""Minimal standalone statusline: the pet, the model, and context %. Never crashes.
With --segment it prints only the pet, for splicing into a bash/node/whatever statusline."""
import os, sys, json

try:
    data = json.load(sys.stdin)
except Exception:
    data = {}
data = data if isinstance(data, dict) else {}

segs = []
only_pet = "--segment" in sys.argv[1:]
try:
    sys.path.insert(0, os.path.dirname(os.path.realpath(__file__)))
    import petlib
    pet = petlib.segment(data, {"good": "\033[38;5;42m", "meh": "\033[38;5;179m",
                                "bad": "\033[38;5;203m", "reset": "\033[0m"})
    if pet:
        segs.append(pet)
except Exception:
    pass
if not only_pet:
    try:
        model = (data.get("model") or {}).get("display_name")
        if model:
            segs.append(model)
        pct = (data.get("context_window") or {}).get("used_percentage")
        if pct is not None:
            segs.append(f"ctx {round(float(pct))}%")
    except Exception:
        pass
sys.stdout.write(" │ ".join(segs))
