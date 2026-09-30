package pet

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func git(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(T.GitTimeout*float64(time.Second)))
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

func gitToplevel(cwd string) string { return git("-C", cwd, "rev-parse", "--show-toplevel") }

func gitEmail(top string) string { return git("-C", top, "config", "user.email") }

// gitCommitsToday is [sha, author-time+subject] authored by exactly email, committed since midnight.
func gitCommitsToday(top, email string) [][2]string {
	out := git("-C", top, "log", "--branches", "--fixed-strings", "--author=<"+email+">",
		"--since=midnight", "--format=%H %at %s")

	var rows [][2]string

	for _, ln := range strings.Split(out, "\n") {
		sha, rest, _ := strings.Cut(ln, " ")
		if sha != "" {
			rows = append(rows, [2]string{sha, rest})
		}
	}

	return rows
}
