package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const bin = "/opt/homebrew/bin/claude-pet"

type rig struct {
	t             *testing.T
	home, petHome string
	settings      string
}

func newRig(t *testing.T) *rig {
	home, petHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PET_HOME", petHome)
	return &rig{t, home, petHome, filepath.Join(home, ".claude", "settings.json")}
}

func (r *rig) run(o Options) (string, error) {
	var out bytes.Buffer
	o.Out, o.Bin, o.Version, o.In = &out, bin, "test", strings.NewReader("")
	err := Run(o)
	return out.String(), err
}

func (r *rig) write(raw string) {
	os.MkdirAll(filepath.Dir(r.settings), 0o755)
	os.WriteFile(r.settings, []byte(raw), 0o644)
}

func (r *rig) read() map[string]any {
	b, err := os.ReadFile(r.settings)
	if err != nil {
		r.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		r.t.Fatal(err)
	}
	return m
}

func statuslineCmd(m map[string]any) string {
	sl, _ := m["statusLine"].(map[string]any)
	c, _ := sl["command"].(string)
	return c
}

func ourHooks(m map[string]any, ev string) int {
	n := 0
	hooks, _ := m["hooks"].(map[string]any)
	list, _ := hooks[ev].([]any)
	for _, mm := range list {
		for _, h := range mm.(map[string]any)["hooks"].([]any) {
			if c, _ := h.(map[string]any)["command"].(string); isOurHook(c) {
				n++
			}
		}
	}
	return n
}

func mustJSON(t *testing.T, s string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestExpressFresh(t *testing.T) {
	r := newRig(t)
	out, err := r.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := r.read()
	if statuslineCmd(m) != bin+" statusline" {
		t.Fatal(statuslineCmd(m))
	}
	if m["statusLine"].(map[string]any)["refreshInterval"] != 5000.0 {
		t.Fatal("no refresh")
	}
	for _, ev := range hookEvents {
		if ourHooks(m, ev) != 1 {
			t.Fatal(ev)
		}
	}
	if !strings.Contains(out, "Change it later") {
		t.Fatal(out)
	}
}

const existing = `{
  "model": "opus",
  "statusLine": {"type": "command", "command": "bash ~/my status.sh", "padding": 1},
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "echo bye"}]}]
  },
  "zzz": "<keep & order>"
}`

func TestExpressWrapsExistingAndKeepsEverythingElse(t *testing.T) {
	r := newRig(t)
	r.write(existing)
	if _, err := r.run(Options{}); err != nil {
		t.Fatal(err)
	}
	m, orig := r.read(), mustJSON(t, existing)
	if statuslineCmd(m) != bin+" statusline --wrap 'bash ~/my status.sh'" {
		t.Fatal(statuslineCmd(m))
	}
	if m["statusLine"].(map[string]any)["padding"] != 1.0 || m["model"] != "opus" || m["zzz"] != "<keep & order>" {
		t.Fatal(m)
	}
	h, oh := m["hooks"].(map[string]any), orig["hooks"].(map[string]any)
	if !reflect.DeepEqual(h["Stop"], oh["Stop"]) || !reflect.DeepEqual(h["SessionEnd"].([]any)[0], oh["SessionEnd"].([]any)[0]) {
		t.Fatal(h)
	}
	b, _ := os.ReadFile(r.settings)
	s := string(b)
	if !(strings.Index(s, `"model"`) < strings.Index(s, `"statusLine"`) && strings.Index(s, `"hooks"`) < strings.Index(s, `"zzz"`)) {
		t.Fatal("key order changed:\n" + s)
	}
	if strings.Contains(s, `\u003c`) {
		t.Fatal("html-escaped output")
	}
	entries, _ := os.ReadDir(filepath.Dir(r.settings))
	backups := 0
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-claude-pet-") {
			backups++
		}
	}
	if backups != 1 {
		t.Fatal(backups)
	}
}

func TestHookOrderPreserved(t *testing.T) {
	r := newRig(t)
	orig := `{"hooks": {"SessionEnd": [{"matcher": "*", "hooks": [{"type": "command", "timeout": 9, "command": "echo bye"}]}]}}`
	r.write(orig)
	r.run(Options{Statusline: "skip"})
	b, _ := os.ReadFile(r.settings)
	if !strings.Contains(string(b), "\"matcher\": \"*\",\n") || strings.Index(string(b), `"timeout": 9`) > strings.Index(string(b), `"echo bye"`) {
		t.Fatal("user hook reordered:\n" + string(b))
	}
	r.run(Options{Uninstall: true})
	b, _ = os.ReadFile(r.settings)
	want, _ := ParseObject([]byte(orig))
	if string(b) != string(want.Pretty()) {
		t.Fatalf("got\n%s\nwant\n%s", b, want.Pretty())
	}
}

func TestRerunIsIdempotent(t *testing.T) {
	r := newRig(t)
	r.run(Options{})
	first, _ := os.ReadFile(r.settings)
	out, _ := r.run(Options{})
	second, _ := os.ReadFile(r.settings)
	if string(first) != string(second) || !strings.Contains(out, "already") {
		t.Fatal(out)
	}
}

func TestUninstallRestoresExactly(t *testing.T) {
	r := newRig(t)
	r.write(existing)
	r.run(Options{})
	if _, err := r.run(Options{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.read(), mustJSON(t, existing)) {
		t.Fatalf("%v", r.read())
	}
	b, _ := os.ReadFile(r.settings)
	want, _ := ParseObject([]byte(existing))
	if string(b) != string(want.Pretty()) {
		t.Fatalf("order/format differs:\n%s\nvs\n%s", b, want.Pretty())
	}
}

func TestUninstallFromFreshRemovesStatusline(t *testing.T) {
	r := newRig(t)
	r.run(Options{})
	r.run(Options{Uninstall: true})
	if len(r.read()) != 0 {
		t.Fatal(r.read())
	}
}

func TestUninstallWithoutRecordStillUnwraps(t *testing.T) {
	r := newRig(t)
	r.write(`{"statusLine": {"type": "command", "command": "node ~/sl.js --x 'y z'"}}`)
	r.run(Options{})
	os.Remove(filepath.Join(r.petHome, "install.json"))
	r.run(Options{Uninstall: true})
	if statuslineCmd(r.read()) != "node ~/sl.js --x 'y z'" {
		t.Fatal(statuslineCmd(r.read()))
	}
}

func TestHandIntegratedStatuslineLeftAlone(t *testing.T) {
	r := newRig(t)
	script := filepath.Join(r.home, "sl.sh")
	os.WriteFile(script, []byte("pet=$(claude-pet statusline --segment)\n"), 0o644)
	r.write(`{"statusLine": {"type": "command", "command": "bash ` + script + `"}}`)
	out, _ := r.run(Options{})
	if statuslineCmd(r.read()) != "bash "+script || !strings.Contains(out, "already shows the pet") {
		t.Fatal(out)
	}
}

func TestBadSettingsNotTouched(t *testing.T) {
	r := newRig(t)
	r.write("{ not json")
	if _, err := r.run(Options{}); err == nil {
		t.Fatal("expected an error")
	}
	if b, _ := os.ReadFile(r.settings); string(b) != "{ not json" {
		t.Fatal(string(b))
	}
}

func TestCustomFlags(t *testing.T) {
	r := newRig(t)
	h, _ := ParseHours("8-16")
	no := false
	if _, err := r.run(Options{Custom: true, Hours: h, Statusline: "skip", Hooks: &no, Name: "Pip"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(r.petHome, "config.json"))
	if !reflect.DeepEqual(mustJSON(t, string(cfg)), map[string]any{"work_start": 8.0, "work_end": 16.0}) {
		t.Fatal(string(cfg))
	}
	st, _ := os.ReadFile(filepath.Join(r.petHome, "state.json"))
	if mustJSON(t, string(st))["name"] != "Pip" {
		t.Fatal(string(st))
	}
	if _, err := os.Stat(r.settings); err == nil {
		t.Fatal("settings written despite skip + no hooks")
	}
}

func TestCustomInteractive(t *testing.T) {
	r := newRig(t)
	r.write(`{"statusLine": {"type": "command", "command": "bash ~/sl.sh"}}`)
	var out bytes.Buffer
	err := Run(Options{Custom: true, Interactive: true, In: strings.NewReader("8-16\nreplace\nn\nPip\n"),
		Out: &out, Bin: bin, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := r.read()
	if statuslineCmd(m) != bin+" statusline" || ourHooks(m, "PreCompact") != 0 {
		t.Fatal(m)
	}
	if !strings.Contains(out.String(), "How the pet works") || !strings.Contains(out.String(), "name: Pip") {
		t.Fatal(out.String())
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	r := newRig(t)
	out, _ := r.run(Options{DryRun: true})
	if !strings.Contains(out, "Would do") {
		t.Fatal(out)
	}
	if _, err := os.Stat(r.settings); err == nil {
		t.Fatal("wrote settings")
	}
	if e, _ := os.ReadDir(r.petHome); len(e) != 0 {
		t.Fatal("wrote pet files")
	}
}

func TestUpgradeFromPythonInstall(t *testing.T) {
	r := newRig(t)
	r.write(`{"statusLine": {"type": "command", "command": "python3 /h/.claude/pet/pet_statusline.py --wrap 'bash ~/sl.sh'", "refreshInterval": 5000},
  "hooks": {"PreCompact": [{"hooks": [{"type": "command", "command": "python3 /h/.claude/pet/hook.py", "async": true, "timeout": 5}]}],
            "SessionEnd": [{"hooks": [{"type": "command", "command": "python3 /h/.claude/pet/hook.py", "async": true, "timeout": 5}]}]}}`)
	os.WriteFile(filepath.Join(r.petHome, "install.json"),
		[]byte(`{"statusline_before": {"type": "command", "command": "bash ~/sl.sh"}, "statusline_after": {"type": "command"}}`), 0o644)
	out, err := r.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := r.read()
	if statuslineCmd(m) != bin+" statusline --wrap 'bash ~/sl.sh'" || !strings.Contains(out, "upgraded") {
		t.Fatal(statuslineCmd(m), out)
	}
	for _, ev := range hookEvents {
		if ourHooks(m, ev) != 1 || strings.Contains(string(mustMarshalT(m)), "hook.py") {
			t.Fatal(ev, m)
		}
	}
	b, _ := os.ReadFile(r.settings)
	raw := string(b)
	if i := strings.Index(raw, `"type"`); i < 0 || i > strings.Index(raw, `claude-pet hook`) {
		t.Fatal("upgraded hook entry lost its key order:\n" + raw)
	}
	r.run(Options{Uninstall: true})
	if statuslineCmd(r.read()) != "bash ~/sl.sh" {
		t.Fatal(r.read())
	}
}

func mustMarshalT(v any) []byte { b, _ := json.Marshal(v); return b }

func TestShellQuoteRoundTrip(t *testing.T) {
	for _, s := range []string{"plain", "bash ~/my status.sh", "it's", `a "b" c`, `C:\Program Files\x.exe`} {
		if got := ShellSplit("x --wrap " + ShellQuote(s)); len(got) != 3 || got[2] != s {
			t.Fatalf("%q → %q", s, got)
		}
	}
}

func TestParseHours(t *testing.T) {
	for _, bad := range []string{"17-9", "9", "a-b", "-1-5", "9-25"} {
		if _, err := ParseHours(bad); err == nil {
			t.Fatal(bad)
		}
	}
	if h, err := ParseHours(" 8 - 16 "); err != nil || h[0] != 8 || h[1] != 16 {
		t.Fatal(h, err)
	}
}

func TestWindowsPathsUseForwardSlashes(t *testing.T) {
	win := shellPath(`C:\Users\x\AppData\Local\Programs\claude-pet\claude-pet.exe`, "windows")
	if win != "C:/Users/x/AppData/Local/Programs/claude-pet/claude-pet.exe" {
		t.Fatal(win)
	}
	if got := ShellSplit(ShellQuote(win) + " statusline"); got[0] != win {
		t.Fatal(got)
	}
	if shellPath("/opt/homebrew/bin/claude-pet", "darwin") != "/opt/homebrew/bin/claude-pet" {
		t.Fatal("unix path changed")
	}
}
