package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func spillDirOf(instanceDir string) string {
	return filepath.Join(instanceDir, workspace.StateDir, spillDirName)
}

// newSpillInstance makes a directory shaped like an instance: a state
// directory at the mode SaveState really uses, which is world-readable. That
// mode is the whole reason the spill gets a subdirectory of its own.
func newSpillInstance(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, workspace.StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSpillFileHoldsTheBodyByteForByte(t *testing.T) {
	inst := newSpillInstance(t)
	// Quotes, backslashes, dollar signs, a control byte, invalid UTF-8, and a
	// NUL: everything the capture deliberately preserves.
	body := "line one\n\"quoted\" $HOME \\ \x1b[31mred\x1b[0m \xff\x00 end\n"

	path, err := writeSpillFile(inst, "0123456789abcdef", body)
	if err != nil {
		t.Fatalf("writeSpillFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("spill file does not match the body byte for byte\n got %q\nwant %q", got, body)
	}
	if strings.HasPrefix(string(got), "#") || strings.HasSuffix(string(got), "\n\n") {
		t.Error("spill file appears to carry a header or footer; it must carry the bytes and nothing else")
	}
}

func TestSpillFileAndDirectoryAreOwnerOnly(t *testing.T) {
	inst := newSpillInstance(t)
	path, err := writeSpillFile(inst, "0123456789abcdef", "x")
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("spill file is mode %#o, want 0600", perm)
	}

	di, err := os.Stat(spillDirOf(inst))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("spill directory is mode %#o; a prompt needs an owner-only directory", perm)
	}
}

// The instance's own .gitignore carries exactly one pattern, "*.local*". A
// workspace nested inside a larger tracked working tree would otherwise let a
// `git add -A` from the outer repo stage a pasted prompt.
func TestSpillFileNameMatchesTheInstanceIgnorePattern(t *testing.T) {
	inst := newSpillInstance(t)
	path, err := writeSpillFile(inst, "0123456789abcdef", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(path), ".local") {
		t.Errorf("spill file %q does not match the instance ignore pattern \"*.local*\"", filepath.Base(path))
	}
}

// A second launch into the same instance is a guaranteed path, not an edge:
// the review-continuation path launches repeatedly into an instance it did not
// create.
func TestTwoSpillsIntoOneInstanceDoNotCollide(t *testing.T) {
	inst := newSpillInstance(t)

	first, err := writeSpillFile(inst, "1111111111111111", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeSpillFile(inst, "2222222222222222", "second")
	if err != nil {
		t.Fatalf("second spill into the same instance failed: %v", err)
	}
	if first == second {
		t.Fatal("two spills produced the same path")
	}

	for path, want := range map[string]string{first: "first", second: "second"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, want %q -- one spill overwrote the other", path, got, want)
		}
	}
}

// O_EXCL defends against a planted symlink at the FILE. The parent needs its
// own check, or a spill directory symlinked at somewhere like ~/.ssh would take
// the prompt with it.
func TestSpillRefusesASymlinkedDirectory(t *testing.T) {
	inst := newSpillInstance(t)
	target := t.TempDir()
	if err := os.Symlink(target, spillDirOf(inst)); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}

	_, err := writeSpillFile(inst, "0123456789abcdef", "secret")
	if err == nil {
		t.Fatal("wrote a prompt through a symlinked spill directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should name the symlink, got: %v", err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Error("the symlink target received a file")
	}
}

func TestSpillRefusesAnOverPermissiveDirectory(t *testing.T) {
	inst := newSpillInstance(t)
	if err := os.MkdirAll(spillDirOf(inst), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSpillFile(inst, "0123456789abcdef", "x"); err == nil {
		t.Fatal("wrote a prompt into a world-readable spill directory")
	}
}

func TestSpillRefusesANonDirectory(t *testing.T) {
	inst := newSpillInstance(t)
	if err := os.WriteFile(spillDirOf(inst), []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSpillFile(inst, "0123456789abcdef", "x"); err == nil {
		t.Fatal("wrote a prompt where the spill directory is a regular file")
	}
}

// The spill must not invent instance structure. A recursive create under a
// missing state directory would leave a directory matching the dispatch-name
// signature but carrying no instance metadata, which the destroy validator then
// refuses to act on -- unreclaimable by both the rollback and the reaper.
func TestSpillDoesNotCreateAMissingStateDirectory(t *testing.T) {
	inst := t.TempDir() // no .niwa inside
	if _, err := writeSpillFile(inst, "0123456789abcdef", "x"); err == nil {
		t.Fatal("spill created a state directory that provisioning had not")
	}
	if _, err := os.Stat(filepath.Join(inst, workspace.StateDir)); !os.IsNotExist(err) {
		t.Error("spill left a state directory behind under an instance root that had none")
	}
}

func TestSpillTokenIsUnpredictableAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		tok, err := spillToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 16 {
			t.Fatalf("token %q is %d characters, want 16 hex", tok, len(tok))
		}
		if seen[tok] {
			t.Fatalf("token %q repeated within 64 draws", tok)
		}
		seen[tok] = true
	}
}

func TestPointerCarriesPathInstructionFenceAndExcerpt(t *testing.T) {
	body := strings.Repeat("stack frame\n", 2000)
	const token = "0123456789abcdef"
	path := "/abs/instance/.niwa/dispatch-prompts/prompt-" + token + ".local.txt"

	got := composeSpillPointer("", path, body, token)

	if !strings.Contains(got, path) {
		t.Error("pointer does not name the spill file")
	}
	if !filepath.IsAbs(path) {
		t.Error("pointer path is not absolute")
	}
	if !strings.Contains(got, "Read the file") {
		t.Error("pointer does not instruct the worker to read the file")
	}
	if !strings.Contains(got, spillFenceLabel+token) {
		t.Error("pointer does not fence the excerpt with the launch token")
	}
	if !strings.Contains(got, "PREFIX") {
		t.Error("pointer does not label the excerpt as a prefix")
	}
	if !strings.Contains(got, "of "+itoa(len(body))+" bytes") {
		t.Errorf("pointer does not say how much of how much was quoted: %q", tail(got, 200))
	}
	if len(got) > maxArgStringBytes {
		t.Errorf("pointer is %d bytes, over the exec cap", len(got))
	}
}

// The excerpt has a floor, and the floor is absolute rather than expressed in
// terms of the excerpt's own length -- a self-referential bound is satisfied by
// a one-byte excerpt.
func TestExcerptClearsItsFloorAndDistinguishesPrompts(t *testing.T) {
	const token = "0123456789abcdef"
	head := strings.Repeat("a", 512)

	one := composeSpillPointer("", "/p", head+"X"+strings.Repeat("z", 10000), token)
	two := composeSpillPointer("", "/p", head+"Y"+strings.Repeat("z", 10000), token)
	if one == two {
		t.Error("two prompts differing at byte 513 produced identical pointers; " +
			"the excerpt is below its 512-byte floor")
	}
}

// The file gets raw bytes; the excerpt does not. An argv element cannot carry a
// NUL, so an unsanitized excerpt would carry a launch failure into the one path
// whose purpose is that a large prompt always dispatches.
func TestExcerptIsNeutralizedWhileTheFileIsNot(t *testing.T) {
	body := "before\x00after\x1b[31m\n"
	got := composeSpillPointer("", "/p", body, "0123456789abcdef")

	if strings.ContainsRune(got, 0) {
		t.Error("pointer carries a NUL; exec would reject the argv element")
	}
	if strings.Contains(got, "\x1b") {
		t.Error("pointer carries a raw escape byte")
	}
	if !strings.Contains(got, "\n") {
		t.Error("excerpt lost its line breaks; a stack trace would render as one line")
	}
}

// The token is minted after the body is fixed, so no submitted text can contain
// it. That is asserted rather than scrubbed: a redaction cannot fire under the
// minting order, and if it ever did it would silently mutate the excerpt.
func TestExcerptCannotContainTheLaunchToken(t *testing.T) {
	const token = "0123456789abcdef"
	body := "harmless text"
	got := composeSpillPointer("", "/p", body, token)

	between := got[strings.Index(got, "<<<"+spillFenceLabel+token)+len("<<<"+spillFenceLabel+token):]
	between = between[:strings.Index(between, spillFenceLabel+token)]
	if strings.Contains(between, token) {
		t.Error("the excerpt contains the fence token; the fence is forgeable")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// niwa watch builds its prompts from fixed templates, so the spill never fires
// on that path. The pin is load-bearing rather than tidy: the instance a watch
// spill would land in is running a review of an untrusted pull-request diff
// under the sandbox and guard triad, and nothing sweeps spilled files there
// before the instance is reclaimed. A template grown past the cap should be
// caught here as a change, not discovered as a file in a review instance.
func TestWatchTemplatesStayBelowTheSpillThreshold(t *testing.T) {
	for name, prompt := range map[string]string{
		"review": watch.BuildReviewPrompt(
			github.PRRef{Owner: "tsukumogami", Repo: "niwa", Number: 999, URL: "https://github.com/tsukumogami/niwa/pull/999"},
			watch.DefaultCloneRelDir, watch.DefaultDraftRelPath),
		"resume": watch.BuildResumePrompt(watch.DefaultCloneRelDir, watch.DefaultDraftRelPath),
	} {
		if len(prompt) > maxArgStringBytes {
			t.Errorf("the %s template is %d bytes, over the %d-byte spill threshold; "+
				"it would now spill into a live review instance", name, len(prompt), maxArgStringBytes)
		}
		// Far below, not merely under: the margin is what makes the property
		// robust to a PR with a long title or body.
		if len(prompt) > maxArgStringBytes/4 {
			t.Errorf("the %s template is %d bytes, within 4x of the spill threshold; "+
				"the margin is the whole reason this path never spills", name, len(prompt))
		}
	}
}
