"""Claude pet engine: one shared pet, fed by good Claude usage, shown in every statusline.

Pure-ish functions take `now` (epoch seconds) so they're testable. Anything the
statusline calls must never raise past segment()'s caller, and must stay fast."""
import os, json, time, math, random, fcntl, subprocess, unicodedata
import datetime as dt
from contextlib import contextmanager

TUNING = {
    "work_start": 9, "work_end": 17,          # local hours, Mon–Fri
    "floor": 5.0,                             # stats never drop below this
    "fed_per_commit": 15, "fed_lines_per_point": 20, "fed_lines_cap": 10,
    "fed_decay_per_min": 0.2,                 # full → empty ≈ one workday
    "fit_clean_wrap": 12, "clean_wrap_ctx": 70,
    "fit_auto_compact": -20, "bloated_secs": 30 * 60,
    "fit_redline_ctx": 80, "fit_redline_per_min": 0.1,
    "rested_easy_until": 50, "rested_min_target": 10, "rested_neutral": 80,
    "rested_tau_secs": 600,
    "xp_per_commit": 10, "xp_lines_per_point": 25,
    "xp_mult": [(50, 1.5), (70, 1.0), (101, 0.7)],   # (ctx below, multiplier)
    "level_div": 10, "level_exp": 0.6,        # L5 ≈ 100xp, L15 ≈ 800xp, L30 ≈ 2700xp
    "active_secs": 300,                       # herdr keeps panes ticking, so only lines/ctx moving counts as you being there
    "steady_full_hours": 2,                   # active hours in the trait window for a full steady score
    "poll_secs": 60, "git_timeout": 0.5,
    "missed_workdays": 2, "crown_streak": 5, "thriving_streak": 3,
    "lock_wait": 0.1, "log_max_bytes": 1_000_000,
    "species_level": 5, "final_level": 15,
    "trait_tau_hours": 4,                     # traits remember roughly the last 4 work hours
    "ship_per_hour": 0.5, "tidy_per_hour": 0.25,   # sustained rate that maxes the trait
    "fox_min": 0.5, "fox_spread": 0.25,       # all traits this high and this close → fox
    "wilt_below": 30, "wilt_recover": 40,     # avg stat that starts / unwinds wilting
    "wilt_minutes": 90, "devolve_levels": 2,  # work-minutes wilting before devolving; levels lost
    "react_secs": 30, "shiny_odds": 20,
    "keep_session_days": 7, "keep_commit_days": 14, "keep_days": 90,
}
T = TUNING
VERSION = 1
NAMES = ["Biscuit", "Pixel", "Mochi", "Gremlin", "Noodle", "Widget", "Pickle",
         "Sprocket", "Dumpling", "Fizz", "Bramble", "Crumpet", "Toast", "Gizmo"]

# form: (face template, mouth overrides). The species' own mouth replaces the default ᴗ;
# mood-specific mouths (□, ロ, _) still show through. Width-checked glyphs only.
FORMS = {
    "hatchling": ("({e}{m}{e})", {}),
    "bear":      ("ʕ{e}{m}{e}ʔ", {"ᴗ": "ᴥ"}),
    "cat":       ("(={e}{m}{e}=)", {"ᴗ": "ω"}),
    "owl":       ("{{{e}{m}{e}}}", {"ᴗ": "v"}),
    "seal":      ("ᶘ{e}{m}{e}ᶅ", {"ᴗ": "ᴥ"}),
    "koala":     ("ʢ{e}{m}{e}ʡ", {"ᴗ": "ᴥ"}),
    "dog":       ("U{e}{m}{e}U", {"ᴗ": "ᴥ"}),
    "tiger":     ("(≡{e}{m}{e}≡)", {"ᴗ": "ω"}),
    "neko":      ("/ᐠ{e}{m}{e}ᐟ\\", {"ᴗ": "ꞈ"}),
    "lynx":      ("/\\{e}{m}{e}/\\", {"ᴗ": "ω"}),
    "penguin":   ("<({e}{m}{e})>", {"ᴗ": "v"}),
    "dove":      ("~({e}{m}{e})~", {"ᴗ": "v"}),
    "great owl": ("{{({e}){m}({e})}}", {"ᴗ": "v"}),
    "fox":       ("ˆ({e}{m}{e})ˆ", {"ᴗ": "ᴥ"}),
    "wolf":      ("ᐡ{e}{m}{e}ᐡ", {"ᴗ": "ᴥ"}),
    "fennec":    ("ᐠ({e}{m}{e})ᐟ", {"ᴗ": "ᴥ"}),
    "kitsune":   ("ミᕱ{e}{m}{e}ᕱ彡", {"ᴗ": "ᴥ"}),
}
# Evolution: dominant trait at L5 picks the species (all three high and close → the hidden fox),
# dominant trait at L15 picks the final form. Wilting devolves one tier.
TRAITS = ("shipper", "tidy", "steady")      # tie-break order
SPECIES = {"shipper": "bear", "tidy": "cat", "steady": "owl", "balanced": "fox"}
FINALS = {
    "bear": {"shipper": "seal", "tidy": "koala", "steady": "dog"},
    "cat":  {"shipper": "tiger", "tidy": "neko", "steady": "lynx"},
    "owl":  {"shipper": "penguin", "tidy": "dove", "steady": "great owl"},
    "fox":  {"shipper": "wolf", "tidy": "fennec", "steady": "kitsune"},
}
PARENT = {fin: sp for sp, d in FINALS.items() for fin in d.values()}
# Per-pet looks, rolled once from the birth time: default eyes, a held charm, rare shiny.
EYES = ["•", "•", "◕", "ᵔ", "ʘ", "⊙"]
CHARMS = ["", "", "", "✿", "♡", "☆", "♣", "❀"]
SHINY = "\033[38;5;220m"
# mood: (eyes, mouth, word, colour key)
MOODS = {
    "bloated":   ("°", "□", "bloated", "bad"),
    "wilting":   (";", "_", "wilting", "bad"),
    "missed":    ("T", "_", "missed you", "meh"),
    "exhausted": ("×", "_", "exhausted", "bad"),
    "starving":  ("•", "ロ", "starving", "bad"),
    "sleepy":    ("-", "_", "sleepy", "meh"),
    "sulky":     ("¬", "_", "sulky", "meh"),
    "thriving":  ("^", "ᴗ", "thriving", "good"),
    "content":   ("•", "ᴗ", "content", "good"),
}
CROWN = "♔"
CAMEOS = ["c[_]", "♪", "✧"]
CAMEO_ODDS = 200


# --- paths & I/O -------------------------------------------------------------
def home():
    return os.environ.get("PET_HOME") or os.path.expanduser("~/.claude/pet")

def _p(name):
    return os.path.join(home(), name)

@contextmanager
def lock(wait=None):
    """Yield True if we got the exclusive lock within `wait` seconds, else False."""
    wait = T["lock_wait"] if wait is None else wait
    os.makedirs(home(), exist_ok=True)
    fd = os.open(_p(".lock"), os.O_RDWR | os.O_CREAT, 0o644)
    got = False
    try:
        deadline = time.monotonic() + wait
        while True:
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                got = True
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    break
                time.sleep(0.01)
        yield got
    finally:
        if got:
            fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)

def roll_look(born_at):
    rng = random.Random(int(born_at * 1000))
    return {"eyes": rng.choice(EYES), "charm": rng.choice(CHARMS),
            "shiny": rng.random() < 1 / T["shiny_odds"]}

def ensure(s):
    """Backfill fields added after a pet hatched. Deterministic, so read-only renders agree."""
    s.setdefault("form", "hatchling")
    s.setdefault("look", roll_look(s.get("born_at") or 0))
    s.setdefault("temper", {"ship": 0.0, "tidy": 0.0, "calm": 0.0, "hot": 0.0})
    s.setdefault("wilt", 0.0)
    s.setdefault("devolved", 0)
    s.setdefault("status", {}).setdefault("react", None)
    return s

def new_state(now):
    return ensure({
        "version": VERSION, "name": random.choice(NAMES),
        "born_at": now, "last_tick": now, "last_commit_at": None, "form": "hatchling",
        "stats": {"fed": 70.0, "fit": 70.0, "rested": 80.0, "xp": 0},
        "line_carry": 0,
        "status": {"bloated_until": 0},
        "sessions": {}, "repos": {}, "seen_commits": {}, "days": {},
        "streak": {"count": 0, "last_day": None, "through": None},
    })

def read_state():
    """Read-only load. None if missing or unreadable — never touches the file."""
    try:
        with open(_p("state.json")) as f:
            s = json.load(f)
        return ensure(s) if isinstance(s, dict) and "stats" in s else None
    except Exception:
        return None

def load_or_hatch(now, events):
    """Call with the lock held. Corrupt state is moved aside, never overwritten."""
    path = _p("state.json")
    if os.path.exists(path):
        s = read_state()
        if s is not None:
            return s
        aside = f"{path}.corrupt-{int(now)}"
        os.replace(path, aside)
        events.append(_ev(now, "corrupt", f"state was unreadable, moved to {os.path.basename(aside)}"))
    s = new_state(now)
    events.append(_ev(now, "hatch", f"{s['name']} hatched"))
    return s

def save_state(s):
    path = _p("state.json")
    tmp = f"{path}.tmp-{os.getpid()}"
    with open(tmp, "w") as f:
        json.dump(s, f, separators=(",", ":"))
    os.replace(tmp, path)

def _ev(now, kind, msg):
    return {"ts": now, "kind": kind, "msg": msg}

def append_events(events):
    if not events:
        return
    path = _p("events.jsonl")
    try:
        if os.path.getsize(path) > T["log_max_bytes"]:
            os.replace(path, path + ".1")
    except OSError:
        pass
    with open(path, "a") as f:
        for e in events:
            f.write(json.dumps(e, ensure_ascii=False) + "\n")

def read_events(n):
    out = []
    for name in ("events.jsonl", "events.jsonl.1"):
        try:
            with open(_p(name)) as f:
                lines = f.readlines()
        except OSError:
            continue
        for ln in reversed(lines):
            try:
                out.append(json.loads(ln))
            except ValueError:
                continue
            if len(out) >= n:
                return list(reversed(out))
    return list(reversed(out))


# --- the work clock ----------------------------------------------------------
def work_minutes(a, b):
    """Minutes of [a, b) overlapping Mon–Fri work hours, local time (DST-safe)."""
    if a is None or b <= a:
        return 0.0
    d = dt.datetime.fromtimestamp(a).date()
    end = dt.datetime.fromtimestamp(b).date()
    if (end - d).days > 400:
        d = end - dt.timedelta(days=400)
    total = 0.0
    while d <= end:
        if d.weekday() < 5:
            s = dt.datetime.combine(d, dt.time(T["work_start"])).timestamp()
            e = dt.datetime.combine(d, dt.time(T["work_end"])).timestamp()
            total += max(0.0, min(b, e) - max(a, s))
        d += dt.timedelta(days=1)
    return total / 60.0

def is_work_time(now):
    t = dt.datetime.fromtimestamp(now)
    return t.weekday() < 5 and T["work_start"] <= t.hour < T["work_end"]

def _date(now):
    return dt.datetime.fromtimestamp(now).date()


# --- scoring -----------------------------------------------------------------
def level(xp):
    return int((max(xp, 0) / T["level_div"]) ** T["level_exp"]) + 1

def xp_for_level(lvl):
    return math.ceil(T["level_div"] * (lvl - 1) ** (1 / T["level_exp"]))

def tier(form):
    return 1 if form == "hatchling" else 2 if form in FINALS else 3

def trait_scores(s, now=None):
    """0–1 per trait from the rolling temperament (decays on the work clock)."""
    t = s.get("temper") or {}
    hrs = T["trait_tau_hours"]
    ship = t.get("ship", 0) / (T["ship_per_hour"] * hrs)
    tidy = t.get("tidy", 0) / (T["tidy_per_hour"] * hrs)
    calm, hot = t.get("calm", 0), t.get("hot", 0)
    # mostly-calm *and* enough active time; a sliver of calm minutes shouldn't max it
    ratio = (calm / (calm + hot) - 0.5) * 2 if calm + hot else 0.0
    steady = ratio * min(1.0, (calm + hot) / (T["steady_full_hours"] * 60))
    return {k: max(0.0, min(1.0, v)) for k, v in zip(TRAITS, (ship, tidy, steady))}

def dominant_trait(s, now=None):
    sc = trait_scores(s, now)
    return max(TRAITS, key=lambda t: (sc[t], -TRAITS.index(t)))

def species_path(s, now=None):
    sc = trait_scores(s, now)
    if min(sc.values()) >= T["fox_min"] and max(sc.values()) - min(sc.values()) <= T["fox_spread"]:
        return "balanced"
    return dominant_trait(s, now)

def react(s, now, text, mood="thriving"):
    s["status"]["react"] = {"until": now + T["react_secs"], "text": text, "mood": mood}

def evolve(s, now, events):
    lvl = level(s["stats"]["xp"])
    form = s.get("form") or "hatchling"
    if form == "hatchling" and lvl >= T["species_level"]:
        trait = species_path(s, now)
        form = SPECIES[trait]
        events.append(_ev(now, "evolve", f"grew into a {form} ({trait})"))
        react(s, now, f"✧ {form}!")
    if form in FINALS and lvl >= T["final_level"]:
        trait = dominant_trait(s, now)
        form = FINALS[form][trait]
        events.append(_ev(now, "evolve", f"evolved into a {form} ({trait})"))
        react(s, now, f"✧ {form}!")
    s["form"] = form

def devolve_preview(s):
    """(form it would become, xp it would keep), or None for a hatchling."""
    form = s.get("form") or "hatchling"
    if form == "hatchling":
        return None
    back, lost = (PARENT[form], T["final_level"]) if form in PARENT else ("hatchling", T["species_level"])
    return back, min(s["stats"]["xp"], xp_for_level(lost - T["devolve_levels"]))

def devolve(s, now, events, reason="wilted"):
    form = s.get("form") or "hatchling"
    if form == "hatchling":
        return
    back, lost = (PARENT[form], T["final_level"]) if form in PARENT else ("hatchling", T["species_level"])
    # drop below the threshold so it has to be re-earned — and may branch differently
    s["stats"]["xp"] = min(s["stats"]["xp"], xp_for_level(lost - T["devolve_levels"]))
    s["form"] = back
    s["devolved"] = s.get("devolved", 0) + 1
    events.append(_ev(now, "devolve", f"{reason} back into a {back}"))
    react(s, now, f"devolved → {back}", mood="wilting")

def xp_multiplier(ctx):
    ctx = ctx or 0
    for below, mult in T["xp_mult"]:
        if ctx < below:
            return mult
    return T["xp_mult"][-1][1]

def rested_target(five_hour_pct):
    if five_hour_pct is None:
        return T["rested_neutral"]
    u = float(five_hour_pct)
    lo = T["rested_easy_until"]
    if u <= lo:
        return 100.0
    frac = min(1.0, (u - lo) / (100 - lo))
    return 100.0 - frac * (100.0 - T["rested_min_target"])

def _clamp(s):
    st = s["stats"]
    for k in ("fed", "fit", "rested"):
        st[k] = max(T["floor"], min(100.0, float(st[k])))

def _day(s, now):
    key = _date(now).isoformat()
    d = s["days"].get(key)
    if d is None:
        d = s["days"][key] = {"commits": 0, "lines": 0, "xp": 0, "auto_compacts": 0,
                              "clean_wraps": 0, "fit": s["stats"]["fit"]}
    return d

def _add_xp(s, now, n):
    s["stats"]["xp"] += n
    _day(s, now)["xp"] += n

def _day_counts(rec):
    return bool(rec) and rec.get("commits", 0) >= 1 and rec.get("fit", 0) >= 50

def update_streak(s, now):
    """Settle every finished weekday since the last settlement. Weekends are skipped."""
    st = s["streak"]
    yesterday = _date(now) - dt.timedelta(days=1)
    through = st.get("through")
    if through is None:
        st["through"] = yesterday.isoformat()
        return
    d = dt.date.fromisoformat(through) + dt.timedelta(days=1)
    if (yesterday - d).days > 400:
        d = yesterday - dt.timedelta(days=400)
    while d <= yesterday:
        if d.weekday() < 5:
            if _day_counts(s["days"].get(d.isoformat())):
                st["count"] += 1
                st["last_day"] = d.isoformat()
            else:
                st["count"] = 0
        d += dt.timedelta(days=1)
    st["through"] = yesterday.isoformat()

def current_streak(s, now):
    """Settled streak plus today, provisionally, if today already qualifies."""
    today = _date(now)
    n = s["streak"]["count"]
    if today.weekday() < 5 and _day_counts(s["days"].get(today.isoformat())):
        n += 1
    return n

def _prune(s, now):
    s["sessions"] = {k: v for k, v in s["sessions"].items()
                     if now - v.get("last_seen", 0) < T["keep_session_days"] * 86400}
    cutoff = (_date(now) - dt.timedelta(days=T["keep_commit_days"])).isoformat()
    s["seen_commits"] = {k: v for k, v in s["seen_commits"].items() if v >= cutoff}
    dcut = (_date(now) - dt.timedelta(days=T["keep_days"])).isoformat()
    s["days"] = {k: v for k, v in s["days"].items() if k >= dcut}
    live_tops = {v.get("top") for v in s["sessions"].values()}
    s["repos"] = {k: v for k, v in s["repos"].items() if k in live_tops}

def advance(s, now, events):
    """Time passing: work-clock decay, redline drain, streak settlement, pruning."""
    last = s.get("last_tick") or now
    wm = work_minutes(last, now)
    st = s["stats"]
    st["fed"] -= T["fed_decay_per_min"] * wm
    redlining = any(now - v.get("active_at", 0) < T["active_secs"]
                    and (v.get("last_ctx") or 0) > T["fit_redline_ctx"]
                    for v in s["sessions"].values())
    if redlining:
        st["fit"] -= T["fit_redline_per_min"] * wm
    decay = math.exp(-wm / (T["trait_tau_hours"] * 60))
    s["temper"] = {k: v * decay for k, v in s["temper"].items()}
    update_streak(s, now)
    _prune(s, now)
    return wm

def misuse(s):
    """Wilting gauge. Fed is left out: it falls with plain absence, and absence isn't misuse."""
    return (s["stats"]["fit"] + s["stats"]["rested"]) / 2

def wilt(s, now, wm, events):
    """Runs after the tick's gains, so a devolve's XP cap can't be climbed back in the same tick."""
    m = misuse(s)
    if m < T["wilt_below"]:
        if now - s.get("active_at", 0) < T["active_secs"]:
            s["wilt"] += wm
    elif m >= T["wilt_recover"]:
        s["wilt"] = max(0.0, s["wilt"] - wm)
    if s["wilt"] >= T["wilt_minutes"]:
        s["wilt"] = 0.0
        devolve(s, now, events)

def _num(v):
    try:
        return None if v is None else float(v)
    except (TypeError, ValueError):
        return None

def _obj(d, *keys):
    """Walk nested dicts; anything malformed along the way becomes {}."""
    for k in keys:
        d = d.get(k) if isinstance(d, dict) else None
    return d if isinstance(d, dict) else {}

def observe(s, data, now, events, poll=True):
    """Apply one statusline payload. Per-session baselines prevent double counting."""
    st = s["stats"]
    # rested eases toward the rate-limit target on wall time (it tracks, it doesn't decay)
    five = _num(_obj(data, "rate_limits", "five_hour").get("used_percentage"))
    gap = max(0.0, now - (s.get("last_tick") or now))
    k = 1 - math.exp(-gap / T["rested_tau_secs"])
    st["rested"] += (rested_target(five) - st["rested"]) * k

    sid = data.get("session_id")
    if not sid or not isinstance(sid, str):
        return
    cost = _obj(data, "cost")
    added, removed = _num(cost.get("total_lines_added")), _num(cost.get("total_lines_removed"))
    lines = None if added is None and removed is None else int((added or 0) + (removed or 0))
    ctx = _num(_obj(data, "context_window").get("used_percentage"))
    cwd = _obj(data, "workspace").get("current_dir") or data.get("cwd") or ""
    cwd = cwd if isinstance(cwd, str) else ""

    sess = s["sessions"].get(sid)
    if sess is None:
        sess = s["sessions"][sid] = {"lines": lines, "last_ctx": ctx, "max_ctx": ctx or 0,
                                     "shipped": False, "last_seen": now, "cwd": None, "top": None}
    else:
        if ((lines is not None and sess["lines"] is not None and lines != sess["lines"])
                or (ctx is not None and sess.get("last_ctx") is not None and ctx != sess["last_ctx"])):
            sess["active_at"] = s["active_at"] = now
        if lines is not None and sess["lines"] is None:
            sess["lines"] = lines           # first sighting of a total is a baseline, not a gain
        elif lines is not None:
            delta = lines - sess["lines"]
            if delta > 0:
                _gain_lines(s, sess, delta, now)
            sess["lines"] = lines
    if ctx is not None:
        sess["last_ctx"] = ctx
        sess["max_ctx"] = max(sess.get("max_ctx") or 0, ctx)
    sess["last_seen"] = now
    if five is not None and now - s.get("active_at", 0) < T["active_secs"]:
        key = "calm" if five <= T["rested_easy_until"] else "hot"
        s["temper"][key] += work_minutes(s.get("last_tick"), now)

    if poll and cwd:
        if sess.get("cwd") != cwd:
            sess["cwd"] = cwd
            sess["top"] = git_toplevel(cwd) or ""
        if sess["top"]:
            _poll_repo(s, sess, now, events)

def _gain_lines(s, sess, delta, now):
    st = s["stats"]
    st["fed"] += min(T["fed_lines_cap"], delta / T["fed_lines_per_point"])
    _day(s, now)["lines"] += delta
    s["line_carry"] = s.get("line_carry", 0) + delta
    pts, s["line_carry"] = divmod(s["line_carry"], T["xp_lines_per_point"])
    if pts:
        _add_xp(s, now, int(pts))
    sess["shipped"] = True

def _poll_repo(s, sess, now, events):
    top = sess["top"]
    repo = s["repos"].setdefault(top, {"checked_at": 0, "email": None})
    if now - repo.get("checked_at", 0) < T["poll_secs"]:
        return
    repo["checked_at"] = now
    if not repo.get("email"):
        repo["email"] = git_email(top)
        if not repo["email"]:
            return
    today = _date(now).isoformat()
    seen, new = s["seen_commits"], []
    for sha, key in git_commits_today(top, repo["email"]):
        if sha in seen or key in seen:      # key survives rebase/amend/cherry-pick; sha covers old state
            seen.setdefault(sha, today)
            seen.setdefault(key, today)
        else:
            new.append((sha, key))
    if not new:
        return
    mult = xp_multiplier(sess.get("last_ctx"))
    per = int(round(T["xp_per_commit"] * mult))
    for sha, key in new:
        seen[sha] = seen[key] = today
        s["stats"]["fed"] += T["fed_per_commit"]
        _add_xp(s, now, per)
        _day(s, now)["commits"] += 1
    s["last_commit_at"] = now
    sess["shipped"] = True
    n = len(new)
    s["temper"]["ship"] += n
    react(s, now, f"nom +{per * n}xp")
    events.append(_ev(now, "commit",
                      f"{n} commit{'s' if n > 1 else ''} in {os.path.basename(top)} (+{per * n} xp, ×{mult})"))

def on_pre_compact(s, data, now, events):
    if data.get("trigger") == "auto":
        s["stats"]["fit"] += T["fit_auto_compact"]
        s["status"]["bloated_until"] = now + T["bloated_secs"]
        _day(s, now)["auto_compacts"] += 1
        s["temper"]["tidy"] -= 1
        events.append(_ev(now, "auto_compact", "auto-compacted — bloated"))
    else:
        events.append(_ev(now, "compact", "manual compact"))

WRAP_REASONS = {"clear", "prompt_input_exit", "logout"}

def on_session_end(s, data, now, events):
    sid = data.get("session_id")
    sess = s["sessions"].get(sid)
    if not sess or data.get("reason") not in WRAP_REASONS:
        return
    ctx = sess.get("last_ctx") or 0
    if sess.get("shipped") and ctx < T["clean_wrap_ctx"]:
        s["stats"]["fit"] += T["fit_clean_wrap"]
        _day(s, now)["clean_wraps"] += 1
        s["temper"]["tidy"] += 1
        react(s, now, f"tidy +{T['fit_clean_wrap']}fit")
        events.append(_ev(now, "clean_wrap", f"clean wrap at {round(ctx)}% ctx"))
    del s["sessions"][sid]

def tick(s, now, events, data=None, hook=None, poll=True):
    """One locked update. `data` is a statusline payload; `hook` is (event, payload)."""
    lvl0 = level(s["stats"]["xp"])
    wm = advance(s, now, events)
    if data:
        observe(s, data, now, events, poll)
    if hook:
        name, payload = hook
        if name == "PreCompact":
            on_pre_compact(s, payload, now, events)
        elif name == "SessionEnd":
            on_session_end(s, payload, now, events)
    _clamp(s)
    wilt(s, now, wm, events)
    _day(s, now)["fit"] = s["stats"]["fit"]
    s["last_tick"] = now
    lvl1 = level(s["stats"]["xp"])
    if lvl1 > lvl0:
        events.append(_ev(now, "level", f"reached level {lvl1}"))
        react(s, now, f"✧ L{lvl1}!")
    evolve(s, now, events)


# --- git ---------------------------------------------------------------------
def _git(args, timeout=None):
    try:
        r = subprocess.run(["git", *args], capture_output=True, text=True,
                           timeout=timeout or T["git_timeout"])
        return r.stdout.strip() if r.returncode == 0 else None
    except Exception:
        return None

def git_toplevel(cwd):
    return _git(["-C", cwd, "rev-parse", "--show-toplevel"])

def git_email(top):
    return _git(["-C", top, "config", "user.email"])

def git_commits_today(top, email):
    """[(sha, author-time+subject)] authored by exactly `email`, committed since midnight."""
    out = _git(["-C", top, "log", "--branches", "--fixed-strings", f"--author=<{email}>",
                "--since=midnight", "--format=%H %at %s"])
    rows = []
    for ln in (out or "").splitlines():
        sha, _, rest = ln.partition(" ")
        if sha:
            rows.append((sha, rest))
    return rows


# --- mood & rendering --------------------------------------------------------
def mood(s, now):
    st = s["stats"]
    if now < s["status"].get("bloated_until", 0):
        return "bloated"
    if s.get("wilt", 0) > 0 and misuse(s) < T["wilt_below"]:
        return "wilting"
    since = s.get("last_commit_at") or s.get("born_at") or now
    if work_minutes(since, now) > T["missed_workdays"] * (T["work_end"] - T["work_start"]) * 60:
        return "missed"
    if st["rested"] < 25:
        return "exhausted"
    if st["fed"] < 20:
        return "starving"
    if not is_work_time(now):
        return "sleepy"
    avg = (st["fed"] + st["fit"] + st["rested"]) / 3
    if avg < 40:
        return "sulky"
    if avg > 80 and current_streak(s, now) >= T["thriving_streak"]:
        return "thriving"
    return "content"

def face(form, mood_key, blink=False, eyes=None):
    tpl, mouths = FORMS.get(form) or FORMS["hatchling"]
    e, m, _, _ = MOODS[mood_key]
    if eyes and e == "•":
        e = eyes
    if blink:
        e = "-"
    return tpl.format(e=e, m=mouths.get(m, m))

def frame(now):
    return int(now // 5)

def cameo(now):
    rng = random.Random(frame(now))
    if rng.random() < 1 / CAMEO_ODDS:
        return rng.choice(CAMEOS)
    return ""

def render(s, now, colors=None):
    colors = colors or {}
    look = s.get("look") or {}
    mk, word = mood(s, now), None
    r = s["status"].get("react")
    if r and now < r.get("until", 0) and r.get("mood") in MOODS:
        mk, word = r["mood"], r.get("text")
    word = word or MOODS[mk][2]
    f = face(s.get("form"), mk, blink=frame(now) % 4 == 3, eyes=look.get("eyes"))
    charm = look.get("charm") or ""
    crown = CROWN if current_streak(s, now) >= T["crown_streak"] else ""
    cam = cameo(now)
    cam = f" {cam}" if cam else ""
    key = MOODS[mk][3]
    col = (SHINY if colors and key == "good" and look.get("shiny") else colors.get(key, ""))
    reset = colors.get("reset", "") if col else ""
    return f"{col}{f}{charm}{crown}{cam} {word}{reset}"

def segment(data, colors=None, now=None):
    """Statusline entrypoint. Ticks if the lock is free, else renders read-only."""
    if os.environ.get("PET_DISABLE") == "1":
        return None
    now = time.time() if now is None else now
    events = []
    with lock() as got:
        if got:
            s = load_or_hatch(now, events)
            tick(s, now, events, data=data if isinstance(data, dict) else None)
            save_state(s)
            append_events(events)
        else:
            s = read_state()
    return render(s, now, colors) if s else None

def width(text):
    w = 0
    for ch in text:
        if unicodedata.combining(ch):
            continue
        w += 2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1
    return w
