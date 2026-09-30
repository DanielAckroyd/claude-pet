#!/usr/bin/env python3
"""claude-pet installer.

  python3 install.py              ask: express or custom
  python3 install.py --express    sensible defaults, no questions
  python3 install.py --custom     a few questions and a quick guide
  python3 install.py --uninstall  put your settings back the way they were

Custom options can also be passed as flags (handy when Claude runs it for you):
  --hours 9-17   --statusline wrap|replace|skip   --no-hooks   --bin DIR | --no-bin   --name NAME
Add --dry-run to see what would change without writing anything."""
import argparse, json, os, shlex, sys, time

PET = os.path.dirname(os.path.realpath(__file__))
sys.path.insert(0, PET)
import petlib as P

CLAUDE = os.path.expanduser("~/.claude")
SETTINGS = os.path.join(CLAUDE, "settings.json")
STATUSLINE = os.path.join(PET, "pet_statusline.py")
HOOK_CMD = f"python3 {shlex.quote(os.path.join(PET, 'hook.py'))}"
HOOK_EVENTS = ("PreCompact", "SessionEnd")
REFRESH_MS = 5000

BOLD, DIM, GREEN, YELLOW, RESET = "\033[1m", "\033[2m", "\033[38;5;42m", "\033[38;5;179m", "\033[0m"
if not sys.stdout.isatty():
    BOLD = DIM = GREEN = YELLOW = RESET = ""


def tilde(path):
    home = os.path.expanduser("~")
    return "~" + path[len(home):] if path == home or path.startswith(home + os.sep) else path


def record_path():
    return os.path.join(P.home(), "install.json")


# --- settings.json -----------------------------------------------------------
def load_settings():
    if not os.path.exists(SETTINGS):
        return {}
    with open(SETTINGS) as f:
        text = f.read()
    if not text.strip():
        return {}
    s = json.loads(text)
    if not isinstance(s, dict):
        raise ValueError("settings.json isn't a JSON object")
    return s


def save_settings(s, dry):
    if dry:
        return None
    os.makedirs(CLAUDE, exist_ok=True)
    backup = None
    if os.path.exists(SETTINGS):
        backup = f"{SETTINGS}.bak-claude-pet-{time.strftime('%Y%m%d-%H%M%S')}"
        with open(SETTINGS) as src, open(backup, "w") as dst:
            dst.write(src.read())
    tmp = SETTINGS + ".tmp-claude-pet"
    with open(tmp, "w") as f:
        json.dump(s, f, indent=2, ensure_ascii=False)
        f.write("\n")
    os.replace(tmp, SETTINGS)
    return backup


def is_ours(cmd):
    return isinstance(cmd, str) and ("pet_statusline.py" in cmd or "petlib" in cmd)


def statusline_has_pet(sl):
    """True if the current statusline already shows the pet (bundled, wrapped, or hand-integrated)."""
    cmd = (sl or {}).get("command") or ""
    if is_ours(cmd):
        return True
    try:
        parts = shlex.split(cmd)
    except ValueError:
        parts = cmd.split()
    for tok in parts:
        path = os.path.expanduser(tok)
        try:
            if os.path.isfile(path) and os.path.getsize(path) < 1_000_000:
                with open(path, errors="ignore") as f:
                    body = f.read()
                if "petlib" in body or "pet_statusline" in body:
                    return True
        except OSError:
            pass
    return False


def unwrap(cmd):
    """Original command from a `pet_statusline.py --wrap '<cmd>'` line, else None."""
    try:
        parts = shlex.split(cmd)
    except ValueError:
        return None
    if "--wrap" in parts and parts.index("--wrap") + 1 < len(parts):
        return parts[parts.index("--wrap") + 1]
    return None


def hook_is_ours(h):
    cmd = h.get("command") if isinstance(h, dict) else None
    return isinstance(cmd, str) and "hook.py" in cmd and "pet" in cmd


def add_hooks(s):
    hooks = s.setdefault("hooks", {})
    added = []
    for ev in HOOK_EVENTS:
        lst = hooks.setdefault(ev, [])
        if any(hook_is_ours(h) for m in lst if isinstance(m, dict) for h in m.get("hooks", [])):
            continue
        lst.append({"hooks": [{"type": "command", "command": HOOK_CMD, "async": True, "timeout": 5}]})
        added.append(ev)
    return added


def remove_hooks(s):
    hooks = s.get("hooks")
    if not isinstance(hooks, dict):
        return []
    removed = []
    for ev in HOOK_EVENTS:
        lst = hooks.get(ev)
        if not isinstance(lst, list):
            continue
        keep = []
        for m in lst:
            if isinstance(m, dict) and isinstance(m.get("hooks"), list):
                inner = [h for h in m["hooks"] if not hook_is_ours(h)]
                if len(inner) != len(m["hooks"]):
                    removed.append(ev)
                if not inner:
                    continue
                m = dict(m, hooks=inner)
            keep.append(m)
        if keep:
            hooks[ev] = keep
        else:
            del hooks[ev]
    if not hooks:
        del s["hooks"]
    return removed


# --- steps -------------------------------------------------------------------
def plan_statusline(s, mode):
    """Apply the statusline choice. Returns (message, record-fields)."""
    cur = s.get("statusLine")
    if mode == "skip":
        return "statusline: left alone (see the README to add the pet by hand)", {}
    if statusline_has_pet(cur):
        return "statusline: already shows the pet, left alone", {}
    before = json.loads(json.dumps(cur)) if cur is not None else None
    if mode == "wrap" and cur and cur.get("command"):
        new = dict(cur, command=f"python3 {shlex.quote(STATUSLINE)} --wrap {shlex.quote(cur['command'])}")
        msg = "statusline: pet added to the front of your existing statusline"
    else:
        new = {"type": "command", "command": f"python3 {shlex.quote(STATUSLINE)}"}
        msg = ("statusline: replaced with the bundled one (pet, model, context %)" if cur
               else "statusline: set to the bundled one (pet, model, context %)")
    if "refreshInterval" not in new:
        new["refreshInterval"] = REFRESH_MS
        msg += f", refreshing every {REFRESH_MS // 1000}s so it animates"
    s["statusLine"] = new
    return msg, {"statusline_before": before, "statusline_after": new}


def link_cli(bin_dir, dry):
    if not bin_dir:
        return "pet command: skipped (run it with python3 " + tilde(os.path.join(PET, "pet_cli.py")) + ")", None
    bin_dir = os.path.expanduser(bin_dir)
    link = os.path.join(bin_dir, "pet")
    target = os.path.join(PET, "pet_cli.py")
    if os.path.lexists(link):
        if os.path.realpath(link) == os.path.realpath(target):
            return f"pet command: already at {tilde(link)}", None
        return f"pet command: {tilde(link)} already exists and isn't ours, left alone", None
    if not dry:
        os.makedirs(bin_dir, exist_ok=True)
        os.symlink(target, link)
    msg = f"pet command: {tilde(link)}"
    if os.path.realpath(bin_dir) not in {os.path.realpath(p) for p in os.environ.get("PATH", "").split(os.pathsep) if p}:
        msg += f" {YELLOW}(not on your PATH yet: add {tilde(bin_dir)} to it){RESET}"
    return msg, link


def write_config(updates, dry):
    if not updates:
        return
    path = os.path.join(P.home(), "config.json")
    cfg = {}
    try:
        with open(path) as f:
            cfg = json.load(f)
    except Exception:
        pass
    cfg.update(updates)
    if not dry:
        os.makedirs(P.home(), exist_ok=True)
        with open(path, "w") as f:
            json.dump(cfg, f, indent=2)
            f.write("\n")


def name_pet(name, dry):
    if not name or dry:
        return
    with P.lock(wait=2) as got:
        if not got:
            return
        now = time.time()
        events = []
        s = P.load_or_hatch(now, events)
        if s["name"] != name:
            events.append(P._ev(now, "rename", f"{s['name']} is now {name}"))
            s["name"] = name
        P.save_state(s)
        P.append_events(events)


def parse_hours(text):
    a, b = (int(x) for x in text.replace(" ", "").split("-"))
    if not (0 <= a < b <= 24):
        raise ValueError
    return a, b


# --- flows -------------------------------------------------------------------
GUIDE = f"""{BOLD}How the pet works{RESET}
  fed     goes up when you commit, drops slowly over work hours
  fit     clean wraps (ending a session that shipped, under 70% context) raise it; auto-compacts hurt
  rested  follows your 5h rate limit: happy under 50%
  It evolves at L5 and L15 into different creatures depending on how you work,
  and only ever counts work hours, so evenings and weekends are free.
"""


def ask(prompt, default, choices=None):
    while True:
        try:
            ans = input(f"{prompt} {DIM}[{default}]{RESET} ").strip() or default
        except EOFError:
            return default
        if not choices or ans.lower() in choices:
            return ans.lower() if choices else ans
        print(f"  pick one of: {', '.join(choices)}")


def install(opts, interactive):
    try:
        s = load_settings()
    except Exception as e:
        sys.exit(f"Couldn't read {tilde(SETTINGS)} ({e}). Nothing was changed. Fix it, or follow the manual steps in the README.")

    has_sl = bool((s.get("statusLine") or {}).get("command"))
    if opts.custom and interactive:
        print(GUIDE)
        print(f"{BOLD}A few questions{RESET} {DIM}(Enter keeps the default){RESET}\n")
        if opts.hours is None:
            while True:
                try:
                    opts.hours = parse_hours(ask("Work hours, Mon to Fri, 24h (decay only counts these)", "9-17"))
                    break
                except ValueError:
                    print("  like 9-17 or 8-16")
        if opts.statusline is None:
            if statusline_has_pet(s.get("statusLine")):
                opts.statusline = "skip"
            elif has_sl:
                print(f"  Your statusline: {DIM}{s['statusLine']['command']}{RESET}")
                opts.statusline = ask("Add the pet to it (wrap), replace it with the bundled one, or skip?",
                                      "wrap", ["wrap", "replace", "skip"])
            else:
                opts.statusline = ask("No statusline yet. Use the bundled one, or skip?", "replace", ["replace", "skip"])
        if opts.hooks is None:
            opts.hooks = ask("Add hooks for auto-compact and clean-wrap scoring?", "y", ["y", "n"]) == "y"
        if opts.bin is None:
            b = ask("Where should the `pet` command go? (none to skip)", "~/.local/bin")
            opts.bin = "" if b == "none" else b
        if opts.name is None:
            nm = ask("Name your pet", "random")
            opts.name = None if nm.lower() == "random" else nm
        print()

    mode = opts.statusline or ("wrap" if has_sl else "replace")
    hooks = True if opts.hooks is None else opts.hooks
    bin_dir = "~/.local/bin" if opts.bin is None else opts.bin

    lines = []
    msg, rec = plan_statusline(s, mode)
    lines.append(msg)
    added = add_hooks(s) if hooks else []
    lines.append(f"hooks: added {', '.join(added)}" if added
                 else "hooks: already there" if hooks else "hooks: skipped (no auto-compact or clean-wrap scoring)")
    backup = save_settings(s, opts.dry_run) if (rec or added) else None
    cli_msg, link = link_cli(bin_dir, opts.dry_run)
    lines.append(cli_msg)
    if opts.hours:
        write_config({"work_start": opts.hours[0], "work_end": opts.hours[1]}, opts.dry_run)
        lines.append(f"work hours: {opts.hours[0]}:00 to {opts.hours[1]}:00, Mon to Fri")
    name_pet(opts.name, opts.dry_run)
    if opts.name:
        lines.append(f"name: {opts.name}")

    if not opts.dry_run:
        prev = {}
        try:
            with open(record_path()) as f:
                prev = json.load(f)
        except Exception:
            pass
        if rec:
            prev.update(rec)
        if link:
            prev["cli_link"] = link
        prev.update(version=P.__version__, installed_at=time.strftime("%Y-%m-%d %H:%M"))
        os.makedirs(P.home(), exist_ok=True)
        with open(record_path(), "w") as f:
            json.dump(prev, f, indent=2)

    head = "Would do (dry run):" if opts.dry_run else f"{GREEN}claude-pet {P.__version__} installed{RESET}"
    print(head)
    for ln in lines:
        print(f"  {GREEN}✓{RESET} {ln}")
    if backup:
        print(f"  {DIM}settings backup: {tilde(backup)}{RESET}")
    here = tilde(PET)
    print(f"""
{BOLD}Change it later{RESET}
  work hours, tuning   {tilde(os.path.join(P.home(), "config.json"))}  {DIM}(any number from TUNING in petlib.py){RESET}
  placement, hooks     python3 {here}/install.py --custom
  switch it off        PET_DISABLE=1
  remove               python3 {here}/install.py --uninstall

Start a new Claude Code session and your pet hatches. Then try {BOLD}pet{RESET}, {BOLD}pet tree{RESET} and {BOLD}pet glyphs{RESET}.""")


def uninstall(opts):
    try:
        s = load_settings()
    except Exception as e:
        sys.exit(f"Couldn't read {tilde(SETTINGS)} ({e}). Nothing was changed.")
    rec = {}
    try:
        with open(record_path()) as f:
            rec = json.load(f)
    except Exception:
        pass
    lines = []
    cur = s.get("statusLine")
    cmd = (cur or {}).get("command") or ""
    if is_ours(cmd):
        if "statusline_before" in rec and rec.get("statusline_after") == cur:
            before = rec["statusline_before"]
        elif unwrap(cmd):
            before = dict(cur, command=unwrap(cmd))
        else:
            before = None
        if before is None:
            del s["statusLine"]
            lines.append("statusline: removed (there wasn't one before)")
        else:
            s["statusLine"] = before
            lines.append("statusline: restored to what you had before")
    else:
        lines.append("statusline: doesn't use pet_statusline.py, left alone "
                     "(if you added the pet by hand, remove it there)")
    removed = remove_hooks(s)
    lines.append(f"hooks: removed {', '.join(removed)}" if removed else "hooks: none found")
    backup = save_settings(s, opts.dry_run) if (is_ours(cmd) or removed) else None
    link = rec.get("cli_link") or os.path.expanduser("~/.local/bin/pet")
    target = os.path.join(PET, "pet_cli.py")
    if os.path.islink(link) and os.path.realpath(link) == os.path.realpath(target):
        if not opts.dry_run:
            os.remove(link)
        lines.append(f"pet command: removed {tilde(link)}")
    if not opts.dry_run:
        try:
            os.remove(record_path())
        except OSError:
            pass
    print("Would do (dry run):" if opts.dry_run else "claude-pet uninstalled")
    for ln in lines:
        print(f"  ✓ {ln}")
    if backup:
        print(f"  settings backup: {tilde(backup)}")
    print(f"\nYour pet and its history are still in {tilde(P.home())}. Delete that folder to say goodbye for good.")


def main(argv):
    ap = argparse.ArgumentParser(add_help=False)
    for flag in ("--express", "--custom", "--uninstall", "--dry-run", "--no-hooks", "--no-bin", "-h", "--help"):
        ap.add_argument(flag, action="store_true")
    ap.add_argument("--hours")
    ap.add_argument("--statusline", choices=["wrap", "replace", "skip"])
    ap.add_argument("--bin")
    ap.add_argument("--name")
    try:
        opts = ap.parse_args(argv)
    except SystemExit:
        print(__doc__)
        sys.exit(2)
    if opts.help or opts.h:
        print(__doc__)
        return
    opts.dry_run = opts.dry_run
    opts.hooks = False if opts.no_hooks else None
    opts.bin = "" if opts.no_bin else opts.bin
    if opts.hours:
        try:
            opts.hours = parse_hours(opts.hours)
        except ValueError:
            sys.exit("--hours looks like 9-17")
    if opts.uninstall:
        return uninstall(opts)

    interactive = sys.stdin.isatty()
    if not (opts.express or opts.custom):
        flags_given = any(v is not None for v in (opts.hours, opts.statusline, opts.hooks, opts.bin, opts.name))
        if flags_given:
            opts.custom = True
        elif interactive:
            print(f"{BOLD}claude-pet {P.__version__}{RESET}\n")
            print(f"  {BOLD}express{RESET}  defaults for everything, takes a second")
            print(f"  {BOLD}custom{RESET}   a few questions and a quick guide\n")
            opts.custom = ask("Express or custom?", "express", ["express", "custom", "e", "c"]) in ("custom", "c")
            print()
        else:
            print(__doc__)
            sys.exit(2)
    install(opts, interactive)


if __name__ == "__main__":
    main(sys.argv[1:])
