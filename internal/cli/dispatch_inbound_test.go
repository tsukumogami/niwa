package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

const (
	hostInboundOn  = "[global]\naccept_session_messages_on_dispatch = true\n"
	hostInboundOff = "[global]\naccept_session_messages_on_dispatch = false\n"

	// Substrings of the audit and override lines. Tests that assert a line is
	// absent look for these rather than for a full line, so a line printed with
	// either source, or any variant of it, still counts as printed.
	auditMarker    = "this worker accepts messages from other sessions"
	overrideMarker = "this worker keeps Claude Code's default"
)

func inboundFlag(b bool) *bool { return &b }

// launchSettingsDocs returns every document that follows a settings flag in
// the passthrough, parsed. Tests that build both keys parse the document rather
// than comparing strings: key order and spacing are the renderer's business.
func launchSettingsDocs(t *testing.T, passthrough []string) []map[string]any {
	t.Helper()
	var docs []map[string]any
	for i := 0; i < len(passthrough); i++ {
		if passthrough[i] != "--settings" {
			continue
		}
		if i+1 >= len(passthrough) {
			t.Fatalf("settings flag with no document after it: %v", passthrough)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(passthrough[i+1]), &doc); err != nil {
			t.Fatalf("settings document %q is not JSON: %v", passthrough[i+1], err)
		}
		docs = append(docs, doc)
	}
	return docs
}

// inboundKeyValue reports the crossSessionInbound value in the single launch
// document, and whether it is there. It fails the test when there is more than
// one document, which is the one-slot rule this feature must not break.
func inboundKeyValue(t *testing.T, passthrough []string) (any, bool) {
	t.Helper()
	docs := launchSettingsDocs(t, passthrough)
	if len(docs) > 1 {
		t.Fatalf("passthrough carries %d settings documents, want at most one: %v", len(docs), passthrough)
	}
	if len(docs) == 0 {
		return nil, false
	}
	v, ok := docs[0][config.CrossSessionInboundKey]
	return v, ok
}

// anyElementMentionsInbound reports whether any argv element contains the key,
// which is a stricter check than parsing: it also catches the key smuggled into
// some element that is not a settings document.
func anyElementMentionsInbound(passthrough []string) bool {
	for _, el := range passthrough {
		if strings.Contains(el, config.CrossSessionInboundKey) {
			return true
		}
	}
	return false
}

func TestResolveDispatchInboundAcceptance(t *testing.T) {
	tr, fa := inboundFlag(true), inboundFlag(false)
	tests := []struct {
		name     string
		flag     *bool
		machine  *bool
		want     inboundResolution
		describe string
	}{
		{"nothing set", nil, nil, inboundResolution{}, "off with no source"},
		{"machine true", nil, tr, inboundResolution{on: true, source: inboundSourceMachine}, "the machine setting decides"},
		{"machine false", nil, fa, inboundResolution{on: false, source: inboundSourceMachine}, "the machine setting decides"},
		{"flag true", tr, nil, inboundResolution{on: true, source: inboundSourceFlag}, "the flag decides"},
		{"flag true over machine true", tr, tr, inboundResolution{on: true, source: inboundSourceFlag}, "the flag decides; nothing overridden"},
		{"flag true over machine false", tr, fa, inboundResolution{on: true, source: inboundSourceFlag}, "the flag wins"},
		{"flag false", fa, nil, inboundResolution{on: false, source: inboundSourceFlag}, "the flag decides; nothing overridden"},
		{"flag false over machine true", fa, tr, inboundResolution{on: false, source: inboundSourceFlag, overrodeMachineOn: true}, "the flag overrides the machine setting"},
		{"flag false over machine false", fa, fa, inboundResolution{on: false, source: inboundSourceFlag}, "nothing overridden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveDispatchInboundAcceptance(tt.flag, config.GlobalSettings{AcceptSessionMessagesOnDispatch: tt.machine})
			if got != tt.want {
				t.Errorf("resolve = %+v, want %+v (%s)", got, tt.want, tt.describe)
			}
		})
	}
}

// TestDispatchCmd_HasAcceptSessionMessagesFlag pins the registration: the flag
// exists, its bare form means true, its help text is the design's wording, and
// it writes the variable runDispatch reads.
func TestDispatchCmd_HasAcceptSessionMessagesFlag(t *testing.T) {
	flag := dispatchCmd.Flags().Lookup("accept-session-messages")
	if flag == nil {
		t.Fatal("expected --accept-session-messages to be registered on dispatch")
	}
	if flag.NoOptDefVal != "true" {
		t.Errorf("NoOptDefVal = %q, want \"true\" so the bare flag means on", flag.NoOptDefVal)
	}
	const wantUsage = "accept messages from other Claude Code sessions without an approval prompt; overrides the [global] accept_session_messages_on_dispatch machine setting in either direction"
	if flag.Usage != wantUsage {
		t.Errorf("usage = %q, want %q", flag.Usage, wantUsage)
	}
	v, ok := flag.Value.(triBoolValue)
	if !ok {
		t.Fatalf("flag value is %T, want triBoolValue so unset and false stay distinct", flag.Value)
	}
	if v.target != &dispatchAcceptSessionMessages {
		t.Error("the registered flag does not write dispatchAcceptSessionMessages, the variable runDispatch reads")
	}
}

// TestAcceptSessionMessagesFlagParsing drives the three spellings through a
// flag set registered the way dispatch registers it.
func TestAcceptSessionMessagesFlagParsing(t *testing.T) {
	tests := []struct {
		args []string
		want *bool
	}{
		{nil, nil},
		{[]string{"--accept-session-messages"}, inboundFlag(true)},
		{[]string{"--accept-session-messages=true"}, inboundFlag(true)},
		{[]string{"--accept-session-messages=false"}, inboundFlag(false)},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var got *bool
			fs := (&cobra.Command{}).Flags()
			fs.Var(triBoolValue{&got}, acceptSessionMessagesFlagName, acceptSessionMessagesFlagUsage)
			fs.Lookup(acceptSessionMessagesFlagName).NoOptDefVal = "true"
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse %v: %v", tt.args, err)
			}
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("parse %v = %v, want unset", tt.args, *got)
			case tt.want != nil && got == nil:
				t.Errorf("parse %v left the flag unset, want %v", tt.args, *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("parse %v = %v, want %v", tt.args, *got, *tt.want)
			}
		})
	}
}

// TestInboundLinesExactText pins the three stderr strings to the design's text,
// written out here rather than rebuilt from the constants, so a reworded
// constant fails.
func TestInboundLinesExactText(t *testing.T) {
	const url = "https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md"
	if inboundGuideURL != url {
		t.Errorf("inboundGuideURL = %q, want %q", inboundGuideURL, url)
	}
	machine := inboundAuditLine(inboundSourceMachine)
	if want := "niwa dispatch: this worker accepts messages from other sessions without asking (source: machine setting accept_session_messages_on_dispatch); see " + url; machine != want {
		t.Errorf("machine audit line = %q, want %q", machine, want)
	}
	flag := inboundAuditLine(inboundSourceFlag)
	if want := "niwa dispatch: this worker accepts messages from other sessions without asking (source: --accept-session-messages); see " + url; flag != want {
		t.Errorf("flag audit line = %q, want %q", flag, want)
	}
	if want := "niwa dispatch: this worker keeps Claude Code's default for messages from other sessions (--accept-session-messages=false overrides the machine setting)"; inboundOverrideLine != want {
		t.Errorf("override line = %q, want %q", inboundOverrideLine, want)
	}
	warning := fmt.Sprintf(inboundUndeliverableFormat, "some-agent", "Some reason.")
	if want := `niwa dispatch: --accept-session-messages does not apply to the "some-agent" agent and was ignored. Some reason.`; warning != want {
		t.Errorf("warning = %q, want %q", warning, want)
	}
}

// TestDispatch_Inbound_PrecedenceMatrix runs a Claude dispatch through
// runDispatch for each combination the requirements name and asserts on the
// launch argv and the stderr line.
func TestDispatch_Inbound_PrecedenceMatrix(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		flag         *bool
		wantKey      bool
		wantAudit    string // full expected audit line, "" for none
		wantOverride bool
	}{
		{"key absent, no flag", "", nil, false, "", false},
		{"key false, no flag", hostInboundOff, nil, false, "", false},
		{"key true, no flag", hostInboundOn, nil, true, inboundAuditLine(inboundSourceMachine), false},
		// The bare flag and =true both parse to true; TestAcceptSessionMessagesFlagParsing
		// pins that, so one row covers both spellings here.
		{"key absent, flag true", "", inboundFlag(true), true, inboundAuditLine(inboundSourceFlag), false},
		{"key false, flag", hostInboundOff, inboundFlag(true), true, inboundAuditLine(inboundSourceFlag), false},
		{"key true, =true", hostInboundOn, inboundFlag(true), true, inboundAuditLine(inboundSourceFlag), false},
		{"key true, =false", hostInboundOn, inboundFlag(false), false, "", true},
		{"key absent, =false", "", inboundFlag(false), false, "", false},
		{"key false, =false", hostInboundOff, inboundFlag(false), false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, tt.host)
			f := installDispatchFakes(t, root)
			var pass []string
			captureLaunchPassthrough(f, &pass)
			dispatchAcceptSessionMessages = tt.flag

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			v, ok := inboundKeyValue(t, pass)
			if ok != tt.wantKey {
				t.Fatalf("crossSessionInbound present = %v, want %v; passthrough %v", ok, tt.wantKey, pass)
			}
			if ok && v != "accept" {
				t.Fatalf("crossSessionInbound = %v, want \"accept\"", v)
			}
			if !tt.wantKey && anyElementMentionsInbound(pass) {
				t.Fatalf("an argv element mentions %s although the behavior is off: %v", config.CrossSessionInboundKey, pass)
			}

			// Counting the marker rather than the full line also catches a second
			// audit line printed with the other source.
			if n := strings.Count(stderr, auditMarker); tt.wantAudit != "" {
				if n != 1 || !strings.Contains(stderr, tt.wantAudit+"\n") {
					t.Fatalf("want exactly one audit line %q, found %d; stderr:\n%s", tt.wantAudit, n, stderr)
				}
			} else if n != 0 {
				t.Fatalf("no audit line expected; stderr:\n%s", stderr)
			}

			if tt.wantOverride {
				if n := strings.Count(stderr, inboundOverrideLine+"\n"); n != 1 {
					t.Fatalf("override line appears %d times, want once; stderr:\n%s", n, stderr)
				}
			} else if strings.Contains(stderr, overrideMarker) {
				t.Fatalf("no override line expected; stderr:\n%s", stderr)
			}
			if strings.Contains(stderr, "does not apply") {
				t.Fatalf("a Claude dispatch must not warn that the behavior does not apply; stderr:\n%s", stderr)
			}
		})
	}
}

// TestDispatch_Inbound_BothKeysOneDocument turns on remote control, keep-alive,
// a declared bypass posture, and the behavior together. The launch carries one
// settings document with both keys, the derived permission mode, and a
// keep-alive arming identical to a dispatch without the behavior.
func TestDispatch_Inbound_BothKeysOneDocument(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "[global]\nremote_control_on_dispatch = true\nkeep_alive_on_dispatch = true\naccept_session_messages_on_dispatch = true\n")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, bypassSettings)
	var prompt string
	var pass []string
	captureLaunchPrompt(f, &prompt, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	docs := launchSettingsDocs(t, pass)
	if len(docs) != 1 {
		t.Fatalf("got %d settings documents, want exactly one: %v", len(docs), pass)
	}
	if docs[0][config.RemoteControlAtStartupKey] != true {
		t.Errorf("document lacks %s: true: %v", config.RemoteControlAtStartupKey, docs[0])
	}
	if docs[0][config.CrossSessionInboundKey] != "accept" {
		t.Errorf("document lacks %s: \"accept\": %v", config.CrossSessionInboundKey, docs[0])
	}
	if len(docs[0]) != 2 {
		t.Errorf("document has %d keys, want exactly the two contributors': %v", len(docs[0]), docs[0])
	}
	foundMode := false
	for i := 0; i+1 < len(pass); i++ {
		if pass[i] == "--permission-mode" && pass[i+1] == "bypassPermissions" {
			foundMode = true
		}
	}
	if !foundMode {
		t.Errorf("passthrough lacks --permission-mode bypassPermissions: %v", pass)
	}
	if want := keepAliveArmingInstruction + "do a thing"; prompt != want {
		t.Errorf("keep-alive must arm exactly as without the behavior; prompt = %q", prompt)
	}
	m, err := workspace.ReadSessionMapping(root, dispatchTestSessionID)
	if err != nil {
		t.Fatalf("reading mapping: %v", err)
	}
	if !m.KeepAlive {
		t.Error("mapping.KeepAlive = false, want true: remote control is on, so keep-alive arms")
	}
}

// TestDispatch_Inbound_DoesNotArmKeepAliveWithoutRemoteControl: rcInjected is
// remote control's own decision, so a document carrying only the inbound key
// must not look like remote control to keep-alive.
func TestDispatch_Inbound_DoesNotArmKeepAliveWithoutRemoteControl(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostInboundOn)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var prompt string
	var pass []string
	captureLaunchPrompt(f, &prompt, &pass)
	dispatchKeepAlive = inboundFlag(true)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, ok := inboundKeyValue(t, pass); !ok {
		t.Fatalf("the behavior is on; the document should carry %s: %v", config.CrossSessionInboundKey, pass)
	}
	docs := launchSettingsDocs(t, pass)
	if _, rc := docs[0][config.RemoteControlAtStartupKey]; rc {
		t.Fatalf("remote control is off; the document must not carry %s: %v", config.RemoteControlAtStartupKey, docs[0])
	}
	if !strings.Contains(stderr, keepAliveNonRCWarning) {
		t.Errorf("--keep-alive without remote control must take the non-RC warning path; stderr:\n%s", stderr)
	}
	if prompt != "do a thing" {
		t.Errorf("keep-alive must not arm without remote control; prompt = %q", prompt)
	}
	m, err := workspace.ReadSessionMapping(root, dispatchTestSessionID)
	if err != nil {
		t.Fatalf("reading mapping: %v", err)
	}
	if m.KeepAlive {
		t.Error("mapping.KeepAlive = true, want false without remote control")
	}
}

// TestDispatch_Inbound_UnreadableHostConfig: a config.toml that cannot be
// opened, one that is not TOML, and one whose key is not a boolean all count as
// an absent machine setting. The flag still applies, and remote control, whose
// preference shares the file, still injects nothing.
func TestDispatch_Inbound_UnreadableHostConfig(t *testing.T) {
	fixtures := []struct {
		name  string
		setup func(t *testing.T, niwaDir string)
	}{
		{"unreadable", func(t *testing.T, niwaDir string) {
			// A directory where the file should be fails the read for every
			// user, root included, on Linux and macOS alike.
			if err := os.MkdirAll(filepath.Join(niwaDir, "config.toml"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"invalid TOML", func(t *testing.T, niwaDir string) {
			writeInboundFixture(t, filepath.Join(niwaDir, "config.toml"), "[global\nremote_control_on_dispatch = true\naccept_session_messages_on_dispatch = true\n")
		}},
		{"non-boolean value", func(t *testing.T, niwaDir string) {
			writeInboundFixture(t, filepath.Join(niwaDir, "config.toml"), "[global]\nremote_control_on_dispatch = true\naccept_session_messages_on_dispatch = \"yes\"\n")
		}},
	}
	for _, fx := range fixtures {
		for _, withFlag := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, flag=%v", fx.name, withFlag), func(t *testing.T) {
				t.Setenv("ANTHROPIC_API_KEY", "")
				root := setupDispatchWorkspace(t)
				chdir(t, root)
				cfgHome := t.TempDir()
				t.Setenv("XDG_CONFIG_HOME", cfgHome)
				niwaDir := filepath.Join(cfgHome, "niwa")
				if err := os.MkdirAll(niwaDir, 0o755); err != nil {
					t.Fatal(err)
				}
				fx.setup(t, niwaDir)
				f := installDispatchFakes(t, root)
				var pass []string
				captureLaunchPassthrough(f, &pass)
				if withFlag {
					dispatchAcceptSessionMessages = inboundFlag(true)
				}

				_, stderr, err := runDispatchCmd(t, "do a thing")
				if err != nil {
					t.Fatalf("an unreadable host config must not fail the dispatch: %v", err)
				}
				_, ok := inboundKeyValue(t, pass)
				if ok != withFlag {
					t.Fatalf("crossSessionInbound present = %v, want %v (only the flag can turn it on here); passthrough %v", ok, withFlag, pass)
				}
				if withFlag {
					if !strings.Contains(stderr, inboundAuditLine(inboundSourceFlag)+"\n") {
						t.Fatalf("expected the audit line naming the flag; stderr:\n%s", stderr)
					}
				} else if strings.Contains(stderr, auditMarker) {
					t.Fatalf("no audit line expected without the flag; stderr:\n%s", stderr)
				}
				for _, doc := range launchSettingsDocs(t, pass) {
					if _, rc := doc[config.RemoteControlAtStartupKey]; rc {
						t.Fatalf("remote control must inject nothing from an unreadable config: %v", doc)
					}
				}
			})
		}
	}
}

// writeInboundFixture writes a fixture file for the tests in this file.
func writeInboundFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDispatch_Inbound_CodexWarnsOnlyWhenTheFlagAsked: Codex has no such
// setting. The flag earns a warning naming the agent and the declaration's
// reason; the machine setting alone says nothing, with or without =false.
func TestDispatch_Inbound_CodexWarnsOnlyWhenTheFlagAsked(t *testing.T) {
	decl, err := agentplan.Lookup(agentplan.DispatchInboundAcceptance, agent.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	if decl.State == agentplan.StateImplemented {
		t.Fatal("the codex row is implemented; this test assumes it is not")
	}
	wantWarning := fmt.Sprintf(inboundUndeliverableFormat, string(agent.AgentCodex), decl.Reason) + "\n"

	tests := []struct {
		name     string
		host     string
		flag     *bool
		wantWarn bool
	}{
		{"flag", "", inboundFlag(true), true},
		{"flag over machine setting", hostInboundOn, inboundFlag(true), true},
		{"machine setting only", hostInboundOn, nil, false},
		{"machine setting with =false", hostInboundOn, inboundFlag(false), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, tt.host)
			f := installDispatchFakes(t, root)
			var pass []string
			captureLaunchPassthrough(f, &pass)
			dispatchDetach = true
			t.Setenv("NIWA_DISPATCH_HARNESS", string(agent.AgentCodex))
			dispatchAcceptSessionMessages = tt.flag

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if f.launchCalled != 1 {
				t.Fatalf("launch called %d times, want 1", f.launchCalled)
			}
			if anyElementMentionsInbound(pass) {
				t.Fatalf("a Codex launch carries %s: %v", config.CrossSessionInboundKey, pass)
			}
			if got := strings.Count(stderr, wantWarning); tt.wantWarn && got != 1 {
				t.Fatalf("warning %q appears %d times, want once; stderr:\n%s", wantWarning, got, stderr)
			}
			if !tt.wantWarn && strings.Contains(stderr, "--accept-session-messages") {
				t.Fatalf("the machine setting alone must say nothing for Codex; stderr:\n%s", stderr)
			}
			if strings.Contains(stderr, auditMarker) || strings.Contains(stderr, overrideMarker) {
				t.Fatalf("no audit or override line for an agent that cannot receive the behavior; stderr:\n%s", stderr)
			}
		})
	}
}

// inboundMode is one way a test sets the behavior up. wantKey says whether
// the behavior takes effect for a Claude dispatch in this mode: the key
// reaches the launch and, on success, the mapping records it.
type inboundMode struct {
	name    string
	host    string
	flag    *bool
	wantKey bool
}

var inboundFailureModes = []inboundMode{
	{"machine setting on", hostInboundOn, nil, true},
	{"flag on", "", inboundFlag(true), true},
	{"=false over machine setting", hostInboundOn, inboundFlag(false), false},
}

// sessionMappingFiles lists the files under the workspace's session store, or
// nil when there is no store directory.
func sessionMappingFiles(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, workspace.StateDir, "sessions")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// assertNoInboundLine fails when stderr carries either the audit or the
// override line.
func assertNoInboundLine(t *testing.T, stderr string) {
	t.Helper()
	if strings.Contains(stderr, auditMarker) {
		t.Errorf("a failed dispatch printed the audit line; stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, overrideMarker) {
		t.Errorf("a failed dispatch printed the override line; stderr:\n%s", stderr)
	}
}

// setupInboundMode prepares a Claude dispatch in the given mode and returns the
// fakes plus a pointer the launch stub fills with the passthrough.
func setupInboundMode(t *testing.T, mode inboundMode) (string, *dispatchFakes, *[]string) {
	t.Helper()
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, mode.host)
	f := installDispatchFakes(t, root)
	pass := new([]string)
	captureLaunchPassthrough(f, pass)
	dispatchAcceptSessionMessages = mode.flag
	return root, f, pass
}

// checkLaunchedKey guards the tests built on inboundMode against passing vacuously: in
// the on modes the key really was handed to the launch.
func checkLaunchedKey(t *testing.T, mode inboundMode, pass []string) {
	t.Helper()
	if _, ok := inboundKeyValue(t, pass); ok != mode.wantKey {
		t.Fatalf("crossSessionInbound handed to the launch = %v, want %v: %v", ok, mode.wantKey, pass)
	}
}

func TestDispatch_Inbound_LaunchFailurePrintsNoLine(t *testing.T) {
	for _, mode := range inboundFailureModes {
		t.Run(mode.name, func(t *testing.T) {
			_, f, pass := setupInboundMode(t, mode)
			dispatchLaunch = func(_ context.Context, req launchRequest) error {
				f.launchCalled++
				*pass = req.Passthrough
				return errors.New("launch refused")
			}

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err == nil {
				t.Fatal("expected the launch failure to fail the dispatch")
			}
			checkLaunchedKey(t, mode, *pass)
			if f.captureCalled != 0 {
				t.Fatalf("capture called %d times after a failed launch, want 0", f.captureCalled)
			}
			assertNoInboundLine(t, stderr)
		})
	}
}

func TestDispatch_Inbound_CaptureFailurePrintsNoLine(t *testing.T) {
	for _, mode := range inboundFailureModes {
		t.Run(mode.name, func(t *testing.T) {
			root, f, pass := setupInboundMode(t, mode)
			dispatchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
				f.captureCalled++
				return "", "", errors.New("no session record")
			}

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err == nil || !strings.Contains(err.Error(), "capturing dispatch session id") {
				t.Fatalf("err = %v, want a capture failure", err)
			}
			checkLaunchedKey(t, mode, *pass)
			assertNoInboundLine(t, stderr)
			if f.destroyCalled != 1 {
				t.Errorf("rollback destroy called %d times, want 1", f.destroyCalled)
			}
			if files := sessionMappingFiles(t, root); len(files) != 0 {
				t.Errorf("a failed capture left files in the session store: %v", files)
			}
		})
	}
}

func TestDispatch_Inbound_MappingWriteFailurePrintsNoLine(t *testing.T) {
	fixtures := []struct {
		name  string
		setup func(t *testing.T, root string, f *dispatchFakes)
		check func(t *testing.T, root string)
	}{
		{
			name: "session id that is not a lowercase UUID",
			setup: func(t *testing.T, root string, f *dispatchFakes) {
				dispatchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
					f.captureCalled++
					return "NOT-A-UUID", dispatchTestShortID, nil
				}
			},
			check: func(t *testing.T, root string) {
				if files := sessionMappingFiles(t, root); len(files) != 0 {
					t.Errorf("a rejected mapping left files in the session store: %v", files)
				}
			},
		},
		{
			name: "session store is a regular file",
			setup: func(t *testing.T, root string, _ *dispatchFakes) {
				store := filepath.Join(root, workspace.StateDir, "sessions")
				if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
					t.Fatal(err)
				}
				writeInboundFixture(t, store, "")
			},
			check: func(t *testing.T, root string) {
				info, err := os.Stat(filepath.Join(root, workspace.StateDir, "sessions"))
				if err != nil || info.IsDir() {
					t.Errorf("the session store should still be the regular file the test put there (err %v)", err)
				}
			},
		},
	}
	for _, fx := range fixtures {
		for _, mode := range inboundFailureModes {
			t.Run(fx.name+", "+mode.name, func(t *testing.T) {
				root, f, pass := setupInboundMode(t, mode)
				fx.setup(t, root, f)

				_, stderr, err := runDispatchCmd(t, "do a thing")
				if err == nil || !strings.Contains(err.Error(), "writing dispatch session mapping") {
					t.Fatalf("err = %v, want a mapping-write failure", err)
				}
				checkLaunchedKey(t, mode, *pass)
				assertNoInboundLine(t, stderr)
				if f.destroyCalled != 1 {
					t.Errorf("rollback destroy called %d times, want 1", f.destroyCalled)
				}
				fx.check(t, root)
			})
		}
	}
}

// TestDispatch_Inbound_StdoutUnchanged: stdout is the same with the behavior on
// and off, apart from the instance path, and the resume command stays the
// agent's plain attach verb.
func TestDispatch_Inbound_StdoutUnchanged(t *testing.T) {
	run := func(t *testing.T, flag *bool) (stdout, stderr string) {
		t.Helper()
		root := setupDispatchWorkspace(t)
		chdir(t, root)
		setHostConfig(t, "")
		f := installDispatchFakes(t, root)
		dispatchDetach = true
		dispatchAcceptSessionMessages = flag
		stdout, stderr, err := runDispatchCmd(t, "do a thing")
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		return strings.ReplaceAll(stdout, f.instancePath, "<instance>"), stderr
	}
	off, offErr := run(t, nil)
	on, onErr := run(t, inboundFlag(true))
	// Without these two checks the comparison could pass between two runs
	// that both left the behavior off.
	if !strings.Contains(onErr, inboundAuditLine(inboundSourceFlag)) {
		t.Fatalf("the 'on' run did not apply the behavior; stderr:\n%s", onErr)
	}
	if strings.Contains(offErr, auditMarker) {
		t.Fatalf("the 'off' run printed an audit line; stderr:\n%s", offErr)
	}
	if on != off {
		t.Fatalf("stdout differs with the behavior on.\noff:\n%s\non:\n%s", off, on)
	}
	if !strings.Contains(on, "claude attach "+dispatchTestShortID) {
		t.Errorf("the resume command should stay `claude attach <id>`; stdout:\n%s", on)
	}
	if strings.Contains(on, auditMarker) {
		t.Errorf("the audit line belongs on stderr, not stdout:\n%s", on)
	}
}

// TestDispatch_Inbound_AuditLinePrecedesTheHints writes stdout and stderr into
// one buffer and checks the audit line lands before step 13's first stdout
// line, and that the attach at step 14 comes after both.
func TestDispatch_Inbound_AuditLinePrecedesTheHints(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostInboundOn)
	installDispatchFakes(t, root)

	var combined bytes.Buffer
	var atAttach string
	dispatchAttach = func(agentplan.LaunchSpec, string, string) error {
		atAttach = combined.String()
		return nil
	}
	cmd := &cobra.Command{}
	cmd.SetOut(&combined)
	cmd.SetErr(&combined)
	cmd.SetContext(context.Background())
	if err := runDispatch(cmd, []string{"do a thing"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	out := combined.String()
	audit := strings.Index(out, inboundAuditLine(inboundSourceMachine))
	hints := strings.Index(out, "Dispatched session ")
	if audit < 0 || hints < 0 {
		t.Fatalf("expected both the audit line and the headline; output:\n%s", out)
	}
	if audit > hints {
		t.Fatalf("the audit line must precede step 13's stdout; output:\n%s", out)
	}
	if !strings.Contains(atAttach, auditMarker) || !strings.Contains(atAttach, "Dispatched session ") {
		t.Fatalf("by the time attach runs, the audit line and hints should both be out; had:\n%s", atAttach)
	}
}
