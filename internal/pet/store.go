package pet

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Home is where the pet lives: $PET_HOME, else ~/.claude/pet.
func Home() string {
	if h := os.Getenv("PET_HOME"); h != "" {
		return h
	}

	h, err := os.UserHomeDir()
	if err != nil {
		h = "."
	}

	return filepath.Join(h, ".claude", "pet")
}

func path(name string) string { return filepath.Join(Home(), name) }

// LoadConfig applies numeric TUNING overrides from config.json, so tweaks survive upgrades.
func LoadConfig() {
	b, err := os.ReadFile(path("config.json"))
	if err != nil {
		return
	}

	var cfg map[string]any
	if json.Unmarshal(b, &cfg) == nil {
		T.Override(cfg)
	}
}

// Lock tries for the exclusive pet lock for up to wait. release is nil when it wasn't won.
func Lock(wait time.Duration) (release func(), err error) {
	if err := os.MkdirAll(Home(), 0o750); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path(".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(wait)

	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}

		if ok {
			return func() { unlock(f); f.Close() }, nil
		}

		if time.Now().After(deadline) {
			f.Close()
			return nil, nil
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// ReadState is a read-only load: nil if missing or unreadable, never touches the file.
func ReadState() *State {
	b, err := os.ReadFile(path("state.json"))
	if err != nil {
		return nil
	}

	var (
		probe struct {
			Stats *Stats `json:"stats"`
		}
		s State
	)
	if json.Unmarshal(b, &probe) != nil || probe.Stats == nil || json.Unmarshal(b, &s) != nil {
		return nil
	}

	return s.Ensure()
}

// LoadOrHatch needs the lock held. A corrupt state file is moved aside, never overwritten.
func LoadOrHatch(now float64, events *[]Event) *State {
	p := path("state.json")
	if _, err := os.Stat(p); err == nil {
		if s := ReadState(); s != nil {
			return s
		}

		aside := fmt.Sprintf("%s.corrupt-%d", p, int64(now))
		if os.Rename(p, aside) == nil {
			*events = append(*events, ev(now, "corrupt", "state was unreadable, moved to "+filepath.Base(aside)))
		}
	}

	s := NewState(now)
	*events = append(*events, ev(now, "hatch", s.Name+" hatched"))

	return s
}

// SaveState writes atomically (tmp + rename), so a killed statusline can't leave a torn file.
func SaveState(s *State) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}

	p := path("state.json")

	tmp := fmt.Sprintf("%s.tmp-%d", p, os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}

	return os.Rename(tmp, p)
}

// AppendEvents adds events to the log, rotating it once it passes the size cap.
func AppendEvents(events []Event) {
	if len(events) == 0 {
		return
	}

	p := path("events.jsonl")
	if fi, err := os.Stat(p); err == nil && float64(fi.Size()) > T.LogMaxBytes {
		_ = os.Rename(p, p+".1") // one old log is kept; losing it is fine
	}

	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}

	defer func() { _ = f.Close() }()

	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}

		if _, err := f.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

// ReadEvents returns the last n events, oldest first.
func ReadEvents(n int) []Event {
	var out []Event

	for _, name := range []string{"events.jsonl", "events.jsonl.1"} {
		f, err := os.Open(path(name))
		if err != nil {
			continue
		}

		var lines []string

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)

		for sc.Scan() {
			lines = append(lines, sc.Text())
		}

		f.Close()

		for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
			var e Event
			if json.Unmarshal([]byte(lines[i]), &e) == nil {
				out = append(out, e)
			}
		}

		if len(out) >= n {
			break
		}
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	return out
}

// Segment is the statusline entrypoint: tick if the lock is free, else render read-only.
// It never panics; any failure yields "".
func Segment(data map[string]any, colors Colors, now float64) (out string) {
	defer func() {
		if recover() != nil {
			out = ""
		}
	}()

	if os.Getenv("PET_DISABLE") == "1" {
		return ""
	}

	LoadConfig()

	release, err := Lock(time.Duration(T.LockWait * float64(time.Second)))

	var s *State

	if err == nil && release != nil {
		var events []Event

		s = LoadOrHatch(now, &events)
		s.Tick(now, &events, data, "", nil, true)
		_ = SaveState(s) // a failed save only loses this tick; still render what we have

		AppendEvents(events)
		release()
	} else {
		s = ReadState()
	}

	if s == nil {
		return ""
	}

	return s.Render(now, colors)
}

// HandleHook applies a PreCompact/SessionEnd payload. Async hook, so it can wait out a tick.
func HandleHook(data map[string]any, now float64) {
	defer func() { _ = recover() }()

	if os.Getenv("PET_DISABLE") == "1" {
		return
	}

	name := str(data["hook_event_name"])
	if name != "PreCompact" && name != "SessionEnd" {
		return
	}

	LoadConfig()

	release, err := Lock(2 * time.Second)
	if err != nil || release == nil {
		return
	}
	defer release()

	var events []Event

	s := LoadOrHatch(now, &events)
	s.Tick(now, &events, nil, name, data, false)

	if SaveState(s) == nil {
		AppendEvents(events)
	}
}

// Now is the current time in epoch seconds.
func Now() float64 { return float64(time.Now().UnixNano()) / 1e9 }
