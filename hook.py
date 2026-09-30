#!/usr/bin/env python3
"""Pet hook for PreCompact and SessionEnd. Prints nothing, always exits 0."""
import os, sys, json, time

try:
    sys.path.insert(0, os.path.dirname(os.path.realpath(__file__)))
    import petlib as P
    if os.environ.get("PET_DISABLE") != "1":
        P.load_config()
        data = json.load(sys.stdin)
        name = data.get("hook_event_name")
        if name in ("PreCompact", "SessionEnd"):
            # async hook, so it can afford to wait out a statusline tick in another pane
            with P.lock(wait=2) as got:
                if got:
                    now = time.time()
                    events = []
                    s = P.load_or_hatch(now, events)
                    P.tick(s, now, events, hook=(name, data))
                    P.save_state(s)
                    P.append_events(events)
except BaseException:
    pass
sys.exit(0)
