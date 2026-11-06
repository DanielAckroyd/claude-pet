// Package update replaces the running claude-pet binary with the latest release, then updates the mod.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	defaultBase = "https://github.com/DanielAckroyd/claude-pet/releases"
	modID       = "claudepet@claude-pet"
	marketplace = "claude-pet"
	maxBinary   = 256 << 20
)

var cleanVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Options are the update choices. Nil/empty means "use the default".
type Options struct {
	Force   bool
	Version string
	Out     io.Writer
	Base    string       // releases URL; the GitHub one if empty
	Exe     string       // binary to replace; the running one if empty
	Client  *http.Client // 60s timeout if nil
	GOOS    string       // runtime.GOOS if empty
	GOARCH  string       // runtime.GOARCH if empty
	// Exec runs a command with the given output streams; os/exec if nil.
	Exec func(argv []string, stdout, stderr io.Writer) error
	// HasClaude reports whether the claude CLI is on PATH; exec.LookPath if nil.
	HasClaude func() bool
}

// Run updates the binary (when it can) and then the mod.
func Run(o Options) error {
	o.defaults()

	if o.Exe == "" {
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("can't find the running binary: %w", err)
		}

		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}

		o.Exe = self
	}

	_ = os.Remove(o.Exe + ".old") // Windows can't delete a running exe, so the last update left it behind

	tag, err := latestTag(o)
	if err != nil {
		return err
	}

	latest := strings.TrimPrefix(tag, "v")

	switch {
	case !cleanVersion.MatchString(o.Version) && !o.Force:
		fmt.Fprintf(o.Out, "claude-pet %s at %s is a dev build, so it's staying put.\n", o.Version, o.Exe)
		fmt.Fprintf(o.Out, "To switch to %s, run 'go install github.com/DanielAckroyd/claude-pet@latest' "+
			"or 'claude-pet update --force'.\n", latest)
	case o.Version == latest:
		fmt.Fprintf(o.Out, "claude-pet %s is up to date.\n", o.Version)
	case newer(o.Version, latest):
		fmt.Fprintf(o.Out, "claude-pet %s is newer than the latest release (%s), so it's staying put.\n",
			o.Version, latest)
	default:
		if err := replaceBinary(o, tag); err != nil {
			return err
		}

		fmt.Fprintf(o.Out, "updated %s → %s\n", o.Version, latest)
	}

	updateMod(o)
	fmt.Fprintln(o.Out, "Start a new Claude Code session to pick up the changes.")

	return nil
}

// newer compares clean X.Y.Z versions; between a merge and its tag, a build can be ahead of the latest release.
func newer(a, b string) bool {
	if !cleanVersion.MatchString(a) || !cleanVersion.MatchString(b) {
		return false
	}

	var x, y [3]int

	_, _ = fmt.Sscanf(a, "%d.%d.%d", &x[0], &x[1], &x[2])
	_, _ = fmt.Sscanf(b, "%d.%d.%d", &y[0], &y[1], &y[2])

	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}

	return false
}

func (o *Options) defaults() {
	if o.Base == "" {
		o.Base = defaultBase
	}

	o.Base = strings.TrimRight(o.Base, "/")

	if o.Client == nil {
		o.Client = &http.Client{Timeout: 60 * time.Second}
	}

	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}

	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}

	if o.Exec == nil {
		o.Exec = func(argv []string, stdout, stderr io.Writer) error {
			c := exec.Command(argv[0], argv[1:]...) //nolint:gosec // fixed argv: the claude CLI
			c.Stdout, c.Stderr = stdout, stderr

			return c.Run()
		}
	}

	if o.HasClaude == nil {
		o.HasClaude = func() bool { _, err := exec.LookPath("claude"); return err == nil }
	}
}

// latestTag reads the tag off the /latest redirect, which avoids the rate-limited API.
func latestTag(o Options) (string, error) {
	// Copy so we don't mutate the caller's client.
	c := *o.Client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := c.Get(o.Base + "/latest")
	if err != nil {
		return "", fmt.Errorf("looking up the latest release: %w", err)
	}

	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")

	if i < 0 || loc[i+len("/tag/"):] == "" {
		return "", fmt.Errorf("no release tag found (GET %s/latest gave HTTP %d)", o.Base, resp.StatusCode)
	}

	return loc[i+len("/tag/"):], nil
}

func fetch(c *http.Client, url string) ([]byte, error) {
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxBinary))
}

func replaceBinary(o Options, tag string) error {
	ext, binName := "tar.gz", "claude-pet"
	if o.GOOS == "windows" {
		ext, binName = "zip", "claude-pet.exe"
	}

	name := fmt.Sprintf("claude-pet_%s_%s.%s", o.GOOS, o.GOARCH, ext)
	dl := o.Base + "/download/" + tag + "/"

	fmt.Fprintf(o.Out, "Downloading %s %s\n", name, tag)

	archive, err := fetch(o.Client, dl+name)
	if err != nil {
		return err
	}

	sums, err := fetch(o.Client, dl+"checksums.txt")
	if err != nil {
		return err
	}

	if err := verify(archive, sums, name); err != nil {
		return err
	}

	bin, err := extract(archive, ext, binName)
	if err != nil {
		return err
	}

	return install(o, bin)
}

func verify(archive, sums []byte, name string) error {
	want := ""

	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}

	sum := sha256.Sum256(archive)
	if want == "" || want != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("checksum mismatch for %s, not updating", name)
	}

	return nil
}

func extract(archive []byte, ext, binName string) ([]byte, error) {
	if ext == "zip" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}

		for _, f := range zr.File {
			if f.Name != binName {
				continue
			}

			rc, err := f.Open()
			if err != nil {
				return nil, err
			}

			defer rc.Close()

			return io.ReadAll(io.LimitReader(rc, maxBinary))
		}

		return nil, fmt.Errorf("%s not found in the archive", binName)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("reading archive: %w", err)
	}

	tr := tar.NewReader(gz)

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s not found in the archive", binName)
		}

		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}

		if h.Name == binName && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxBinary))
		}
	}
}

// install swaps the binary in via a same-dir temp file so the rename is atomic on one filesystem.
func install(o Options, bin []byte) (err error) {
	defer func() {
		if errors.Is(err, fs.ErrPermission) {
			err = fmt.Errorf("%w\nNo permission to replace %s. Re-run the install script, "+
				"or run this with the right privileges", err, o.Exe)
		}
	}()

	tmp, err := os.CreateTemp(filepath.Dir(o.Exe), ".claude-pet-update-*")
	if err != nil {
		return err
	}

	defer os.Remove(tmp.Name())

	_, werr := tmp.Write(bin)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}

	if werr != nil {
		return werr
	}

	if err := os.Chmod(tmp.Name(), 0o755); err != nil { //nolint:gosec // it's an executable
		return err
	}

	if o.GOOS != "windows" {
		return os.Rename(tmp.Name(), o.Exe)
	}

	// A running exe can be renamed but not overwritten.
	old := o.Exe + ".old"
	_ = os.Remove(old)

	if err := os.Rename(o.Exe, old); err != nil {
		return err
	}

	if err := os.Rename(tmp.Name(), o.Exe); err != nil {
		_ = os.Rename(old, o.Exe)

		return err
	}

	return nil
}

func updateMod(o Options) {
	manual := "claude plugin update " + modID

	if !o.HasClaude() {
		fmt.Fprintf(o.Out, "Claude Code CLI not found on PATH. To update the mod, run: %s\n", manual)

		return
	}

	var list bytes.Buffer
	if err := o.Exec([]string{"claude", "plugin", "list", "--json"}, &list, io.Discard); err != nil {
		fmt.Fprintf(o.Out, "Couldn't list plugins (%v). To update the mod, run: %s\n", err, manual)

		return
	}

	var plugins []struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal(list.Bytes(), &plugins); err != nil {
		fmt.Fprintf(o.Out, "Couldn't read the plugin list (%v). To update the mod, run: %s\n", err, manual)

		return
	}

	installed := false

	for _, p := range plugins {
		installed = installed || p.ID == modID
	}

	if !installed {
		fmt.Fprintln(o.Out, "The mod isn't installed. 'claude-pet setup' installs it.")

		return
	}

	steps := [][]string{
		{"claude", "plugin", "marketplace", "update", marketplace},
		{"claude", "plugin", "update", modID},
	}

	for _, argv := range steps {
		if err := o.Exec(argv, o.Out, o.Out); err != nil {
			fmt.Fprintf(o.Out, "'%s' failed (%v). Run by hand:\n  claude plugin marketplace update %s\n  %s\n",
				strings.Join(argv, " "), err, marketplace, manual)

			return
		}
	}
}
