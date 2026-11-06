package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	oldBin = "old-binary"
	newBin = "new-binary"
)

func binName() string {
	if runtime.GOOS == "windows" {
		return "claude-pet.exe"
	}

	return "claude-pet"
}

func archiveName() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("claude-pet_%s_%s.zip", runtime.GOOS, runtime.GOARCH)
	}

	return fmt.Sprintf("claude-pet_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

func buildArchive(t *testing.T, content string) []byte {
	t.Helper()

	var buf bytes.Buffer

	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)

		f, err := zw.Create(binName())
		if err != nil {
			t.Fatal(err)
		}

		_, _ = f.Write([]byte(content))

		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}

		return buf.Bytes()
	}

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{Name: binName(), Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}

	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	_ = gz.Close()

	return buf.Bytes()
}

type rig struct {
	t      *testing.T
	exe    string
	out    bytes.Buffer
	calls  [][]string
	listed string
	failOn string
	claude bool
	hits   map[string]int
}

// server serves a release; sumOverride replaces the real checksum line when non-empty.
func (r *rig) server(tag, sumOverride string) string {
	arc := buildArchive(r.t, newBin)
	sum := sha256.Sum256(arc)
	line := hex.EncodeToString(sum[:]) + "  " + archiveName() + "\n"

	if sumOverride != "" {
		line = sumOverride
	}

	r.hits = map[string]int{}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	r.t.Cleanup(srv.Close)

	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		r.hits["latest"]++

		w.Header().Set("Location", srv.URL+"/releases/tag/"+tag)
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+archiveName(), func(w http.ResponseWriter, _ *http.Request) {
		r.hits["archive"]++

		_, _ = w.Write(arc)
	})
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, line)
	})

	return srv.URL + "/releases"
}

func newRig(t *testing.T) *rig {
	t.Helper()

	exe := filepath.Join(t.TempDir(), binName())
	if err := os.WriteFile(exe, []byte(oldBin), 0o755); err != nil {
		t.Fatal(err)
	}

	return &rig{t: t, exe: exe, claude: true, listed: `[{"id":"claudepet@claude-pet","version":"0.5.0"}]`}
}

func (r *rig) run(version, base string, force bool) error {
	return Run(Options{
		Force: force, Version: version, Out: &r.out, Base: base, Exe: r.exe,
		HasClaude: func() bool { return r.claude },
		Exec: func(argv []string, stdout, _ io.Writer) error {
			r.calls = append(r.calls, argv)

			if strings.Join(argv, " ") == r.failOn {
				return errors.New("boom")
			}

			if argv[2] == "list" {
				_, _ = io.WriteString(stdout, r.listed)
			}

			return nil
		},
	})
}

func (r *rig) exeContent() string {
	b, err := os.ReadFile(r.exe)
	if err != nil {
		r.t.Fatal(err)
	}

	return string(b)
}

func wantCalls(t *testing.T, got [][]string, want ...string) {
	t.Helper()

	lines := make([]string, 0, len(got))

	for _, c := range got {
		lines = append(lines, strings.Join(c, " "))
	}

	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", lines, want)
	}
}

const (
	list   = "claude plugin list --json"
	market = "claude plugin marketplace update claude-pet"
	update = "claude plugin update claudepet@claude-pet"
)

func TestReplacesBinaryAndUpdatesMod(t *testing.T) {
	r := newRig(t)
	if err := r.run("0.5.0", r.server("v0.6.0", ""), false); err != nil {
		t.Fatal(err)
	}

	if got := r.exeContent(); got != newBin {
		t.Fatalf("exe = %q, want %q", got, newBin)
	}

	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(r.exe); fi.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v", fi.Mode().Perm())
		}
	}

	wantCalls(t, r.calls, list, market, update)

	for _, s := range []string{"updated 0.5.0 → 0.6.0", "new Claude Code session"} {
		if !strings.Contains(r.out.String(), s) {
			t.Fatalf("output missing %q:\n%s", s, r.out.String())
		}
	}

	entries, _ := os.ReadDir(filepath.Dir(r.exe))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".claude-pet-update-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestLeftoverOldRemoved(t *testing.T) {
	r := newRig(t)
	old := r.exe + ".old"
	_ = os.WriteFile(old, []byte("x"), 0o644)

	if err := r.run("0.6.0", r.server("v0.6.0", ""), false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf(".old still there: %v", err)
	}
}

func TestChecksumMismatchLeavesBinary(t *testing.T) {
	for name, sums := range map[string]string{
		"wrong":   strings.Repeat("0", 64) + "  " + archiveName() + "\n",
		"missing": strings.Repeat("0", 64) + "  other.tar.gz\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)

			err := r.run("0.5.0", r.server("v0.6.0", sums), false)
			if err == nil || !strings.Contains(err.Error(), "checksum") {
				t.Fatalf("err = %v, want checksum error", err)
			}

			if got := r.exeContent(); got != oldBin {
				t.Fatalf("exe changed to %q", got)
			}

			if len(r.calls) != 0 {
				t.Fatalf("mod step ran after a failed update: %v", r.calls)
			}
		})
	}
}

func TestUpToDate(t *testing.T) {
	r := newRig(t)
	if err := r.run("0.6.0", r.server("v0.6.0", ""), false); err != nil {
		t.Fatal(err)
	}

	if r.hits["archive"] != 0 || r.exeContent() != oldBin {
		t.Fatal("downloaded or replaced when already up to date")
	}

	if !strings.Contains(r.out.String(), "up to date") {
		t.Fatalf("output:\n%s", r.out.String())
	}

	wantCalls(t, r.calls, list, market, update)
}

func TestNewerThanLatestIsNotDowngraded(t *testing.T) {
	r := newRig(t)
	if err := r.run("0.10.0", r.server("v0.9.1", ""), false); err != nil {
		t.Fatal(err)
	}

	if r.hits["archive"] != 0 || r.exeContent() != oldBin || !strings.Contains(r.out.String(), "newer") {
		t.Fatalf("downgraded, or didn't say why:\n%s", r.out.String())
	}

	wantCalls(t, r.calls, list, market, update)
}

func TestDevBuildSkipsBinaryButUpdatesMod(t *testing.T) {
	r := newRig(t)
	if err := r.run("0.3.1-0.20261105061244-285445c0f0df+dirty", r.server("v0.6.0", ""), false); err != nil {
		t.Fatal(err)
	}

	if r.hits["archive"] != 0 || r.exeContent() != oldBin {
		t.Fatal("replaced a dev build without --force")
	}

	for _, s := range []string{"dev build", r.exe, "go install", "--force"} {
		if !strings.Contains(r.out.String(), s) {
			t.Fatalf("output missing %q:\n%s", s, r.out.String())
		}
	}

	wantCalls(t, r.calls, list, market, update)
}

func TestForceReplacesDevBuild(t *testing.T) {
	r := newRig(t)
	if err := r.run("dev", r.server("v0.6.0", ""), true); err != nil {
		t.Fatal(err)
	}

	if r.exeContent() != newBin {
		t.Fatal("--force didn't replace the binary")
	}
}

func TestModNotInstalled(t *testing.T) {
	r := newRig(t)
	r.listed = `[{"id":"other@somewhere","version":"1.0.0"}]`

	if err := r.run("0.6.0", r.server("v0.6.0", ""), false); err != nil {
		t.Fatal(err)
	}

	wantCalls(t, r.calls, list)

	if !strings.Contains(r.out.String(), "claude-pet setup") {
		t.Fatalf("output:\n%s", r.out.String())
	}
}

func TestNoClaudeOnPath(t *testing.T) {
	r := newRig(t)
	r.claude = false

	if err := r.run("0.6.0", r.server("v0.6.0", ""), false); err != nil {
		t.Fatal(err)
	}

	wantCalls(t, r.calls)

	if !strings.Contains(r.out.String(), update) {
		t.Fatalf("output missing the command:\n%s", r.out.String())
	}
}

func TestModStepFailurePrintsCommand(t *testing.T) {
	r := newRig(t)
	r.failOn = market

	if err := r.run("0.6.0", r.server("v0.6.0", ""), false); err != nil {
		t.Fatal(err)
	}

	wantCalls(t, r.calls, list, market)

	if !strings.Contains(r.out.String(), "by hand") || !strings.Contains(r.out.String(), update) {
		t.Fatalf("output:\n%s", r.out.String())
	}
}

func TestNoTag(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	r := newRig(t)

	err := r.run("0.5.0", srv.URL, false)
	if err == nil || !strings.Contains(err.Error(), "no release tag") {
		t.Fatalf("err = %v", err)
	}
}
