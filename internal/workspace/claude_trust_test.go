package workspace

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/claudetrust"
	"github.com/tsukumogami/niwa/internal/config"
)

// These tests drive real Create and Apply runs with the Claude trust step on.
// The developer home is always a t.TempDir, HOME points at another one, and
// CLAUDE_CONFIG_DIR is cleared or set to a temp directory, so no run can reach
// the developer's own Claude config.

const trustOn = "\n[claude]\ntrust_instances = true\n"
const trustOff = "\n[claude]\ntrust_instances = false\n"

// appClaudeOff turns Claude off for the fixture's one repository, so a test
// about the instance root's record sees no repository grant beside it.
const appClaudeOff = "\n[repos.app.claude]\nenabled = false\n"

// trustFixture is a posture workspace whose applier delivers Claude trust into
// a temporary developer home.
type trustFixture struct {
	*postureFixture
	workspaceRoot string
	home          string // the applier's DeveloperHome
	claudeJSON    string // <home>/.claude.json
	out           *syncBuffer
}

func newTrustFixture(t *testing.T) *trustFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	pf := newPostureFixture(t, "")
	home := t.TempDir()
	a := pf.applier
	a.DeveloperHome = home
	a.prunePluginRecords = nil
	a.reconcileMarketplaceAutoUpdate = nil
	a.headSHA = func(string) (string, error) { return "abc123", nil }
	out := &syncBuffer{}
	a.Reporter = NewReporterWithTTY(out, false)

	return &trustFixture{
		postureFixture: pf,
		workspaceRoot:  filepath.Dir(pf.instanceRoot),
		home:           home,
		claudeJSON:     filepath.Join(home, ".claude.json"),
		out:            out,
	}
}

// withOverlay makes every run in the workspace merge overlayTOML as the
// workspace overlay.
func (f *trustFixture) withOverlay(t *testing.T, overlayTOML string) {
	t.Helper()
	if err := SaveState(f.workspaceRoot, &InstanceState{
		SchemaVersion: SchemaVersion,
		OverlayURL:    "testorg/posture-ws-overlay",
	}); err != nil {
		t.Fatal(err)
	}
	f.applier.cloneOrSync = func(_ context.Context, _, dir string) (bool, int, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, 0, err
		}
		return false, 0, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), []byte(overlayTOML), 0o644)
	}
}

// freshOutput gives the next run an empty output buffer.
func (f *trustFixture) freshOutput() {
	f.out = &syncBuffer{}
	f.applier.Reporter = NewReporterWithTTY(f.out, false)
}

// instance returns the root of the named instance, with its one repo already
// "cloned" so a run never reaches the network.
func (f *trustFixture) instance(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(f.workspaceRoot, name)
	if err := os.MkdirAll(filepath.Join(root, "default", "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// createNamed runs Create for the named instance and returns its output.
func (f *trustFixture) createNamed(t *testing.T, cfg *config.WorkspaceConfig, name string) (string, error) {
	t.Helper()
	f.instance(t, name)
	f.freshOutput()
	_, err := f.applier.Create(context.Background(), cfg, f.niwaDir, f.workspaceRoot, name)
	return permanentOutput(f.out.String()), err
}

// mustCreate creates the fixture's default instance and fails on an error.
func (f *trustFixture) mustCreate(t *testing.T, cfg *config.WorkspaceConfig) string {
	t.Helper()
	out, err := f.createNamed(t, cfg, cfg.Workspace.Name)
	if err != nil {
		t.Fatalf("Create: %v\n%s", err, out)
	}
	return out
}

// applyAt runs Apply on root and returns its output.
func (f *trustFixture) applyAt(t *testing.T, cfg *config.WorkspaceConfig, root string) (string, error) {
	t.Helper()
	f.freshOutput()
	err := f.applier.Apply(context.Background(), cfg, f.niwaDir, root)
	return permanentOutput(f.out.String()), err
}

// mustApply applies the default instance and fails on an error.
func (f *trustFixture) mustApply(t *testing.T, cfg *config.WorkspaceConfig) string {
	t.Helper()
	out, err := f.applyAt(t, cfg, f.instanceRoot)
	if err != nil {
		t.Fatalf("Apply: %v\n%s", err, out)
	}
	return out
}

// grantOf returns the Claude trust grant recorded in root's state.
func grantOf(t *testing.T, root string) *claudetrust.Grant {
	t.Helper()
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState(%s): %v", root, err)
	}
	return state.ClaudeTrust
}

// appRepo is the fixture's one repository, by the instance-relative path its
// Claude trust grant is recorded under.
const appRepo = "default/app"

// repoDir returns the fixture's repository directory in the default instance.
func (f *trustFixture) repoDir() string {
	return filepath.Join(f.instanceRoot, filepath.FromSlash(appRepo))
}

// repoGrantsOf returns the repository Claude trust grants recorded in root's
// state.
func repoGrantsOf(t *testing.T, root string) map[string]*claudetrust.Grant {
	t.Helper()
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState(%s): %v", root, err)
	}
	return state.ClaudeTrustRepos
}

// recordRepoClaudeTrust stores grants as root's recorded repository grants.
func recordRepoClaudeTrust(t *testing.T, root string, grants map[string]*claudetrust.Grant) {
	t.Helper()
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	state.ClaudeTrustRepos = grants
	if err := SaveState(root, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// fileSnap is a file's bytes and identity, to prove a run left it alone.
type fileSnap struct {
	data []byte
	info os.FileInfo
}

func snapFile(t *testing.T, path string) fileSnap {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fileSnap{data: mustRead(t, path), info: info}
}

// assertFileUntouched fails unless path has the same bytes, inode and mtime.
func assertFileUntouched(t *testing.T, path string, before fileSnap) {
	t.Helper()
	after := snapFile(t, path)
	if !bytes.Equal(before.data, after.data) {
		t.Errorf("%s changed:\nbefore: %s\nafter:  %s", path, before.data, after.data)
	}
	if !os.SameFile(before.info, after.info) {
		t.Errorf("%s was replaced (inode changed)", path)
	}
	if !before.info.ModTime().Equal(after.info.ModTime()) {
		t.Errorf("%s mtime changed: %v -> %v", path, before.info.ModTime(), after.info.ModTime())
	}
}

func assertNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists (err=%v), want it never created", path, err)
	}
}

// entryAt returns projects[key] from the Claude config at path, and whether
// it exists.
func entryAt(t *testing.T, path, key string) (map[string]any, bool) {
	t.Helper()
	doc := readClaudeConfig(t, path)
	projects, _ := doc["projects"].(map[string]any)
	e, ok := projects[key].(map[string]any)
	return e, ok
}

func wantGrant(file, key string, mode claudetrust.Mode) *claudetrust.Grant {
	return &claudetrust.Grant{ConfigFile: file, Key: key, Mode: mode, Source: claudetrust.SourceWorkspace}
}

const overlayNoticeMarker = "workspace overlay sets [claude] trust_instances"

// TestMergeWorkspaceOverlay_TrustInstances is the merge-level precedence rule:
// a base value wins whenever the base set the key, and only an overlay true
// taken over an absent base counts as the overlay turning trust on.
func TestMergeWorkspaceOverlay_TrustInstances(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name            string
		base, overlay   *bool
		want            *bool
		wantFromOverlay bool
	}{
		{name: "neither"},
		{name: "overlay true", overlay: &yes, want: &yes, wantFromOverlay: true},
		{name: "overlay false", overlay: &no, want: &no},
		{name: "base true", base: &yes, want: &yes},
		{name: "base false wins over overlay true", base: &no, overlay: &yes, want: &no},
		{name: "base true wins over overlay false", base: &yes, overlay: &no, want: &yes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := baseWS()
			ws.Claude.TrustInstances = tt.base
			overlay := &config.WorkspaceOverlay{Claude: config.OverlayClaudeConfig{TrustInstances: tt.overlay}}
			merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
			if err != nil {
				t.Fatalf("MergeWorkspaceOverlay: %v", err)
			}
			got := merged.Claude.TrustInstances
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("TrustInstances = %v, want %v", got, tt.want)
			}
			if merged.TrustInstancesFromOverlay != tt.wantFromOverlay {
				t.Errorf("TrustInstancesFromOverlay = %v, want %v", merged.TrustInstancesFromOverlay, tt.wantFromOverlay)
			}
			if ws.Claude.TrustInstances != tt.base {
				t.Error("the merge changed the base config")
			}
		})
	}
}

// TestClaudeTrust_CreateWritesAndRecords is the main path: with the option on
// in the base config and no Claude config yet, create writes the file private,
// holding only the flags for the resolved root and its repository, and records
// both grants.
func TestClaudeTrust_CreateWritesAndRecords(t *testing.T) {
	f := newTrustFixture(t)
	out := f.mustCreate(t, f.writeConfig(t, trustOn))

	key := trustKey(t, f.instanceRoot)
	repoKey := trustKey(t, f.repoDir())
	info, err := os.Stat(f.claudeJSON)
	if err != nil {
		t.Fatalf("no Claude config written: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("%s mode = %o, want 600", f.claudeJSON, got)
	}
	want := map[string]any{"projects": map[string]any{
		key:     map[string]any{"hasTrustDialogAccepted": true},
		repoKey: map[string]any{"hasTrustDialogAccepted": true},
	}}
	if got := readClaudeConfig(t, f.claudeJSON); !reflect.DeepEqual(got, want) {
		t.Errorf("Claude config = %v, want %v", got, want)
	}
	if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, wantGrant(f.claudeJSON, key, claudetrust.ModeCreated)) {
		t.Errorf("recorded grant = %+v", g)
	}
	wantRepos := map[string]*claudetrust.Grant{appRepo: wantGrant(f.claudeJSON, repoKey, claudetrust.ModeCreated)}
	if gs := repoGrantsOf(t, f.instanceRoot); !reflect.DeepEqual(gs, wantRepos) {
		t.Errorf("recorded repository grants = %+v, want %+v", gs, wantRepos)
	}
	if strings.Contains(out, overlayNoticeMarker) {
		t.Errorf("overlay notice printed for a base-config setting:\n%s", out)
	}
}

// TestClaudeTrust_OverlayAndBasePrecedence covers who decides: the overlay
// alone turns trust on, a base false refuses it, and a base true wins over an
// overlay false.
func TestClaudeTrust_OverlayAndBasePrecedence(t *testing.T) {
	tests := []struct {
		name       string
		base       string
		overlay    string
		wantEntry  bool
		wantNotice bool
	}{
		{name: "overlay only", overlay: "[claude]\ntrust_instances = true\n", wantEntry: true, wantNotice: true},
		{name: "base false, overlay true", base: trustOff, overlay: "[claude]\ntrust_instances = true\n"},
		{name: "base true, overlay false", base: trustOn, overlay: "[claude]\ntrust_instances = false\n", wantEntry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTrustFixture(t)
			f.withOverlay(t, tt.overlay)
			out := f.mustCreate(t, f.writeConfig(t, tt.base))

			g := grantOf(t, f.instanceRoot)
			if !tt.wantEntry {
				assertNoFile(t, f.claudeJSON)
				if g != nil {
					t.Errorf("recorded grant = %+v, want none", g)
				}
			} else {
				key := trustKey(t, f.instanceRoot)
				if e, ok := entryAt(t, f.claudeJSON, key); !ok || e["hasTrustDialogAccepted"] != true {
					t.Errorf("entry = %v, want the flag set", e)
				}
				if !reflect.DeepEqual(g, wantGrant(f.claudeJSON, key, claudetrust.ModeCreated)) {
					t.Errorf("recorded grant = %+v", g)
				}
			}
			if got := strings.Contains(out, overlayNoticeMarker); got != tt.wantNotice {
				t.Errorf("overlay notice shown = %v, want %v:\n%s", got, tt.wantNotice, out)
			}
		})
	}
}

// TestClaudeTrust_OverlayNoticeOncePerWorkspace checks the notice names the
// file and what trust lets Claude Code do, shows on the first write in a
// workspace only, and shows again in a different workspace.
func TestClaudeTrust_OverlayNoticeOncePerWorkspace(t *testing.T) {
	const overlay = "[claude]\ntrust_instances = true\n"
	f := newTrustFixture(t)
	f.withOverlay(t, overlay)
	cfg := f.writeConfig(t, "")

	first := f.mustCreate(t, cfg)
	if !strings.Contains(first, overlayNoticeMarker) || !strings.Contains(first, f.claudeJSON) ||
		!strings.Contains(first, "project permissions, hooks and MCP servers without asking") {
		t.Errorf("first create: notice missing or incomplete:\n%s", first)
	}
	if again := f.mustApply(t, cfg); strings.Contains(again, overlayNoticeMarker) {
		t.Errorf("apply after the notice repeated it:\n%s", again)
	}
	second, err := f.createNamed(t, cfg, "posture-ws-2")
	if err != nil {
		t.Fatalf("second Create: %v\n%s", err, second)
	}
	if grantOf(t, filepath.Join(f.workspaceRoot, "posture-ws-2")) == nil {
		t.Fatal("second instance not trusted")
	}
	if strings.Contains(second, overlayNoticeMarker) {
		t.Errorf("second instance in the same workspace repeated the notice:\n%s", second)
	}

	other := newTrustFixture(t)
	other.withOverlay(t, overlay)
	if out := other.mustCreate(t, other.writeConfig(t, "")); !strings.Contains(out, overlayNoticeMarker) {
		t.Errorf("a different workspace did not get the notice:\n%s", out)
	}
}

// TestClaudeTrust_OverlayNoticeForRecordedGrant covers an instance trusted
// before the overlay took over the option: the apply that finds trust on only
// because of the overlay shows the notice although it writes nothing, and the
// apply after it doesn't. A review grant carried forward never gets it.
func TestClaudeTrust_OverlayNoticeForRecordedGrant(t *testing.T) {
	const overlay = "[claude]\ntrust_instances = true\n"

	t.Run("workspace grant", func(t *testing.T) {
		f := newTrustFixture(t)
		f.withOverlay(t, overlay)
		if out := f.mustCreate(t, f.writeConfig(t, trustOn)); strings.Contains(out, overlayNoticeMarker) {
			t.Fatalf("create with the option in the base config showed the notice:\n%s", out)
		}
		before := snapFile(t, f.claudeJSON)

		cfg := f.writeConfig(t, "")
		if out := f.mustApply(t, cfg); !strings.Contains(out, overlayNoticeMarker) {
			t.Errorf("apply with trust on from the overlay and a recorded grant did not show the notice:\n%s", out)
		}
		assertFileUntouched(t, f.claudeJSON, before)
		if again := f.mustApply(t, cfg); strings.Contains(again, overlayNoticeMarker) {
			t.Errorf("the next apply repeated the notice:\n%s", again)
		}
	})

	t.Run("review grant", func(t *testing.T) {
		f := newTrustFixture(t)
		f.withOverlay(t, overlay)
		// With Claude off for the repository, the review grant is the only
		// grant the instance could hold.
		f.mustCreate(t, f.writeConfig(t, trustOff+appClaudeOff))
		key := trustKey(t, f.instanceRoot)
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
			key: map[string]any{"hasTrustDialogAccepted": true},
		}})
		recordClaudeTrust(t, f.instanceRoot, &claudetrust.Grant{
			ConfigFile: f.claudeJSON, Key: key, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceReview,
		})
		if out := f.mustApply(t, f.writeConfig(t, appClaudeOff)); strings.Contains(out, overlayNoticeMarker) {
			t.Errorf("a review grant got the overlay notice:\n%s", out)
		}
	})
}

// TestClaudeTrust_OverlayNoticeUnreadableRootState checks that a workspace
// root state that exists but can't be parsed is left byte-identical and the
// notice is held back, then shown once the file can be read again.
func TestClaudeTrust_OverlayNoticeUnreadableRootState(t *testing.T) {
	f := newTrustFixture(t)
	f.withOverlay(t, "[claude]\ntrust_instances = true\n")
	f.mustCreate(t, f.writeConfig(t, trustOn))

	rootStatePath := filepath.Join(f.workspaceRoot, StateDir, StateFile)
	good := mustRead(t, rootStatePath)
	if err := os.WriteFile(rootStatePath, []byte(`{"overlay_url": "testorg/posture-ws-overlay", not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapFile(t, rootStatePath)

	cfg := f.writeConfig(t, "")
	out := f.mustApply(t, cfg)
	assertFileUntouched(t, rootStatePath, before)
	if strings.Contains(out, overlayNoticeMarker) {
		t.Errorf("notice shown although it could not be recorded:\n%s", out)
	}
	if grantOf(t, f.instanceRoot) == nil {
		t.Fatal("the apply dropped the grant; the overlay did not keep trust on")
	}

	if err := os.WriteFile(rootStatePath, good, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := f.mustApply(t, cfg); !strings.Contains(out, overlayNoticeMarker) {
		t.Errorf("notice not shown once the root state was readable:\n%s", out)
	}
	state, err := LoadState(f.workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.OverlayURL != "testorg/posture-ws-overlay" || !sliceContains(state.DisclosedNotices, noticeClaudeTrustOverlay) {
		t.Errorf("root state = %+v, want the overlay URL kept and the notice recorded", state)
	}
}

// TestClaudeTrust_OverlayNoticeSingleInstanceLayout drives the notice where
// the workspace root holds no state: it is shown only by a run that wrote
// trust, and nothing is written to the root.
func TestClaudeTrust_OverlayNoticeSingleInstanceLayout(t *testing.T) {
	root := t.TempDir()
	out := &syncBuffer{}
	a := &Applier{Reporter: NewReporterWithTTY(out, false)}
	grant := wantGrant("/home/dev/.claude.json", "/work/ws", claudetrust.ModeCreated)

	a.discloseOverlayTrust(root, true, false, grant, nil)
	a.Reporter.FlushDeferred()
	if strings.Contains(out.String(), overlayNoticeMarker) {
		t.Errorf("notice shown by a run that wrote nothing:\n%s", out.String())
	}

	a.discloseOverlayTrust(root, true, true, grant, nil)
	a.Reporter.FlushDeferred()
	if !strings.Contains(out.String(), overlayNoticeMarker) {
		t.Errorf("notice not shown by a run that wrote trust:\n%s", out.String())
	}
	assertNoFile(t, filepath.Join(root, StateDir))
}

// TestClaudeTrust_KeyAbsentTouchesNothing checks that with the option absent
// an existing Claude config is left byte-identical and none is created.
func TestClaudeTrust_KeyAbsentTouchesNothing(t *testing.T) {
	t.Run("existing file", func(t *testing.T) {
		f := newTrustFixture(t)
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{"/elsewhere": map[string]any{"x": 1}}})
		before := snapFile(t, f.claudeJSON)
		cfg := f.writeConfig(t, "")
		f.mustCreate(t, cfg)
		f.mustApply(t, cfg)
		assertFileUntouched(t, f.claudeJSON, before)
		if g := grantOf(t, f.instanceRoot); g != nil {
			t.Errorf("recorded grant = %+v, want none", g)
		}
	})
	t.Run("no file", func(t *testing.T) {
		f := newTrustFixture(t)
		cfg := f.writeConfig(t, "")
		f.mustCreate(t, cfg)
		f.mustApply(t, cfg)
		assertNoFile(t, f.claudeJSON)
	})
}

// TestClaudeTrust_ApplyAddsThenLeavesAlone covers an instance created before
// the option was on: the next apply adds and records the entry, and the one
// after leaves the file's bytes, inode and mtime as they were.
func TestClaudeTrust_ApplyAddsThenLeavesAlone(t *testing.T) {
	f := newTrustFixture(t)
	f.mustCreate(t, f.writeConfig(t, ""))
	assertNoFile(t, f.claudeJSON)

	on := f.writeConfig(t, trustOn)
	f.mustApply(t, on)
	key := trustKey(t, f.instanceRoot)
	if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, wantGrant(f.claudeJSON, key, claudetrust.ModeCreated)) {
		t.Fatalf("recorded grant = %+v", g)
	}

	before := snapFile(t, f.claudeJSON)
	f.mustApply(t, on)
	assertFileUntouched(t, f.claudeJSON, before)
	if g := grantOf(t, f.instanceRoot); g == nil {
		t.Error("second apply dropped the record")
	}
}

// TestClaudeTrust_OffSwitchRetracts covers turning the option off: the flag
// goes, keys Claude Code added to the entry stay, and the record is cleared
// for a created grant and a flag-only grant alike. An entry already removed
// by hand leaves the file alone and clears the record too.
func TestClaudeTrust_OffSwitchRetracts(t *testing.T) {
	for name, offBody := range map[string]string{"false": trustOff, "absent": ""} {
		t.Run(name, func(t *testing.T) {
			f := newTrustFixture(t)
			f.mustCreate(t, f.writeConfig(t, trustOn))
			key := trustKey(t, f.instanceRoot)

			// Claude Code adds its own keys to the entry once a session runs.
			writeClaudeConfig(t, f.claudeJSON, map[string]any{
				"numStartups": 3,
				"projects": map[string]any{
					key:        map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []any{"Bash(ls)"}},
					"/another": map[string]any{"hasTrustDialogAccepted": true},
				},
			})

			f.mustApply(t, f.writeConfig(t, offBody))
			e, ok := entryAt(t, f.claudeJSON, key)
			if !ok {
				t.Fatal("the off switch removed the whole entry")
			}
			if _, has := e["hasTrustDialogAccepted"]; has {
				t.Errorf("flag still set after the off switch: %v", e)
			}
			if !reflect.DeepEqual(e["allowedTools"], []any{"Bash(ls)"}) {
				t.Errorf("allowedTools = %v, want it kept", e["allowedTools"])
			}
			if other, _ := entryAt(t, f.claudeJSON, "/another"); other["hasTrustDialogAccepted"] != true {
				t.Errorf("another project's entry changed: %v", other)
			}
			if g := grantOf(t, f.instanceRoot); g != nil {
				t.Errorf("record = %+v, want it cleared", g)
			}

			// A second apply with the option off has no record to act on
			// and leaves the file as it is.
			before := snapFile(t, f.claudeJSON)
			if out := f.mustApply(t, f.writeConfig(t, offBody)); strings.Contains(out, "Claude trust") {
				t.Errorf("second off apply warned about Claude trust:\n%s", out)
			}
			assertFileUntouched(t, f.claudeJSON, before)
			if g := grantOf(t, f.instanceRoot); g != nil {
				t.Errorf("record after a second off apply = %+v, want none", g)
			}
		})
	}

	t.Run("flag-only record is cleared", func(t *testing.T) {
		f := newTrustFixture(t)
		f.instance(t, filepath.Base(f.instanceRoot))
		key := trustKey(t, f.instanceRoot)
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
			key: map[string]any{"allowedTools": []any{"Read"}},
		}})
		f.mustCreate(t, f.writeConfig(t, trustOn))
		if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, wantGrant(f.claudeJSON, key, claudetrust.ModeFlagOnly)) {
			t.Fatalf("record after create = %+v, want a flag-only grant", g)
		}

		f.mustApply(t, f.writeConfig(t, trustOff))
		e, ok := entryAt(t, f.claudeJSON, key)
		if !ok || !reflect.DeepEqual(e, map[string]any{"allowedTools": []any{"Read"}}) {
			t.Errorf("entry = %v (present %v), want only the flag removed", e, ok)
		}
		if g := grantOf(t, f.instanceRoot); g != nil {
			t.Errorf("record = %+v, want it cleared", g)
		}
	})

	t.Run("entry removed by hand", func(t *testing.T) {
		f := newTrustFixture(t)
		f.mustCreate(t, f.writeConfig(t, trustOn))
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{}})
		before := snapFile(t, f.claudeJSON)

		if out, err := f.applyAt(t, f.writeConfig(t, trustOff), f.instanceRoot); err != nil {
			t.Fatalf("Apply: %v\n%s", err, out)
		}
		assertFileUntouched(t, f.claudeJSON, before)
		if g := grantOf(t, f.instanceRoot); g != nil {
			t.Errorf("record = %+v, want it cleared", g)
		}
	})
}

// TestClaudeTrust_OffThenDestroyLeavesFlaglessEntry checks the cost of
// clearing the record at the off switch: the entry niwa created stays in the
// config without the flag, and destroying the instance afterwards, with no
// record left, leaves the file as it is.
func TestClaudeTrust_OffThenDestroyLeavesFlaglessEntry(t *testing.T) {
	f := newTrustFixture(t)
	f.mustCreate(t, f.writeConfig(t, trustOn))
	key := trustKey(t, f.instanceRoot)
	writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
		key:        map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []any{"Bash(ls)"}},
		"/another": map[string]any{"hasTrustDialogAccepted": true},
	}})
	f.mustApply(t, f.writeConfig(t, trustOff))
	if g := grantOf(t, f.instanceRoot); g != nil {
		t.Fatalf("record after off = %+v, want it cleared", g)
	}
	before := snapFile(t, f.claudeJSON)

	if err := DestroyInstance(f.instanceRoot, WithDestroyReporter(f.applier.Reporter), WithPluginRecordBaseDir(f.home)); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertFileUntouched(t, f.claudeJSON, before)
	if e, ok := entryAt(t, f.claudeJSON, key); !ok || !reflect.DeepEqual(e, map[string]any{"allowedTools": []any{"Bash(ls)"}}) {
		t.Errorf("entry = %v (present %v), want the flagless entry left behind", e, ok)
	}
}

// TestClaudeTrust_OffThenOnWritesFreshGrant checks turning the option back on
// after the off switch writes a new grant. The entry niwa created is still
// there without the flag, so the flag is set on it and the grant is recorded
// flag-only.
func TestClaudeTrust_OffThenOnWritesFreshGrant(t *testing.T) {
	f := newTrustFixture(t)
	on := f.writeConfig(t, trustOn)
	f.mustCreate(t, on)
	key := trustKey(t, f.instanceRoot)
	f.mustApply(t, f.writeConfig(t, trustOff))
	if e, _ := entryAt(t, f.claudeJSON, key); e["hasTrustDialogAccepted"] != nil {
		t.Fatalf("entry after off = %v, want the flag gone", e)
	}

	f.mustApply(t, on)
	if e, ok := entryAt(t, f.claudeJSON, key); !ok || e["hasTrustDialogAccepted"] != true {
		t.Errorf("entry after on = %v (present %v), want the flag set", e, ok)
	}
	if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, wantGrant(f.claudeJSON, key, claudetrust.ModeFlagOnly)) {
		t.Errorf("record = %+v, want a fresh flag-only grant", g)
	}
}

// TestClaudeTrust_OffLeavesHandSetFlag checks the off switch retracts once:
// after niwa withdrew its own flag, a flag the developer sets by hand is
// someone else's trust, and a later apply with the option off leaves it.
func TestClaudeTrust_OffLeavesHandSetFlag(t *testing.T) {
	f := newTrustFixture(t)
	f.mustCreate(t, f.writeConfig(t, trustOn))
	key := trustKey(t, f.instanceRoot)
	off := f.writeConfig(t, trustOff)
	f.mustApply(t, off)

	writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
		key: map[string]any{"hasTrustDialogAccepted": true},
	}})
	before := snapFile(t, f.claudeJSON)
	f.mustApply(t, off)
	assertFileUntouched(t, f.claudeJSON, before)
	if g := grantOf(t, f.instanceRoot); g != nil {
		t.Errorf("record = %+v, want none", g)
	}
}

// TestClaudeTrust_OffSwitchDropsMalformedRecord checks a workspace record the
// off switch can't act on (unknown mode, unknown source, a bad config_file)
// is dropped with one warning, as Ensure drops it, rather than kept and
// warned about on every apply. The Claude config is left alone.
func TestClaudeTrust_OffSwitchDropsMalformedRecord(t *testing.T) {
	cases := map[string]func(g *claudetrust.Grant){
		"unknown mode":        func(g *claudetrust.Grant) { g.Mode = "bogus" },
		"unknown source":      func(g *claudetrust.Grant) { g.Source = "bogus" },
		"relative file":       func(g *claudetrust.Grant) { g.ConfigFile = "rel/.claude.json" },
		"wrong file basename": func(g *claudetrust.Grant) { g.ConfigFile = filepath.Join(filepath.Dir(g.ConfigFile), "settings.json") },
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newTrustFixture(t)
			f.mustCreate(t, f.writeConfig(t, trustOn))
			key := trustKey(t, f.instanceRoot)
			repoKey := trustKey(t, f.repoDir())
			g := grantOf(t, f.instanceRoot)
			corrupt(g)
			recordClaudeTrust(t, f.instanceRoot, g)
			rg := repoGrantsOf(t, f.instanceRoot)[appRepo]
			corrupt(rg)
			recordRepoClaudeTrust(t, f.instanceRoot, map[string]*claudetrust.Grant{appRepo: rg})
			before := snapFile(t, f.claudeJSON)

			off := f.writeConfig(t, trustOff)
			out := f.mustApply(t, off)
			for _, k := range []string{key, repoKey} {
				if !strings.Contains(out, "warning:") || !strings.Contains(out, "malformed Claude trust record for "+k) {
					t.Errorf("no warning about the malformed record for %s:\n%s", k, out)
				}
			}
			if g := grantOf(t, f.instanceRoot); g != nil {
				t.Errorf("record = %+v, want it dropped", g)
			}
			if gs := repoGrantsOf(t, f.instanceRoot); gs != nil {
				t.Errorf("repository records = %+v, want them dropped", gs)
			}
			if out := f.mustApply(t, off); strings.Contains(out, "malformed") {
				t.Errorf("the dropped record was warned about again:\n%s", out)
			}
			assertFileUntouched(t, f.claudeJSON, before)
		})
	}
}

// TestClaudeTrust_ConfigDirChanged covers CLAUDE_CONFIG_DIR set at create and
// unset at a later apply: the flag leaves the first file, the entry lands in
// $HOME/.claude.json, and the record names the new file.
func TestClaudeTrust_ConfigDirChanged(t *testing.T) {
	f := newTrustFixture(t)
	dirA := filepath.Join(t.TempDir(), "claude-a")
	fileA := filepath.Join(dirA, ".claude.json")
	on := f.writeConfig(t, trustOn)

	t.Setenv("CLAUDE_CONFIG_DIR", dirA)
	f.mustCreate(t, on)
	key := trustKey(t, f.instanceRoot)
	if g := grantOf(t, f.instanceRoot); g == nil || g.ConfigFile != fileA {
		t.Fatalf("grant after create = %+v, want it in %s", g, fileA)
	}
	assertNoFile(t, f.claudeJSON)

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f.mustApply(t, on)
	if e, ok := entryAt(t, fileA, key); ok {
		if _, has := e["hasTrustDialogAccepted"]; has {
			t.Errorf("flag still in %s: %v", fileA, e)
		}
	}
	if e, ok := entryAt(t, f.claudeJSON, key); !ok || e["hasTrustDialogAccepted"] != true {
		t.Errorf("entry in %s = %v, want the flag set", f.claudeJSON, e)
	}
	if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, wantGrant(f.claudeJSON, key, claudetrust.ModeCreated)) {
		t.Errorf("recorded grant = %+v, want it to name %s", g, f.claudeJSON)
	}
}

// TestClaudeTrust_MovedInstance covers a record whose key is no longer the
// instance's path: the old entry stays as it was, the warning names it and
// its file, and a fresh grant is recorded for the new path.
func TestClaudeTrust_MovedInstance(t *testing.T) {
	f := newTrustFixture(t)
	on := f.writeConfig(t, trustOn)
	f.mustCreate(t, on)
	oldKey := trustKey(t, f.instanceRoot)
	oldEntry, _ := entryAt(t, f.claudeJSON, oldKey)

	moved := filepath.Join(f.workspaceRoot, "posture-ws-moved")
	if err := os.Rename(f.instanceRoot, moved); err != nil {
		t.Fatal(err)
	}
	out, err := f.applyAt(t, on, moved)
	if err != nil {
		t.Fatalf("Apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, oldKey) || !strings.Contains(out, f.claudeJSON) {
		t.Errorf("no warning naming %s and %s:\n%s", oldKey, f.claudeJSON, out)
	}
	if e, ok := entryAt(t, f.claudeJSON, oldKey); !ok || !reflect.DeepEqual(e, oldEntry) {
		t.Errorf("old entry = %v, want it left as %v", e, oldEntry)
	}
	newKey := trustKey(t, moved)
	if g := grantOf(t, moved); !reflect.DeepEqual(g, wantGrant(f.claudeJSON, newKey, claudetrust.ModeCreated)) {
		t.Errorf("recorded grant = %+v, want a fresh grant for %s", g, newKey)
	}
}

// TestClaudeTrust_MovedInstanceOptionOff covers the off switch on a moved
// instance: the record is dropped with a warning naming the old entry and its
// file, and the Claude config is left byte-identical.
func TestClaudeTrust_MovedInstanceOptionOff(t *testing.T) {
	f := newTrustFixture(t)
	f.mustCreate(t, f.writeConfig(t, trustOn))
	oldKey := trustKey(t, f.instanceRoot)
	before := snapFile(t, f.claudeJSON)

	moved := filepath.Join(f.workspaceRoot, "posture-ws-moved")
	if err := os.Rename(f.instanceRoot, moved); err != nil {
		t.Fatal(err)
	}
	out, err := f.applyAt(t, f.writeConfig(t, trustOff), moved)
	if err != nil {
		t.Fatalf("Apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, oldKey) || !strings.Contains(out, f.claudeJSON) {
		t.Errorf("no warning naming %s and %s:\n%s", oldKey, f.claudeJSON, out)
	}
	assertFileUntouched(t, f.claudeJSON, before)
	if g := grantOf(t, moved); g != nil {
		t.Errorf("recorded grant = %+v, want it dropped", g)
	}
}

// TestClaudeTrust_SaveFailureRollsBack injects a state-save failure after the
// trust step and checks only this run's write is undone.
func TestClaudeTrust_SaveFailureRollsBack(t *testing.T) {
	failSave := func(string, *InstanceState) error { return errors.New("injected save failure") }

	t.Run("first-time created grant", func(t *testing.T) {
		f := newTrustFixture(t)
		f.applier.saveState = failSave
		if _, err := f.createNamed(t, f.writeConfig(t, trustOn), "posture-ws"); err == nil {
			t.Fatal("Create succeeded despite the injected save failure")
		}
		if _, ok := entryAt(t, f.claudeJSON, trustKey(t, f.instanceRoot)); ok {
			t.Error("the created entry survived the failed save")
		}
	})

	t.Run("first-time flag-only grant", func(t *testing.T) {
		f := newTrustFixture(t)
		key := trustKey(t, f.instance(t, "posture-ws"))
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
			key: map[string]any{"allowedTools": []any{"Read"}, "lastCost": 1.5},
		}})
		f.applier.saveState = failSave
		if _, err := f.createNamed(t, f.writeConfig(t, trustOn), "posture-ws"); err == nil {
			t.Fatal("Create succeeded despite the injected save failure")
		}
		e, ok := entryAt(t, f.claudeJSON, key)
		if !ok {
			t.Fatal("the rollback removed an entry niwa didn't create")
		}
		if want := map[string]any{"allowedTools": []any{"Read"}, "lastCost": 1.5}; !reflect.DeepEqual(e, want) {
			t.Errorf("entry = %v, want %v (flag gone, other keys intact)", e, want)
		}
	})

	t.Run("grant recorded before the run", func(t *testing.T) {
		f := newTrustFixture(t)
		on := f.writeConfig(t, trustOn)
		f.mustCreate(t, on)
		statePath := filepath.Join(f.instanceRoot, StateDir, StateFile)
		beforeState := mustRead(t, statePath)
		before := snapFile(t, f.claudeJSON)

		f.applier.saveState = failSave
		if _, err := f.applyAt(t, on, f.instanceRoot); err == nil {
			t.Fatal("Apply succeeded despite the injected save failure")
		}
		assertFileUntouched(t, f.claudeJSON, before)
		if !bytes.Equal(mustRead(t, statePath), beforeState) {
			t.Error("instance state changed although the save failed")
		}
	})
}

// TestClaudeTrust_LatePipelineFailureWritesNothing injects a failure at the
// pipeline's last step and checks no trust was written before it.
func TestClaudeTrust_LatePipelineFailureWritesNothing(t *testing.T) {
	lateFailure := func() error { return errors.New("injected late pipeline failure") }

	t.Run("create", func(t *testing.T) {
		f := newTrustFixture(t)
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{"/elsewhere": map[string]any{"x": 1}}})
		before := snapFile(t, f.claudeJSON)
		f.applier.afterPipeline = lateFailure
		if _, err := f.createNamed(t, f.writeConfig(t, trustOn), "posture-ws"); err == nil {
			t.Fatal("Create succeeded despite the injected failure")
		}
		assertFileUntouched(t, f.claudeJSON, before)
		assertNoFile(t, f.instanceRoot)
	})

	t.Run("apply", func(t *testing.T) {
		f := newTrustFixture(t)
		f.mustCreate(t, f.writeConfig(t, trustOn))
		before := snapFile(t, f.claudeJSON)
		recorded := grantOf(t, f.instanceRoot)

		// Both directions would write if the step ran: off retracts the flag.
		f.applier.afterPipeline = lateFailure
		for _, body := range []string{trustOff, trustOn} {
			if _, err := f.applyAt(t, f.writeConfig(t, body), f.instanceRoot); err == nil {
				t.Fatal("Apply succeeded despite the injected failure")
			}
			assertFileUntouched(t, f.claudeJSON, before)
			if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, recorded) {
				t.Errorf("record = %+v, want it unchanged (%+v)", g, recorded)
			}
		}
	})
}

// TestClaudeTrust_EmptyDeveloperHome checks an applier with no developer home
// writes nothing outside the instance and carries an existing record forward.
func TestClaudeTrust_EmptyDeveloperHome(t *testing.T) {
	f := newTrustFixture(t)
	on := f.writeConfig(t, trustOn)
	f.mustCreate(t, on)
	recorded := grantOf(t, f.instanceRoot)
	before := snapFile(t, f.claudeJSON)

	f.applier.DeveloperHome = ""
	f.mustApply(t, f.writeConfig(t, trustOff))
	assertFileUntouched(t, f.claudeJSON, before)
	if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, recorded) {
		t.Errorf("record = %+v, want it carried forward as %+v", g, recorded)
	}

	// A fresh instance with no home writes nowhere, HOME included.
	if out, err := f.createNamed(t, on, "posture-ws-2"); err != nil {
		t.Fatalf("Create: %v\n%s", err, out)
	}
	if g := grantOf(t, filepath.Join(f.workspaceRoot, "posture-ws-2")); g != nil {
		t.Errorf("record = %+v, want none", g)
	}
	assertNoFile(t, filepath.Join(os.Getenv("HOME"), ".claude.json"))
	assertFileUntouched(t, f.claudeJSON, before)
}

// TestClaudeTrust_InstanceRootIsHomeRefused checks an instance rooted at the
// developer home itself, as a single-instance `niwa init` run in the home
// directory gives, is never trusted: Claude Code would then trust every
// directory under home. Create and apply both warn and record nothing for
// the root. The repository inside it is a directory of its own, a git
// repository Claude Code bounds its trust lookup at, so it is still trusted.
func TestClaudeTrust_InstanceRootIsHomeRefused(t *testing.T) {
	f := newTrustFixture(t)
	on := f.writeConfig(t, trustOn)
	root := f.instance(t, on.Workspace.Name)
	if root != f.instanceRoot {
		t.Fatalf("instance root %s, want the fixture's %s", root, f.instanceRoot)
	}
	f.applier.DeveloperHome = root
	homeJSON := filepath.Join(root, ".claude.json")
	key := trustKey(t, root)
	repoKey := trustKey(t, f.repoDir())

	check := func(run, output string) {
		t.Helper()
		if !strings.Contains(output, key) || !strings.Contains(output, "is the home directory") {
			t.Errorf("%s output lacks the refusal naming %s:\n%s", run, key, output)
		}
		if g := grantOf(t, root); g != nil {
			t.Errorf("after %s, record = %+v, want none", run, g)
		}
		if _, ok := entryAt(t, homeJSON, key); ok {
			t.Errorf("after %s, %s holds an entry for the home directory %s", run, homeJSON, key)
		}
		if e, ok := entryAt(t, homeJSON, repoKey); !ok || e["hasTrustDialogAccepted"] != true {
			t.Errorf("after %s, the repository entry = %v (present %v), want it trusted", run, e, ok)
		}
		assertNoFile(t, homeJSON+".lock")
		assertNoFile(t, f.claudeJSON)
	}
	check("create", f.mustCreate(t, on))
	check("apply", f.mustApply(t, on))
}

// TestClaudeTrust_ReviewRecordKept checks a review-sourced record is the
// review command's: an apply with the option on or off keeps it as it is and
// doesn't rewrite the file, for the instance root and a repository alike.
func TestClaudeTrust_ReviewRecordKept(t *testing.T) {
	for _, body := range []string{trustOn, trustOff} {
		f := newTrustFixture(t)
		f.mustCreate(t, f.writeConfig(t, ""))
		key := trustKey(t, f.instanceRoot)
		repoKey := trustKey(t, f.repoDir())
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
			key:     map[string]any{"hasTrustDialogAccepted": true},
			repoKey: map[string]any{"allowedTools": []any{"Read"}},
		}})
		review := &claudetrust.Grant{ConfigFile: f.claudeJSON, Key: key, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceReview}
		recordClaudeTrust(t, f.instanceRoot, review)
		// A review record whose flag is gone: Ensure would set it again if
		// the apply acted on it.
		repoReview := map[string]*claudetrust.Grant{appRepo: {
			ConfigFile: f.claudeJSON, Key: repoKey, Mode: claudetrust.ModeFlagOnly, Source: claudetrust.SourceReview,
		}}
		recordRepoClaudeTrust(t, f.instanceRoot, repoReview)
		before := snapFile(t, f.claudeJSON)

		f.mustApply(t, f.writeConfig(t, body))
		assertFileUntouched(t, f.claudeJSON, before)
		if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, review) {
			t.Errorf("record = %+v, want the review grant kept", g)
		}
		if gs := repoGrantsOf(t, f.instanceRoot); !reflect.DeepEqual(gs, repoReview) {
			t.Errorf("repository records = %+v, want the review grant kept", gs)
		}
	}
}

// TestClaudeTrust_RelabeledReviewGrantSurvivesOffSwitch covers a review
// instance created with the option on: its workspace grant is re-labeled
// review when the review seeds trust, and an apply with the option off then
// leaves the entry trusted and the record as it is. The review relabels only
// the root's grant, so the repository's workspace grant is retracted as
// usual.
func TestClaudeTrust_RelabeledReviewGrantSurvivesOffSwitch(t *testing.T) {
	f := newTrustFixture(t)
	f.mustCreate(t, f.writeConfig(t, trustOn))
	created := grantOf(t, f.instanceRoot)
	if created == nil || created.Source != claudetrust.SourceWorkspace {
		t.Fatalf("record after create = %+v, want a workspace grant", created)
	}
	review := *created
	review.Source = claudetrust.SourceReview
	recordClaudeTrust(t, f.instanceRoot, &review)
	rootEntry, _ := entryAt(t, f.claudeJSON, review.Key)

	f.mustApply(t, f.writeConfig(t, trustOff))
	if g := grantOf(t, f.instanceRoot); g == nil || *g != review {
		t.Errorf("record = %+v, want the review grant kept", g)
	}
	if e, ok := entryAt(t, f.claudeJSON, review.Key); !ok || !reflect.DeepEqual(e, rootEntry) {
		t.Errorf("entry = %v (present %v), want it still trusted as %v", e, ok, rootEntry)
	}
	if e, _ := entryAt(t, f.claudeJSON, trustKey(t, f.repoDir())); e["hasTrustDialogAccepted"] != nil {
		t.Errorf("repository entry = %v, want its flag retracted", e)
	}
	if gs := repoGrantsOf(t, f.instanceRoot); gs != nil {
		t.Errorf("repository records = %+v, want them cleared", gs)
	}
}

// TestClaudeTrust_SkippedWritesDontFail covers a config file niwa can't parse
// and a lock held by another process: create and apply succeed with a warning
// naming the file and leave it byte-identical; create records nothing, and a
// skipped withdrawal keeps the record for the next apply.
func TestClaudeTrust_SkippedWritesDontFail(t *testing.T) {
	blockers := map[string]func(t *testing.T, f *trustFixture){
		"unparseable": func(t *testing.T, f *trustFixture) {
			if err := os.WriteFile(f.claudeJSON, []byte(`{"projects": {not json`), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"lock held": func(t *testing.T, f *trustFixture) {
			t.Cleanup(claudetrust.SetLockWaitForTest(100 * time.Millisecond))
			if _, err := os.Stat(f.claudeJSON); err != nil {
				writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{}})
			}
			lock := f.claudeJSON + ".lock"
			if err := os.Mkdir(lock, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(lock) })
		},
	}
	for name, block := range blockers {
		t.Run(name+"/create", func(t *testing.T) {
			f := newTrustFixture(t)
			block(t, f)
			before := snapFile(t, f.claudeJSON)
			out, err := f.createNamed(t, f.writeConfig(t, trustOn), "posture-ws")
			if err != nil {
				t.Fatalf("Create: %v\n%s", err, out)
			}
			if !strings.Contains(out, "warning:") || !strings.Contains(out, f.claudeJSON) {
				t.Errorf("no warning naming %s:\n%s", f.claudeJSON, out)
			}
			assertFileUntouched(t, f.claudeJSON, before)
			if g := grantOf(t, f.instanceRoot); g != nil {
				t.Errorf("record = %+v, want none after a skipped write", g)
			}
		})
		t.Run(name+"/apply off", func(t *testing.T) {
			f := newTrustFixture(t)
			f.mustCreate(t, f.writeConfig(t, trustOn))
			recorded := grantOf(t, f.instanceRoot)
			if recorded == nil {
				t.Fatal("no grant recorded at create")
			}
			block(t, f)
			before := snapFile(t, f.claudeJSON)
			out, err := f.applyAt(t, f.writeConfig(t, trustOff), f.instanceRoot)
			if err != nil {
				t.Fatalf("Apply: %v\n%s", err, out)
			}
			if !strings.Contains(out, "warning:") || !strings.Contains(out, f.claudeJSON) {
				t.Errorf("no warning naming %s:\n%s", f.claudeJSON, out)
			}
			assertFileUntouched(t, f.claudeJSON, before)
			if g := grantOf(t, f.instanceRoot); !reflect.DeepEqual(g, recorded) {
				t.Errorf("record = %+v, want it kept as %+v", g, recorded)
			}
		})
	}
}

// TestClaudeTrust_StaysInsideItsBoundary runs create, apply and destroy with
// the option on and checks niwa wrote no Claude Code setting anywhere under
// HOME and touched no projects key but those of the instance root and its
// repository.
func TestClaudeTrust_StaysInsideItsBoundary(t *testing.T) {
	f := newTrustFixture(t)
	settings := filepath.Join(f.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"permissions": {"allow": ["Read"]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	others := map[string]any{
		"/work/a": map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []any{"Read"}},
		"/work/b": map[string]any{"hasTrustDialogAccepted": false},
	}
	writeClaudeConfig(t, f.claudeJSON, map[string]any{"numStartups": 9, "projects": others})
	settingsBefore := snapFile(t, settings)

	on := f.writeConfig(t, trustOn+"\n[mcp.servers.docs]\nurl = \"https://docs.example.com\"\n")
	f.mustCreate(t, on)
	f.mustApply(t, on)
	key := trustKey(t, f.instanceRoot)
	repoKey := trustKey(t, f.repoDir())

	projects := projectsOf(t, readClaudeConfig(t, f.claudeJSON))
	for k, v := range others {
		if !reflect.DeepEqual(projects[k], v) {
			t.Errorf("projects[%s] = %v, want %v", k, projects[k], v)
		}
	}
	if len(projects) != len(others)+2 || projects[key] == nil || projects[repoKey] == nil {
		t.Errorf("projects = %v, want only %s and %s added", projects, key, repoKey)
	}

	if err := DestroyInstance(f.instanceRoot, WithDestroyReporter(f.applier.Reporter), WithPluginRecordBaseDir(f.home)); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	doc := readClaudeConfig(t, f.claudeJSON)
	if want := map[string]any{"numStartups": float64(9), "projects": others}; !reflect.DeepEqual(doc, want) {
		t.Errorf("after destroy the Claude config = %v, want %v", doc, want)
	}

	assertFileUntouched(t, settings, settingsBefore)
	for _, root := range []string{f.home, os.Getenv("HOME")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, rerr := os.ReadFile(path)
			if rerr == nil && bytes.Contains(data, []byte("enableAllProjectMcpServers")) {
				t.Errorf("%s contains enableAllProjectMcpServers", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
