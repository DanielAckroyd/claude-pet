package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/DanielAckroyd/claude-pet/internal/pet"
)

const (
	reset = "\033[0m"
	dim   = "\033[2m"
	bold  = "\033[1m"
)

func bar(v float64) string {
	const n = 20

	full := int(math.Round(v / 100 * n))

	col := pet.ANSI()["good"]
	if v < 30 {
		col = pet.ANSI()["bad"]
	} else if v < 60 {
		col = pet.ANSI()["meh"]
	}

	return fmt.Sprintf("%s%s%s%s%s %5.1f", col, strings.Repeat("█", full), dim, strings.Repeat("░", n-full), reset, v)
}

func when(ts float64) string {
	return time.Unix(int64(ts), 0).In(pet.Zone()).Format("Mon 02 Jan 15:04")
}

func pad(text string, n int) string {
	if w := pet.Width(text); w < n {
		return text + strings.Repeat(" ", n-w)
	}

	return text
}

func moodColor(key string, shiny bool) string {
	for _, m := range pet.Moods() {
		if m.Key == key {
			if shiny && m.Color == "good" {
				return pet.Shiny
			}

			return pet.ANSI()[m.Color]
		}
	}

	return ""
}

func moodWord(key string) string {
	for _, m := range pet.Moods() {
		if m.Key == key {
			return m.Word
		}
	}

	return key
}

func sheet() {
	s := pet.ReadState()
	if s == nil {
		fmt.Println("No pet yet. It hatches the first time a Claude statusline renders " +
			"(run 'claude-pet setup' if you haven't).")

		return
	}

	now := pet.Now()
	mk := s.MoodOf(now)
	col := moodColor(mk, s.Look.Shiny)
	streak := s.CurrentStreak(now)

	sheetFace(s, mk, col, streak)
	sheetIdentity(s, mk, col, now)
	sheetLevel(s)
	sheetStats(s)
	sheetDays(s, streak, now)
	sheetRecent()
}

func crownFor(streak int) string {
	if streak >= int(pet.T.CrownStreak) {
		return pet.Crown
	}

	return ""
}

func sheetFace(s *pet.State, mood, col string, streak int) {
	face := pet.Face(s.Form, mood, false, s.Look.Eyes) + s.Look.Charm + crownFor(streak)
	w := pet.Width(face) + 8
	fmt.Printf("  %s╭%s╮%s\n", col, strings.Repeat("─", w), reset)
	fmt.Printf("  %s│    %s    │%s\n", col, face, reset)
	fmt.Printf("  %s╰%s╯%s\n", col, strings.Repeat("─", w), reset)
}

func sheetIdentity(s *pet.State, mood, col string, now float64) {
	shiny := ""
	if s.Look.Shiny {
		shiny = pet.Shiny + "shiny" + reset + " "
	}

	fmt.Printf("\n  %s%s%s the %s%s · %s%s%s · %dd old\n",
		bold, s.Name, reset, shiny, s.Form, col, moodWord(mood), reset, int(now-s.BornAt)/86400)

	bits := []string{s.Look.Eyes + " eyes"}
	if s.Look.Charm != "" {
		bits = append(bits, "holding "+s.Look.Charm)
	}

	if s.Devolved > 0 {
		bits = append(bits, fmt.Sprintf("devolved %d×", int(s.Devolved)))
	}

	fmt.Printf("  %s%s%s\n", dim, strings.Join(bits, " · "), reset)
}

func sheetLevel(s *pet.State) {
	xp, lvl := s.Stats.XP, pet.Level(s.Stats.XP)
	lo, hi := pet.XPForLevel(lvl), pet.XPForLevel(lvl+1)

	frac := 1.0
	if hi > lo {
		frac = (xp - lo) / (hi - lo)
	}

	f := int(frac * 20)
	fmt.Printf("  level %s%d%s  %s%s%s%s%s  %d xp (%d to L%d)\n\n", bold, lvl, reset, pet.ANSI()["good"],
		strings.Repeat("▰", f), dim, strings.Repeat("▱", 20-f), reset, int(xp), int(hi-xp), lvl+1)
}

func sheetStats(s *pet.State) {
	fmt.Printf("  %-7s%s\n  %-7s%s\n  %-7s%s\n",
		"fed", bar(s.Stats.Fed), "fit", bar(s.Stats.Fit), "rested", bar(s.Stats.Rested))
	pathLine(s)

	if s.Wilt > 0 {
		fmt.Printf("  %swilting %.0f/%g active min — devolves at %g; get fit and rested back over %g to recover%s\n",
			pet.ANSI()["bad"], s.Wilt, pet.T.WiltMinutes, pet.T.WiltMinutes, pet.T.WiltRecover, reset)
	}
}

func sheetDays(s *pet.State, streak int, now float64) {
	plural := "s"
	if streak == 1 {
		plural = ""
	}

	cr := ""
	if crownFor(streak) != "" {
		cr = " " + pet.Crown
	}

	fmt.Printf("\n  streak  %d workday%s%s\n", streak, plural, cr)

	d := s.Days[pet.DateKey(now)]
	if d == nil {
		d = &pet.Day{}
	}

	fmt.Printf("  today   %d commits · %d lines · %d xp · %d auto-compacts · %d clean wraps\n",
		int(d.Commits), int(d.Lines), int(d.XP), int(d.AutoCompacts), int(d.CleanWraps))

	if k, bd := s.BestDay(); bd != nil && bd.XP > 0 {
		fmt.Printf("  best    %s: %d xp, %d commits\n", k, int(bd.XP), int(bd.Commits))
	}
}

func sheetRecent() {
	evs := pet.ReadEvents(5)
	if len(evs) == 0 {
		return
	}

	fmt.Printf("\n  %srecent%s\n", dim, reset)

	for _, e := range evs {
		fmt.Printf("  %s%s%s  %s\n", dim, when(e.TS), reset, e.Msg)
	}
}

func pathLine(s *pet.State) {
	sc := s.TraitScores()

	parts := make([]string, 0, len(pet.Traits()))
	for _, t := range pet.Traits() {
		parts = append(parts, fmt.Sprintf("%s %d", t, int(math.Round(sc[t]*100))))
	}

	traits := strings.Join(parts, " · ")

	var nxt string

	switch fins, species := pet.Finals()[s.Form]; {
	case s.Form == "hatchling":
		lean := s.SpeciesPath()
		nxt = fmt.Sprintf("leaning %s → %s at L%g", lean, pet.Species()[lean], pet.T.SpeciesLevel)
	case species:
		lean := s.DominantTrait()
		nxt = fmt.Sprintf("leaning %s → %s at L%g", lean, fins[lean], pet.T.FinalLevel)
	default:
		nxt = "final form"
	}

	fmt.Printf("\n  path    %s  %s%s%s\n", traits, dim, nxt, reset)
}

func showLog(n int) {
	for _, e := range pet.ReadEvents(n) {
		fmt.Printf("%s%s%s  %-13s %s\n", dim, when(e.TS), reset, e.Kind, e.Msg)
	}
}

func withLock(fn func(s *pet.State, now float64, events *[]pet.Event)) *pet.State {
	release, err := pet.Lock(2 * time.Second)
	if err != nil || release == nil {
		fail("pet is busy (lock held), try again")
	}
	defer release()

	now := pet.Now()

	var events []pet.Event

	s := pet.LoadOrHatch(now, &events)
	fn(s, now, &events)

	if err := pet.SaveState(s); err != nil {
		fail("couldn't save: " + err.Error())
	}

	pet.AppendEvents(events)

	return s
}

func rename(name string) {
	var old string

	withLock(func(s *pet.State, now float64, events *[]pet.Event) {
		old = s.Name
		s.Name = name
		*events = append(*events, pet.Event{TS: now, Kind: "rename", Msg: old + " is now " + name})
	})
	fmt.Printf("%s is now %s.\n", old, name)
}

func devolve(yes bool) {
	s := pet.ReadState()
	if s == nil {
		fail("No pet yet.")
	}

	back, keep, ok := s.DevolvePreview()
	if !ok {
		fail(s.Name + " is already a hatchling, nothing to devolve.")
	}

	xp := s.Stats.XP
	fmt.Printf("%s: %s → %s, L%d → L%d (%d xp lost).\n",
		s.Name, s.Form, back, pet.Level(xp), pet.Level(keep), int(xp-keep))
	fmt.Println("Its next evolution re-picks the branch from how you work then.")

	if !yes {
		fmt.Print("Devolve? [y/N] ")

		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "y" {
			fmt.Println("Left as is.")
			return
		}
	}

	s = withLock(func(s *pet.State, now float64, events *[]pet.Event) {
		s.Devolve(now, events, "chose to devolve")
	})
	fmt.Printf("%s is a %s again: %s\n", s.Name, s.Form, pet.Face(s.Form, "content", false, s.Look.Eyes))
}

func tree() {
	fmt.Printf("  %s hatchling  (L1–%g)\n", pet.Face("hatchling", "content", false, ""), pet.T.SpeciesLevel-1)

	for _, trait := range pet.SpeciesOrd() {
		sp := pet.Species()[trait]

		hint := ""
		if trait == "balanced" {
			hint = "  — hidden: all three traits high and even"
		}

		face := pad(pet.Face(sp, "content", false, ""), 9)
		fmt.Printf("   ├─ %-8s %s %s  (L%g)%s\n", trait, face, sp, pet.T.SpeciesLevel, hint)

		for _, t2 := range pet.Traits() {
			fin := pet.Finals()[sp][t2]
			face := pad(pet.Face(fin, "content", false, ""), 10)
			fmt.Printf("   │    └─ %-8s %s %s  (L%g)\n", t2, face, fin, pet.T.FinalLevel)
		}
	}
}

// glyphs: the right-hand │ must line up on every row. Anything jutting out is a bad glyph.
func glyphs() {
	type row struct{ label, face string }

	var rows []row

	for _, f := range pet.Forms() {
		for _, m := range pet.Moods() {
			rows = append(rows, row{f.Name + "/" + m.Key, pet.Face(f.Name, m.Key, false, "")},
				row{f.Name + "/" + m.Key + "/blink", pet.Face(f.Name, m.Key, true, "")})
		}

		rows = append(rows, row{f.Name + "/crown", pet.Face(f.Name, "thriving", false, "") + pet.Crown})
		for _, c := range pet.Cameos() {
			rows = append(rows, row{f.Name + "/cameo", pet.Face(f.Name, "content", false, "") + " " + c})
		}
	}

	seen := map[string]bool{}
	for _, e := range pet.Eyes() {
		if seen[e] {
			continue
		}

		seen[e] = true
		for _, n := range []string{"hatchling", "bear", "great owl", "kitsune"} {
			rows = append(rows, row{"eyes " + e + "/" + n, pet.Face(n, "content", false, e)})
		}
	}

	for _, c := range pet.Charms() {
		if c != "" && !seen[c] {
			seen[c] = true
			rows = append(rows, row{"charm " + c, pet.Face("tiger", "content", false, "") + c + pet.Crown})
		}
	}

	wmax := 0
	for _, r := range rows {
		if w := pet.Width(r.face); w > wmax {
			wmax = w
		}
	}

	fmt.Println("Right edge should be a straight line. If you use tmux/zellij/etc., check inside that too.")
	fmt.Println()

	for _, r := range rows {
		fmt.Printf("  %s│%s│\n", pad(r.label, 28), pad(r.face, wmax))
	}
}
