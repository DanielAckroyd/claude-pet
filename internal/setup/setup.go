// Package setup wires claude-pet into Claude Code's settings.json, and takes it back out.
package setup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DanielAckroyd/claude-pet/internal/pet"
)

const refreshMs = 5000

var hookEvents = []string{"PreCompact", "SessionEnd"}

// Options are the setup choices. Nil/empty means "use the default".
type Options struct {
	Custom, Uninstall, DryRun bool
	Hours                     *[2]float64
	Statusline                string // wrap | replace | skip
	Hooks                     *bool
	Name                      string
	Interactive               bool
	In                        io.Reader
	Out                       io.Writer
	Bin                       string // command that runs this binary; resolved if empty
	Version                   string
}

func settingsPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "settings.json")
}

func recordPath() string { return filepath.Join(pet.Home(), "install.json") }

func tilde(p string) string {
	h, _ := os.UserHomeDir()
	if h != "" && (p == h || strings.HasPrefix(p, h+string(os.PathSeparator))) {
		return "~" + p[len(h):]
	}
	return p
}

// ResolveBin prefers the claude-pet on PATH (brew's stable symlink survives upgrades),
// falling back to this executable's own path.
func ResolveBin() string {
	if p, err := exec.LookPath("claude-pet"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "claude-pet"
}

func loadSettings() (*Object, error) {
	b, err := os.ReadFile(settingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return NewObject(), nil
	}
	if err != nil {
		return nil, err
	}
	return ParseObject(b)
}

func saveSettings(s *Object, dry bool) (string, error) {
	if dry {
		return "", nil
	}
	p := settingsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	backup := ""
	if old, err := os.ReadFile(p); err == nil {
		backup = fmt.Sprintf("%s.bak-claude-pet-%s", p, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, old, 0o644); err != nil {
			return "", err
		}
	}
	tmp := p + ".tmp-claude-pet"
	if err := os.WriteFile(tmp, s.Pretty(), 0o644); err != nil {
		return "", err
	}
	return backup, os.Rename(tmp, p)
}

// --- recognising our own entries --------------------------------------------

func isOurStatusline(cmd string) bool {
	return (strings.Contains(cmd, "claude-pet") && strings.Contains(cmd, " statusline")) ||
		strings.Contains(cmd, "pet_statusline.py") || strings.Contains(cmd, "petlib") // pre-0.2 Python install
}

func isOurHook(cmd string) bool {
	return (strings.Contains(cmd, "claude-pet") && strings.HasSuffix(strings.TrimSpace(cmd), " hook")) ||
		(strings.Contains(cmd, "hook.py") && strings.Contains(cmd, "pet"))
}

// statuslineHasPet is true if the statusline already shows the pet: ours, or hand-built into a script.
func statuslineHasPet(cmd string) bool {
	if isOurStatusline(cmd) {
		return true
	}
	for _, tok := range ShellSplit(cmd) {
		p := tok
		if strings.HasPrefix(p, "~/") {
			h, _ := os.UserHomeDir()
			p = filepath.Join(h, p[2:])
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() || fi.Size() > 1_000_000 {
			continue
		}
		if b, err := os.ReadFile(p); err == nil {
			body := string(b)
			if strings.Contains(body, "petlib") || strings.Contains(body, "pet_statusline") ||
				strings.Contains(body, "claude-pet") {
				return true
			}
		}
	}
	return false
}

// unwrap returns the original command from a `... --wrap '<cmd>'` line.
func unwrap(cmd string) string {
	parts := ShellSplit(cmd)
	for i, p := range parts {
		if p == "--wrap" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func rawList(raw json.RawMessage) []json.RawMessage {
	var l []json.RawMessage
	json.Unmarshal(raw, &l)
	return l
}

func ourHookEntry(bin string) json.RawMessage {
	h := NewObject()
	h.SetValue("type", "command")
	h.SetValue("command", ShellQuote(bin)+" hook")
	h.SetValue("async", true)
	h.SetValue("timeout", 5)
	m := NewObject()
	m.SetValue("hooks", []json.RawMessage{mustMarshal(h)})
	return mustMarshal(m)
}

// editHooks walks every hook object under our events. fn returns (keep, replacement or nil).
// Everything is re-encoded through Object, so key order survives.
func editHooks(s *Object, fn func(ev string, h *Object) (bool, *Object)) (touched map[string]bool, found map[string]bool) {
	touched, found = map[string]bool{}, map[string]bool{}
	if !s.Has("hooks") {
		return
	}
	hooks := s.Child("hooks")
	for _, ev := range hookEvents {
		if !hooks.Has(ev) {
			continue
		}
		var list []json.RawMessage
		for _, mr := range rawList(hooks.Raw(ev)) {
			m, err := ParseObject(mr)
			if err != nil || !m.Has("hooks") {
				list = append(list, mr)
				continue
			}
			var inner []json.RawMessage
			changed := false
			for _, hr := range rawList(m.Raw("hooks")) {
				h, err := ParseObject(hr)
				if err != nil || !isOurHook(h.String("command")) {
					inner = append(inner, hr)
					continue
				}
				found[ev] = true
				keep, repl := fn(ev, h)
				switch {
				case !keep:
					changed = true
				case repl != nil:
					inner = append(inner, mustMarshal(repl))
					changed = true
				default:
					inner = append(inner, hr)
				}
			}
			if !changed {
				list = append(list, mr)
				continue
			}
			touched[ev] = true
			if len(inner) > 0 {
				m.SetValue("hooks", inner)
				list = append(list, mustMarshal(m))
			}
		}
		if len(list) > 0 {
			hooks.SetValue(ev, list)
		} else {
			hooks.Delete(ev)
		}
	}
	if hooks.Len() == 0 {
		s.Delete("hooks")
	} else {
		s.SetValue("hooks", hooks)
	}
	return
}

// addHooks adds our hook to each event, or updates an older one in place. Returns what changed.
func addHooks(s *Object, bin string) (added, updated []string) {
	want := ShellQuote(bin) + " hook"
	touched, found := editHooks(s, func(ev string, h *Object) (bool, *Object) {
		if h.String("command") == want {
			return true, nil
		}
		h.SetValue("command", want)
		return true, h
	})
	var missing []string
	for _, ev := range hookEvents {
		if touched[ev] {
			updated = append(updated, ev)
		}
		if !found[ev] {
			missing = append(missing, ev)
		}
	}
	if len(missing) > 0 {
		hooks := s.Child("hooks")
		for _, ev := range missing {
			hooks.SetValue(ev, append(rawList(hooks.Raw(ev)), ourHookEntry(bin)))
			added = append(added, ev)
		}
		s.SetValue("hooks", hooks)
	}
	return
}

func removeHooks(s *Object) []string {
	touched, _ := editHooks(s, func(string, *Object) (bool, *Object) { return false, nil })
	var removed []string
	for _, ev := range hookEvents {
		if touched[ev] {
			removed = append(removed, ev)
		}
	}
	return removed
}

// --- the statusline ----------------------------------------------------------

type record struct {
	StatuslineBefore json.RawMessage `json:"statusline_before,omitempty"`
	HadStatusline    bool            `json:"had_statusline"`
	StatuslineAfter  string          `json:"statusline_after,omitempty"`
	Version          string          `json:"version"`
	InstalledAt      string          `json:"installed_at"`
}

func loadRecord() (*record, bool) {
	rec := &record{}
	b, err := os.ReadFile(recordPath())
	if err != nil {
		return rec, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return rec, false
	}
	rec.StatuslineBefore = m["statusline_before"]
	if json.Unmarshal(m["had_statusline"], &rec.HadStatusline) != nil {
		rec.HadStatusline = len(rec.StatuslineBefore) > 0 && string(rec.StatuslineBefore) != "null"
	}
	if json.Unmarshal(m["statusline_after"], &rec.StatuslineAfter) != nil {
		var after map[string]any
		if json.Unmarshal(m["statusline_after"], &after) == nil {
			rec.StatuslineAfter, _ = after["command"].(string)
		}
	}
	return rec, true
}

func planStatusline(s *Object, mode, bin string, rec *record) (string, bool) {
	cur := s.Child("statusLine")
	cmd := cur.String("command")
	if mode == "skip" {
		return "statusline: left alone (see the README to add the pet by hand)", false
	}
	legacy := strings.Contains(cmd, "pet_statusline.py")
	if statuslineHasPet(cmd) && !legacy {
		return "statusline: already shows the pet, left alone", false
	}
	orig := cmd
	switch {
	case !legacy:
		rec.HadStatusline = s.Has("statusLine")
		rec.StatuslineBefore = s.Raw("statusLine")
	case unwrap(cmd) != "":
		orig = unwrap(cmd) // upgrading a pre-0.2 wrap keeps whatever it wrapped
		before := s.Child("statusLine")
		before.SetValue("command", orig)
		rec.StatuslineBefore, _ = before.MarshalJSON()
		rec.HadStatusline = true
	default:
		orig = ""
		rec.HadStatusline = len(rec.StatuslineBefore) > 0 && string(rec.StatuslineBefore) != "null"
	}
	var msg string
	if mode == "wrap" && orig != "" {
		cur.SetValue("command", ShellQuote(bin)+" statusline --wrap "+ShellQuote(orig))
		msg = "statusline: pet added to the front of your existing statusline"
	} else {
		cur = NewObject()
		cur.SetValue("type", "command")
		cur.SetValue("command", ShellQuote(bin)+" statusline")
		msg = "statusline: set to the bundled one (pet, model, context %)"
		if cmd != "" && !legacy {
			msg = "statusline: replaced with the bundled one (pet, model, context %)"
		}
	}
	if legacy {
		msg = "statusline: upgraded from the Python version"
	}
	if !cur.Has("refreshInterval") {
		cur.SetValue("refreshInterval", refreshMs)
		msg += fmt.Sprintf(", refreshing every %ds so it animates", refreshMs/1000)
	}
	s.SetValue("statusLine", cur)
	rec.StatuslineAfter = cur.String("command")
	return msg, true
}

// --- prompts -----------------------------------------------------------------

type prompter struct {
	r   *bufio.Reader
	out io.Writer
}

func (p prompter) ask(q, def string, choices ...string) string {
	for {
		fmt.Fprintf(p.out, "%s \033[2m[%s]\033[0m ", q, def)
		line, err := p.r.ReadString('\n')
		ans := strings.TrimSpace(line)
		if ans == "" {
			ans = def
		}
		if err != nil && line == "" {
			fmt.Fprintln(p.out)
			return def
		}
		if len(choices) == 0 {
			return ans
		}
		for _, c := range choices {
			if strings.EqualFold(ans, c) {
				return strings.ToLower(ans)
			}
		}
		fmt.Fprintf(p.out, "  pick one of: %s\n", strings.Join(choices, ", "))
	}
}

// ParseHours reads "9-17" style hours.
func ParseHours(s string) (*[2]float64, error) {
	a, b, ok := strings.Cut(strings.ReplaceAll(s, " ", ""), "-")
	x, e1 := strconv.ParseFloat(a, 64)
	y, e2 := strconv.ParseFloat(b, 64)
	if !ok || e1 != nil || e2 != nil || x < 0 || y > 24 || x >= y {
		return nil, errors.New("hours look like 9-17")
	}
	return &[2]float64{x, y}, nil
}

const guide = `How the pet works
  fed     goes up when you commit, drops slowly over work hours
  fit     clean wraps (ending a session that shipped, under 70% context) raise it; auto-compacts hurt
  rested  follows your 5h rate limit: happy under 50%
  It evolves at L5 and L15 into different creatures depending on how you work,
  and only ever counts work hours, so evenings and weekends are free.
`

// Run does the install (or uninstall) and prints what it did.
func Run(o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Bin == "" {
		o.Bin = ResolveBin()
	}
	if o.In == nil {
		o.In = os.Stdin
	}
	if o.Uninstall {
		return uninstall(o)
	}
	s, err := loadSettings()
	if err != nil {
		return fmt.Errorf("couldn't read %s (%v). Nothing was changed. Fix it, or follow the manual steps in the README", tilde(settingsPath()), err)
	}
	curCmd := s.Child("statusLine").String("command")
	p := prompter{bufio.NewReader(o.In), o.Out}

	if o.Custom && o.Interactive {
		fmt.Fprint(o.Out, guide+"\n")
		fmt.Fprintf(o.Out, "A few questions \033[2m(Enter keeps the default)\033[0m\n\n")
		for o.Hours == nil {
			h, err := ParseHours(p.ask("Work hours, Mon to Fri, 24h (decay only counts these)", "9-17"))
			if err != nil {
				fmt.Fprintln(o.Out, "  like 9-17 or 8-16")
				continue
			}
			o.Hours = h
		}
		if o.Statusline == "" {
			switch {
			case statuslineHasPet(curCmd) && !strings.Contains(curCmd, "pet_statusline.py"):
				o.Statusline = "skip"
			case curCmd != "":
				fmt.Fprintf(o.Out, "  Your statusline: \033[2m%s\033[0m\n", curCmd)
				o.Statusline = p.ask("Add the pet to it (wrap), replace it with the bundled one, or skip?",
					"wrap", "wrap", "replace", "skip")
			default:
				o.Statusline = p.ask("No statusline yet. Use the bundled one, or skip?", "replace", "replace", "skip")
			}
		}
		if o.Hooks == nil {
			y := p.ask("Add hooks for auto-compact and clean-wrap scoring?", "y", "y", "n") == "y"
			o.Hooks = &y
		}
		if o.Name == "" {
			if n := p.ask("Name your pet", "random"); !strings.EqualFold(n, "random") {
				o.Name = n
			}
		}
		fmt.Fprintln(o.Out)
	}

	mode := o.Statusline
	if mode == "" {
		mode = "replace"
		if curCmd != "" {
			mode = "wrap"
		}
	}
	hooks := o.Hooks == nil || *o.Hooks

	rec, _ := loadRecord()
	var lines []string
	msg, slChanged := planStatusline(s, mode, o.Bin, rec)
	lines = append(lines, msg)
	var added, updated []string
	if hooks {
		added, updated = addHooks(s, o.Bin)
		switch {
		case len(added) > 0:
			lines = append(lines, "hooks: added "+strings.Join(added, ", "))
		case len(updated) > 0:
			lines = append(lines, "hooks: upgraded "+strings.Join(updated, ", "))
		default:
			lines = append(lines, "hooks: already there")
		}
	} else {
		lines = append(lines, "hooks: skipped (no auto-compact or clean-wrap scoring)")
	}
	backup := ""
	if slChanged || len(added)+len(updated) > 0 {
		if backup, err = saveSettings(s, o.DryRun); err != nil {
			return err
		}
	}
	if o.Hours != nil {
		if err := writeConfig(map[string]any{"work_start": o.Hours[0], "work_end": o.Hours[1]}, o.DryRun); err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("work hours: %g:00 to %g:00, Mon to Fri", o.Hours[0], o.Hours[1]))
	}
	if o.Name != "" {
		if !o.DryRun {
			namePet(o.Name)
		}
		lines = append(lines, "name: "+o.Name)
	}
	if !o.DryRun {
		rec.Version, rec.InstalledAt = o.Version, time.Now().Format("2006-01-02 15:04")
		os.MkdirAll(pet.Home(), 0o755)
		if b, err := json.MarshalIndent(rec, "", "  "); err == nil {
			os.WriteFile(recordPath(), b, 0o644)
		}
	}

	if o.DryRun {
		fmt.Fprintln(o.Out, "Would do (dry run):")
	} else {
		fmt.Fprintf(o.Out, "\033[38;5;42mclaude-pet %s set up\033[0m\n", o.Version)
	}
	for _, l := range lines {
		fmt.Fprintf(o.Out, "  \033[38;5;42m✓\033[0m %s\n", l)
	}
	if backup != "" {
		fmt.Fprintf(o.Out, "  \033[2msettings backup: %s\033[0m\n", tilde(backup))
	}
	fmt.Fprintf(o.Out, `
Change it later
  work hours, tuning   %s  (any number from the tuning list in the README)
  placement, hooks     claude-pet setup --custom
  switch it off        PET_DISABLE=1
  remove               claude-pet setup --uninstall

Start a new Claude Code session and your pet hatches. Then try claude-pet, claude-pet tree and claude-pet glyphs.
`, tilde(filepath.Join(pet.Home(), "config.json")))
	return nil
}

func writeConfig(updates map[string]any, dry bool) error {
	p := filepath.Join(pet.Home(), "config.json")
	cfg := NewObject()
	if b, err := os.ReadFile(p); err == nil {
		if c, err := ParseObject(b); err == nil {
			cfg = c
		}
	}
	for _, k := range []string{"work_start", "work_end"} {
		if v, ok := updates[k]; ok {
			cfg.SetValue(k, v)
		}
	}
	if dry {
		return nil
	}
	if err := os.MkdirAll(pet.Home(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, cfg.Pretty(), 0o644)
}

func namePet(name string) {
	release, err := pet.Lock(2 * time.Second)
	if err != nil || release == nil {
		return
	}
	defer release()
	now := pet.Now()
	var events []pet.Event
	s := pet.LoadOrHatch(now, &events)
	if s.Name != name {
		events = append(events, pet.Event{TS: now, Kind: "rename", Msg: s.Name + " is now " + name})
		s.Name = name
	}
	pet.SaveState(s)
	pet.AppendEvents(events)
}

func uninstall(o Options) error {
	s, err := loadSettings()
	if err != nil {
		return fmt.Errorf("couldn't read %s (%v). Nothing was changed", tilde(settingsPath()), err)
	}
	rec, haveRec := loadRecord()
	var lines []string
	cmd := s.Child("statusLine").String("command")
	changed := false
	if isOurStatusline(cmd) {
		changed = true
		switch {
		case haveRec && rec.StatuslineAfter == cmd && rec.HadStatusline:
			s.Set("statusLine", rec.StatuslineBefore)
			lines = append(lines, "statusline: restored to what you had before")
		case haveRec && rec.StatuslineAfter == cmd:
			s.Delete("statusLine")
			lines = append(lines, "statusline: removed (there wasn't one before)")
		case unwrap(cmd) != "":
			sl := s.Child("statusLine")
			sl.SetValue("command", unwrap(cmd))
			s.SetValue("statusLine", sl)
			lines = append(lines, "statusline: restored to what you had before")
		default:
			s.Delete("statusLine")
			lines = append(lines, "statusline: removed")
		}
	} else {
		lines = append(lines, "statusline: doesn't run claude-pet, left alone (if you added the pet by hand, remove it there)")
	}
	removed := removeHooks(s)
	if len(removed) > 0 {
		lines = append(lines, "hooks: removed "+strings.Join(removed, ", "))
	} else {
		lines = append(lines, "hooks: none found")
	}
	backup := ""
	if changed || len(removed) > 0 {
		if backup, err = saveSettings(s, o.DryRun); err != nil {
			return err
		}
	}
	if !o.DryRun {
		os.Remove(recordPath())
	}
	if o.DryRun {
		fmt.Fprintln(o.Out, "Would do (dry run):")
	} else {
		fmt.Fprintln(o.Out, "claude-pet removed from Claude Code")
	}
	for _, l := range lines {
		fmt.Fprintf(o.Out, "  ✓ %s\n", l)
	}
	if backup != "" {
		fmt.Fprintf(o.Out, "  settings backup: %s\n", tilde(backup))
	}
	fmt.Fprintf(o.Out, "\nYour pet and its history are still in %s. Delete that folder to say goodbye for good,\n"+
		"and uninstall the binary the way you installed it (e.g. brew uninstall claude-pet).\n", tilde(pet.Home()))
	return nil
}
