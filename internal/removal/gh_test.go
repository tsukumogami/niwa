package removal

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ghFixture is a workspace root with a bin directory inside it and one
// outside, each holding a gh. The gh inside writes the marker it is given
// when it runs; the one outside records how it was started (its path, PATH,
// the sentinel variable, its arguments and its directory) in record and
// prints a dummy token with blanks around it.
type ghFixture struct {
	home, root    string
	inside, bin   string
	record        string
	insideMarker  string
	outsideScript string
}

const ghSentinel = "NIWA_GH_TEST_SENTINEL"

func newGhFixture(t *testing.T) ghFixture {
	t.Helper()
	var f ghFixture
	f.home, f.root = newScratchFixture(t)
	base := filepath.Dir(f.home)
	f.inside = filepath.Join(f.root, "bin")
	f.bin = filepath.Join(base, "bin")
	for _, d := range []string{f.inside, f.bin} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.record = filepath.Join(base, "gh-record")
	f.insideMarker = filepath.Join(base, "inside-gh-ran")
	inside := "#!/bin/sh\n: > '" + f.insideMarker + "'\nprintf 'inside-token\\n'\n"
	if err := os.WriteFile(filepath.Join(f.inside, "gh"), []byte(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	f.outsideScript = filepath.Join(f.bin, "gh")
	outside := "#!/bin/sh\n" +
		"printf '%s\\n' \"$0\" \"$PATH\" \"$" + ghSentinel + "\" \"$*\" \"$(pwd -P)\" > '" + f.record + "'\n" +
		"printf '  gh-dummy-token \\n'\n"
	if err := os.WriteFile(f.outsideScript, []byte(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// unsetTokens clears both token variables, so GitHubToken has to ask gh.
func unsetTokens(t *testing.T) {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
}

func assertGhDidNotRun(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("a gh that isn't on the filtered PATH ran")
	}
}

func TestNewEnvResolvesGhFromTheFilteredPATH(t *testing.T) {
	f := newGhFixture(t)
	e := NewEnv(f.root, f.home, f.inside+string(os.PathListSeparator)+f.bin)
	defer e.Close()
	if e.Gh != f.outsideScript {
		t.Errorf("Gh = %q, want %q: the gh inside the workspace root comes first on PATH and must be skipped", e.Gh, f.outsideScript)
	}

	e = NewEnv(f.root, f.home, f.inside)
	defer e.Close()
	if e.Gh != "" {
		t.Errorf("Gh = %q from a PATH whose only gh is inside the workspace root", e.Gh)
	}

	// A link outside the root that resolves to the gh inside it is refused
	// too.
	links := filepath.Join(filepath.Dir(f.home), "links")
	if err := os.Mkdir(links, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.inside, "gh"), filepath.Join(links, "gh")); err != nil {
		t.Fatal(err)
	}
	e = NewEnv(f.root, f.home, links)
	defer e.Close()
	if e.Gh != "" {
		t.Errorf("Gh = %q, a link to the gh inside the workspace root", e.Gh)
	}
}

func TestGhCommandRunsByItsPathFromTheScratchDirectoryWithTheFilteredPATH(t *testing.T) {
	f := newGhFixture(t)
	unsetTokens(t)
	t.Setenv(ghSentinel, "kept")
	t.Setenv("PATH", f.inside+string(os.PathListSeparator)+f.bin)
	e := NewEnv(f.root, f.home, os.Getenv("PATH"))
	defer e.Close()
	cmd, err := e.ghCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != f.outsideScript {
		t.Errorf("gh started as %q, want %q", cmd.Path, f.outsideScript)
	}
	if !slices.Equal(cmd.Args[1:], []string{"auth", "token"}) {
		t.Errorf("gh's arguments are %q", cmd.Args[1:])
	}
	if cmd.Dir != filepath.Join(e.Scratch.Dir, scratchNet) {
		t.Errorf("gh runs from %q, want the scratch directory's empty net directory", cmd.Dir)
	}
	var paths []string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "PATH=") {
			paths = append(paths, kv)
		}
	}
	if !slices.Equal(paths, []string{"PATH=" + f.bin}) {
		t.Errorf("gh's PATH entries are %q, want only the filtered PATH %q", paths, f.bin)
	}
	if !slices.Contains(cmd.Env, ghSentinel+"=kept") {
		t.Error("gh's environment dropped a variable it inherits")
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("gh doesn't run in its own process group")
	}
}

func TestGitHubTokenRunsTheGhOnTheFilteredPATH(t *testing.T) {
	f := newGhFixture(t)
	unsetTokens(t)
	t.Setenv(ghSentinel, "kept")
	t.Setenv("PATH", f.inside+string(os.PathListSeparator)+f.bin)
	e := NewEnv(f.root, f.home, os.Getenv("PATH"))
	defer e.Close()

	if got := e.GitHubToken(context.Background()); got != "gh-dummy-token" {
		t.Errorf("GitHubToken = %q, want gh's output trimmed", got)
	}
	assertGhDidNotRun(t, f.insideMarker)
	data, err := os.ReadFile(f.record)
	if err != nil {
		t.Fatalf("the gh outside the workspace root didn't run: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := []string{f.outsideScript, f.bin, "kept", "auth token", filepath.Join(e.Scratch.Dir, scratchNet)}
	if !slices.Equal(got, want) {
		t.Errorf("gh recorded its path, PATH, sentinel, arguments and directory as\n%q\nwant\n%q", got, want)
	}
}

func TestGitHubTokenPrefersTheEnvironmentsTokens(t *testing.T) {
	f := newGhFixture(t)
	e := NewEnv(f.root, f.home, f.bin)
	defer e.Close()

	t.Setenv("GITHUB_TOKEN", "env-github-token")
	t.Setenv("GH_TOKEN", "env-gh-token")
	if got := e.GitHubToken(context.Background()); got != "env-github-token" {
		t.Errorf("GitHubToken = %q, want GITHUB_TOKEN's value", got)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if got := e.GitHubToken(context.Background()); got != "env-gh-token" {
		t.Errorf("GitHubToken = %q, want GH_TOKEN's value", got)
	}
	if _, err := os.Stat(f.record); err == nil {
		t.Error("gh ran although the environment carried a token")
	}
}

func TestGitHubTokenIsEmptyWithoutAUsableGh(t *testing.T) {
	f := newGhFixture(t)
	unsetTokens(t)

	var none *Env
	if got := none.GitHubToken(context.Background()); got != "" {
		t.Errorf("a nil env gave a token: %q", got)
	}

	// The only gh is inside the workspace root.
	t.Setenv("PATH", f.inside)
	e := NewEnv(f.root, f.home, os.Getenv("PATH"))
	defer e.Close()
	if got := e.GitHubToken(context.Background()); got != "" {
		t.Errorf("GitHubToken = %q with no gh on the filtered PATH", got)
	}
	assertGhDidNotRun(t, f.insideMarker)

	// A gh that fails gives no token, whatever it printed.
	failing := filepath.Join(filepath.Dir(f.home), "failbin")
	if err := os.Mkdir(failing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failing, "gh"), []byte("#!/bin/sh\nprintf 'partial\\n'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e = NewEnv(f.root, f.home, failing)
	defer e.Close()
	if got := e.GitHubToken(context.Background()); got != "" {
		t.Errorf("GitHubToken = %q from a gh that failed", got)
	}

	// With no scratch directory there's nowhere safe to run gh from.
	e = NewEnv(f.root, f.home, f.bin)
	e.Close()
	e.Scratch, e.ScratchErr = nil, os.ErrNotExist
	if got := e.GitHubToken(context.Background()); got != "" {
		t.Errorf("GitHubToken = %q with no scratch directory", got)
	}
	if _, err := os.Stat(f.record); err == nil {
		t.Error("gh ran with no scratch directory to run from")
	}
}
