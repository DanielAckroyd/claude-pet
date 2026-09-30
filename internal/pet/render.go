package pet

import (
	"math"
	"math/rand/v2"
	"strings"
	"unicode"
)

// Form is a face template (E = eye, M = mouth) and the species' own mouth, which replaces the
// default ᴗ. Mood-specific mouths (□, ロ, _) still show through. Width-checked glyphs only.
type Form struct {
	Name, Tpl, Mouth string
}

var Forms = []Form{
	{"hatchling", "(EME)", ""},
	{"bear", "ʕEMEʔ", "ᴥ"},
	{"cat", "(=EME=)", "ω"},
	{"owl", "{EME}", "v"},
	{"seal", "ᶘEMEᶅ", "ᴥ"},
	{"koala", "ʢEMEʡ", "ᴥ"},
	{"dog", "UEMEU", "ᴥ"},
	{"tiger", "(≡EME≡)", "ω"},
	{"neko", "/ᐠEMEᐟ\\", "ꞈ"},
	{"lynx", "/\\EME/\\", "ω"},
	{"penguin", "<(EME)>", "v"},
	{"dove", "~(EME)~", "v"},
	{"great owl", "{(E)M(E)}", "v"},
	{"fox", "ˆ(EME)ˆ", "ᴥ"},
	{"wolf", "ᐡEMEᐡ", "ᴥ"},
	{"fennec", "ᐠ(EME)ᐟ", "ᴥ"},
	{"kitsune", "ミᕱEMEᕱ彡", "ᴥ"},
}

// Evolution: dominant trait at L5 picks the species (all three high and close → the hidden fox),
// dominant trait at L15 picks the final form. Wilting devolves one tier.
var (
	Species    = map[string]string{"shipper": "bear", "tidy": "cat", "steady": "owl", "balanced": "fox"}
	SpeciesOrd = []string{"shipper", "tidy", "steady", "balanced"}
	Finals     = map[string]map[string]string{
		"bear": {"shipper": "seal", "tidy": "koala", "steady": "dog"},
		"cat":  {"shipper": "tiger", "tidy": "neko", "steady": "lynx"},
		"owl":  {"shipper": "penguin", "tidy": "dove", "steady": "great owl"},
		"fox":  {"shipper": "wolf", "tidy": "fennec", "steady": "kitsune"},
	}
	Parent = func() map[string]string {
		p := map[string]string{}
		for sp, d := range Finals {
			for _, f := range d {
				p[f] = sp
			}
		}
		return p
	}()
)

type Mood struct {
	Key, Eyes, Mouth, Word, Color string
}

// Moods in priority order (see MoodOf).
var Moods = []Mood{
	{"bloated", "°", "□", "bloated", "bad"},
	{"wilting", ";", "_", "wilting", "bad"},
	{"missed", "T", "_", "missed you", "meh"},
	{"exhausted", "×", "_", "exhausted", "bad"},
	{"starving", "•", "ロ", "starving", "bad"},
	{"sleepy", "-", "_", "sleepy", "meh"},
	{"sulky", "¬", "_", "sulky", "meh"},
	{"thriving", "^", "ᴗ", "thriving", "good"},
	{"content", "•", "ᴗ", "content", "good"},
}

func moodByKey(k string) (Mood, bool) {
	for _, m := range Moods {
		if m.Key == k {
			return m, true
		}
	}
	return Mood{}, false
}

func formByName(n string) Form {
	for _, f := range Forms {
		if f.Name == n {
			return f
		}
	}
	return Forms[0]
}

const (
	Crown     = "♔"
	Shiny     = "\033[38;5;220m"
	cameoOdds = 200
)

var Cameos = []string{"c[_]", "♪", "✧"}

// Colors maps a mood's colour key ("good", "meh", "bad") plus "reset" to ANSI codes.
type Colors map[string]string

var ANSI = Colors{"good": "\033[38;5;42m", "meh": "\033[38;5;179m", "bad": "\033[38;5;203m", "reset": "\033[0m"}

func (s *State) MoodOf(now float64) string {
	st := s.Stats
	if now < s.Status.BloatedUntil {
		return "bloated"
	}
	if s.Wilt > 0 && s.Misuse() < T.WiltBelow {
		return "wilting"
	}
	since := s.BornAt
	if s.LastCommitAt != nil {
		since = *s.LastCommitAt
	}
	if since == 0 {
		since = now
	}
	if WorkMinutes(since, now) > T.MissedWorkdays*(T.WorkEnd-T.WorkStart)*60 {
		return "missed"
	}
	if st.Rested < 25 {
		return "exhausted"
	}
	if st.Fed < 20 {
		return "starving"
	}
	if !IsWorkTime(now) {
		return "sleepy"
	}
	avg := (st.Fed + st.Fit + st.Rested) / 3
	if avg < 40 {
		return "sulky"
	}
	if avg > 80 && s.CurrentStreak(now) >= int(T.ThrivingStreak) {
		return "thriving"
	}
	return "content"
}

// Face renders a form in a mood. eyes, if set, replaces the default • eye.
func Face(form, mood string, blink bool, eyes string) string {
	f := formByName(form)
	m, _ := moodByKey(mood)
	e, mouth := m.Eyes, m.Mouth
	if eyes != "" && e == "•" {
		e = eyes
	}
	if blink {
		e = "-"
	}
	if mouth == "ᴗ" && f.Mouth != "" {
		mouth = f.Mouth
	}
	return strings.NewReplacer("E", e, "M", mouth).Replace(f.Tpl)
}

func Frame(now float64) int64 { return int64(math.Floor(now / 5)) }

func Cameo(now float64) string {
	r := rand.New(rand.NewPCG(uint64(Frame(now)), 7))
	if r.Float64() < 1.0/cameoOdds {
		return Cameos[r.IntN(len(Cameos))]
	}
	return ""
}

// Render is the statusline segment: face, charm, crown, cameo, and the mood word (or a reaction).
func (s *State) Render(now float64, colors Colors) string {
	s.Ensure()
	mk, word := s.MoodOf(now), ""
	if r := s.Status.React; r != nil && now < r.Until {
		if _, ok := moodByKey(r.Mood); ok {
			mk, word = r.Mood, r.Text
		}
	}
	m, _ := moodByKey(mk)
	if word == "" {
		word = m.Word
	}
	f := Face(s.Form, mk, Frame(now)%4 == 3, s.Look.Eyes)
	crown := ""
	if s.CurrentStreak(now) >= int(T.CrownStreak) {
		crown = Crown
	}
	cam := Cameo(now)
	if cam != "" {
		cam = " " + cam
	}
	col := colors[m.Color]
	if colors != nil && m.Color == "good" && s.Look.Shiny {
		col = Shiny
	}
	reset := ""
	if col != "" {
		reset = colors["reset"]
	}
	return col + f + s.Look.Charm + crown + cam + " " + word + reset
}

// Width is the terminal column width of text (East Asian wide/fullwidth = 2, combining = 0).
func Width(text string) int {
	w := 0
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Mn, r):
		case wide(r):
			w += 2
		default:
			w++
		}
	}
	return w
}

func wide(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) || (r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
		(r >= 0xAC00 && r <= 0xD7A3) || (r >= 0xF900 && r <= 0xFAFF) || (r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) || (r >= 0xFFE0 && r <= 0xFFE6) || (r >= 0x1F300 && r <= 0x1FAFF)
}
