package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claude-pet-bin")
	binPath = filepath.Join(dir, "claude-pet")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, stdin string, args ...string) (string, int) {
	t.Helper()
	c := exec.Command(binPath, args...)
	c.Env = append(os.Environ(), "PET_HOME="+t.TempDir())
	c.Stdin = strings.NewReader(stdin)
	out, err := c.Output()
	code := 0
	if e, ok := err.(*exec.ExitError); ok {
		code = e.ExitCode()
	}
	return string(out), code
}

const payload = `{"session_id":"s","model":{"display_name":"Opus"},"context_window":{"used_percentage":34}}`

func TestStatuslineModes(t *testing.T) {
	out, code := run(t, payload, "statusline")
	if code != 0 || !strings.Contains(out, "(") || !strings.HasSuffix(out, "Opus │ ctx 34%") {
		t.Fatalf("%d %q", code, out)
	}
	seg, _ := run(t, payload, "statusline", "--segment")
	if strings.Contains(seg, "Opus") || !strings.Contains(seg, "(") {
		t.Fatalf("%q", seg)
	}
}

func TestStatuslineNeverFails(t *testing.T) {
	for _, in := range []string{"", "junk", "[]", `{"session_id":3,"cost":"x"}`} {
		for _, args := range [][]string{{"statusline"}, {"statusline", "--segment"}, {"statusline", "--wrap"},
			{"statusline", "--wrap", "exit 3"}} {
			if _, code := run(t, in, args...); code != 0 {
				t.Fatalf("%q %v exited %d", in, args, code)
			}
		}
	}
}

func TestWrapPrefixesAndPassesStdin(t *testing.T) {
	out, _ := run(t, payload, "statusline", "--wrap", "cat") // cat echoes the same stdin back
	if !strings.HasSuffix(out, " "+payload) || !strings.HasPrefix(out, "\033[") {
		t.Fatalf("%q", out)
	}
	out, _ = run(t, payload, "statusline", "--wrap", `printf 'one\ntwo\n'`)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], " one") || lines[1] != "two" {
		t.Fatalf("%q", out)
	}
}

func TestHookIsSilent(t *testing.T) {
	for _, in := range []string{"", "junk", `{"hook_event_name":"PreCompact","trigger":"auto"}`,
		`{"hook_event_name":"SessionEnd","reason":"clear","session_id":"x"}`} {
		if out, code := run(t, in, "hook"); out != "" || code != 0 {
			t.Fatalf("%q → %q %d", in, out, code)
		}
	}
}

func TestSetupNonInteractiveWithoutFlagShowsHelp(t *testing.T) {
	c := exec.Command(binPath, "setup")
	home := t.TempDir()
	c.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PET_HOME="+t.TempDir())
	out, _ := c.Output()
	if c.ProcessState.ExitCode() != 2 || !strings.Contains(string(out), "--express") {
		t.Fatalf("%d %s", c.ProcessState.ExitCode(), out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Fatal("wrote settings")
	}
}

func TestCommands(t *testing.T) {
	for _, args := range [][]string{{"tree"}, {"glyphs"}, {"version"}, {"log"}, {}} {
		if _, code := run(t, "", args...); code != 0 {
			t.Fatalf("%v exited %d", args, code)
		}
	}
	if _, code := run(t, "", "nope"); code != 2 {
		t.Fatal("unknown command should exit 2")
	}
}
