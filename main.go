// claude-pet: a little pet in your Claude Code statusline that thrives when you use Claude well.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/DanielAckroyd/claude-pet/internal/pet"
	"github.com/DanielAckroyd/claude-pet/internal/setup"
)

// version is set by the release build; `go install` falls back to the module version.
var version = "dev"

func Version() string {
	if version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return strings.TrimPrefix(bi.Main.Version, "v")
		}
	}
	return version
}

const usage = `claude-pet: a little pet in your Claude Code statusline

  claude-pet                 stat sheet: face, level, stats, path, streak, today, recent events
  claude-pet setup           wire it into Claude Code (asks express or custom)
  claude-pet log [n]         recent events
  claude-pet name NEW        rename
  claude-pet tree            the evolution paths
  claude-pet devolve [-y]    drop back a tier on purpose (costs xp, re-rolls the next branch)
  claude-pet glyphs          every form × mood × frame, to check your terminal renders them evenly
  claude-pet version

Used by Claude Code (set up for you by 'claude-pet setup'):
  claude-pet statusline [--segment | --wrap 'CMD']
  claude-pet hook

No reset command: delete ~/.claude/pet/state.json if you really mean it.
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "statusline":
		statusline(args)
		return
	case "hook":
		hook()
		return
	}
	pet.LoadConfig()
	switch cmd {
	case "":
		sheet()
	case "setup", "install":
		runSetup(args)
	case "log":
		n := 20
		if len(args) > 0 {
			if v, err := strconv.Atoi(args[0]); err == nil {
				n = v
			}
		}
		showLog(n)
	case "name":
		if len(args) == 0 || strings.TrimSpace(strings.Join(args, " ")) == "" {
			fail("claude-pet name NEW — name can't be empty")
		}
		rename(strings.TrimSpace(strings.Join(args, " ")))
	case "tree":
		tree()
	case "devolve":
		devolve(len(args) > 0 && args[0] == "-y")
	case "glyphs":
		glyphs()
	case "version", "--version", "-v":
		fmt.Println("claude-pet", Version())
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// statusline never fails: whatever happens, it prints what it can and exits 0.
func statusline(args []string) {
	raw, _ := io.ReadAll(os.Stdin)
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		data = map[string]any{}
	}
	seg := pet.Segment(data, pet.ANSI, pet.Now())
	mode, wrapped := "", ""
	for i, a := range args {
		switch a {
		case "--segment":
			mode = "segment"
		case "--wrap":
			mode = "wrap"
			if i+1 < len(args) {
				wrapped = args[i+1]
			}
		}
	}
	switch mode {
	case "segment":
		fmt.Print(seg)
	case "wrap":
		theirs := strings.TrimRight(runTheirs(wrapped, raw), "\n")
		switch {
		case seg != "" && theirs != "":
			fmt.Print(seg + " " + theirs)
		case seg != "":
			fmt.Print(seg)
		default:
			fmt.Print(theirs)
		}
	default:
		parts := []string{}
		if seg != "" {
			parts = append(parts, seg)
		}
		if m, ok := data["model"].(map[string]any); ok {
			if n, ok := m["display_name"].(string); ok && n != "" {
				parts = append(parts, n)
			}
		}
		if cw, ok := data["context_window"].(map[string]any); ok {
			if p, ok := cw["used_percentage"].(float64); ok {
				parts = append(parts, fmt.Sprintf("ctx %d%%", int(p+0.5)))
			}
		}
		fmt.Print(strings.Join(parts, " │ "))
	}
}

// runTheirs runs the user's own statusline command with the same stdin.
func runTheirs(cmd string, stdin []byte) string {
	if cmd == "" {
		return ""
	}
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		if sh, err := exec.LookPath("bash"); err == nil {
			c = exec.Command(sh, "-c", cmd)
		} else {
			c = exec.Command("cmd", "/C", cmd)
		}
	} else {
		c = exec.Command("sh", "-c", cmd)
	}
	c.Stdin = strings.NewReader(string(stdin))
	done := make(chan []byte, 1)
	go func() {
		out, _ := c.Output()
		done <- out
	}()
	select {
	case out := <-done:
		return string(out)
	case <-time.After(10 * time.Second):
		if c.Process != nil {
			c.Process.Kill()
		}
		return ""
	}
}

func hook() {
	defer func() { recover(); os.Exit(0) }()
	raw, _ := io.ReadAll(os.Stdin)
	var data map[string]any
	if json.Unmarshal(raw, &data) == nil {
		pet.HandleHook(data, pet.Now())
	}
}

func runSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	express := fs.Bool("express", false, "")
	custom := fs.Bool("custom", false, "")
	uninstall := fs.Bool("uninstall", false, "")
	dry := fs.Bool("dry-run", false, "")
	hours := fs.String("hours", "", "")
	sl := fs.String("statusline", "", "")
	noHooks := fs.Bool("no-hooks", false, "")
	name := fs.String("name", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Print(setupUsage)
		os.Exit(2)
	}
	o := setup.Options{Custom: *custom, Uninstall: *uninstall, DryRun: *dry, Statusline: *sl, Name: *name,
		In: os.Stdin, Out: os.Stdout, Version: Version()}
	if *sl != "" && *sl != "wrap" && *sl != "replace" && *sl != "skip" {
		fail("--statusline is wrap, replace or skip")
	}
	if *noHooks {
		f := false
		o.Hooks = &f
	}
	if *hours != "" {
		h, err := setup.ParseHours(*hours)
		if err != nil {
			fail("--hours looks like 9-17")
		}
		o.Hours = h
	}
	o.Interactive = isTerminal(os.Stdin) // not just "a char device": /dev/null is one too
	if !o.Uninstall && !*express && !*custom {
		if o.Hours != nil || o.Statusline != "" || o.Hooks != nil || o.Name != "" {
			o.Custom = true
		} else if o.Interactive {
			fmt.Printf("\033[1mclaude-pet %s\033[0m\n\n", Version())
			fmt.Println("  \033[1mexpress\033[0m  defaults for everything, takes a second")
			fmt.Printf("  \033[1mcustom\033[0m   a few questions and a quick guide\n\n")
			fmt.Print("Express or custom? \033[2m[express]\033[0m ")
			br := bufio.NewReader(os.Stdin) // shared with setup's prompts, so no buffered input is lost
			line, _ := br.ReadString('\n')
			a := strings.ToLower(strings.TrimSpace(line))
			o.Custom = a == "custom" || a == "c"
			o.In = br
			fmt.Println()
		} else {
			fmt.Print(setupUsage)
			os.Exit(2)
		}
	}
	if err := setup.Run(o); err != nil {
		fail(err.Error())
	}
}

const setupUsage = `claude-pet setup: wire the pet into Claude Code

  claude-pet setup              ask: express or custom
  claude-pet setup --express    sensible defaults, no questions
  claude-pet setup --custom     a few questions and a quick guide
  claude-pet setup --uninstall  put your settings back the way they were

Custom options can also be passed as flags (handy when Claude runs it for you):
  --hours 9-17   --statusline wrap|replace|skip   --no-hooks   --name NAME
Add --dry-run to see what would change without writing anything.
`
