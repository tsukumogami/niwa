package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// These tests cover the session name a named dispatch forwards, records and
// prints. Expected names are literals built from a stubbed random source
// (0xab for every byte gives the token "abababab"), never values computed
// through the functions under test.

// constByteReader fills every read with one byte, so the token is predictable.
type constByteReader byte

func (c constByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(c)
	}
	return len(p), nil
}

// stubDispatchRand points the dispatch token at r for the rest of the test.
func stubDispatchRand(t *testing.T, r io.Reader) {
	t.Helper()
	prev := dispatchRandReader
	dispatchRandReader = r
	t.Cleanup(func() { dispatchRandReader = prev })
}

// forwardedDisplayName returns the element after flag in pass, and whether the
// flag appears at all.
func forwardedDisplayName(pass []string, flag string) (string, bool) {
	for i, a := range pass {
		if a == flag {
			if i+1 < len(pass) {
				return pass[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

// recordPassthrough replaces the launch seam with one that succeeds and keeps
// the pass-through argv it was handed. installDispatchFakes restores the seam.
func recordPassthrough(f *dispatchFakes) *[]string {
	var got []string
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		got = req.Passthrough
		return nil
	}
	return &got
}

// readDispatchMapping returns the mapping the dispatch wrote, decoded and raw.
func readDispatchMapping(t *testing.T, root string) (workspace.SessionMapping, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".niwa", "sessions", dispatchTestSessionID+".json"))
	if err != nil {
		t.Fatalf("reading the mapping file: %v", err)
	}
	m, err := workspace.ReadSessionMapping(root, dispatchTestSessionID)
	if err != nil {
		t.Fatalf("decoding the mapping: %v", err)
	}
	return m, string(raw)
}

// assertSessionNameLineAfterInstance checks that stdout carries exactly one
// session name line, directly after the instance line, reading want.
func assertSessionNameLineAfterInstance(t *testing.T, stdout, want string) {
	t.Helper()
	if n := strings.Count(stdout, "session name:"); n != 1 {
		t.Fatalf("stdout carries %d session name lines, want 1:\n%s", n, stdout)
	}
	lines := strings.Split(stdout, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "  instance: ") {
			continue
		}
		if i+1 >= len(lines) || lines[i+1] != "  session name: "+want {
			t.Fatalf("the line after the instance line is not %q:\n%s", "  session name: "+want, stdout)
		}
		return
	}
	t.Fatalf("stdout has no instance line:\n%s", stdout)
}

// namedDispatchEnv sets up a fresh workspace with every seam faked, a stubbed
// token source and --name review. The caller picks the launch mode.
func namedDispatchEnv(t *testing.T, r io.Reader) (string, *dispatchFakes) {
	t.Helper()
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	stubDispatchRand(t, r)
	dispatchName = "review"
	return root, f
}

func TestNewDispatchToken_ReadsFourBytes(t *testing.T) {
	r := bytes.NewReader([]byte{0xde, 0xad, 0xbe, 0xef, 0x01})
	stubDispatchRand(t, r)
	tok, err := newDispatchToken()
	if err != nil {
		t.Fatalf("newDispatchToken: %v", err)
	}
	if tok != "deadbeef" {
		t.Errorf("token = %q, want %q", tok, "deadbeef")
	}
	if r.Len() != 1 {
		t.Errorf("%d bytes left unread, want 1: the token must read exactly 4", r.Len())
	}

	// A source that runs dry before 4 bytes is an error, not a short token.
	stubDispatchRand(t, bytes.NewReader([]byte{0x01, 0x02, 0x03}))
	if tok, err := newDispatchToken(); err == nil {
		t.Errorf("a 3-byte source gave token %q and no error", tok)
	}
}

func TestDispatchInstancePrefix(t *testing.T) {
	if got := dispatchInstancePrefix("review", "abababab"); got != "review-abababab" {
		t.Errorf(`dispatchInstancePrefix("review", ...) = %q, want "review-abababab"`, got)
	}
	if got := dispatchInstancePrefix("", "abababab"); got != "-abababab" {
		t.Errorf(`dispatchInstancePrefix("", ...) = %q, want "-abababab"`, got)
	}

	// The wrapper watch still calls reads the same source and gives the same
	// strings.
	stubDispatchRand(t, constByteReader(0xab))
	for slug, want := range map[string]string{"review": "review-abababab", "": "-abababab"} {
		got, err := dispatchNameSuffix(slug)
		if err != nil {
			t.Fatalf("dispatchNameSuffix(%q): %v", slug, err)
		}
		if got != want {
			t.Errorf("dispatchNameSuffix(%q) = %q, want %q", slug, got, want)
		}
	}
}

func TestDispatchSessionName_EmptySlug(t *testing.T) {
	if got := dispatchSessionName("", "abababab"); got != "" {
		t.Errorf(`dispatchSessionName("", ...) = %q, want ""`, got)
	}
	if got := dispatchSessionName("review", "abababab"); got != "review-abababab" {
		t.Errorf(`dispatchSessionName("review", ...) = %q, want "review-abababab"`, got)
	}
}

// TestDispatchSessionName_StubbedBytes pins the whole chain for one token: the
// forwarded name, the instance name and the recorded name all carry it.
func TestDispatchSessionName_StubbedBytes(t *testing.T) {
	root, f := namedDispatchEnv(t, constByteReader(0xab))
	pass := recordPassthrough(f)
	dispatchDetach = true

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	got, found := forwardedDisplayName(*pass, "--name")
	if !found || got != "review-abababab" {
		t.Errorf("forwarded display name = %q (flag present: %v), want %q; passthrough %q", got, found, "review-abababab", *pass)
	}
	if name := filepath.Base(f.instancePath); name != "test-ws+review-abababab" {
		t.Errorf("instance name = %q, want %q", name, "test-ws+review-abababab")
	}
	if !isDispatchInstanceName(filepath.Base(f.instancePath)) {
		t.Errorf("instance name %q no longer matches the dispatch signature", filepath.Base(f.instancePath))
	}
	m, raw := readDispatchMapping(t, root)
	if m.SessionName != "review-abababab" {
		t.Errorf("mapping SessionName = %q, want %q", m.SessionName, "review-abababab")
	}
	if !strings.Contains(raw, `"session_name": "review-abababab"`) {
		t.Errorf("mapping file does not carry the session name:\n%s", raw)
	}
}

func TestDispatchSessionName_TwoDispatchesDiffer(t *testing.T) {
	run := func(r io.Reader) string {
		_, f := namedDispatchEnv(t, r)
		pass := recordPassthrough(f)
		dispatchDetach = true
		if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		got, _ := forwardedDisplayName(*pass, "--name")
		return got
	}
	first := run(constByteReader(0xab))
	second := run(constByteReader(0xcd))

	if first != "review-abababab" || second != "review-cdcdcdcd" {
		t.Errorf("forwarded %q and %q, want %q and %q", first, second, "review-abababab", "review-cdcdcdcd")
	}
	if first == second {
		t.Errorf("two dispatches with --name review forwarded the same name %q", first)
	}
	for _, name := range []string{first, second} {
		if !dispatchSessionNameRe.MatchString(name) {
			t.Errorf("forwarded name %q does not match %s", name, dispatchSessionNamePattern)
		}
	}
}

// TestDispatchSessionName_ConcurrentUnique mints names from many goroutines at
// once with the real random source. Run it under -race.
func TestDispatchSessionName_ConcurrentUnique(t *testing.T) {
	const n = 64
	var (
		mu    sync.Mutex
		names = make(map[string]bool, n)
		wg    sync.WaitGroup
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := newDispatchToken()
			if err != nil {
				t.Errorf("newDispatchToken: %v", err)
				return
			}
			name := dispatchSessionName("review", tok)
			mu.Lock()
			names[name] = true
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(names) != n {
		t.Errorf("%d goroutines minted %d distinct names", n, len(names))
	}
	for name := range names {
		if !dispatchSessionNameRe.MatchString(name) {
			t.Errorf("minted name %q does not match %s", name, dispatchSessionNamePattern)
		}
	}
}

func TestDispatchSessionName_RandErrorProvisionsNothing(t *testing.T) {
	root, f := namedDispatchEnv(t, iotest.ErrReader(errors.New("entropy exhausted")))
	dispatchDetach = true

	stdout, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("dispatch succeeded with a failing random source")
	}
	if !strings.Contains(err.Error(), "generating instance name") {
		t.Errorf("error = %q, want the generating instance name error", err)
	}
	if f.provisionCalled != 0 {
		t.Errorf("provision called %d times after the token read failed", f.provisionCalled)
	}
	if f.launchCalled != 0 {
		t.Errorf("launch called %d times after the token read failed", f.launchCalled)
	}
	if dirs, _ := filepath.Glob(filepath.Join(root, "test-ws+*")); len(dirs) != 0 {
		t.Errorf("instance directories exist after the token read failed: %q", dirs)
	}
	if strings.Contains(stdout, "session name:") {
		t.Errorf("stdout carries a session name line after a failed dispatch:\n%s", stdout)
	}
}

func TestDispatchSessionName_SlugShapes(t *testing.T) {
	forty := strings.Repeat("abcdefghij", 4)
	for _, tc := range []struct {
		name, in, wantSlug string
	}{
		{"capitalized", "Review", "review"},
		{"space", "auth layer", "auth_layer"},
		{"non-ascii and punctuation", "café!!", "caf"},
		{"capped at 40 runes", strings.Repeat("abcdefghij", 5), forty},
		{"one character", "x", "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := sanitizeInstanceSlug(tc.in)
			if slug != tc.wantSlug {
				t.Fatalf("sanitizeInstanceSlug(%q) = %q, want %q", tc.in, slug, tc.wantSlug)
			}
			if tc.wantSlug == forty && len([]rune(slug)) != maxDispatchSlugRunes {
				t.Fatalf("the long input sanitizes to %d runes, want %d", len([]rune(slug)), maxDispatchSlugRunes)
			}

			_, f := namedDispatchEnv(t, constByteReader(0xab))
			dispatchName = tc.in
			pass := recordPassthrough(f)
			dispatchDetach = true
			if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			got, _ := forwardedDisplayName(*pass, "--name")
			if want := tc.wantSlug + "-abababab"; got != want {
				t.Errorf("forwarded %q, want %q", got, want)
			}
			if got != slug+"-abababab" {
				t.Errorf("forwarded %q, want sanitizeInstanceSlug(%q) plus the token", got, tc.in)
			}
			if i := strings.LastIndex(got, "-"); i < 0 || got[:i] != slug {
				t.Errorf("cutting %q at its last dash does not give the slug %q", got, slug)
			}
			if !dispatchSessionNameRe.MatchString(got) {
				t.Errorf("forwarded %q does not match %s", got, dispatchSessionNamePattern)
			}
		})
	}
}

// TestDispatchSessionName_ReportLineOnSuccess covers the three success exits of
// a self-backgrounding agent. The foreground and ResumeDuringTurn=false exits
// are in dispatch_launchmode_test.go.
func TestDispatchSessionName_ReportLineOnSuccess(t *testing.T) {
	for _, tc := range []struct {
		name       string
		detach     bool
		attachFail bool
	}{
		{"attach succeeds", false, false},
		{"detached", true, false},
		{"attach fails", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, f := namedDispatchEnv(t, constByteReader(0xab))
			pass := recordPassthrough(f)
			dispatchDetach = tc.detach
			if tc.attachFail {
				dispatchAttach = func(agentplan.LaunchSpec, string, string) error {
					return errors.New("the terminal could not be handed over")
				}
			}

			stdout, _, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			forwarded, _ := forwardedDisplayName(*pass, "--name")
			if forwarded != "review-abababab" {
				t.Fatalf("forwarded %q, want %q", forwarded, "review-abababab")
			}
			assertSessionNameLineAfterInstance(t, stdout, forwarded)

			lines := strings.Split(stdout, "\n")
			if lines[0] != "Dispatched session "+dispatchTestSessionID {
				t.Errorf("headline = %q", lines[0])
			}
			if !strings.HasPrefix(lines[1], "  instance: ") || lines[2] != "  session name: review-abababab" {
				t.Errorf("the report does not open headline, instance, session name:\n%s", stdout)
			}
			if lines[3] != "  claude attach "+dispatchTestShortID {
				t.Errorf("the first hint does not follow the session name line:\n%s", stdout)
			}
		})
	}
}

// TestDispatchSessionName_AbsentOnFailure holds the report line back on every
// failure exit, detached and foreground. The line is printed only once the
// mapping is durable.
func TestDispatchSessionName_AbsentOnFailure(t *testing.T) {
	base, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	failCapture := func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		return "", "", errors.New("capture timeout")
	}
	// WriteSessionMapping rejects an id that is not a UUID without writing.
	failMapping := func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		return "not-a-uuid", dispatchTestShortID, nil
	}

	for _, mode := range []struct {
		name       string
		foreground bool
	}{
		{"detached", false},
		{"foreground", true},
	} {
		for _, fail := range []string{"launch", "capture", "mapping"} {
			t.Run(mode.name+"/"+fail, func(t *testing.T) {
				_, _ = namedDispatchEnv(t, constByteReader(0xab))
				if mode.foreground {
					spec := base
					spec.Runner = agentplan.RunnerForeground
					substituteLaunchSpec(t, spec)
					dispatchDetach = false
				} else {
					dispatchDetach = true
				}
				switch fail {
				case "launch":
					dispatchLaunch = func(context.Context, launchRequest) error { return errors.New("launch boom") }
				case "capture":
					dispatchCapture = failCapture
				case "mapping":
					dispatchCapture = failMapping
				}

				stdout, _, _ := runDispatchCmd(t, "do a thing")
				if strings.Contains(stdout, "session name:") {
					t.Errorf("a failed dispatch printed a session name line:\n%s", stdout)
				}
			})
		}
	}
}

func TestDispatchSessionName_UnnamedAndEmptySlug(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"no name", ""},
		{"name sanitizes to empty", "!!!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, f := namedDispatchEnv(t, constByteReader(0xab))
			dispatchName = tc.in
			pass := recordPassthrough(f)
			dispatchDetach = true

			stdout, _, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if _, found := forwardedDisplayName(*pass, "--name"); found {
				t.Errorf("a display name was forwarded: %q", *pass)
			}
			if strings.Contains(stdout, "session name:") {
				t.Errorf("stdout carries a session name line:\n%s", stdout)
			}
			m, raw := readDispatchMapping(t, root)
			if m.SessionName != "" {
				t.Errorf("mapping SessionName = %q, want empty", m.SessionName)
			}
			if strings.Contains(raw, "session_name") {
				t.Errorf("mapping file carries a session_name key:\n%s", raw)
			}
		})
	}
}

// TestDispatchSessionName_CodexSpecForwardsNothing runs a named dispatch
// against Codex's declared launch spec, which has no display-name flag. No
// Codex binary is needed: the launch is faked and detached.
func TestDispatchSessionName_CodexSpecForwardsNothing(t *testing.T) {
	spec, ok := agentplan.For(agent.AgentCodex).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for Codex")
	}
	if spec.Flags.DisplayName != "" {
		t.Fatalf("Codex now declares display-name flag %q; this test's premise is gone", spec.Flags.DisplayName)
	}

	root, f := namedDispatchEnv(t, constByteReader(0xab))
	substituteLaunchSpec(t, spec)
	lookAgentBinary = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	pass := recordPassthrough(f)
	dispatchDetach = true

	stdout, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	for _, a := range *pass {
		if a == "--name" || strings.Contains(a, "review-") {
			t.Errorf("a display name reached an agent with no flag for it: %q", *pass)
		}
	}
	if strings.Contains(stdout, "session name:") {
		t.Errorf("stdout carries a session name line:\n%s", stdout)
	}
	m, raw := readDispatchMapping(t, root)
	if m.SessionName != "" {
		t.Errorf("mapping SessionName = %q, want empty", m.SessionName)
	}
	if strings.Contains(raw, "session_name") {
		t.Errorf("mapping file carries a session_name key:\n%s", raw)
	}
}

func TestDispatchSessionName_HelpAndDocText(t *testing.T) {
	usage := dispatchCmd.Flags().Lookup("name").Usage
	for _, want := range []string{"suffix", "session name"} {
		if !strings.Contains(usage, want) {
			t.Errorf("--name help does not mention %q: %q", want, usage)
		}
	}
	src, err := os.ReadFile("dispatch.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "carries the same display name embedded in the instance directory") {
		t.Error("dispatch.go still says the display name is the one embedded in the instance directory")
	}
}

// TestDispatchSessionName_CreateUnchanged pins that `niwa create --name` keeps
// its token-free "<config>+<slug>" shape.
func TestDispatchSessionName_CreateUnchanged(t *testing.T) {
	got, err := computeInstanceName("tsuku", sanitizeInstanceSlug("Auth Layer"), "+", t.TempDir())
	if err != nil {
		t.Fatalf("computeInstanceName: %v", err)
	}
	if got != "tsuku+auth_layer" {
		t.Errorf("create --name \"Auth Layer\" = %q, want %q", got, "tsuku+auth_layer")
	}
}
