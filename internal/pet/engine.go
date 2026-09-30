// Package pet is the claude-pet engine: one shared pet, fed by good Claude usage.
//
// Functions take `now` (epoch seconds) so they're testable. Anything the statusline
// calls must never panic past Segment's recover, and must stay fast.
package pet

import (
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"sort"
	"time"
)

// Tuning holds every scoring constant. Numeric fields can be overridden by config.json.
type Tuning struct {
	WorkStart, WorkEnd                              float64 // local hours, Mon–Fri
	Floor                                           float64 // stats never drop below this
	FedPerCommit, FedLinesPerPoint, FedLinesCap     float64
	FedDecayPerMin                                  float64 // full → empty ≈ one workday
	FitCleanWrap, CleanWrapCtx                      float64
	FitAutoCompact, BloatedSecs                     float64
	FitRedlineCtx, FitRedlinePerMin                 float64
	RestedEasyUntil, RestedMinTarget, RestedNeutral float64
	RestedTauSecs                                   float64
	XPPerCommit, XPLinesPerPoint                    float64
	LevelDiv, LevelExp                              float64 // L5 ≈ 100xp, L15 ≈ 800xp, L30 ≈ 2700xp
	// long-lived panes keep ticking, so only lines/ctx moving counts as you being there
	ActiveSecs                                  float64
	SteadyFullHours                             float64 // active hours in the trait window for a full steady score
	PollSecs, GitTimeout                        float64
	MissedWorkdays, CrownStreak, ThrivingStreak float64
	LockWait, LogMaxBytes                       float64
	SpeciesLevel, FinalLevel                    float64
	TraitTauHours                               float64 // traits remember roughly the last 4 work hours
	ShipPerHour, TidyPerHour                    float64 // sustained rate that maxes the trait
	FoxMin, FoxSpread                           float64 // all traits this high and this close → fox
	WiltBelow, WiltRecover                      float64 // misuse gauge that starts / unwinds wilting
	WiltMinutes, DevolveLevels                  float64 // active minutes wilting before devolving; levels lost
	ReactSecs, ShinyOdds                        float64
	KeepSessionDays, KeepCommitDays, KeepDays   float64
}

// XPMult maps "ctx below" → multiplier for commit XP.
func XPMult() [][2]float64 { return [][2]float64{{50, 1.5}, {70, 1.0}, {101, 0.7}} }

// DefaultTuning is the stock scoring.
func DefaultTuning() Tuning {
	return Tuning{
		WorkStart: 9, WorkEnd: 17, Floor: 5,
		FedPerCommit: 15, FedLinesPerPoint: 20, FedLinesCap: 10, FedDecayPerMin: 0.2,
		FitCleanWrap: 12, CleanWrapCtx: 70, FitAutoCompact: -20, BloatedSecs: 30 * 60,
		FitRedlineCtx: 80, FitRedlinePerMin: 0.1,
		RestedEasyUntil: 50, RestedMinTarget: 10, RestedNeutral: 80, RestedTauSecs: 600,
		XPPerCommit: 10, XPLinesPerPoint: 25, LevelDiv: 10, LevelExp: 0.6,
		ActiveSecs: 300, SteadyFullHours: 2, PollSecs: 60, GitTimeout: 0.5,
		MissedWorkdays: 2, CrownStreak: 5, ThrivingStreak: 3, LockWait: 0.1, LogMaxBytes: 1_000_000,
		SpeciesLevel: 5, FinalLevel: 15, TraitTauHours: 4, ShipPerHour: 0.5, TidyPerHour: 0.25,
		FoxMin: 0.5, FoxSpread: 0.25, WiltBelow: 30, WiltRecover: 40, WiltMinutes: 90, DevolveLevels: 2,
		ReactSecs: 30, ShinyOdds: 20, KeepSessionDays: 7, KeepCommitDays: 14, KeepDays: 90,
	}
}

// T is the live tuning: process-wide, set once from DefaultTuning and config.json (and by tests).
var T = DefaultTuning() //nolint:gochecknoglobals // process-wide config, threading it through every call adds nothing

func (t *Tuning) fields() map[string]*float64 {
	return map[string]*float64{
		"work_start": &t.WorkStart, "work_end": &t.WorkEnd, "floor": &t.Floor,
		"fed_per_commit": &t.FedPerCommit, "fed_lines_per_point": &t.FedLinesPerPoint,
		"fed_lines_cap": &t.FedLinesCap, "fed_decay_per_min": &t.FedDecayPerMin,
		"fit_clean_wrap": &t.FitCleanWrap, "clean_wrap_ctx": &t.CleanWrapCtx,
		"fit_auto_compact": &t.FitAutoCompact, "bloated_secs": &t.BloatedSecs,
		"fit_redline_ctx": &t.FitRedlineCtx, "fit_redline_per_min": &t.FitRedlinePerMin,
		"rested_easy_until": &t.RestedEasyUntil, "rested_min_target": &t.RestedMinTarget,
		"rested_neutral": &t.RestedNeutral, "rested_tau_secs": &t.RestedTauSecs,
		"xp_per_commit": &t.XPPerCommit, "xp_lines_per_point": &t.XPLinesPerPoint,
		"level_div": &t.LevelDiv, "level_exp": &t.LevelExp, "active_secs": &t.ActiveSecs,
		"steady_full_hours": &t.SteadyFullHours, "poll_secs": &t.PollSecs, "git_timeout": &t.GitTimeout,
		"missed_workdays": &t.MissedWorkdays, "crown_streak": &t.CrownStreak,
		"thriving_streak": &t.ThrivingStreak, "lock_wait": &t.LockWait, "log_max_bytes": &t.LogMaxBytes,
		"species_level": &t.SpeciesLevel, "final_level": &t.FinalLevel, "trait_tau_hours": &t.TraitTauHours,
		"ship_per_hour": &t.ShipPerHour, "tidy_per_hour": &t.TidyPerHour, "fox_min": &t.FoxMin,
		"fox_spread": &t.FoxSpread, "wilt_below": &t.WiltBelow, "wilt_recover": &t.WiltRecover,
		"wilt_minutes": &t.WiltMinutes, "devolve_levels": &t.DevolveLevels, "react_secs": &t.ReactSecs,
		"shiny_odds": &t.ShinyOdds, "keep_session_days": &t.KeepSessionDays,
		"keep_commit_days": &t.KeepCommitDays, "keep_days": &t.KeepDays,
	}
}

// Override applies numeric config values by their snake_case name. Unknown keys are ignored.
func (t *Tuning) Override(cfg map[string]any) {
	f := t.fields()
	for k, v := range cfg {
		if p, ok := f[k]; ok {
			if n, ok := v.(float64); ok {
				*p = n
			}
		}
	}
}

// Names are the names a new pet can hatch with.
func Names() []string {
	return []string{"Biscuit", "Pixel", "Mochi", "Gremlin", "Noodle", "Widget", "Pickle",
		"Sprocket", "Dumpling", "Fizz", "Bramble", "Crumpet", "Toast", "Gizmo"}
}

// --- state -------------------------------------------------------------------

// Stats are the pet's vitals (0–100) and its experience.
type Stats struct {
	Fed    float64 `json:"fed"`
	Fit    float64 `json:"fit"`
	Rested float64 `json:"rested"`
	XP     float64 `json:"xp"`
}

// React is a short-lived reaction shown instead of the mood word (e.g. "nom +15xp").
type React struct {
	Until float64 `json:"until"`
	Text  string  `json:"text"`
	Mood  string  `json:"mood"`
}

// Status holds temporary conditions.
type Status struct {
	BloatedUntil float64 `json:"bloated_until"`
	React        *React  `json:"react"`
}

// Session is one Claude Code session, tracked by baseline so panes never double count.
type Session struct {
	Lines    *float64 `json:"lines"`
	LastCtx  *float64 `json:"last_ctx"`
	MaxCtx   float64  `json:"max_ctx"`
	Shipped  bool     `json:"shipped"`
	LastSeen float64  `json:"last_seen"`
	Cwd      string   `json:"cwd"`
	Top      string   `json:"top"`
	ActiveAt float64  `json:"active_at,omitempty"`
}

// Repo throttles commit polling for one repository.
type Repo struct {
	CheckedAt float64 `json:"checked_at"`
	Email     string  `json:"email"`
}

// Day is one day's totals, for the stat sheet and streaks.
type Day struct {
	Commits      float64 `json:"commits"`
	Lines        float64 `json:"lines"`
	XP           float64 `json:"xp"`
	AutoCompacts float64 `json:"auto_compacts"`
	CleanWraps   float64 `json:"clean_wraps"`
	Fit          float64 `json:"fit"`
}

// Streak counts qualifying weekdays in a row.
type Streak struct {
	Count   float64 `json:"count"`
	LastDay *string `json:"last_day"`
	Through *string `json:"through"`
}

// Look is the per-pet cosmetics rolled at hatch.
type Look struct {
	Eyes  string `json:"eyes"`
	Charm string `json:"charm"`
	Shiny bool   `json:"shiny"`
}

// Temper is the rolling behaviour that steers evolution.
type Temper struct {
	Ship float64 `json:"ship"`
	Tidy float64 `json:"tidy"`
	Calm float64 `json:"calm"`
	Hot  float64 `json:"hot"`
}

// State is the whole pet, as stored in state.json.
type State struct {
	Version      float64             `json:"version"`
	Name         string              `json:"name"`
	BornAt       float64             `json:"born_at"`
	LastTick     float64             `json:"last_tick"`
	LastCommitAt *float64            `json:"last_commit_at"`
	Form         string              `json:"form"`
	Stats        Stats               `json:"stats"`
	LineCarry    float64             `json:"line_carry"`
	Status       Status              `json:"status"`
	Sessions     map[string]*Session `json:"sessions"`
	Repos        map[string]*Repo    `json:"repos"`
	SeenCommits  map[string]string   `json:"seen_commits"`
	Days         map[string]*Day     `json:"days"`
	Streak       Streak              `json:"streak"`
	Look         *Look               `json:"look"`
	Temper       Temper              `json:"temper"`
	Wilt         float64             `json:"wilt"`
	Devolved     float64             `json:"devolved"`
	ActiveAt     float64             `json:"active_at"`
}

// Event is one line of the history log.
type Event struct {
	TS   float64 `json:"ts"`
	Kind string  `json:"kind"`
	Msg  string  `json:"msg"`
}

func ev(now float64, kind, msg string) Event { return Event{now, kind, msg} }

// Ensure backfills anything missing, deterministically, so read-only renders agree.
func (s *State) Ensure() *State {
	if s.Form == "" {
		s.Form = "hatchling"
	}

	if s.Look == nil {
		l := RollLook(s.BornAt)
		s.Look = &l
	}

	if s.Sessions == nil {
		s.Sessions = map[string]*Session{}
	}

	if s.Repos == nil {
		s.Repos = map[string]*Repo{}
	}

	if s.SeenCommits == nil {
		s.SeenCommits = map[string]string{}
	}

	if s.Days == nil {
		s.Days = map[string]*Day{}
	}

	if s.Version == 0 {
		s.Version = 1
	}

	return s
}

// NewState hatches a fresh pet.
func NewState(now float64) *State {
	return (&State{
		Name: randomName(), BornAt: now, LastTick: now, Form: "hatchling",
		Stats: Stats{Fed: 70, Fit: 70, Rested: 80},
	}).Ensure()
}

// Eyes are the eye styles a pet can hatch with (• is twice as likely).
func Eyes() []string { return []string{"•", "•", "◕", "ᵔ", "ʘ", "⊙"} }

// Charms are what a pet can hold; most hold nothing.
func Charms() []string { return []string{"", "", "", "✿", "♡", "☆", "♣", "❀"} }

func randomName() string {
	n := Names()

	return n[rand.IntN(len(n))] //nolint:gosec // picking a pet name, not a secret
}

// RollLook picks a pet's eyes, charm and shininess, seeded by its birth time so every pane agrees.
func RollLook(bornAt float64) Look {
	//nolint:gosec // seeded on purpose: cosmetics must come out the same in every process
	r := rand.New(rand.NewPCG(uint64(int64(bornAt*1000)), 0x9e3779b97f4a7c15))
	eyes, charms := Eyes(), Charms()

	return Look{Eyes: eyes[r.IntN(len(eyes))], Charm: charms[r.IntN(len(charms))],
		Shiny: r.Float64() < 1/T.ShinyOdds}
}

// --- the work clock ----------------------------------------------------------

// Zone is the timezone the work clock runs in: the user's own local time, by design.
func Zone() *time.Location {
	return time.Local //nolint:gosmopolitan // work hours are the user's local hours
}

func at(now float64) time.Time {
	sec, frac := math.Modf(now)
	return time.Unix(int64(sec), int64(frac*1e9)).In(Zone())
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Zone())
}

func clockAt(d time.Time, hour float64) float64 {
	h := int(hour)
	m := int((hour - float64(h)) * 60)

	return float64(time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, Zone()).Unix())
}

func isWeekday(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday }

// WorkMinutes is the overlap of [a, b) with Mon–Fri work hours, local time (DST-safe).
func WorkMinutes(a, b float64) float64 {
	if b <= a {
		return 0
	}

	d, end := dayStart(at(a)), dayStart(at(b))
	if end.Sub(d) > 400*24*time.Hour {
		d = end.AddDate(0, 0, -400)
	}

	total := 0.0

	for !d.After(end) {
		if isWeekday(d) {
			s, e := clockAt(d, T.WorkStart), clockAt(d, T.WorkEnd)
			total += math.Max(0, math.Min(b, e)-math.Max(a, s))
		}

		d = d.AddDate(0, 0, 1)
	}

	return total / 60
}

// IsWorkTime is whether now falls in work hours.
func IsWorkTime(now float64) bool {
	t := at(now)
	h := float64(t.Hour()) + float64(t.Minute())/60

	return isWeekday(t) && h >= T.WorkStart && h < T.WorkEnd
}

// DateKey is the local date (YYYY-MM-DD) for now.
func DateKey(now float64) string { return at(now).Format("2006-01-02") }

func parseDate(k string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", k, Zone())
	return t, err == nil
}

// --- scoring -----------------------------------------------------------------

// Level is the level for an XP total.
func Level(xp float64) int {
	return int(math.Pow(math.Max(xp, 0)/T.LevelDiv, T.LevelExp)+1e-9) + 1
}

// XPForLevel is the XP needed to reach lvl.
func XPForLevel(lvl int) float64 {
	return math.Ceil(T.LevelDiv*math.Pow(float64(lvl-1), 1/T.LevelExp) - 1e-6) // float noise at exact boundaries
}

// XPMultiplier rewards commits made with lean context.
func XPMultiplier(ctx *float64) float64 {
	c := 0.0
	if ctx != nil {
		c = *ctx
	}

	mults := XPMult()
	for _, m := range mults {
		if c < m[0] {
			return m[1]
		}
	}

	return mults[len(mults)-1][1]
}

// RestedTarget is where rested heads given 5h rate-limit usage (neutral when unknown).
func RestedTarget(five *float64) float64 {
	if five == nil {
		return T.RestedNeutral
	}

	lo := T.RestedEasyUntil
	if *five <= lo {
		return 100
	}

	frac := math.Min(1, (*five-lo)/(100-lo))

	return 100 - frac*(100-T.RestedMinTarget)
}

func clamp(s *State) {
	c := func(v float64) float64 { return math.Max(T.Floor, math.Min(100, v)) }
	s.Stats.Fed, s.Stats.Fit, s.Stats.Rested = c(s.Stats.Fed), c(s.Stats.Fit), c(s.Stats.Rested)
}

func (s *State) day(now float64) *Day {
	k := DateKey(now)

	d := s.Days[k]
	if d == nil {
		d = &Day{Fit: s.Stats.Fit}
		s.Days[k] = d
	}

	return d
}

func (s *State) addXP(now, n float64) {
	s.Stats.XP += n
	s.day(now).XP += n
}

// Traits are the three behaviours that steer evolution, in tie-break order.
func Traits() []string { return []string{"shipper", "tidy", "steady"} }

// TraitScores are 0–1 per trait from the rolling temperament.
func (s *State) TraitScores() map[string]float64 {
	t, hrs := s.Temper, T.TraitTauHours
	ship := t.Ship / (T.ShipPerHour * hrs)
	tidy := t.Tidy / (T.TidyPerHour * hrs)
	steady := 0.0

	if t.Calm+t.Hot > 0 {
		// mostly-calm *and* enough active time; a sliver of calm minutes shouldn't max it
		ratio := (t.Calm/(t.Calm+t.Hot) - 0.5) * 2
		steady = ratio * math.Min(1, (t.Calm+t.Hot)/(T.SteadyFullHours*60))
	}

	c := func(v float64) float64 { return math.Max(0, math.Min(1, v)) }

	return map[string]float64{"shipper": c(ship), "tidy": c(tidy), "steady": c(steady)}
}

// DominantTrait is the strongest trait, ties going to the earlier one.
func (s *State) DominantTrait() string {
	sc := s.TraitScores()

	traits := Traits()

	best := traits[0]
	for _, t := range traits[1:] {
		if sc[t] > sc[best] {
			best = t
		}
	}

	return best
}

// SpeciesPath is the dominant trait, or "balanced" when all three are high and even.
func (s *State) SpeciesPath() string {
	sc := s.TraitScores()

	lo, hi := 1.0, 0.0
	for _, v := range sc {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}

	if lo >= T.FoxMin && hi-lo <= T.FoxSpread {
		return "balanced"
	}

	return s.DominantTrait()
}

func (s *State) react(now float64, text, mood string) {
	s.Status.React = &React{Until: now + T.ReactSecs, Text: text, Mood: mood}
}

func (s *State) evolve(now float64, events *[]Event) {
	lvl := Level(s.Stats.XP)

	form := s.Form
	if form == "hatchling" && lvl >= int(T.SpeciesLevel) {
		trait := s.SpeciesPath()
		form = Species()[trait]
		*events = append(*events, ev(now, "evolve", fmt.Sprintf("grew into a %s (%s)", form, trait)))
		s.react(now, "✧ "+form+"!", "thriving")
	}

	if fins, ok := Finals()[form]; ok && lvl >= int(T.FinalLevel) {
		trait := s.DominantTrait()
		form = fins[trait]
		*events = append(*events, ev(now, "evolve", fmt.Sprintf("evolved into a %s (%s)", form, trait)))
		s.react(now, "✧ "+form+"!", "thriving")
	}

	s.Form = form
}

// DevolvePreview is (form it would become, xp it would keep), ok=false for a hatchling.
func (s *State) DevolvePreview() (string, float64, bool) {
	if s.Form == "hatchling" || s.Form == "" {
		return "", 0, false
	}

	back, lost := "hatchling", T.SpeciesLevel
	if p, ok := Parent()[s.Form]; ok {
		back, lost = p, T.FinalLevel
	}
	// drop below the threshold so it has to be re-earned — and may branch differently
	return back, math.Min(s.Stats.XP, XPForLevel(int(lost-T.DevolveLevels))), true
}

// Devolve drops the pet back a tier (see DevolvePreview).
func (s *State) Devolve(now float64, events *[]Event, reason string) {
	back, keep, ok := s.DevolvePreview()
	if !ok {
		return
	}

	s.Stats.XP, s.Form = keep, back
	s.Devolved++

	*events = append(*events, ev(now, "devolve", fmt.Sprintf("%s back into a %s", reason, back)))
	s.react(now, "devolved → "+back, "wilting")
}

func dayCounts(d *Day) bool { return d != nil && d.Commits >= 1 && d.Fit >= 50 }

// updateStreak settles every finished weekday since the last settlement. Weekends are skipped.
func (s *State) updateStreak(now float64) {
	st := &s.Streak
	today := dayStart(at(now))
	yesterday := today.AddDate(0, 0, -1)

	yk := yesterday.Format("2006-01-02")
	if st.Through == nil {
		st.Through = &yk
		return
	}

	th, ok := parseDate(*st.Through)
	if !ok {
		st.Through = &yk
		return
	}

	d := th.AddDate(0, 0, 1)
	if yesterday.Sub(d) > 400*24*time.Hour {
		d = yesterday.AddDate(0, 0, -400)
	}

	for !d.After(yesterday) {
		if isWeekday(d) {
			k := d.Format("2006-01-02")
			if dayCounts(s.Days[k]) {
				st.Count++
				st.LastDay = &k
			} else {
				st.Count = 0
			}
		}

		d = d.AddDate(0, 0, 1)
	}

	st.Through = &yk
}

// CurrentStreak is the settled streak plus today, provisionally, if today already qualifies.
func (s *State) CurrentStreak(now float64) int {
	n := int(s.Streak.Count)
	if isWeekday(at(now)) && dayCounts(s.Days[DateKey(now)]) {
		n++
	}

	return n
}

func (s *State) prune(now float64) {
	for k, v := range s.Sessions {
		if now-v.LastSeen >= T.KeepSessionDays*86400 {
			delete(s.Sessions, k)
		}
	}

	today := dayStart(at(now))

	cc := today.AddDate(0, 0, -int(T.KeepCommitDays)).Format("2006-01-02")
	for k, v := range s.SeenCommits {
		if v < cc {
			delete(s.SeenCommits, k)
		}
	}

	dc := today.AddDate(0, 0, -int(T.KeepDays)).Format("2006-01-02")
	for k := range s.Days {
		if k < dc {
			delete(s.Days, k)
		}
	}

	live := map[string]bool{}
	for _, v := range s.Sessions {
		live[v.Top] = true
	}

	for k := range s.Repos {
		if !live[k] {
			delete(s.Repos, k)
		}
	}
}

// advance is time passing: work-clock decay, redline drain, temper decay, streaks, pruning.
func (s *State) advance(now float64) float64 {
	last := s.LastTick
	if last == 0 {
		last = now
	}

	wm := WorkMinutes(last, now)

	s.Stats.Fed -= T.FedDecayPerMin * wm
	for _, v := range s.Sessions {
		if now-v.ActiveAt < T.ActiveSecs && v.LastCtx != nil && *v.LastCtx > T.FitRedlineCtx {
			s.Stats.Fit -= T.FitRedlinePerMin * wm
			break
		}
	}

	decay := math.Exp(-wm / (T.TraitTauHours * 60))
	s.Temper.Ship *= decay
	s.Temper.Tidy *= decay
	s.Temper.Calm *= decay
	s.Temper.Hot *= decay
	s.updateStreak(now)
	s.prune(now)

	return wm
}

// Misuse is the wilting gauge. Fed is left out: it falls with plain absence, and absence isn't misuse.
func (s *State) Misuse() float64 { return (s.Stats.Fit + s.Stats.Rested) / 2 }

// wilt runs after the tick's gains, so a devolve's XP cap can't be climbed back in the same tick.
func (s *State) wilt(now, wm float64, events *[]Event) {
	m := s.Misuse()
	if m < T.WiltBelow {
		if now-s.ActiveAt < T.ActiveSecs {
			s.Wilt += wm
		}
	} else if m >= T.WiltRecover {
		s.Wilt = math.Max(0, s.Wilt-wm)
	}

	if s.Wilt >= T.WiltMinutes {
		s.Wilt = 0
		s.Devolve(now, events, "wilted")
	}
}

// --- observing a statusline payload -----------------------------------------

func obj(d any, keys ...string) map[string]any {
	for _, k := range keys {
		m, ok := d.(map[string]any)
		if !ok {
			return map[string]any{}
		}

		d = m[k]
	}

	if m, ok := d.(map[string]any); ok {
		return m
	}

	return map[string]any{}
}

func num(v any) *float64 {
	switch x := v.(type) {
	case float64:
		return &x
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%g", &f); err == nil {
			return &f
		}
	}

	return nil
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return ""
}

// payload is the part of a statusline payload the pet cares about.
type payload struct {
	sid        string
	lines, ctx *float64
	five       *float64
	cwd        string
}

func parsePayload(data map[string]any) payload {
	p := payload{
		sid:  str(data["session_id"]),
		ctx:  num(obj(data, "context_window")["used_percentage"]),
		five: num(obj(data, "rate_limits", "five_hour")["used_percentage"]),
		cwd:  str(obj(data, "workspace")["current_dir"]),
	}
	if p.cwd == "" {
		p.cwd = str(data["cwd"])
	}

	cost := obj(data, "cost")
	added, removed := num(cost["total_lines_added"]), num(cost["total_lines_removed"])

	if added != nil || removed != nil {
		v := 0.0

		for _, x := range []*float64{added, removed} {
			if x != nil {
				v += *x
			}
		}

		v = math.Trunc(v)
		p.lines = &v
	}

	return p
}

func (s *State) observe(data map[string]any, now float64, events *[]Event, poll bool) {
	p := parsePayload(data)

	// rested eases toward the rate-limit target on wall time (it tracks, it doesn't decay)
	k := 1 - math.Exp(-math.Max(0, now-s.LastTick)/T.RestedTauSecs)
	s.Stats.Rested += (RestedTarget(p.five) - s.Stats.Rested) * k

	if p.sid == "" {
		return
	}

	sess := s.trackSession(p, now)
	s.accrueTemper(p.five, now)

	if poll && p.cwd != "" {
		if sess.Cwd != p.cwd {
			sess.Cwd = p.cwd
			sess.Top = gitToplevel(p.cwd)
		}

		if sess.Top != "" {
			s.pollRepo(sess, now, events)
		}
	}
}

// trackSession updates one session's baseline, gains and activity from a payload.
func (s *State) trackSession(p payload, now float64) *Session {
	sess := s.Sessions[p.sid]
	if sess == nil {
		sess = &Session{Lines: p.lines, LastCtx: p.ctx, LastSeen: now}
		if p.ctx != nil {
			sess.MaxCtx = *p.ctx
		}

		s.Sessions[p.sid] = sess

		return sess
	}

	moved := (p.lines != nil && sess.Lines != nil && *p.lines != *sess.Lines) ||
		(p.ctx != nil && sess.LastCtx != nil && *p.ctx != *sess.LastCtx)
	if moved {
		sess.ActiveAt, s.ActiveAt = now, now
	}

	switch {
	case p.lines != nil && sess.Lines == nil:
		sess.Lines = p.lines // first sighting of a total is a baseline, not a gain
	case p.lines != nil:
		if delta := *p.lines - *sess.Lines; delta > 0 {
			s.gainLines(sess, delta, now)
		}

		sess.Lines = p.lines
	}

	if p.ctx != nil {
		sess.LastCtx = p.ctx
		sess.MaxCtx = math.Max(sess.MaxCtx, *p.ctx)
	}

	sess.LastSeen = now

	return sess
}

// accrueTemper counts active work minutes as calm or hot, by 5h rate-limit usage.
func (s *State) accrueTemper(five *float64, now float64) {
	if five == nil || now-s.ActiveAt >= T.ActiveSecs {
		return
	}

	wm := WorkMinutes(s.LastTick, now)
	if *five <= T.RestedEasyUntil {
		s.Temper.Calm += wm
	} else {
		s.Temper.Hot += wm
	}
}

func (s *State) gainLines(sess *Session, delta, now float64) {
	s.Stats.Fed += math.Min(T.FedLinesCap, delta/T.FedLinesPerPoint)
	s.day(now).Lines += delta
	s.LineCarry += delta
	pts := math.Floor(s.LineCarry / T.XPLinesPerPoint)

	s.LineCarry -= pts * T.XPLinesPerPoint
	if pts > 0 {
		s.addXP(now, pts)
	}

	sess.Shipped = true
}

func (s *State) pollRepo(sess *Session, now float64, events *[]Event) {
	top := sess.Top

	repo := s.Repos[top]
	if repo == nil {
		repo = &Repo{}
		s.Repos[top] = repo
	}

	if now-repo.CheckedAt < T.PollSecs {
		return
	}

	repo.CheckedAt = now
	if repo.Email == "" {
		if repo.Email = gitEmail(top); repo.Email == "" {
			return
		}
	}

	fresh := s.freshCommits(gitCommitsToday(top, repo.Email), DateKey(now))
	if len(fresh) > 0 {
		s.creditCommits(sess, fresh, now, filepath.Base(top), events)
	}
}

// freshCommits filters out commits already counted. The author-time+subject key survives
// rebase/amend/cherry-pick; the sha covers state written before keys existed.
func (s *State) freshCommits(commits [][2]string, today string) [][2]string {
	var fresh [][2]string

	for _, c := range commits {
		sha, key := c[0], c[1]
		_, a := s.SeenCommits[sha]
		_, b := s.SeenCommits[key]

		if !a && !b {
			fresh = append(fresh, c)

			continue
		}

		if !a {
			s.SeenCommits[sha] = today
		}

		if !b {
			s.SeenCommits[key] = today
		}
	}

	return fresh
}

func (s *State) creditCommits(sess *Session, fresh [][2]string, now float64, repo string, events *[]Event) {
	today := DateKey(now)
	mult := XPMultiplier(sess.LastCtx)
	per := math.Round(T.XPPerCommit * mult)

	for _, c := range fresh {
		s.SeenCommits[c[0]], s.SeenCommits[c[1]] = today, today
		s.Stats.Fed += T.FedPerCommit
		s.addXP(now, per)
		s.day(now).Commits++
	}

	n := float64(len(fresh))
	s.LastCommitAt = &now
	sess.Shipped = true
	s.Temper.Ship += n
	s.react(now, fmt.Sprintf("nom +%dxp", int(per*n)), "thriving")

	plural := ""
	if n > 1 {
		plural = "s"
	}

	*events = append(*events, ev(now, "commit", fmt.Sprintf("%d commit%s in %s (+%d xp, ×%g)",
		int(n), plural, repo, int(per*n), mult)))
}

// --- hooks -------------------------------------------------------------------

func (s *State) onPreCompact(data map[string]any, now float64, events *[]Event) {
	if str(data["trigger"]) == "auto" {
		s.Stats.Fit += T.FitAutoCompact
		s.Status.BloatedUntil = now + T.BloatedSecs
		s.day(now).AutoCompacts++
		s.Temper.Tidy--

		*events = append(*events, ev(now, "auto_compact", "auto-compacted — bloated"))
	} else {
		*events = append(*events, ev(now, "compact", "manual compact"))
	}
}

// isWrapReason is whether a SessionEnd reason counts as wrapping up (not resuming elsewhere).
func isWrapReason(r string) bool { return r == "clear" || r == "prompt_input_exit" || r == "logout" }

func (s *State) onSessionEnd(data map[string]any, now float64, events *[]Event) {
	sid := str(data["session_id"])

	sess := s.Sessions[sid]
	if sess == nil || !isWrapReason(str(data["reason"])) {
		return
	}

	ctx := 0.0
	if sess.LastCtx != nil {
		ctx = *sess.LastCtx
	}

	if sess.Shipped && ctx < T.CleanWrapCtx {
		s.Stats.Fit += T.FitCleanWrap
		s.day(now).CleanWraps++
		s.Temper.Tidy++
		s.react(now, fmt.Sprintf("tidy +%gfit", T.FitCleanWrap), "thriving")
		*events = append(*events, ev(now, "clean_wrap", fmt.Sprintf("clean wrap at %d%% ctx", int(math.Round(ctx)))))
	}

	delete(s.Sessions, sid)
}

// Tick is one locked update. data is a statusline payload; hook is the hook event name + payload.
func (s *State) Tick(
	now float64, events *[]Event, data map[string]any, hook string, hookData map[string]any, poll bool,
) {
	s.Ensure()
	lvl0 := Level(s.Stats.XP)

	wm := s.advance(now)
	if data != nil {
		s.observe(data, now, events, poll)
	}

	switch hook {
	case "PreCompact":
		s.onPreCompact(hookData, now, events)
	case "SessionEnd":
		s.onSessionEnd(hookData, now, events)
	}

	clamp(s)
	s.wilt(now, wm, events)
	s.day(now).Fit = s.Stats.Fit

	s.LastTick = now
	if lvl1 := Level(s.Stats.XP); lvl1 > lvl0 {
		*events = append(*events, ev(now, "level", fmt.Sprintf("reached level %d", lvl1)))
		s.react(now, fmt.Sprintf("✧ L%d!", lvl1), "thriving")
	}

	s.evolve(now, events)
}

// BestDay is the highest-XP day on record.
func (s *State) BestDay() (string, *Day) {
	keys := make([]string, 0, len(s.Days))
	for k := range s.Days {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var (
		bk string
		bd *Day
	)
	for _, k := range keys {
		if d := s.Days[k]; bd == nil || d.XP > bd.XP {
			bk, bd = k, d
		}
	}

	return bk, bd
}
