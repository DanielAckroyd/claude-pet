<p align="center"><img src="docs/header.png" alt="claude-pet" width="100%"></p>

A little pet that lives in your Claude Code statusline. It does well when you use Claude well: shipping commits, keeping context lean, wrapping sessions cleanly, not redlining your rate limits. It sulks when neglected, and evolves into different creatures depending on how you work.

<p align="center"><img src="docs/screenshot.png" alt="Four statuslines showing the pet in different moods, and the pet stat sheet" width="820"></p>

One pet is shared across every Claude session on the machine, so every pane shows the same face and the same animation frame. Everything is local: state lives in a JSON file, the only external command is a local `git log`, and nothing touches the network.

## Install

Needs Claude Code, git and Python 3.9+ (the macOS system `python3` is fine) on macOS, Linux or WSL. Any terminal works.

```sh
git clone https://github.com/DanielAckroyd/claude-pet ~/.claude/pet
python3 ~/.claude/pet/install.py
```

It asks **express** or **custom**:

- **Express:** sensible defaults, no questions. If you already have a statusline, the pet goes in front of it and your script stays untouched. Otherwise you get the bundled one (pet, model, context %). It also adds two hooks and a `pet` command in `~/.local/bin`.
- **Custom:** a quick guide, then a few questions: work hours, where the pet goes, hooks, where `pet` lives, and a name.

It backs up `~/.claude/settings.json` before touching it, only adds its own entries, and is safe to re-run. Start a new Claude Code session and your pet hatches.

**With Claude Code:** paste this in.

> Clone https://github.com/DanielAckroyd/claude-pet to `~/.claude/pet` and run `python3 ~/.claude/pet/install.py --express`. Show me the output.

For a custom install through Claude, ask it to pass the options as flags: `--hours 8-16`, `--statusline wrap|replace|skip`, `--no-hooks`, `--bin DIR` or `--no-bin`, `--name NAME`. Add `--dry-run` to preview first.

**Uninstall:** `python3 ~/.claude/pet/install.py --uninstall` puts your statusline and hooks back the way they were. Your pet stays in `~/.claude/pet` until you delete the folder.

<details>
<summary>Manual install, or adding the pet inside your own statusline script</summary>

The installer's wrap mode runs your statusline as a child process, which costs a Python start (about 25ms) per render. If you'd rather build the pet into your own script, do that and point the installer at `--statusline skip`.

*A shell script:* read stdin once, then pipe it through `--segment`, which prints only the pet.

```sh
input=$(cat)
pet=$(printf '%s' "$input" | python3 ~/.claude/pet/pet_statusline.py --segment)
printf '%s │ %s' "$pet" "$(echo "$input" | jq -r .model.display_name)"
```

*A Python script:* import it directly.

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

Set `"refreshInterval": 5000` on your `statusLine` so it animates between messages. None of these ever raise; if anything goes wrong the pet just doesn't show.

*Hooks*, merged into `hooks` in `~/.claude/settings.json`:

```json
"PreCompact": [{ "hooks": [{ "type": "command", "command": "python3 ~/.claude/pet/hook.py", "async": true, "timeout": 5 }] }],
"SessionEnd": [{ "hooks": [{ "type": "command", "command": "python3 ~/.claude/pet/hook.py", "async": true, "timeout": 5 }] }]
```

*The `pet` command:* `ln -s ~/.claude/pet/pet_cli.py ~/.local/bin/pet`, or anywhere on your PATH.

</details>

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

## Updating

`git -C ~/.claude/pet pull`. Your pet, log and config are gitignored, so they're kept.

## Notes

- **Multiple panes:** they share state safely. Updates take an `flock`, and a pane that can't get the lock renders read-only. Per-session baselines mean lines are never counted twice.
- **Commits:** read with `git log --branches`, at most once a minute per repo, matched exactly on `user.email`. Amends and rebases don't count twice.
- **Windows:** native Windows isn't supported (it uses `fcntl`), but WSL works.
- **Tests:** `python3 -m unittest test_petlib.py test_install.py`. They use a temp `PET_HOME`, so they never touch your real pet.
