package pet

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // Windows CI has no system zoneinfo
)

func init() {
	loc, err := time.LoadLocation("Australia/Melbourne") // DST starts Sun 4 Oct 2026
	if err != nil {
		panic(err)
	}
	Loc = loc
}

// env gives each test its own pet home and default tuning, and stubs git unless asked.
func env(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PET_HOME", dir)
	t.Setenv("PET_DISABLE", "")
	T = DefaultTuning()
	t.Cleanup(func() { T = DefaultTuning() })
	return dir
}

func ts(y, mo, d, h, mi int) float64 {
	return float64(time.Date(y, time.Month(mo), d, h, mi, 0, 0, Loc).Unix())
}

var mon = [3]int{2026, 9, 28} // a Monday

func monAt(h, mi int) float64 { return ts(mon[0], mon[1], mon[2], h, mi) }

func f(v float64) *float64 { return &v }

type pl struct {
	sid              string
	lines, ctx, five *float64
	cwd              string
}

func (p pl) m() map[string]any {
	d := map[string]any{"session_id": p.sid, "workspace": map[string]any{"current_dir": p.cwd}}
	if p.lines != nil {
		d["cost"] = map[string]any{"total_lines_added": *p.lines, "total_lines_removed": 0.0}
	}
	if p.ctx != nil {
		d["context_window"] = map[string]any{"used_percentage": *p.ctx}
	}
	if p.five != nil {
		d["rate_limits"] = map[string]any{"five_hour": map[string]any{"used_percentage": *p.five}}
	}
	return d
}

func fresh(now float64) *State {
	s := NewState(now)
	y := dayStart(at(now)).AddDate(0, 0, -1).Format("2006-01-02")
	s.Streak.Through = &y
	return s
}

func tick(s *State, now float64, ev *[]Event, data map[string]any) {
	s.Tick(now, ev, data, "", nil, false)
}

func hookTick(s *State, now float64, ev *[]Event, name string, data map[string]any) {
	s.Tick(now, ev, nil, name, data, false)
}

// work ticks once a minute with ctx changing: what real use looks like to the pet.
func work(s *State, start float64, minutes int, ev *[]Event, five float64, lines *float64) {
	for i := 0; i <= minutes*60; i += 60 {
		var ln *float64
		if lines != nil {
			ln = f(*lines + float64(i))
		}
		tick(s, start+float64(i), ev, pl{sid: "W", ctx: f(40 + float64((i/60)%2)), five: f(five), lines: ln}.m())
	}
}

func near(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func has(evs []Event, kind string) bool {
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// --- work clock --------------------------------------------------------------

func TestWorkClock(t *testing.T) {
	env(t)
	near(t, WorkMinutes(monAt(8, 0), monAt(18, 0)), 480, 1e-9, "weekday clipped")
	near(t, WorkMinutes(monAt(16, 0), ts(2026, 9, 29, 10, 0)), 120, 1e-9, "overnight")
	near(t, WorkMinutes(ts(2026, 10, 3, 9, 0), ts(2026, 10, 4, 23, 0)), 0, 0, "weekend")
	near(t, WorkMinutes(ts(2026, 10, 2, 16, 0), ts(2026, 10, 5, 10, 0)), 120, 1e-9, "across DST")
	near(t, WorkMinutes(monAt(12, 0), monAt(10, 0)), 0, 0, "reversed")
}

// --- baselines ---------------------------------------------------------------

func TestTwoSessionsNoDoubleCount(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	for i, p := range []pl{{sid: "A", lines: f(100)}, {sid: "B", lines: f(150)}, {sid: "A", lines: f(150)},
		{sid: "B", lines: f(150)}, {sid: "A", lines: f(150)}} {
		tick(s, now+float64(i*5), &ev, p.m())
	}
	near(t, s.Days["2026-09-28"].Lines, 50, 0, "lines")
}

func TestNewSessionNonzeroStartAddsNothing(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	tick(s, now, &ev, pl{sid: "A", lines: f(900)}.m())
	tick(s, now+5, &ev, pl{sid: "A", lines: f(900)}.m())
	near(t, s.Days["2026-09-28"].Lines, 0, 0, "lines")
	near(t, s.Stats.XP, 0, 0, "xp")
}

func TestMissingCostThenTotalIsBaseline(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	tick(s, now, &ev, pl{sid: "A"}.m())
	tick(s, now+5, &ev, pl{sid: "A", lines: f(500)}.m())
	tick(s, now+10, &ev, pl{sid: "A", lines: f(525)}.m())
	near(t, s.Days["2026-09-28"].Lines, 25, 0, "lines")
	near(t, s.Stats.XP, 1, 0, "xp")
}

func TestFedLinesCappedPerTick(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	s.Stats.Fed = 50
	tick(s, now, &ev, pl{sid: "A", lines: f(0)}.m())
	tick(s, now, &ev, pl{sid: "A", lines: f(10000)}.m())
	near(t, s.Stats.Fed, 60, 1e-9, "fed")
}

// --- commits (real git) ------------------------------------------------------

func gitRun(t *testing.T, dir string, envs []string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	c.Dir = dir
	c.Env = append(os.Environ(), envs...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func makeRepo(t *testing.T, root, name string) string {
	p := filepath.Join(root, name)
	os.MkdirAll(p, 0o755)
	gitRun(t, p, nil, "init", "-q", "-b", "main")
	gitRun(t, p, nil, "config", "user.email", "pet@test.local")
	gitRun(t, p, nil, "config", "user.name", "Pet")
	gitRun(t, p, nil, "commit", "-q", "--allow-empty", "-m", "init "+name)
	r, _ := filepath.EvalSymlinks(p)
	return r
}

func poll(s *State, ev *[]Event, now float64, cwd, sid string) float64 {
	s.Tick(now, ev, pl{sid: sid, cwd: cwd}.m(), "", nil, true)
	return s.Days[DateKey(now)].Commits
}

func TestCommitDedupeAcrossWorktreesAndRepos(t *testing.T) {
	dir := env(t)
	a := makeRepo(t, dir, "a")
	wt := filepath.Join(dir, "a-wt")
	gitRun(t, a, nil, "worktree", "add", "-q", "-b", "feat", wt)
	b := makeRepo(t, dir, "b")
	now := Now()
	s, ev := fresh(now), []Event{}
	s.Tick(now, &ev, pl{sid: "S1", ctx: f(10), cwd: a}.m(), "", nil, true)
	s.Tick(now+1, &ev, pl{sid: "S2", ctx: f(10), cwd: wt}.m(), "", nil, true)
	s.Tick(now+2, &ev, pl{sid: "S3", ctx: f(10), cwd: b}.m(), "", nil, true)
	near(t, s.Days[DateKey(now)].Commits, 2, 0, "commits")
	near(t, s.Stats.XP, 30, 0, "xp (2 × 10 × 1.5)")
	s.Tick(now+120, &ev, pl{sid: "S1", ctx: f(10), cwd: a}.m(), "", nil, true)
	near(t, s.Days[DateKey(now)].Commits, 2, 0, "re-poll")
}

func TestAmendAndRebaseNotRecounted(t *testing.T) {
	dir := env(t)
	a := makeRepo(t, dir, "a")
	gitRun(t, a, nil, "commit", "-q", "--allow-empty", "-m", "two")
	now := Now()
	s, ev := fresh(now), []Event{}
	near(t, poll(s, &ev, now, a, "S1"), 2, 0, "first poll")
	before := gitRun(t, a, nil, "rev-parse", "HEAD")
	later := []string{"GIT_COMMITTER_DATE=@" + itoa(int64(now)+5) + " +0000"} // same second = same sha
	gitRun(t, a, later, "commit", "-q", "--amend", "--no-edit", "--allow-empty")
	if gitRun(t, a, nil, "rev-parse", "HEAD") == before {
		t.Fatal("amend didn't change the sha, test is vacuous")
	}
	near(t, poll(s, &ev, now+61, a, "S1"), 2, 0, "after amend")
}

func TestAuthorIsExact(t *testing.T) {
	dir := env(t)
	a := makeRepo(t, dir, "a")
	gitRun(t, a, nil, "-c", "user.email=teampet@test.local", "commit", "-q", "--allow-empty", "-m", "theirs")
	gitRun(t, a, nil, "-c", "user.email=pet@test_local", "commit", "-q", "--allow-empty", "-m", "dot trap")
	now := Now()
	s, ev := fresh(now), []Event{}
	near(t, poll(s, &ev, now, a, "S1"), 1, 0, "commits")
}

func TestPlusInEmail(t *testing.T) {
	dir := env(t)
	a := makeRepo(t, dir, "a")
	gitRun(t, a, nil, "config", "user.email", "pet+cc@test.local")
	gitRun(t, a, nil, "commit", "-q", "--allow-empty", "-m", "mine")
	now := Now()
	s, ev := fresh(now), []Event{}
	near(t, poll(s, &ev, now, a, "S1"), 1, 0, "commits")
}

func TestOldShaOnlyStateDoesNotRecount(t *testing.T) {
	dir := env(t)
	a := makeRepo(t, dir, "a")
	now := Now()
	s, ev := fresh(now), []Event{}
	s.SeenCommits[gitRun(t, a, nil, "rev-parse", "HEAD")] = DateKey(now)
	near(t, poll(s, &ev, now, a, "S1"), 0, 0, "commits")
}

func TestPollThrottled(t *testing.T) {
	dir := env(t)
	a := makeRepo(t, dir, "a")
	now := Now()
	s, ev := fresh(now), []Event{}
	poll(s, &ev, now, a, "S1")
	gitRun(t, a, nil, "commit", "-q", "--allow-empty", "-m", "two")
	near(t, poll(s, &ev, now+10, a, "S1"), 1, 0, "throttled")
	near(t, poll(s, &ev, now+61, a, "S1"), 2, 0, "after throttle")
}

func TestXPMultiplier(t *testing.T) {
	env(t)
	for _, c := range []struct {
		ctx  *float64
		want float64
	}{{f(49), 1.5}, {f(69), 1.0}, {f(95), 0.7}, {nil, 1.5}} {
		near(t, XPMultiplier(c.ctx), c.want, 0, "mult")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// --- hooks -------------------------------------------------------------------

func TestAutoVsManualCompact(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	s.LastCommitAt = &now
	hookTick(s, now, &ev, "PreCompact", map[string]any{"trigger": "manual"})
	near(t, s.Stats.Fit, 70, 0, "manual is neutral")
	if s.MoodOf(now) != "content" {
		t.Fatal(s.MoodOf(now))
	}
	hookTick(s, now, &ev, "PreCompact", map[string]any{"trigger": "auto"})
	near(t, s.Stats.Fit, 50, 0, "auto")
	if s.MoodOf(now+60) != "bloated" || s.MoodOf(now+31*60) == "bloated" {
		t.Fatal("bloated window")
	}
	near(t, s.Days["2026-09-28"].AutoCompacts, 1, 0, "count")
}

func wrapFit(t *testing.T, ctx, lines float64, reason string) *State {
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	tick(s, now, &ev, pl{sid: "A", lines: f(0), ctx: f(ctx)}.m())
	tick(s, now, &ev, pl{sid: "A", lines: f(lines), ctx: f(ctx)}.m())
	hookTick(s, now, &ev, "SessionEnd", map[string]any{"session_id": "A", "reason": reason})
	return s
}

func TestCleanWrapThreshold(t *testing.T) {
	env(t)
	near(t, wrapFit(t, 69, 10, "clear").Stats.Fit, 82, 0, "clean")
	near(t, wrapFit(t, 70, 10, "clear").Stats.Fit, 70, 0, "at threshold")
	near(t, wrapFit(t, 20, 0, "clear").Stats.Fit, 70, 0, "nothing shipped")
	near(t, wrapFit(t, 20, 10, "resume").Stats.Fit, 70, 0, "resume")
	s := wrapFit(t, 20, 10, "prompt_input_exit")
	near(t, s.Stats.Fit, 82, 0, "exit")
	if _, ok := s.Sessions["A"]; ok {
		t.Fatal("session should be gone")
	}
}

// --- moods -------------------------------------------------------------------

func TestMoodPriority(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s := fresh(now)
	s.LastCommitAt = &now
	s.Stats = Stats{Fed: 10, Fit: 10, Rested: 10}
	s.Status.BloatedUntil = now + 10
	want := func(at float64, m string) {
		t.Helper()
		if got := s.MoodOf(at); got != m {
			t.Fatalf("got %s want %s", got, m)
		}
	}
	want(now, "bloated")
	s.Status.BloatedUntil = 0
	want(now, "exhausted")
	s.Stats.Rested = 30
	want(now, "starving")
	s.Stats.Fed = 30
	want(monAt(20, 0), "sleepy")
	want(now, "sulky")
	s.Stats = Stats{Fed: 90, Fit: 90, Rested: 90}
	want(now, "content")
	s.Streak.Count = 3
	want(now, "thriving")
}

func TestMissedYouUntilCommit(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s := fresh(now)
	wed := ts(2026, 9, 23, 10, 0)
	s.LastCommitAt = &wed
	if s.MoodOf(now) != "missed" {
		t.Fatal(s.MoodOf(now))
	}
	s.LastCommitAt = &now
	if s.MoodOf(now) == "missed" {
		t.Fatal("still missed")
	}
}

func TestRestedTarget(t *testing.T) {
	env(t)
	near(t, RestedTarget(nil), 80, 0, "none")
	near(t, RestedTarget(f(40)), 100, 0, "easy")
	near(t, RestedTarget(f(75)), 55, 1e-9, "mid")
	near(t, RestedTarget(f(100)), 10, 1e-9, "max")
}

func TestFloorAndDecay(t *testing.T) {
	env(t)
	s, ev := fresh(monAt(9, 0)), []Event{}
	tick(s, monAt(17, 0), &ev, nil)
	near(t, s.Stats.Fed, 5, 0, "fed floors")
}

func TestRedlineDrainsFit(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	for i, c := range []float64{90, 91, 92} {
		tick(s, now+float64(i*100), &ev, pl{sid: "A", ctx: f(c)}.m())
	}
	if s.Stats.Fit >= 70 {
		t.Fatal(s.Stats.Fit)
	}
}

func TestIdleParkedSessionIsNotYou(t *testing.T) {
	// a multiplexer keeps a parked 90% session rendering all day: no fit drain, no steady credit
	env(t)
	now := monAt(9, 0)
	s, ev := fresh(now), []Event{}
	for i := 0; i < 8*3600; i += 60 {
		tick(s, now+float64(i), &ev, pl{sid: "A", ctx: f(90), five: f(10), lines: f(5)}.m())
	}
	near(t, s.Stats.Fit, 70, 0, "fit")
	near(t, s.Temper.Calm, 0, 0, "calm")
}

// --- streaks -----------------------------------------------------------------

func TestStreakWeekendsSkipped(t *testing.T) {
	env(t)
	now := ts(2026, 10, 6, 10, 0) // Tue
	s := fresh(now)
	th := "2026-09-30"
	s.Streak.Through = &th
	for _, k := range []string{"2026-10-01", "2026-10-02", "2026-10-05"} {
		s.Days[k] = &Day{Commits: 1, Fit: 60}
	}
	s.updateStreak(now)
	near(t, s.Streak.Count, 3, 0, "count")
	if *s.Streak.Through != "2026-10-05" {
		t.Fatal(*s.Streak.Through)
	}
}

func TestStreakBadWeekdayBreaks(t *testing.T) {
	env(t)
	now := ts(2026, 10, 6, 10, 0)
	s := fresh(now)
	th := "2026-10-01"
	s.Streak = Streak{Count: 4, Through: &th}
	s.Days["2026-10-02"] = &Day{Commits: 1, Fit: 40}
	s.Days["2026-10-05"] = &Day{Commits: 2, Fit: 90}
	s.updateStreak(now)
	near(t, s.Streak.Count, 1, 0, "count")
}

func TestStreakProvisionalTodayAndCrown(t *testing.T) {
	env(t)
	now := ts(2026, 10, 6, 10, 0)
	s := fresh(now)
	s.Streak.Count = 4
	s.Days["2026-10-06"] = &Day{Commits: 1, Fit: 80}
	if s.CurrentStreak(now) != 5 || !strings.Contains(s.Render(now, nil), Crown) {
		t.Fatal("no crown")
	}
}

// --- levels & forms ----------------------------------------------------------

func TestLevelCurve(t *testing.T) {
	env(t)
	if Level(0) != 1 {
		t.Fatal(Level(0))
	}
	for lvl := 2; lvl < 500; lvl++ {
		x := XPForLevel(lvl)
		if Level(x) != lvl || Level(x-1) != lvl-1 {
			t.Fatalf("level %d: xp %v gives %d / %d", lvl, x, Level(x), Level(x-1))
		}
	}
	if x := XPForLevel(5); x < 80 || x > 130 {
		t.Fatal("L5", x)
	}
	if x := XPForLevel(15); x < 700 || x > 900 {
		t.Fatal("L15", x)
	}
	if x := XPForLevel(30); x < 2400 || x > 3000 {
		t.Fatal("L30", x)
	}
}

func TestFacesKeepMoodMouths(t *testing.T) {
	env(t)
	for _, c := range [][2]string{
		{Face("bear", "content", false, ""), "ʕ•ᴥ•ʔ"},
		{Face("bear", "bloated", false, ""), "ʕ°□°ʔ"},
		{Face("great owl", "sleepy", true, ""), "{(-)_(-)}"},
		{Face("no-such-form", "content", false, ""), "(•ᴗ•)"},
		{Face("kitsune", "content", false, ""), "ミᕱ•ᴥ•ᕱ彡"},
		{Face("dog", "missed", false, ""), "UT_TU"},
	} {
		if c[0] != c[1] {
			t.Fatalf("got %s want %s", c[0], c[1])
		}
	}
}

func TestEveryFormReachable(t *testing.T) {
	reach := map[string]bool{"hatchling": true}
	for _, sp := range Species {
		reach[sp] = true
		for _, fin := range Finals[sp] {
			reach[fin] = true
		}
	}
	if len(reach) != len(Forms) {
		t.Fatalf("%d reachable, %d forms", len(reach), len(Forms))
	}
	for _, f := range Forms {
		if !reach[f.Name] {
			t.Fatal(f.Name)
		}
	}
}

func TestWidth(t *testing.T) {
	if Width("ミᕱ•ᴥ•ᕱ彡") != 9 || Width("(•ロ•)") != 6 || Width("ʕ•ᴥ•ʔ") != 5 {
		t.Fatal(Width("ミᕱ•ᴥ•ᕱ彡"), Width("(•ロ•)"), Width("ʕ•ᴥ•ʔ"))
	}
}

// --- evolution ---------------------------------------------------------------

func evolveWith(t *testing.T, temper Temper, xp float64, form string) (*State, []Event) {
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	s.Form, s.Temper, s.Stats.XP = form, temper, xp
	tick(s, now, &ev, nil)
	return s, ev
}

func TestSpeciesByTrait(t *testing.T) {
	env(t)
	L5 := XPForLevel(5)
	for _, c := range []struct {
		temper Temper
		xp     float64
		want   string
	}{
		{Temper{Ship: 2}, L5, "bear"}, {Temper{Tidy: 1}, L5, "cat"}, {Temper{Calm: 200}, L5, "owl"},
		{Temper{Ship: 2, Tidy: 1, Calm: 200}, L5, "fox"}, {Temper{Ship: 1}, L5 - 1, "hatchling"},
	} {
		if s, _ := evolveWith(t, c.temper, c.xp, "hatchling"); s.Form != c.want {
			t.Fatalf("%+v: got %s want %s", c.temper, s.Form, c.want)
		}
	}
}

func TestFinalByTraitAtL15(t *testing.T) {
	env(t)
	L15 := XPForLevel(15)
	s, ev := evolveWith(t, Temper{Tidy: 1}, L15, "bear")
	if s.Form != "koala" || !has(ev, "evolve") || !strings.Contains(s.Status.React.Text, "koala") {
		t.Fatal(s.Form)
	}
	if s, _ := evolveWith(t, Temper{Ship: 2}, L15, "owl"); s.Form != "penguin" {
		t.Fatal(s.Form)
	}
	if s, _ := evolveWith(t, Temper{Calm: 200}, L15, "fox"); s.Form != "kitsune" {
		t.Fatal(s.Form)
	}
}

func TestSkippedTiersStillBranch(t *testing.T) {
	env(t)
	if s, _ := evolveWith(t, Temper{Ship: 2}, XPForLevel(20), "hatchling"); s.Form != "seal" {
		t.Fatal(s.Form)
	}
}

func TestFinalIsLocked(t *testing.T) {
	env(t)
	if s, _ := evolveWith(t, Temper{Tidy: 5}, XPForLevel(30), "seal"); s.Form != "seal" {
		t.Fatal(s.Form)
	}
}

func TestTraitScoring(t *testing.T) {
	env(t)
	sc := func(tp Temper) map[string]float64 { return (&State{Temper: tp}).TraitScores() }
	near(t, sc(Temper{Tidy: -2})["tidy"], 0, 0, "auto-compacts cancel tidy")
	near(t, sc(Temper{Calm: 200, Hot: 200})["steady"], 0, 0, "hot usage isn't steady")
	if sc(Temper{Calm: 0.01})["steady"] > 0.01 {
		t.Fatal("a sliver of calm maxed steady")
	}
	near(t, sc(Temper{Calm: 120})["steady"], 1, 0, "full steady")
}

func TestTemperTracksAndDecays(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	s, ev := fresh(now), []Event{}
	tick(s, now, &ev, pl{sid: "A", ctx: f(10), five: f(20)}.m())
	tick(s, now+600, &ev, pl{sid: "A", ctx: f(11), five: f(20)}.m())
	near(t, s.Temper.Calm, 10, 0.5, "calm")
	s.Temper.Ship = 2
	tick(s, now+600+4*3600, &ev, nil) // one tau of work time
	near(t, s.Temper.Ship, 2/math.E, 0.05, "decay")
	tick(s, ts(2026, 10, 3, 12, 0), &ev, nil) // Saturday: no work time, no decay
	frozen := s.Temper.Ship
	tick(s, ts(2026, 10, 4, 12, 0), &ev, nil)
	near(t, s.Temper.Ship, frozen, 0, "weekend freeze")
}

// --- devolution --------------------------------------------------------------

func wilting(form string, xp float64, st Stats) (*State, []Event, float64) {
	now := monAt(9, 0)
	s := fresh(now)
	s.Form, s.Stats.XP = form, xp
	if st != (Stats{}) {
		st.XP = xp
		s.Stats = st
	}
	return s, []Event{}, now
}

var misused = Stats{Fed: 5, Fit: 10, Rested: 10}

func TestMisuseDevolvesFinalToSpecies(t *testing.T) {
	env(t)
	s, ev, now := wilting("koala", XPForLevel(16), misused)
	work(s, now, 60, &ev, 100, nil)
	if s.Form != "koala" || s.MoodOf(now+3600) != "wilting" {
		t.Fatal("60 min: should be wilting, not devolved", s.Form, s.MoodOf(now+3600))
	}
	work(s, now+3660, 31, &ev, 100, nil)
	if s.Form != "bear" || Level(s.Stats.XP) != int(T.FinalLevel-T.DevolveLevels) || s.Devolved != 1 || !has(ev, "devolve") {
		t.Fatal(s.Form, Level(s.Stats.XP), s.Devolved)
	}
}

func TestSpeciesToHatchlingThenRebranch(t *testing.T) {
	env(t)
	s, ev, now := wilting("bear", XPForLevel(8), misused)
	work(s, now, 91, &ev, 100, nil)
	if s.Form != "hatchling" {
		t.Fatal(s.Form)
	}
	s.Temper = Temper{Tidy: 1}
	s.Stats.XP = XPForLevel(5)
	tick(s, now+92*60, &ev, nil)
	if s.Form != "cat" {
		t.Fatal(s.Form)
	}
}

func TestAbsenceWithLowStatsNeverDevolves(t *testing.T) {
	// avg 50 → away a workday → fed floors; must not count as misuse
	env(t)
	s, ev, now := wilting("koala", XPForLevel(16), Stats{Fed: 70, Fit: 40, Rested: 40})
	tick(s, now, &ev, pl{sid: "A", ctx: f(30)}.m())
	tick(s, now+86400, &ev, pl{sid: "A", ctx: f(30)}.m())
	if s.Form != "koala" || s.Wilt != 0 {
		t.Fatal(s.Form, s.Wilt)
	}
}

func TestBusyDayWithoutCommitsDoesNotWilt(t *testing.T) {
	env(t)
	s, ev, now := wilting("koala", XPForLevel(16), Stats{Fed: 5, Fit: 40, Rested: 40})
	work(s, now, 180, &ev, 75, nil) // rested target 55
	if s.Form != "koala" || s.Wilt != 0 {
		t.Fatal(s.Form, s.Wilt)
	}
}

func TestIdleMisuseStateDoesNotAccrue(t *testing.T) {
	env(t)
	s, ev, now := wilting("koala", XPForLevel(16), Stats{Fed: 70, Fit: 10, Rested: 10})
	for i := 0; i < 4*3600; i += 60 {
		tick(s, now+float64(i), &ev, pl{sid: "A", ctx: f(30), five: f(100)}.m())
	}
	if s.Form != "koala" {
		t.Fatal(s.Form)
	}
}

func TestDevolveCapsSameTickGains(t *testing.T) {
	env(t)
	s, ev, now := wilting("bear", XPForLevel(8), misused)
	work(s, now, 89, &ev, 100, f(0))
	if s.Form != "bear" {
		t.Fatal(s.Form)
	}
	// the crossing tick also carries a huge line gain: devolve must still win
	tick(s, now+90*60+30, &ev, pl{sid: "W", ctx: f(77), five: f(100), lines: f(100000)}.m())
	if !has(ev, "devolve") || s.Form != "hatchling" || Level(s.Stats.XP) >= int(T.SpeciesLevel) {
		t.Fatal(s.Form, Level(s.Stats.XP))
	}
}

func TestAbsenceNeverWilts(t *testing.T) {
	env(t)
	s, ev, now := wilting("seal", XPForLevel(16), Stats{})
	tick(s, now+5*86400, &ev, nil)
	if s.Form != "seal" || s.Wilt != 0 {
		t.Fatal(s.Form, s.Wilt)
	}
}

func TestRecoveryUnwindsWilt(t *testing.T) {
	env(t)
	s, ev, now := wilting("seal", XPForLevel(16), misused)
	work(s, now, 60, &ev, 100, nil)
	if s.Wilt <= 0 {
		t.Fatal("not wilting")
	}
	s.Stats = Stats{Fed: 90, Fit: 90, Rested: 90, XP: s.Stats.XP}
	tick(s, now+120*60, &ev, nil)
	if s.Wilt != 0 || s.Form != "seal" {
		t.Fatal(s.Wilt, s.Form)
	}
}

func TestManualDevolveMatchesPreview(t *testing.T) {
	env(t)
	s, ev, now := wilting("koala", XPForLevel(18), Stats{})
	back, keep, _ := s.DevolvePreview()
	s.Devolve(now, &ev, "chose to devolve")
	if s.Form != back || s.Stats.XP != keep || ev[len(ev)-1].Msg != "chose to devolve back into a bear" {
		t.Fatal(s.Form, s.Stats.XP, ev)
	}
	h := &State{Form: "hatchling"}
	if _, _, ok := h.DevolvePreview(); ok {
		t.Fatal("hatchling devolved")
	}
}

func TestHatchlingCannotDevolve(t *testing.T) {
	env(t)
	s, ev, now := wilting("hatchling", 10, misused)
	work(s, now, 91, &ev, 100, nil)
	if s.Form != "hatchling" || s.Stats.XP != 10 {
		t.Fatal(s.Form, s.Stats.XP)
	}
}

// --- looks -------------------------------------------------------------------

func TestLookDeterministicAndBackfilled(t *testing.T) {
	env(t)
	a := RollLook(1234.5)
	if a != RollLook(1234.5) {
		t.Fatal("not deterministic")
	}
	old := NewState(1234.5)
	old.Look = nil
	if *old.Ensure().Look != a {
		t.Fatal("backfill differs")
	}
}

func TestLooksVary(t *testing.T) {
	env(t)
	eyes, charms, shiny := map[string]bool{}, map[string]bool{}, false
	for i := 0; i < 500; i++ {
		l := RollLook(float64(i))
		eyes[l.Eyes], charms[l.Charm] = true, true
		shiny = shiny || l.Shiny
	}
	if len(eyes) < 4 || len(charms) < 4 || !shiny {
		t.Fatal(len(eyes), len(charms), shiny)
	}
}

func TestRenderUsesLook(t *testing.T) {
	env(t)
	now := monAt(10, 0) + 1 // not a blink frame
	s := fresh(now)
	s.LastCommitAt = &now
	s.Look = &Look{Eyes: "◕", Charm: "✿", Shiny: true}
	got := s.Render(now, Colors{"good": "G", "meh": "M", "bad": "B", "reset": "R"})
	if got != Shiny+"(◕ᴗ◕)✿ contentR" {
		t.Fatalf("%q", got)
	}
}

func TestReactionThenMood(t *testing.T) {
	env(t)
	now := monAt(10, 0) + 1
	s := fresh(now)
	s.LastCommitAt = &now
	s.react(now, "nom +15xp", "thriving")
	if r := s.Render(now, nil); !strings.Contains(r, "nom +15xp") || !strings.Contains(r, "(^ᴗ^)") {
		t.Fatal(r)
	}
	if r := s.Render(now+31, nil); !strings.Contains(r, "content") {
		t.Fatal(r)
	}
}

// --- storage -----------------------------------------------------------------

func TestCorruptStateMovedAside(t *testing.T) {
	dir := env(t)
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("{nope"), 0o644)
	if Segment(map[string]any{}, nil, monAt(10, 0)) == "" {
		t.Fatal("no render")
	}
	entries, _ := os.ReadDir(dir)
	found := false
	for _, e := range entries {
		found = found || strings.HasPrefix(e.Name(), "state.json.corrupt-")
	}
	if !found || ReadState() == nil {
		t.Fatal("not moved aside / not re-hatched")
	}
}

func TestPythonStateIsRead(t *testing.T) {
	// the 0.1 Python engine wrote ints and nulls where Go writes floats; both must load
	dir := env(t)
	py := `{"version":1,"name":"Bramble","born_at":1790000000.5,"last_tick":1790000100.0,"last_commit_at":null,
	"form":"bear","stats":{"fed":70.0,"fit":70.0,"rested":80.0,"xp":197},"line_carry":3,
	"status":{"bloated_until":0,"react":null},"sessions":{"s":{"lines":12,"last_ctx":null,"max_ctx":0,
	"shipped":false,"last_seen":1790000100.0,"cwd":null,"top":null}},"repos":{},"seen_commits":{"abc":"2026-09-30"},
	"days":{"2026-09-30":{"commits":1,"lines":10,"xp":10,"auto_compacts":0,"clean_wraps":0,"fit":70.0,"calm_min":3.2}},
	"streak":{"count":0,"last_day":null,"through":"2026-09-29"},"look":{"eyes":"ᵔ","charm":"✿","shiny":false},
	"temper":{"ship":0.5,"tidy":0,"calm":1.0,"hot":0},"wilt":0.0,"devolved":0,"active_at":1790000100.0}`
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(py), 0o644)
	s := ReadState()
	if s == nil || s.Name != "Bramble" || s.Form != "bear" || s.Stats.XP != 197 || s.Look.Eyes != "ᵔ" {
		t.Fatalf("%+v", s)
	}
}

func TestGarbagePayloadIsFine(t *testing.T) {
	env(t)
	for _, junk := range []map[string]any{{}, nil, {"session_id": 3.0, "cost": "bad", "context_window": 3.0},
		{"session_id": "A", "cost": []any{1.0}, "rate_limits": map[string]any{"five_hour": "x"}}} {
		Segment(junk, nil, monAt(10, 0))
	}
}

func TestLockContentionIsReadOnly(t *testing.T) {
	dir := env(t)
	now := monAt(10, 0)
	Segment(pl{sid: "A", lines: f(0)}.m(), nil, now)
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	release, err := Lock(time.Second)
	if err != nil || release == nil {
		t.Fatal("couldn't take the lock")
	}
	defer release()
	if Segment(pl{sid: "A", lines: f(500)}.m(), nil, now+60) == "" {
		t.Fatal("no read-only render")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("state changed while locked")
	}
}

func TestDisable(t *testing.T) {
	dir := env(t)
	t.Setenv("PET_DISABLE", "1")
	if Segment(map[string]any{}, nil, monAt(10, 0)) != "" {
		t.Fatal("rendered while disabled")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err == nil {
		t.Fatal("hatched while disabled")
	}
}

func TestEventsLogged(t *testing.T) {
	env(t)
	Segment(map[string]any{}, nil, monAt(10, 0))
	if evs := ReadEvents(5); len(evs) == 0 || evs[0].Kind != "hatch" {
		t.Fatal(evs)
	}
}

func TestConfigOverridesNumbersOnly(t *testing.T) {
	dir := env(t)
	os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"work_start": 7, "work_end": 15, "bogus": 1, "floor": true}`), 0o644)
	LoadConfig()
	if T.WorkStart != 7 || T.WorkEnd != 15 || T.Floor != 5 {
		t.Fatal(T.WorkStart, T.WorkEnd, T.Floor)
	}
	if !IsWorkTime(monAt(7, 30)) || IsWorkTime(monAt(15, 30)) {
		t.Fatal("hours not applied")
	}
}

func TestBadConfigIgnored(t *testing.T) {
	dir := env(t)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{nope"), 0o644)
	LoadConfig()
	if Segment(map[string]any{}, nil, monAt(10, 0)) == "" {
		t.Fatal("no render")
	}
}

func TestHookHandling(t *testing.T) {
	env(t)
	now := monAt(10, 0)
	Segment(pl{sid: "A", lines: f(0), ctx: f(20)}.m(), nil, now)
	Segment(pl{sid: "A", lines: f(40), ctx: f(20)}.m(), nil, now+5)
	HandleHook(map[string]any{"hook_event_name": "PreCompact", "trigger": "auto"}, now+10)
	HandleHook(map[string]any{"hook_event_name": "SessionEnd", "reason": "clear", "session_id": "A"}, now+15)
	HandleHook(map[string]any{"hook_event_name": "Stop"}, now+20)
	s := ReadState()
	if s.Days["2026-09-28"].AutoCompacts != 1 || s.Days["2026-09-28"].CleanWraps != 1 {
		t.Fatalf("%+v", s.Days["2026-09-28"])
	}
}
