# claude-pet

A little pet that lives in your Claude Code statusline. It does well when you use Claude well: shipping commits, keeping context lean, wrapping sessions cleanly, not redlining your rate limits. It sulks when neglected, and evolves into different creatures depending on how you work.

```
(ᵔᴗᵔ)✿ content │ Opus thinking:high │ ctx 68k/200k 34% │ 5h 22% │ ~/code/thing │ ⎇ main
```

One pet is shared across every Claude session on the machine, so every pane shows the same face and the same animation frame. Everything is local: state lives in a JSON file, the only external command is a local `git log`, and nothing touches the network.

## How it scores

| Stat | Goes up | Goes down |
|---|---|---|
| **fed** | Commits (+15), lines changed | Slowly over work hours |
| **fit** | Clean wraps: ending a session that shipped something while context is under 70% | Auto-compacts (-20, and it's bloated for 30 min), working in a session above 80% context |
| **rested** | Tracks your 5h rate limit usage, full while you're under 50% | Falls as you approach 100% |
| **xp** | Commits (x1.5 if context is under 50%), lines changed | Only when it devolves |

Decay only counts Mon to Fri, 09:00 to 17:00 local time, so evenings and weekends are free. Manual `/compact` is neutral. Stats floor at 5 and the pet never dies. A streak counts weekdays with at least one commit that ended with fit ≥ 50, and at 5+ days it gets a crown ♔.

## Evolution

The hatchling picks a species at L5 based on how you've worked over roughly the last 4 work hours, then a final form at L15 the same way. Final forms are locked.

```
(•ᴗ•) hatchling  (L1–4)
 ├─ shipper  ʕ•ᴥ•ʔ    bear      → seal ᶘ•ᴥ•ᶅ · koala ʢ•ᴥ•ʡ · dog U•ᴥ•U
 ├─ tidy     (=•ω•=)  cat       → tiger (≡•ω•≡) · neko /ᐠ•ꞈ•ᐟ\ · lynx /\•ω•/\
 ├─ steady   {•v•}    owl       → penguin <(•v•)> · dove ~(•v•)~ · great owl {(•)v(•)}
 └─ ???      ˆ(•ᴥ•)ˆ  fox       → wolf ᐡ•ᴥ•ᐡ · fennec ᐠ(•ᴥ•)ᐟ · kitsune ミᕱ•ᴥ•ᕱ彡
```

- **shipper:** committing regularly
- **tidy:** clean wraps, minus auto-compacts
- **steady:** active time spent under 50% of your 5h limit
- The fox is the hidden one. You'll have to find it.

Each pet also rolls its own look when it hatches: eye style, a held charm (✿ ♡ ☆ ♣ ❀) and a 1 in 20 chance of being shiny, which shows as gold when it's happy.

**Devolving:** if fit and rested average under 30 for 90 minutes of active use, the pet wilts back a tier and loses a couple of levels. Its next evolution picks a branch fresh, so it might go a different way. Being away never devolves it. Only line or context changes count as activity, so a pane left open in a terminal multiplexer doesn't count as you being there. You can also do it on purpose with `pet devolve`.

## Install

Needs Python 3.9+ and macOS or Linux (`fcntl`).

```sh
git clone https://github.com/DanielAckroyd/claude-pet ~/.claude/pet
ln -s ~/.claude/pet/pet_cli.py ~/.local/bin/pet   # or anywhere on your PATH
```

**Statusline.** If you already have a Python statusline script, add this near the start. It never raises, and prints nothing if anything goes wrong:

```python
import os, sys
try:
    sys.path.insert(0, os.path.expanduser("~/.claude/pet"))
    import petlib
    pet = petlib.segment(data, {  # data = the JSON Claude Code sends on stdin
        "good": "\033[38;5;42m", "meh": "\033[38;5;179m", "bad": "\033[38;5;203m", "reset": "\033[0m",
    })
    if pet:
        segments.insert(0, pet)
except Exception:
    pass
```

If you don't have one, use the bundled minimal one (pet, model, context %) in `~/.claude/settings.json`:

```json
"statusLine": {
  "type": "command",
  "command": "python3 ~/.claude/pet/pet_statusline.py",
  "refreshInterval": 5000
}
```

A refresh interval keeps the animation and decay ticking between messages.

**Hooks**, for auto-compact penalties and clean-wrap bonuses. Add to `hooks` in `~/.claude/settings.json`:

```json
"PreCompact": [{ "hooks": [{ "type": "command", "command": "python3 ~/.claude/pet/hook.py", "async": true, "timeout": 5 }] }],
"SessionEnd": [{ "hooks": [{ "type": "command", "command": "python3 ~/.claude/pet/hook.py", "async": true, "timeout": 5 }] }]
```

Your pet hatches the first time the statusline renders.

## Commands

```
pet              stat sheet: face, level, stats, path, streak, today, recent events
pet log [n]      recent events
pet name NEW     rename
pet tree         the evolution paths
pet devolve      drop back a tier on purpose (asks first; -y skips the prompt)
pet glyphs       every form × mood × frame, to check your terminal renders them evenly
```

There's no reset command. Delete `~/.claude/pet/state.json` if you really mean it.

## Notes

- **Tuning:** all the numbers live in the `TUNING` dict at the top of `petlib.py`, including work hours, decay rates, XP and level curve.
- **Multiple panes:** they share state safely. Updates take an `flock`, and a pane that can't get the lock renders read-only. Per-session baselines mean lines are never counted twice.
- **Commits:** read from each repo you're working in with `git log --branches`, at most once a minute per repo, matched exactly on your `user.email`. Amends and rebases don't count twice.
- **Kill switch:** `PET_DISABLE=1`.
- **Tests:** `python3 -m unittest test_petlib.py`. They use a temp `PET_HOME`, so they never touch your real pet.
- **Glyph width:** if a face looks misaligned in your terminal, `pet glyphs` shows which glyph is at fault.
