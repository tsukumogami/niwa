package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// These tests drive runDispatch with a machine config under a temporary
// XDG_CONFIG_HOME and a workspace posture declared in workspace.toml and run
// through a real Applier.Create (see dispatch_permissionmode_test.go), and
// assert on the launch argv and stderr. They cover the machine setting
// [global] dispatch_permission_mode: its place between the explicit flag and
// the workspace posture, its absence from every non-Claude launch, the stderr
// line naming its source, and the fail-closed refusal at step (2a) for a bad
// value or an unloadable file. The pure derivation table is
// TestDerivePermissionMode.

// workspaceDerivedLine is the stderr line dispatch has always printed for a
// mode derived from a `bypass` posture. It is spelled out in full, not built
// from the format dispatch uses, so a change to the line fails here.
const workspaceDerivedLine = "niwa dispatch: derived --permission-mode bypassPermissions from the workspace's declared permissions posture"

// hostLineNeedle is the start of the machine-setting stderr line.
const hostLineNeedle = "niwa dispatch: using --permission-mode"

// hostModeTOML is a machine config whose only key is the permission mode, raw
// as written (whitespace and case included).
func hostModeTOML(raw string) string {
	return fmt.Sprintf("[global]\ndispatch_permission_mode = %q\n", raw)
}

// hostConfigPath is where the machine config lives for the current test's
// XDG_CONFIG_HOME, i.e. what dispatch must name.
func hostConfigPath(t *testing.T) string {
	t.Helper()
	p, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// hostLine is the exact machine-setting stderr line for mode.
func hostLine(t *testing.T, mode string) string {
	t.Helper()
	return fmt.Sprintf("niwa dispatch: using --permission-mode %s from [global] dispatch_permission_mode in %s", mode, hostConfigPath(t))
}

// permissionLines returns the stderr lines about the permission mode: the
// machine-setting line and the workspace-derived line, whatever their mode.
func permissionLines(stderr string) []string {
	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, hostLineNeedle) || strings.HasPrefix(l, "niwa dispatch: derived --permission-mode") {
			lines = append(lines, l)
		}
	}
	return lines
}

func requirePermissionLines(t *testing.T, stderr string, want ...string) {
	t.Helper()
	got := permissionLines(stderr)
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !slices.Equal(got, want) {
		t.Fatalf("permission-mode stderr lines = %q, want %q\nstderr:\n%s", got, want, stderr)
	}
}

// sandboxValues returns the value following each --sandbox in argv.
func sandboxValues(argv []string) []string {
	var vals []string
	for i, a := range argv {
		if a == "--sandbox" && i+1 < len(argv) {
			vals = append(vals, argv[i+1])
		}
	}
	return vals
}

// hostDispatch is one dispatch's inputs for runHostDispatch.
type hostDispatch struct {
	posture  string // a postureS* declaration
	host     string // the machine config body; "" writes no file
	flag     string // --permission-mode, "" for none
	harness  string // --harness, "" for the default (Claude)
	mutateFS func(t *testing.T, cfgPath string)
}

// hostDispatchResult is what one dispatch produced.
type hostDispatchResult struct {
	root   string
	pass   []string
	stderr string
	err    error
	fakes  *dispatchFakes
}

// runHostDispatch sets up a declared workspace and a machine config, runs one
// dispatch, and returns its argv and stderr. The random source is fixed so two
// dispatches with the same inputs produce the same argv.
func runHostDispatch(t *testing.T, in hostDispatch) hostDispatchResult {
	t.Helper()
	root := setupDeclaredDispatchWorkspace(t, in.posture)
	chdir(t, root)
	setHostConfig(t, in.host)
	if in.mutateFS != nil {
		in.mutateFS(t, hostConfigPath(t))
	}
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	stubDispatchRand(t, constByteReader(0xab))
	dispatchPermissionMode = in.flag
	dispatchHarness = in.harness
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	return hostDispatchResult{root: root, pass: pass, stderr: stderr, err: err, fakes: f}
}

func (r hostDispatchResult) mustSucceed(t *testing.T) hostDispatchResult {
	t.Helper()
	if r.err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", r.err, r.stderr)
	}
	return r
}

// normalized replaces the per-test workspace root in argv and stderr so two
// dispatches from different temp roots can be compared element for element.
func (r hostDispatchResult) normalized() ([]string, string) {
	pass := make([]string, len(r.pass))
	for i, a := range r.pass {
		pass[i] = strings.ReplaceAll(a, r.root, "<root>")
	}
	return pass, strings.ReplaceAll(r.stderr, r.root, "<root>")
}

// requireNoInstanceDir asserts the refused dispatch left the workspace root
// holding only its .niwa config directory: nothing provisioned, nothing
// launched.
func requireNoInstanceDir(t *testing.T, r hostDispatchResult) {
	t.Helper()
	if r.fakes.provisionCalled != 0 || r.fakes.launchCalled != 0 {
		t.Fatalf("a refused dispatch provisioned %d and launched %d", r.fakes.provisionCalled, r.fakes.launchCalled)
	}
	entries, err := os.ReadDir(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != config.ConfigDir {
			t.Fatalf("a refused dispatch left %s in the workspace root", e.Name())
		}
	}
}

// TestDispatch_HostPermissionMode_Claude is the Claude half of the PRD
// acceptance matrix: each accepted value, both directions over the workspace
// posture, and the flag over both.
func TestDispatch_HostPermissionMode_Claude(t *testing.T) {
	cases := []struct {
		name     string
		posture  string
		host     string
		flag     string
		wantMode string // "" for no --permission-mode
		wantLine string // "host", "workspace", or "" for none
	}{
		{"auto over bypass", postureS1, "auto", "", "auto", "host"},
		{"default over bypass", postureS1, "default", "", "default", "host"},
		{"bypassPermissions with no posture", postureS3, "bypassPermissions", "", "bypassPermissions", "host"},
		{"bypassPermissions over ask", postureS2, "bypassPermissions", "", "bypassPermissions", "host"},
		{"acceptEdits", postureS3, "acceptEdits", "", "acceptEdits", "host"},
		{"plan", postureS3, "plan", "", "plan", "host"},
		{"dontAsk", postureS3, "dontAsk", "", "dontAsk", "host"},
		{"surrounding whitespace is trimmed", postureS3, "  auto  ", "", "auto", "host"},
		{"flag over host", postureS3, "auto", "default", "default", ""},
		{"flag over host and bypass", postureS1, "auto", "default", "default", ""},
		{"flag over workspace", postureS1, "", "plan", "plan", ""},
		{"empty value is no setting, bypass", postureS1, "", "", "bypassPermissions", "workspace"},
		{"whitespace value is no setting, bypass", postureS1, "  ", "", "bypassPermissions", "workspace"},
		{"empty value is no setting, none", postureS3, "", "", "", ""},
		{"whitespace value is no setting, none", postureS3, "  ", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// An empty or whitespace value is still written, so these cases
			// exercise a present key rather than a missing file.
			r := runHostDispatch(t, hostDispatch{posture: tc.posture, host: hostModeTOML(tc.host), flag: tc.flag}).mustSucceed(t)
			if tc.wantMode == "" {
				requireNoPermissionMode(t, r.pass)
			} else {
				// Exactly one pair: the flag never rides alongside a host- or
				// workspace-derived mode.
				requirePermissionMode(t, r.pass, tc.wantMode)
			}
			switch tc.wantLine {
			case "host":
				requirePermissionLines(t, r.stderr, hostLine(t, tc.wantMode))
			case "workspace":
				requirePermissionLines(t, r.stderr, workspaceDerivedLine)
			default:
				requirePermissionLines(t, r.stderr)
			}
		})
	}
}

// TestDispatch_HostPermissionMode_Codex: a valid machine setting changes
// nothing about a Codex launch, and the explicit flag still reaches Codex as
// its --sandbox, as it always has. Neither prints a permission-mode line.
func TestDispatch_HostPermissionMode_Codex(t *testing.T) {
	t.Run("host value is inert", func(t *testing.T) {
		for _, posture := range []struct{ name, decl string }{{"none", postureS3}, {"bypass", postureS1}} {
			t.Run(posture.name, func(t *testing.T) {
				with := runHostDispatch(t, hostDispatch{posture: posture.decl, host: hostModeTOML("bypassPermissions"), harness: "codex"}).mustSucceed(t)
				without := runHostDispatch(t, hostDispatch{posture: posture.decl, harness: "codex"}).mustSucceed(t)
				if got := sandboxValues(with.pass); len(got) != 0 {
					t.Fatalf("Codex got --sandbox %v from the machine setting: %v", got, with.pass)
				}
				requireNoPermissionMode(t, with.pass)
				requirePermissionLines(t, with.stderr)
				withPass, _ := with.normalized()
				withoutPass, _ := without.normalized()
				if !reflect.DeepEqual(withPass, withoutPass) {
					t.Fatalf("Codex argv changed with a machine setting:\n with    %q\n without %q", withPass, withoutPass)
				}
			})
		}
	})

	t.Run("flag still reaches --sandbox", func(t *testing.T) {
		r := runHostDispatch(t, hostDispatch{posture: postureS3, host: hostModeTOML("auto"), flag: "workspace-write", harness: "codex"}).mustSucceed(t)
		if got := sandboxValues(r.pass); !slices.Equal(got, []string{"workspace-write"}) {
			t.Fatalf("--sandbox values = %v, want [workspace-write] (argv %v)", got, r.pass)
		}
		if slices.Contains(r.pass, "--permission-mode") || slices.Contains(r.pass, "auto") {
			t.Fatalf("the machine setting leaked into a Codex launch: %v", r.pass)
		}
		requirePermissionLines(t, r.stderr)
	})
}

// TestDispatch_HostPermissionMode_InvalidValue: a value outside the accepted
// set stops every dispatch before anything is provisioned -- with or without
// the flag, and for Codex, which the value could never reach -- and the error
// names the key, the value, and all six accepted values.
func TestDispatch_HostPermissionMode_InvalidValue(t *testing.T) {
	cases := []struct {
		name, raw, value, flag, harness string
	}{
		{"misspelled", "atuo", "atuo", "", ""},
		{"misspelled with the flag", "atuo", "atuo", "default", ""},
		{"misspelled for Codex", "atuo", "atuo", "", "codex"},
		{"misspelled for Codex with the flag", "atuo", "atuo", "workspace-write", "codex"},
		{"wrong case", "Auto", "Auto", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runHostDispatch(t, hostDispatch{posture: postureS1, host: hostModeTOML(tc.raw), flag: tc.flag, harness: tc.harness})
			if r.err == nil {
				t.Fatalf("an invalid machine setting must fail the dispatch; argv %v", r.pass)
			}
			msg := r.err.Error()
			for _, want := range append([]string{"dispatch_permission_mode", fmt.Sprintf("%q", tc.value), hostConfigPath(t)}, config.DispatchPermissionModes...) {
				if !strings.Contains(msg, want) {
					t.Errorf("error lacks %q: %v", want, msg)
				}
			}
			requireNoInstanceDir(t, r)
		})
	}
}

// TestDispatch_HostPermissionMode_UnloadableConfig: a machine config that is
// not TOML, one whose dispatch_permission_mode is not a string, and one the
// process cannot read each stop the dispatch with an error naming the file,
// before anything is provisioned. A missing file is no setting and proceeds.
func TestDispatch_HostPermissionMode_UnloadableConfig(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		mutateFS func(t *testing.T, cfgPath string)
		root     bool // whether the case still breaks when running as root
	}{
		{"invalid TOML", "[global\ndispatch_permission_mode = \"auto\"\n", nil, true},
		{"non-string value", "[global]\ndispatch_permission_mode = 3\n", nil, true},
		{"unreadable file", hostModeTOML("auto"), func(t *testing.T, p string) {
			if err := os.Chmod(p, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
		}, false},
	}
	for _, tc := range cases {
		for _, harness := range []string{"", "codex"} {
			t.Run(fmt.Sprintf("%s, harness=%q", tc.name, harness), func(t *testing.T) {
				if !tc.root && os.Geteuid() == 0 {
					t.Skip("root reads a 0000 file")
				}
				r := runHostDispatch(t, hostDispatch{posture: postureS1, host: tc.host, harness: harness, mutateFS: tc.mutateFS})
				if r.err == nil {
					t.Fatalf("an unloadable machine config must fail the dispatch; argv %v", r.pass)
				}
				if p := hostConfigPath(t); !strings.Contains(r.err.Error(), p) {
					t.Fatalf("the error must name %s; got %v", p, r.err)
				}
				requireNoInstanceDir(t, r)
			})
		}
	}

	t.Run("missing file proceeds", func(t *testing.T) {
		r := runHostDispatch(t, hostDispatch{posture: postureS1}).mustSucceed(t)
		if _, err := os.Stat(hostConfigPath(t)); !os.IsNotExist(err) {
			t.Fatalf("precondition: the machine config must be missing; stat err %v", err)
		}
		requirePermissionMode(t, r.pass, "bypassPermissions")
		requirePermissionLines(t, r.stderr, workspaceDerivedLine)
	})
}

// TestDispatch_HostPermissionMode_NoSettingRegression: with no machine
// setting, a dispatch forwards the argv and prints the stderr it did before the
// setting existed, for both agents under every posture -- and a readable
// config without the key is the same as no config at all.
func TestDispatch_HostPermissionMode_NoSettingRegression(t *testing.T) {
	cases := []struct {
		name, posture, harness string
		wantMode               string
		wantWorkspaceLine      bool
	}{
		{"Claude bypass", postureS1, "", "bypassPermissions", true},
		{"Claude ask", postureS2, "", "", false},
		{"Claude none", postureS3, "", "", false},
		{"Codex bypass", postureS1, "codex", "", false},
		{"Codex ask", postureS2, "codex", "", false},
		{"Codex none", postureS3, "codex", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			missing := runHostDispatch(t, hostDispatch{posture: tc.posture, harness: tc.harness}).mustSucceed(t)
			keyless := runHostDispatch(t, hostDispatch{posture: tc.posture, harness: tc.harness, host: "[global]\n"}).mustSucceed(t)
			for name, r := range map[string]hostDispatchResult{"missing config": missing, "config without the key": keyless} {
				if tc.wantMode == "" {
					requireNoPermissionMode(t, r.pass)
				} else {
					requirePermissionMode(t, r.pass, tc.wantMode)
				}
				if got := sandboxValues(r.pass); len(got) != 0 {
					t.Fatalf("%s: unexpected --sandbox %v", name, got)
				}
				if tc.wantWorkspaceLine {
					requirePermissionLines(t, r.stderr, workspaceDerivedLine)
				} else {
					requirePermissionLines(t, r.stderr)
				}
			}
			mPass, mErr := missing.normalized()
			kPass, kErr := keyless.normalized()
			if !reflect.DeepEqual(mPass, kPass) {
				t.Fatalf("argv differs between a missing config and one without the key:\n missing %q\n keyless %q", mPass, kPass)
			}
			if mErr != kErr {
				t.Fatalf("stderr differs between a missing config and one without the key:\n missing:\n%s\n keyless:\n%s", mErr, kErr)
			}
		})
	}
}

// TestWatch_IgnoresHostPermissionMode: `niwa watch` never reads the machine
// setting. Its fresh-review launch through stageReview, and both launch
// builders, produce identical argv with and without a machine setting present
// -- including bypassPermissions, the value that would matter most if it
// leaked.
func TestWatch_IgnoresHostPermissionMode(t *testing.T) {
	stage := func(t *testing.T, hostBody string) ([]string, []string, []string) {
		t.Helper()
		root, home := setupWatchCharEnv(t)
		if hostBody != "" {
			p := hostConfigPath(t)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(hostBody), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		fakes := installWatchCharFakes(t)
		stubAskPostureSeams(t, home, nil)
		client := newPullHeadServer(t)
		cmd, _, _ := newWatchCharCmd()
		stubDispatchRand(t, constByteReader(0xab))
		if err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{}); err != nil {
			t.Fatalf("stageReview: %v", err)
		}
		if len(fakes.launches) != 1 {
			t.Fatalf("launches = %d, want 1", len(fakes.launches))
		}
		inst := fakes.provisioned.path
		fresh := watchReviewLaunch(inst, "watch_acme_widget_7", "review", true)
		resume := watchResumeLaunch(inst, "watch_acme_widget_7", watchCharSessionID, "again", true)
		return fakes.launches[0].Passthrough, fresh.Passthrough, resume.Passthrough
	}

	baseStaged, baseFresh, baseResume := stage(t, "")
	for _, m := range config.DispatchPermissionModes {
		t.Run(m, func(t *testing.T) {
			staged, fresh, resume := stage(t, hostModeTOML(m))
			if !reflect.DeepEqual(staged, baseStaged) {
				t.Errorf("staged review argv = %q, want %q (as without a machine setting)", staged, baseStaged)
			}
			if !reflect.DeepEqual(fresh, baseFresh) {
				t.Errorf("fresh review argv = %q, want %q", fresh, baseFresh)
			}
			if !reflect.DeepEqual(resume, baseResume) {
				t.Errorf("continuation argv = %q, want %q", resume, baseResume)
			}
			requireNoPermissionMode(t, staged)
		})
	}
}
