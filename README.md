<p align="center"><img src="docs/header.png" alt="claude-pet" width="100%"></p>

A little pet that lives in your Claude Code statusline. It does well when you use Claude well: shipping commits, keeping context lean, wrapping sessions cleanly, not redlining your rate limits. It sulks when neglected, and evolves into different creatures depending on how you work.

<p align="center"><img src="docs/screenshot.png" alt="Four statuslines showing the pet in different moods, and the pet stat sheet" width="820"></p>

One pet is shared across every Claude session on the machine, so every pane shows the same face and the same animation frame. Everything is local: state lives in a JSON file, the only external command is a local `git log`, and nothing touches the network.

## Install with Claude Code

Paste this into Claude Code:

> Install claude-pet from https://github.com/DanielAckroyd/claude-pet by following the "Manual install" section of its README. Clone it to `~/.claude/pet`. Look at my current `statusLine` setting and pick the matching option. Merge the hooks into my existing `~/.claude/settings.json` without removing anything already there, and back the file up first. Then run `pet glyphs` and show me the output.

Restart Claude Code (or start a new session) afterwards and the pet hatches on the first statusline render.

## Manual install

**Requirements:** Claude Code, git, Python 3.9+ (the macOS system `python3` is fine), macOS, Linux or WSL. It doesn't need a particular terminal, multiplexer or font. If a face looks misaligned, `pet glyphs` shows which glyph your font is struggling with.

**1. Clone and put `pet` on your PATH**

```sh
git clone https://github.com/DanielAckroyd/claude-pet ~/.claude/pet
mkdir -p ~/.local/bin && ln -sf ~/.claude/pet/pet_cli.py ~/.local/bin/pet
```

If `~/.local/bin` isn't on your PATH, link it somewhere that is, or run `python3 ~/.claude/pet/pet_cli.py` directly.

**2. Add it to your statusline.** Pick the option that matches your `statusLine` in `~/.claude/settings.json`.

*No statusline yet:* use the bundled one (pet, model, context %).

```json
"statusLine": {
  "type": "command",
  "command": "python3 ~/.claude/pet/pet_statusline.py",
  "refreshInterval": 5000
}
```

*A shell script (bash, zsh, or anything else):* read stdin once, then pipe it through `--segment`, which prints only the pet.

```sh
input=$(cat)
pet=$(printf '%s' "$input" | python3 ~/.claude/pet/pet_statusline.py --segment)
# ...then put "$pet" wherever you like in your output, e.g.
printf '%s │ %s' "$pet" "$(echo "$input" | jq -r .model.display_name)"
```

*A Python script:* import it directly, which saves starting a second Python.

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

Either way, set `"refreshInterval": 5000` on your `statusLine` so the animation and decay keep ticking between messages. None of these ever raise; if anything goes wrong the pet just doesn't show.

**3. Add the hooks** for auto-compact penalties and clean-wrap bonuses. Merge these into `hooks` in `~/.claude/settings.json`, keeping any hooks you already have:

```json
"PreCompact": [{ "hooks": [{ "type": "command", "command": "python3 ~/.claude/pet/hook.py", "async": true, "timeout": 5 }] }],
"SessionEnd": [{ "hooks": [{ "type": "command", "command": "python3 ~/.claude/pet/hook.py", "async": true, "timeout": 5 }] }]
```

## How it scores

| Stat | Goes up | Goes down |
|---|---|---|
| **fed** | Commits (+15), lines changed | Slowly over work hours |
| **fit** | Clean wraps: ending a session that shipped something while context is under 70% | Auto-compacts (-20, and it's bloated for 30 min), working in a session above 80% context |
| **rested** | Tracks your 5h rate limit usage, full while you're under 50% | Falls as you approach 100% |
| **xp** | Commits (x1.5 if context is under 50%), lines changed | Only when it devolves |

Decay only counts work hours (Mon to Fri, 09:00 to 17:00 local by default), so evenings and weekends are free. Manual `/compact` is neutral. Stats floor at 5 and the pet never dies. A streak counts weekdays with at least one commit that ended with fit ≥ 50, and at 5+ days it gets a crown ♔.

Commits are counted from the repo you're working in, matched on that repo's `user.email`, so make sure it's set. Rate limit data only comes through on Claude subscription plans. On an API key, rested sits at a neutral 80 and the steady trait won't build.

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

## Config

Put overrides in `~/.claude/pet/config.json`. It's gitignored, so updates won't clobber it. Any numeric key from the `TUNING` dict at the top of `petlib.py` works, e.g. different work hours:

```json
{ "work_start": 8, "work_end": 16 }
```

Set `PET_DISABLE=1` to switch the pet off without uninstalling.

## Updating and uninstalling

- **Update:** `git -C ~/.claude/pet pull`. Your pet, log and config are gitignored, so they're kept.
- **Uninstall:** remove the pet from your `statusLine` and the two hook entries, delete the `pet` symlink, then delete `~/.claude/pet`, which takes your pet with it.

## Notes

- **Multiple panes:** they share state safely. Updates take an `flock`, and a pane that can't get the lock renders read-only. Per-session baselines mean lines are never counted twice.
- **Commits:** read with `git log --branches`, at most once a minute per repo, matched exactly on `user.email`. Amends and rebases don't count twice.
- **Windows:** native Windows isn't supported (it uses `fcntl`), but WSL works.
- **Tests:** `python3 -m unittest test_petlib.py`. They use a temp `PET_HOME`, so they never touch your real pet.
