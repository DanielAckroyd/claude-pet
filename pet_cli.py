#!/usr/bin/env python3
"""pet — stat sheet for the statusline pet.
  pet            stat sheet
  pet log [n]    recent events
  pet name NEW   rename
  pet tree       the evolution paths
  pet devolve    drop back a tier (costs xp, re-rolls the next branch; -y skips the prompt)
  pet glyphs     every form × mood × frame, for eyeballing terminal width
  pet --version
(No reset: delete ~/.claude/pet/state.json if you really mean it.)"""
import os, sys, time, datetime as dt

sys.path.insert(0, os.path.dirname(os.path.realpath(__file__)))
import petlib as P

RESET = "\033[0m"; DIM = "\033[2m"; BOLD = "\033[1m"
COL = {"good": "\033[38;5;42m", "meh": "\033[38;5;179m", "bad": "\033[38;5;203m", "reset": RESET}


def bar(v, n=20):
    full = int(round(v / 100 * n))
    col = COL["good"] if v >= 60 else COL["meh"] if v >= 30 else COL["bad"]
    return f"{col}{'█' * full}{DIM}{'░' * (n - full)}{RESET} {v:5.1f}"


def when(t):
    return dt.datetime.fromtimestamp(t).strftime("%a %d %b %H:%M")


def boxed(text, pad=4):
    w = P.width(text) + pad * 2
    return [f"╭{'─' * w}╮", f"│{' ' * pad}{text}{' ' * pad}│", f"╰{'─' * w}╯"]


def sheet():
    s = P.read_state()
    if not s:
        print("No pet yet — it hatches the first time a Claude statusline renders.")
        return
    now = time.time()
    st = s["stats"]
    xp, lvl = st["xp"], P.level(st["xp"])
    mk = P.mood(s, now)
    col = COL[P.MOODS[mk][3]]
    streak = P.current_streak(s, now)
    crown = P.CROWN if streak >= P.T["crown_streak"] else ""

    form = s.get("form") or "hatchling"
    look = s.get("look") or {}
    if look.get("shiny") and P.MOODS[mk][3] == "good":
        col = P.SHINY
    for ln in boxed(P.face(form, mk, eyes=look.get("eyes")) + (look.get("charm") or "") + crown):
        print(f"  {col}{ln}{RESET}")
    age = (dt.date.today() - dt.date.fromtimestamp(s["born_at"])).days
    shiny = f"{P.SHINY}shiny{RESET} " if look.get("shiny") else ""
    print(f"\n  {BOLD}{s['name']}{RESET} the {shiny}{form} · {col}{P.MOODS[mk][2]}{RESET} · {age}d old")
    bits = [f"{look.get('eyes', '•')} eyes"] + ([f"holding {look['charm']}"] if look.get("charm") else [])
    if s.get("devolved"):
        bits.append(f"devolved {s['devolved']}×")
    print(f"  {DIM}{' · '.join(bits)}{RESET}")
    lo, hi = P.xp_for_level(lvl), P.xp_for_level(lvl + 1)
    frac = (xp - lo) / (hi - lo) if hi > lo else 1
    print(f"  level {BOLD}{lvl}{RESET}  {COL['good']}{'▰' * int(frac * 20)}{DIM}{'▱' * (20 - int(frac * 20))}{RESET}"
          f"  {xp} xp ({hi - xp} to L{lvl + 1})\n")
    for k in ("fed", "fit", "rested"):
        print(f"  {k:<7}{bar(st[k])}")
    path_line(s, now, form)
    if s.get("wilt"):
        w, cap = s["wilt"], P.T["wilt_minutes"]
        print(f"  {COL['bad']}wilting {w:.0f}/{cap} work-min — devolves at {cap}; get the average back over "
              f"{P.T['wilt_recover']} to recover{RESET}")
    print(f"\n  streak  {streak} workday{'s' if streak != 1 else ''}{' ' + P.CROWN if crown else ''}")

    today = s["days"].get(dt.date.today().isoformat()) or {}
    print(f"  today   {today.get('commits', 0)} commits · {today.get('lines', 0)} lines · "
          f"{today.get('xp', 0)} xp · {today.get('auto_compacts', 0)} auto-compacts · "
          f"{today.get('clean_wraps', 0)} clean wraps")
    if s["days"]:
        bd, rec = max(s["days"].items(), key=lambda kv: kv[1].get("xp", 0))
        if rec.get("xp"):
            print(f"  best    {bd}: {rec['xp']} xp, {rec.get('commits', 0)} commits")

    evs = P.read_events(5)
    if evs:
        print(f"\n  {DIM}recent{RESET}")
        for e in evs:
            print(f"  {DIM}{when(e['ts'])}{RESET}  {e['msg']}")


def path_line(s, now, form):
    sc = P.trait_scores(s, now)
    lean = P.dominant_trait(s, now)
    traits = " · ".join(f"{t} {round(sc[t] * 100)}" for t in P.TRAITS)
    if form == "hatchling":
        lean = P.species_path(s, now)
        nxt = f"→ {P.SPECIES[lean]} at L{P.T['species_level']}"
    elif form in P.FINALS:
        nxt = f"→ {P.FINALS[form][lean]} at L{P.T['final_level']}"
    else:
        print(f"\n  path    {traits}  {DIM}final form{RESET}")
        return
    print(f"\n  path    {traits}  {DIM}leaning {lean} {nxt}{RESET}")


def log(n):
    for e in P.read_events(n):
        print(f"{DIM}{when(e['ts'])}{RESET}  {e['kind']:<13} {e['msg']}")


def rename(new):
    new = new.strip()
    if not new:
        sys.exit("pet name NEW — name can't be empty")
    with P.lock(wait=2) as got:
        if not got:
            sys.exit("pet is busy (lock held) — try again")
        now = time.time()
        events = []
        s = P.load_or_hatch(now, events)
        old = s["name"]
        s["name"] = new
        events.append(P._ev(now, "rename", f"{old} is now {new}"))
        P.save_state(s)
        P.append_events(events)
    print(f"{old} is now {new}.")


def devolve(assume_yes=False):
    s = P.read_state()
    if not s:
        sys.exit("No pet yet.")
    prev = P.devolve_preview(s)
    if not prev:
        sys.exit(f"{s['name']} is already a hatchling — nothing to devolve.")
    back, keep = prev
    xp = s["stats"]["xp"]
    print(f"{s['name']}: {s['form']} → {back}, L{P.level(xp)} → L{P.level(keep)} ({xp - keep} xp lost).")
    print("Its next evolution re-picks the branch from how you work then.")
    if not assume_yes:
        try:
            ok = input("Devolve? [y/N] ").strip().lower() == "y"
        except EOFError:
            ok = False
        if not ok:
            print("Left as is.")
            return
    with P.lock(wait=2) as got:
        if not got:
            sys.exit("pet is busy (lock held) — try again")
        now = time.time()
        events = []
        s = P.load_or_hatch(now, events)
        P.devolve(s, now, events, reason="chose to devolve")
        P.save_state(s)
        P.append_events(events)
    print(f"{s['name']} is a {s['form']} again: {P.face(s['form'], 'content', eyes=(s.get('look') or {}).get('eyes'))}")


def pad(text, n):
    return text + " " * max(0, n - P.width(text))


def tree():
    print(f"  {P.face('hatchling', 'content')} hatchling  (L1–{P.T['species_level'] - 1})")
    for trait, sp in P.SPECIES.items():
        hint = "  — hidden: all three traits high and even" if trait == "balanced" else ""
        print(f"   ├─ {trait:<8} {pad(P.face(sp, 'content'), 9)} {sp}  (L{P.T['species_level']}){hint}")
        for t2, fin in P.FINALS[sp].items():
            print(f"   │    └─ {t2:<8} {pad(P.face(fin, 'content'), 10)} {fin}  (L{P.T['final_level']})")


def glyphs():
    """The right-hand │ must line up on every row. Anything jutting out is a bad glyph."""
    rows = []
    for name in P.FORMS:
        for mk in P.MOODS:
            for blink in (False, True):
                f = P.face(name, mk, blink)
                rows.append((f"{name}/{mk}{'/blink' if blink else ''}", f))
        rows.append((f"{name}/crown", P.face(name, "thriving") + P.CROWN))
        for c in P.CAMEOS:
            rows.append((f"{name}/cameo", f"{P.face(name, 'content')} {c}"))
    for eye in dict.fromkeys(P.EYES):
        for name in ("hatchling", "bear", "great owl", "kitsune"):
            rows.append((f"eyes {eye}/{name}", P.face(name, "content", eyes=eye)))
    for charm in filter(None, dict.fromkeys(P.CHARMS)):
        rows.append((f"charm {charm}", P.face("tiger", "content") + charm + P.CROWN))
    wmax = max(P.width(f) for _, f in rows)
    print("Right edge should be a straight line. If you use tmux/zellij/etc., check inside that too.\n")
    for label, f in rows:
        print(f"  {label:<28}│{f}{' ' * (wmax - P.width(f))}│")


def main(argv):
    P.load_config()
    cmd = argv[0] if argv else ""
    if cmd == "":
        sheet()
    elif cmd == "log":
        log(int(argv[1]) if len(argv) > 1 and argv[1].isdigit() else 20)
    elif cmd == "name" and len(argv) > 1:
        rename(" ".join(argv[1:]))
    elif cmd == "glyphs":
        glyphs()
    elif cmd == "tree":
        tree()
    elif cmd in ("--version", "version"):
        print(f"claude-pet {P.__version__}")
    elif cmd == "devolve":
        devolve(assume_yes="-y" in argv[1:])
    else:
        print(__doc__.strip())
        sys.exit(0 if cmd in ("-h", "--help", "help") else 2)


if __name__ == "__main__":
    main(sys.argv[1:])
