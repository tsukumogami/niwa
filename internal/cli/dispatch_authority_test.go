package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// r8aExample is the PRD's example workspace policy: a supervised workspace
// whose ceiling holds every authority and whose coordinator role names them
// all.
const r8aExample = `[authority]
level = "supervised"
ceiling = ["merge-pr", "close-issue", "cut-release"]

[authority.roles.coordinator]
authorities = ["merge-pr", "close-issue", "cut-release"]
`

// authorityWorkspace is a dispatch workspace whose workspace.toml carries
// body after the [workspace] table, with the machine config set to host and
// the dispatch fakes installed. It runs detached, from the workspace root.
func authorityWorkspace(t *testing.T, body, host string) (string, *dispatchFakes) {
	t.Helper()
	root := setupDispatchWorkspace(t)
	writeWorkspaceConfig(t, root, body)
	chdir(t, root)
	setHostConfig(t, host)
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	f := installDispatchFakes(t, root)
	dispatchDetach = true
	return root, f
}

func writeWorkspaceConfig(t *testing.T, root, body string) {
	t.Helper()
	path := filepath.Join(root, config.ConfigDir, config.ConfigFile)
	if err := os.WriteFile(path, []byte("[workspace]\nname = \"test-ws\"\n\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readRecord reads the record the last dispatch wrote, failing when there is
// none.
func readRecord(t *testing.T, f *dispatchFakes) authority.Record {
	t.Helper()
	rec, found, err := workspace.ReadAuthorityRecord(f.instancePath)
	if err != nil || !found {
		t.Fatalf("authority record in %s: found %v, err %v", f.instancePath, found, err)
	}
	return rec
}

func recordBytes(t *testing.T, f *dispatchFakes) []byte {
	t.Helper()
	data, err := os.ReadFile(workspace.AuthorityRecordPath(f.instancePath))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

var dispatchIDField = regexp.MustCompile(`"dispatch_id": "[0-9a-f-]{36}"`)

// withoutDispatchID blanks the one field that differs between two otherwise
// identical dispatches.
func withoutDispatchID(b []byte) string {
	return dispatchIDField.ReplaceAllString(string(b), `"dispatch_id": "<id>"`)
}

const authorityLinePrefix = "niwa dispatch: authority level"

// refusalCase is one dispatch that must be refused at (2e).
type refusalCase struct {
	name  string
	body  string // workspace.toml after [workspace]
	host  string // machine config
	setup func(t *testing.T, root string)
	flags func()
	// exact, when set, is the whole error text; otherwise contains must all
	// appear in it.
	exact    string
	contains []string
}

func refusalCases() []refusalCase {
	return []refusalCase{
		{
			name:  "level wider than the workspace ceiling",
			body:  "[authority]\nlevel = \"strict\"\n",
			flags: func() { dispatchLevel = "supervised" },
			exact: "niwa: error: --level supervised is wider than the authority level ceiling strict from workspace",
		},
		{
			name:  "level wider than the machine ceiling",
			body:  "[authority]\nlevel = \"autonomous\"\n",
			host:  "[global]\nauthority_level = \"strict\"\n",
			flags: func() { dispatchLevel = "supervised" },
			exact: "niwa: error: --level supervised is wider than the authority level ceiling strict from machine",
		},
		{
			name:  "grant outside the ceiling",
			body:  "[authority]\nceiling = [\"merge-pr\"]\n",
			flags: func() { dispatchGrants = []string{"close-issue"} },
			exact: "niwa: error: authority close-issue (requested by --grant) is outside the grant ceiling: the grant ceiling is merge-pr from workspace",
		},
		{
			name:  "role outside the ceiling, one line per offender",
			body:  "[authority]\nceiling = [\"merge-pr\"]\n\n[authority.roles.coordinator]\nauthorities = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n",
			flags: func() { dispatchRoles = []string{"coordinator"} },
			exact: "niwa: error: authority close-issue (requested by role coordinator) is outside the grant ceiling: the grant ceiling is merge-pr from workspace\n" +
				"niwa: error: authority cut-release (requested by role coordinator) is outside the grant ceiling: the grant ceiling is merge-pr from workspace",
		},
		{
			name:  "grant with no ceiling declared anywhere",
			flags: func() { dispatchGrants = []string{"merge-pr"} },
			exact: "niwa: error: authority merge-pr (requested by --grant) is outside the grant ceiling: the grant ceiling is empty because no layer declares one; declare it in the workspace config's [authority] ceiling",
		},
		{
			name:  "unknown authority on --grant",
			body:  r8aExample,
			flags: func() { dispatchGrants = []string{"teardown"} },
			exact: `niwa: error: invalid --grant "teardown"; accepted values are merge-pr, close-issue, cut-release`,
		},
		{
			name:  "unknown role",
			body:  r8aExample,
			flags: func() { dispatchRoles = []string{"reviewer"} },
			exact: `niwa: error: invalid --role "reviewer"; accepted values are coordinator`,
		},
		{
			name:  "level with a control byte prints escaped",
			flags: func() { dispatchLevel = "strict\x1b[2J" },
			exact: `niwa: error: invalid --level "strict\x1b[2J"; accepted values are autonomous, supervised, strict`,
		},
		{
			name:  "explicit empty --level",
			body:  "[authority]\nlevel = \"autonomous\"\n",
			flags: func() { dispatchLevel, dispatchLevelSet = "", true },
			exact: "niwa: error: --level is empty; accepted values are autonomous, supervised, strict. Leave out --level to take the level ceiling",
		},
		{
			name:  "whitespace-only --level",
			body:  "[authority]\nlevel = \"autonomous\"\n",
			flags: func() { dispatchLevel, dispatchLevelSet = "  ", true },
			exact: "niwa: error: --level is empty; accepted values are autonomous, supervised, strict. Leave out --level to take the level ceiling",
		},
		{
			name:     "invalid machine authority_level",
			host:     "[global]\nauthority_level = \"godmode\"\n",
			contains: []string{`niwa: error: invalid [global] authority_level "godmode"; accepted values are autonomous, supervised, strict`, "config.toml"},
		},
		{
			name:     "invalid machine authority_ceiling entry",
			host:     "[global]\nauthority_ceiling = [\"Merge PR\"]\n",
			contains: []string{`niwa: error: invalid [global] authority_ceiling "Merge PR"`, "config.toml"},
		},
		{
			name: "workspace config that doesn't parse",
			setup: func(t *testing.T, root string) {
				path := filepath.Join(root, config.ConfigDir, config.ConfigFile)
				if err := os.WriteFile(path, []byte("[workspace]\nname = \"test-ws\"\nnot toml\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			contains: []string{"niwa: error: loading ", config.ConfigFile, "won't fall back to a default"},
		},
		{
			name: "recorded overlay whose clone is missing",
			setup: func(t *testing.T, root string) {
				if err := workspace.SaveState(root, &workspace.InstanceState{SchemaVersion: workspace.SchemaVersion, OverlayURL: "file:///nowhere/ws-overlay.git"}); err != nil {
					t.Fatal(err)
				}
			},
			contains: []string{"niwa: error: ", "has no local clone", "run niwa apply"},
		},
		{
			name:  "trailing empty --grant entry",
			body:  r8aExample,
			flags: func() { dispatchGrants = []string{"merge-pr,"} },
			exact: `niwa: error: --grant: empty entry in "merge-pr,"`,
		},
		{
			name:  "leading empty --role entry",
			body:  r8aExample,
			flags: func() { dispatchRoles = []string{",coordinator"} },
			exact: `niwa: error: --role: empty entry in ",coordinator"`,
		},
		{
			name: "request whose record would pass the reader's cap",
			body: manyRolesConfig(300),
			flags: func() {
				dispatchRoles = []string{strings.Join(manyRoleNames(300), ",")}
			},
			contains: []string{"niwa: error: the authority record for this request would be", "over the 65536-byte limit"},
		},
		{
			name:     "empty --parent-from",
			flags:    func() { dispatchParentFrom, dispatchParentFromSet = "", true },
			contains: []string{"niwa: error: --parent-from is empty"},
		},
		{
			name:     "relative --parent-from",
			flags:    func() { dispatchParentFrom, dispatchParentFromSet = "some/dir", true },
			contains: []string{`niwa: error: --parent-from "some/dir" is not an absolute path`},
		},
		{
			name:     "nonexistent --parent-from",
			flags:    func() { dispatchParentFrom, dispatchParentFromSet = "/nonexistent/niwa-test-dir", true },
			contains: []string{`niwa: error: --parent-from "/nonexistent/niwa-test-dir" does not exist`},
		},
	}
}

// manyRoleNames returns n distinct role names of the longest allowed length.
func manyRoleNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("role-%s-%05d", strings.Repeat("x", 52), i)
	}
	return out
}

func manyRolesConfig(n int) string {
	var b strings.Builder
	b.WriteString("[authority]\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n")
	for _, name := range manyRoleNames(n) {
		b.WriteString("\n[authority.roles." + name + "]\nauthorities = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n")
	}
	return b.String()
}

// TestDispatchAuthority_RefusalsLeaveNothingBehind runs every (2e) refusal
// through the interactive path, so the test also sees whether the prompt was
// asked for, and checks the whole of R16: exit with the refusal, no
// resolution line, no prompt, nothing provisioned, no record, no mapping, no
// launch.
func TestDispatchAuthority_RefusalsLeaveNothingBehind(t *testing.T) {
	for _, tc := range refusalCases() {
		t.Run(tc.name, func(t *testing.T) {
			root, f := authorityWorkspace(t, tc.body, tc.host)
			if tc.setup != nil {
				tc.setup(t, root)
			}
			if tc.flags != nil {
				tc.flags()
			}
			stubCaptureTTY(t, true, true)
			prompts := 0
			stubCapture(t, func() (string, error) { prompts++; return "do a thing", nil })
			var preflighted []string
			recordPreflightedBinary(t, &preflighted)
			before := rootEntries(t, root)

			cmd, _, errBuf := dispatchCmdWithBuffers()
			err := runDispatch(cmd, nil)

			if err == nil {
				t.Fatal("the dispatch was not refused")
			}
			if tc.exact != "" && err.Error() != tc.exact {
				t.Errorf("refusal =\n%s\nwant\n%s", err, tc.exact)
			}
			for _, want := range tc.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not contain %q", err, want)
				}
			}
			if strings.Contains(errBuf.String(), authorityLinePrefix) {
				t.Errorf("a refused dispatch printed its resolution line: %s", errBuf.String())
			}
			if prompts != 0 || len(preflighted) != 0 {
				t.Errorf("prompted %d times and preflighted %v before refusing", prompts, preflighted)
			}
			if f.provisionCalled != 0 || f.launchCalled != 0 || f.captureCalled != 0 {
				t.Errorf("provisioned %d, launched %d, captured %d times", f.provisionCalled, f.launchCalled, f.captureCalled)
			}
			if after := rootEntries(t, root); !reflect.DeepEqual(before, after) {
				t.Errorf("the workspace root changed: before %v, after %v", before, after)
			}
			if files := sessionMappingFiles(t, root); len(files) != 0 {
				t.Errorf("session mappings written: %v", files)
			}
		})
	}
}

// rootEntries lists the workspace root and its .niwa directory, which is
// where a new instance, a session mapping or a sweep's removal would show.
func rootEntries(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{root, filepath.Join(root, workspace.StateDir)} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// TestDispatchAuthority_MissingWorkspaceConfigRefuses covers the config
// vanishing between the workspace lookup and the load: (2e) refuses naming
// the file rather than resolving against nothing.
func TestDispatchAuthority_MissingWorkspaceConfigRefuses(t *testing.T) {
	root := setupDispatchWorkspace(t)
	resetDispatchFlags(t)
	path := filepath.Join(root, config.ConfigDir, config.ConfigFile)
	_, err := resolveDispatchAuthority(&bytes.Buffer{}, root, root, path, "", nil, os.ErrNotExist, nil)
	if err == nil || !strings.Contains(err.Error(), "loading "+path) {
		t.Fatalf("err = %v; want a refusal naming %s", err, path)
	}
}

// installedInstance creates an instance directory under root, optionally with
// an authority record body, and returns its path.
func installedInstance(t *testing.T, root, name, record string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := writeMinimalInstanceState(dir); err != nil {
		t.Fatal(err)
	}
	if record != "" {
		if err := os.WriteFile(workspace.AuthorityRecordPath(dir), []byte(record), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const parentDispatchID = "7f1d2c3b-9a8e-4d6c-b5a4-0e1f2a3b4c5d"

// parentRecord is a valid v1 record for the dispatching instance.
func parentRecord(t *testing.T) string {
	t.Helper()
	rec, err := authority.Resolve(authority.Policy{}, authority.Request{})
	if err != nil {
		t.Fatal(err)
	}
	rec.DispatchID = parentDispatchID
	b, err := rec.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDispatchAuthority_ParentLink(t *testing.T) {
	v2 := `{"schema": "niwa.authority.record/v2", "dispatch_id": "` + parentDispatchID + `"}`
	cases := []struct {
		name       string
		record     string // the dispatching instance's record; "" for none
		fromInside bool   // run from inside the dispatching instance
		anchor     bool   // pass --parent-from naming it
		wantID     string
		wantSource string
	}{
		{"cwd in a dispatched instance", "valid", true, false, parentDispatchID, "cwd"},
		{"workspace root", "valid", false, false, "", "none"},
		{"workspace root with --parent-from", "valid", false, true, parentDispatchID, "anchor"},
		{"inside with --parent-from naming the same instance", "valid", true, true, parentDispatchID, "anchor"},
		{"--parent-from naming an instance with no record", "", false, true, "", "none"},
		{"cwd in an instance with no record", "", true, false, "", "none"},
		{"cwd in an instance with a newer-version record", v2, true, false, parentDispatchID, "cwd"},
		{"--parent-from an instance with a newer-version record", v2, false, true, parentDispatchID, "anchor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, f := authorityWorkspace(t, "", "")
			body := tc.record
			if body == "valid" {
				body = parentRecord(t)
			}
			parent := installedInstance(t, root, "test-ws-parent", body)
			sub := filepath.Join(parent, "group", "repo")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.fromInside {
				chdir(t, sub)
			}
			if tc.anchor {
				dispatchParentFrom, dispatchParentFromSet = sub, true
			}
			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v\n%s", err, stderr)
			}
			rec := readRecord(t, f)
			if rec.ParentDispatchID != tc.wantID || rec.ParentDispatchSource != tc.wantSource {
				t.Errorf("parent = %q from %q, want %q from %q", rec.ParentDispatchID, rec.ParentDispatchSource, tc.wantID, tc.wantSource)
			}
			if tc.record == v2 && (len(rec.Grant) != 0 || rec.Level != authority.Autonomous) {
				t.Errorf("the child took something from a newer-version parent: %+v", rec)
			}
		})
	}
}

// TestDispatchAuthority_KeepsTheWholeParentRecord: (2e) keeps the
// dispatcher's whole record, not just its id, so a never-wider check at the
// same step needs no second read.
func TestDispatchAuthority_KeepsTheWholeParentRecord(t *testing.T) {
	root, _ := authorityWorkspace(t, "", "")
	parent := installedInstance(t, root, "test-ws-parent", parentRecord(t))
	rec, source, _, _, err := dispatchParent(parent, root)
	if err != nil || rec == nil {
		t.Fatalf("dispatchParent = %v, %v", rec, err)
	}
	if rec.DispatchID != parentDispatchID || source != authority.ParentSourceCwd || rec.GrantCeilingSource != authority.LayerNone {
		t.Errorf("kept %+v from %s", rec, source)
	}
}

// TestDispatchAuthority_CodexParentFromWorkingDirectory: a Codex worker
// inherits its dispatcher's session id variable, so the parent dispatch must
// come from the working directory's instance, not from that variable.
func TestDispatchAuthority_CodexParentFromWorkingDirectory(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	parent := installedInstance(t, root, "test-ws-parent", parentRecord(t))
	chdir(t, parent)
	dispatchHarness = "codex"
	t.Setenv("CODEX_THREAD_ID", "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb")
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	rec := readRecord(t, f)
	if rec.ParentDispatchID != parentDispatchID || rec.ParentDispatchSource != authority.ParentSourceCwd {
		t.Errorf("parent = %q from %q, want the working directory's instance", rec.ParentDispatchID, rec.ParentDispatchSource)
	}
}

func TestDispatchAuthority_ParentRefusals(t *testing.T) {
	malformed := `{"schema": "niwa.authority.record/v1", "dispatch_id": "` + parentDispatchID + `"}`
	v2NoUUID := `{"schema": "niwa.authority.record/v2", "dispatch_id": "not-a-uuid"}`
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		want  string
	}{
		{"malformed record in the working directory's instance", func(t *testing.T, root string) {
			chdir(t, installedInstance(t, root, "test-ws-parent", malformed))
		}, "has an authority record that can't be used: authority record is malformed"},
		{"newer-version record without a UUID", func(t *testing.T, root string) {
			chdir(t, installedInstance(t, root, "test-ws-parent", v2NoUUID))
		}, "authority record is malformed"},
		{"malformed record named by --parent-from", func(t *testing.T, root string) {
			dispatchParentFrom, dispatchParentFromSet = installedInstance(t, root, "test-ws-parent", malformed), true
		}, "authority record is malformed"},
		{"--parent-from a directory in no instance", func(t *testing.T, root string) {
			dir := filepath.Join(root, "loose")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			dispatchParentFrom, dispatchParentFromSet = dir, true
		}, "is not inside an instance of this workspace"},
		{"--parent-from an instance of another workspace", func(t *testing.T, _ string) {
			other := setupDispatchWorkspace(t)
			dispatchParentFrom, dispatchParentFromSet = installedInstance(t, other, "other-inst", parentRecord(t)), true
		}, "not this dispatch's workspace"},
		{"--parent-from another instance than the working directory's", func(t *testing.T, root string) {
			chdir(t, installedInstance(t, root, "test-ws-x", parentRecord(t)))
			dispatchParentFrom, dispatchParentFromSet = installedInstance(t, root, "test-ws-y", parentRecord(t)), true
		}, "but this dispatch runs inside instance"},
		{"--parent-from another instance than the working directory's, which has no record", func(t *testing.T, root string) {
			chdir(t, installedInstance(t, root, "test-ws-x", ""))
			dispatchParentFrom, dispatchParentFromSet = installedInstance(t, root, "test-ws-y", parentRecord(t)), true
		}, "but this dispatch runs inside instance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, f := authorityWorkspace(t, "", "")
			tc.setup(t, root)
			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to contain %q", err, tc.want)
			}
			if f.provisionCalled != 0 || strings.Contains(stderr, authorityLinePrefix) {
				t.Errorf("provisioned %d times / printed the line before refusing", f.provisionCalled)
			}
		})
	}
}

// TestDispatchAuthority_ParentFromThroughSymlinkedWorkspace: a workspace
// reached through a symlinked parent directory still accepts an anchor in it,
// because both roots are compared resolved.
func TestDispatchAuthority_ParentFromThroughSymlinkedWorkspace(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	parent := installedInstance(t, root, "test-ws-parent", parentRecord(t))
	link := filepath.Join(canonicalTempDir(t), "ws-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	chdir(t, link)
	dispatchParentFrom, dispatchParentFromSet = filepath.Join(link, filepath.Base(parent)), true
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if rec := readRecord(t, f); rec.ParentDispatchID != parentDispatchID || rec.ParentDispatchSource != "anchor" {
		t.Errorf("parent = %q from %q", rec.ParentDispatchID, rec.ParentDispatchSource)
	}
}

func TestDispatchAuthority_NoSettingsLineAndRecord(t *testing.T) {
	_, f := authorityWorkspace(t, "", "")
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "niwa dispatch: authority level autonomous from built-in; grant none\n") {
		t.Errorf("stderr lacks the no-settings line:\n%s", stderr)
	}
	if n := strings.Count(stderr, authorityLinePrefix); n != 1 {
		t.Errorf("printed the resolution line %d times", n)
	}
	rec := readRecord(t, f)
	if rec.GrantCeilingSource != authority.LayerNone || len(rec.Grant) != 0 {
		t.Errorf("record = %+v; want an empty grant with grant_ceiling_source none", rec)
	}
	if !strings.Contains(string(recordBytes(t, f)), `"grant_ceiling_source": "none"`) {
		t.Errorf("stored record lacks grant_ceiling_source none:\n%s", recordBytes(t, f))
	}
	info, err := os.Stat(workspace.AuthorityRecordPath(f.instancePath))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("record mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
}

func TestDispatchAuthority_DeclaredEmptyCeilingRecordsWorkspace(t *testing.T) {
	_, f := authorityWorkspace(t, "[authority]\nceiling = []\n", "")
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if !strings.Contains(string(recordBytes(t, f)), `"grant_ceiling_source": "workspace"`) {
		t.Errorf("stored record:\n%s", recordBytes(t, f))
	}
}

func TestDispatchAuthority_LineBeforeTheSweepAndAfterTheID(t *testing.T) {
	_, _ = authorityWorkspace(t, r8aExample, "")
	dispatchRoles = []string{"coordinator"}
	dispatchGrants = []string{"close-issue"}
	cmd, _, errBuf := dispatchCmdWithBuffers()
	var atProvision string
	prev := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, p, sep string, n int) (provisionResult, error) {
		atProvision = errBuf.String()
		return prev(ctx, r, cwd, p, sep, n)
	}
	if err := runDispatch(cmd, []string{"do a thing"}); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, errBuf)
	}
	want := "niwa dispatch: authority level supervised from workspace; grant merge-pr (role coordinator), close-issue (dispatch, role coordinator), cut-release (role coordinator)\n"
	if !strings.Contains(atProvision, want) {
		t.Errorf("the line was not on stderr when provisioning began.\nwant %q\nstderr then: %q", want, atProvision)
	}
}

func TestDispatchAuthority_NoLineWhenPreflightOrCaptureFails(t *testing.T) {
	t.Run("binary preflight", func(t *testing.T) {
		_, _ = authorityWorkspace(t, "", "")
		lookAgentBinary = func(string) (string, error) { return "", errors.New("not found") }
		_, stderr, err := runDispatchCmd(t, "do a thing")
		if err == nil || strings.Contains(stderr, authorityLinePrefix) {
			t.Fatalf("err %v, stderr %q", err, stderr)
		}
	})
	t.Run("prompt capture", func(t *testing.T) {
		_, _ = authorityWorkspace(t, "", "")
		stubCaptureTTY(t, true, true)
		stubCapture(t, func() (string, error) { return "", errors.New("terminal went away") })
		cmd, _, errBuf := dispatchCmdWithBuffers()
		if err := runDispatch(cmd, nil); err == nil || strings.Contains(errBuf.String(), authorityLinePrefix) {
			t.Fatalf("err %v, stderr %q", err, errBuf)
		}
	})
}

func TestDispatchAuthority_LevelResolution(t *testing.T) {
	cases := []struct {
		name       string
		body, host string
		level      string
		want       authority.Level
		wantSource authority.Layer
	}{
		{"workspace strict, no flag", "[authority]\nlevel = \"strict\"\n", "", "", authority.Strict, authority.LayerWorkspace},
		{"workspace autonomous, --level supervised", "[authority]\nlevel = \"autonomous\"\n", "", "supervised", authority.Supervised, authority.LayerFlag},
		{"workspace autonomous, no flag", "[authority]\nlevel = \"autonomous\"\n", "", "", authority.Autonomous, authority.LayerWorkspace},
		{"workspace supervised, --level supervised ties to the flag", "[authority]\nlevel = \"supervised\"\n", "", "supervised", authority.Supervised, authority.LayerFlag},
		{"machine strict over workspace autonomous", "[authority]\nlevel = \"autonomous\"\n", "[global]\nauthority_level = \"strict\"\n", "", authority.Strict, authority.LayerMachine},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, f := authorityWorkspace(t, tc.body, tc.host)
			dispatchLevel = tc.level
			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v\n%s", err, stderr)
			}
			rec := readRecord(t, f)
			if rec.Level != tc.want || rec.LevelSource != tc.wantSource {
				t.Errorf("level = %s from %s, want %s from %s", rec.Level, rec.LevelSource, tc.want, tc.wantSource)
			}
		})
	}
}

func TestDispatchAuthority_GrantScenarios(t *testing.T) {
	t.Run("role coordinator", func(t *testing.T) {
		_, f := authorityWorkspace(t, r8aExample, "")
		dispatchRoles = []string{"coordinator"}
		if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v\n%s", err, stderr)
		}
		want := []authority.GrantEntry{
			{Authority: authority.MergePR, Sources: []authority.Source{"role:coordinator"}},
			{Authority: authority.CloseIssue, Sources: []authority.Source{"role:coordinator"}},
			{Authority: authority.CutRelease, Sources: []authority.Source{"role:coordinator"}},
		}
		if rec := readRecord(t, f); !reflect.DeepEqual(rec.Grant, want) {
			t.Errorf("grant = %+v", rec.Grant)
		}
	})
	t.Run("grant and role name one authority once", func(t *testing.T) {
		_, f := authorityWorkspace(t, r8aExample, "")
		dispatchRoles = []string{"coordinator"}
		dispatchGrants = []string{"close-issue"}
		if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v\n%s", err, stderr)
		}
		rec := readRecord(t, f)
		if len(rec.Grant) != 3 || !reflect.DeepEqual(rec.Grant[1].Sources, []authority.Source{"dispatch", "role:coordinator"}) {
			t.Errorf("grant = %+v", rec.Grant)
		}
	})
	t.Run("comma list equals repeated flags", func(t *testing.T) {
		record := func(grants []string) string {
			_, f := authorityWorkspace(t, r8aExample, "")
			dispatchGrants = grants
			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v\n%s", err, stderr)
			}
			return withoutDispatchID(recordBytes(t, f))
		}
		comma := record([]string{"merge-pr,close-issue"})
		repeated := record([]string{"merge-pr", "close-issue"})
		if comma != repeated {
			t.Errorf("records differ:\n%s\n---\n%s", comma, repeated)
		}
	})
}

// conventionOverlay registers the workspace under name with a file:// source
// and plants that source's convention overlay clone holding body, so the
// overlay is found the way provisioning finds it. The machine config is
// rewritten to hold the registry entry plus extraHost.
func conventionOverlay(t *testing.T, name, body, extraHost string) {
	t.Helper()
	registerSource(t, name, extraHost)
	plantConventionOverlay(t, name, body)
}

// registerSource rewrites the machine config to register the workspace under
// name with a file:// source, plus extraHost.
func registerSource(t *testing.T, name, extraHost string) {
	t.Helper()
	setHostConfig(t, extraHost+"\n[registry."+name+"]\nsource_url = \"file:///srv/configs/"+name+".git\"\n")
}

// plantConventionOverlay writes the convention overlay clone of the source
// registerSource registered for name.
func plantConventionOverlay(t *testing.T, name, body string) {
	t.Helper()
	source := "file:///srv/configs/" + name + ".git"
	url, ok := config.DeriveOverlayURL(source)
	if !ok {
		t.Fatalf("no convention overlay for %s", source)
	}
	dir, err := config.OverlayDir(url)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDispatchAuthority_WholeRecord compares the stored file with the whole
// expected document for a request mixing --grant, --role and an
// overlay-narrowed ceiling.
func TestDispatchAuthority_WholeRecord(t *testing.T) {
	_, f := authorityWorkspace(t, r8aExample+"\n[authority.roles.reviewer]\nauthorities = [\"merge-pr\"]\n", "")
	conventionOverlay(t, "test-ws", "[authority]\nceiling = [\"merge-pr\", \"close-issue\"]\n", "")
	dispatchGrants = []string{"close-issue"}
	dispatchRoles = []string{"reviewer"}
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	want := `{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "<id>",
  "parent_session_id": "",
  "parent_dispatch_id": "",
  "parent_dispatch_source": "none",
  "parent_session_source": "none",
  "principal": {
    "name": "",
    "source": "none",
    "trust": "observed"
  },
  "level": "supervised",
  "level_source": "workspace",
  "level_ceiling": "supervised",
  "level_ceiling_source": "workspace",
  "grant_ceiling": [
    "merge-pr",
    "close-issue"
  ],
  "grant_ceiling_source": "overlay",
  "roles": [
    "reviewer"
  ],
  "grant": [
    {
      "authority": "merge-pr",
      "sources": [
        "role:reviewer"
      ]
    },
    {
      "authority": "close-issue",
      "sources": [
        "dispatch"
      ]
    }
  ],
  "unknown_grant": []
}
`
	if got := withoutDispatchID(recordBytes(t, f)); got != want {
		t.Errorf("record =\n%s\nwant\n%s", got, want)
	}
	if rec := readRecord(t, f); !authority.ValidDispatchID(rec.DispatchID) {
		t.Errorf("dispatch_id %q", rec.DispatchID)
	}
}

// TestDispatchAuthority_RenamedWorkspaceFindsTheSameOverlay: a workspace
// initialized under another name looks its registry entry up by that name,
// in provisioning and at (2e) alike, so both derive the same overlay.
func TestDispatchAuthority_RenamedWorkspaceFindsTheSameOverlay(t *testing.T) {
	root, _ := authorityWorkspace(t, r8aExample, "")
	if err := workspace.SaveState(root, &workspace.InstanceState{SchemaVersion: workspace.SchemaVersion, ConfigNameOverride: "renamed"}); err != nil {
		t.Fatal(err)
	}
	conventionOverlay(t, "renamed", "[authority]\nceiling = [\"merge-pr\"]\n", "[registry.test-ws]\nsource_url = \"file:///srv/configs/other.git\"\n")
	gc, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(root, config.ConfigDir, config.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	got, err := registeredConfigSourceURL(root, cfg.Config, gc)
	if err != nil || got != "file:///srv/configs/renamed.git" {
		t.Fatalf("source URL = %q (%v); want the renamed workspace's entry", got, err)
	}

	dispatchGrants = []string{"close-issue"}
	_, _, err = runDispatchCmd(t, "do a thing")
	if err == nil || !strings.Contains(err.Error(), "the grant ceiling is merge-pr from overlay") {
		t.Fatalf("err = %v; want the renamed workspace's overlay to narrow the ceiling", err)
	}
}

// provisionThen wraps the fake provisioner so mutate runs after the instance
// exists, the way a real provisioning refreshes the config or clones an
// overlay after (2e) resolved.
func provisionThen(t *testing.T, mutate func(root, instance string)) {
	t.Helper()
	prev := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, p, sep string, n int) (provisionResult, error) {
		res, err := prev(ctx, r, cwd, p, sep, n)
		if err == nil {
			mutate(r, res.Path)
		}
		return res, err
	}
}

func TestDispatchAuthority_RecheckAfterProvisioning(t *testing.T) {
	type check struct {
		refused     bool
		wantSetting string // named in the refusal
		ceiling     []authority.Authority
		ceilingSrc  authority.Layer
		level       authority.Level
		levelSrc    authority.Layer
		levelCeil   authority.Layer
	}
	cases := []struct {
		name   string
		body   string
		flags  func()
		mutate func(t *testing.T, root, instance string)
		want   check
	}{
		{
			name:  "a convention overlay that drops a requested authority refuses",
			body:  r8aExample,
			flags: func() { dispatchGrants = []string{"cut-release"} },
			mutate: func(t *testing.T, _, _ string) {
				plantConventionOverlay(t, "test-ws", "[authority]\nceiling = [\"merge-pr\", \"close-issue\"]\n")
			},
			want: check{refused: true, wantSetting: "the overlay [authority] ceiling"},
		},
		{
			name:  "a refreshed base config with a narrower level refuses",
			body:  r8aExample,
			flags: func() {},
			mutate: func(t *testing.T, root, _ string) {
				writeWorkspaceConfig(t, root, strings.Replace(r8aExample, `level = "supervised"`, `level = "strict"`, 1))
			},
			want: check{refused: true, wantSetting: "the workspace config's [authority] level"},
		},
		{
			name:  "a refreshed base config with a wider level refuses",
			body:  "[authority]\nlevel = \"strict\"\n",
			flags: func() {},
			mutate: func(t *testing.T, root, _ string) {
				writeWorkspaceConfig(t, root, "[authority]\nlevel = \"autonomous\"\n")
			},
			want: check{refused: true, wantSetting: "the workspace config's [authority] level"},
		},
		{
			name:  "an overlay that drops an unrequested authority is recorded",
			body:  r8aExample,
			flags: func() { dispatchGrants = []string{"merge-pr"} },
			mutate: func(t *testing.T, _, _ string) {
				plantConventionOverlay(t, "test-ws", "[authority]\nceiling = [\"merge-pr\", \"close-issue\"]\n")
			},
			want: check{ceiling: []authority.Authority{authority.MergePR, authority.CloseIssue}, ceilingSrc: authority.LayerOverlay,
				level: authority.Supervised, levelSrc: authority.LayerWorkspace, levelCeil: authority.LayerWorkspace},
		},
		{
			name:  "an overlay at the same level keeps the printed level source",
			body:  "[authority]\nlevel = \"strict\"\n",
			flags: func() {},
			mutate: func(t *testing.T, _, _ string) {
				plantConventionOverlay(t, "test-ws", "[authority]\nlevel = \"strict\"\n")
			},
			want: check{ceiling: []authority.Authority{}, ceilingSrc: authority.LayerNone,
				level: authority.Strict, levelSrc: authority.LayerWorkspace, levelCeil: authority.LayerOverlay},
		},
		{
			name:  "a ceiling that only widened is recorded with the grant unchanged",
			body:  "[authority]\nceiling = [\"merge-pr\"]\n",
			flags: func() { dispatchGrants = []string{"merge-pr"} },
			mutate: func(t *testing.T, root, _ string) {
				writeWorkspaceConfig(t, root, "[authority]\nceiling = [\"merge-pr\", \"close-issue\"]\n")
			},
			want: check{ceiling: []authority.Authority{authority.MergePR, authority.CloseIssue}, ceilingSrc: authority.LayerWorkspace,
				level: authority.Autonomous, levelSrc: authority.LayerBuiltIn, levelCeil: authority.LayerBuiltIn},
		},
		{
			name:   "an unchanged policy records (2e)'s resolution",
			body:   r8aExample,
			flags:  func() { dispatchRoles = []string{"coordinator"} },
			mutate: func(*testing.T, string, string) {},
			want: check{ceiling: []authority.Authority{authority.MergePR, authority.CloseIssue, authority.CutRelease}, ceilingSrc: authority.LayerWorkspace,
				level: authority.Supervised, levelSrc: authority.LayerWorkspace, levelCeil: authority.LayerWorkspace},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, f := authorityWorkspace(t, tc.body, "")
			// Registered before the dispatch reads the machine config, so an
			// overlay planted during provisioning is the convention one.
			registerSource(t, "test-ws", "")
			tc.flags()
			provisionThen(t, func(root, instance string) { tc.mutate(t, root, instance) })
			_, stderr, err := runDispatchCmd(t, "do a thing")
			if tc.want.refused {
				if err == nil || !strings.Contains(err.Error(), tc.want.wantSetting) {
					t.Fatalf("err = %v; want a refusal naming %q", err, tc.want.wantSetting)
				}
				if !strings.Contains(stderr, authorityLinePrefix) {
					t.Errorf("the line should have printed before the late refusal: %s", stderr)
				}
				if f.destroyCalled != 1 || f.destroyedPath != f.instancePath {
					t.Errorf("rollback destroyed %d times (%s)", f.destroyCalled, f.destroyedPath)
				}
				if f.launchCalled != 0 || len(sessionMappingFiles(t, filepath.Dir(f.instancePath))) != 0 {
					t.Errorf("launched %d times or wrote a mapping", f.launchCalled)
				}
				if _, statErr := os.Stat(workspace.AuthorityRecordPath(f.instancePath)); statErr == nil {
					t.Error("a record was written for a refused dispatch")
				}
				return
			}
			if err != nil {
				t.Fatalf("dispatch: %v\n%s", err, stderr)
			}
			rec := readRecord(t, f)
			if !reflect.DeepEqual(rec.GrantCeiling, tc.want.ceiling) || rec.GrantCeilingSource != tc.want.ceilingSrc {
				t.Errorf("grant ceiling = %v from %s, want %v from %s", rec.GrantCeiling, rec.GrantCeilingSource, tc.want.ceiling, tc.want.ceilingSrc)
			}
			if rec.Level != tc.want.level || rec.LevelSource != tc.want.levelSrc || rec.LevelCeilingSource != tc.want.levelCeil {
				t.Errorf("level %s from %s, ceiling source %s; want %s from %s, ceiling source %s",
					rec.Level, rec.LevelSource, rec.LevelCeilingSource, tc.want.level, tc.want.levelSrc, tc.want.levelCeil)
			}
		})
	}
}

// TestDispatchAuthority_RecheckRefusesAChangedGrant: provisioning brings in a
// config whose role names fewer authorities. The request still resolves at
// the same level and inside the ceiling, but to a smaller grant than the
// line printed, so (8a) refuses on the grant and the instance is rolled back.
func TestDispatchAuthority_RecheckRefusesAChangedGrant(t *testing.T) {
	_, f := authorityWorkspace(t, r8aExample, "")
	registerSource(t, "test-ws", "")
	dispatchRoles = []string{"coordinator"}
	provisionThen(t, func(root, _ string) {
		writeWorkspaceConfig(t, root, strings.Replace(r8aExample,
			`authorities = ["merge-pr", "close-issue", "cut-release"]`, `authorities = ["merge-pr"]`, 1))
	})
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("dispatch succeeded with a grant different from the one printed")
	}
	for _, want := range []string{"the workspace config's [authority.roles] changed", "the grant would now be", "printed above"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v; want it to contain %q", err, want)
		}
	}
	if !strings.Contains(stderr, authorityLinePrefix) {
		t.Errorf("the line should have printed before the late refusal: %s", stderr)
	}
	if f.destroyCalled != 1 || f.destroyedPath != f.instancePath {
		t.Errorf("rollback destroyed %d times (%s)", f.destroyCalled, f.destroyedPath)
	}
	if f.launchCalled != 0 || len(sessionMappingFiles(t, filepath.Dir(f.instancePath))) != 0 {
		t.Errorf("launched %d times or wrote a mapping", f.launchCalled)
	}
	if _, statErr := os.Stat(workspace.AuthorityRecordPath(f.instancePath)); statErr == nil {
		t.Error("a record was written for a refused dispatch")
	}
}

// TestDispatchAuthority_RecheckFallsBackToTheFirstOverlay: with no overlay
// URL in the instance state and no convention clone left, (8a) still uses
// the overlay (2e) resolved against, rather than reading "no overlay".
func TestDispatchAuthority_RecheckFallsBackToTheFirstOverlay(t *testing.T) {
	_, f := authorityWorkspace(t, r8aExample, "")
	conventionOverlay(t, "test-ws", "[authority]\nceiling = [\"merge-pr\"]\n", "")
	dispatchGrants = []string{"merge-pr"}
	provisionThen(t, func(string, string) {
		url, _ := config.DeriveOverlayURL("file:///srv/configs/test-ws.git")
		dir, _ := config.OverlayDir(url)
		_ = os.RemoveAll(dir)
	})
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if rec := readRecord(t, f); rec.GrantCeilingSource != authority.LayerOverlay {
		t.Errorf("grant ceiling source = %s, want overlay", rec.GrantCeilingSource)
	}
}

func TestDispatchAuthority_FailedRecordWriteRollsBack(t *testing.T) {
	_, f := authorityWorkspace(t, "", "")
	prev := writeAuthorityRecordFunc
	writeAuthorityRecordFunc = func(string, authority.Record) error { return errors.New("read-only file system") }
	t.Cleanup(func() { writeAuthorityRecordFunc = prev })
	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil || !strings.Contains(err.Error(), "writing the dispatch's authority record") {
		t.Fatalf("err = %v", err)
	}
	if f.destroyCalled != 1 || f.launchCalled != 0 {
		t.Errorf("destroyed %d, launched %d; want the rollback and no launch", f.destroyCalled, f.launchCalled)
	}
}

// TestDispatchAuthority_ReadOnlyStateDirRollsBack makes the instance's .niwa
// read-only after the pending marker lands, so the real writer fails at
// (8a).
func TestDispatchAuthority_ReadOnlyStateDirRollsBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	_, f := authorityWorkspace(t, "", "")
	prev := writeAuthorityRecordFunc
	writeAuthorityRecordFunc = func(inst string, rec authority.Record) error {
		dir := filepath.Join(inst, workspace.StateDir)
		if err := os.Chmod(dir, 0o500); err != nil {
			return err
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return prev(inst, rec)
	}
	t.Cleanup(func() { writeAuthorityRecordFunc = prev })
	if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
		t.Fatal("dispatch succeeded with a read-only .niwa")
	}
	if f.destroyCalled != 1 || f.launchCalled != 0 {
		t.Errorf("destroyed %d, launched %d", f.destroyCalled, f.launchCalled)
	}
}

func TestDispatchAuthority_PermissionModeIsIndependent(t *testing.T) {
	t.Run("--level strict with bypassPermissions", func(t *testing.T) {
		_, f := authorityWorkspace(t, "", "")
		var got lineageLaunch
		captureLineageLaunch(f, &got)
		dispatchLevel = "strict"
		dispatchPermissionMode = "bypassPermissions"
		if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v\n%s", err, stderr)
		}
		if !strings.Contains(strings.Join(got.pass, " "), "--permission-mode bypassPermissions") {
			t.Errorf("pass-through %v lacks bypass mode", got.pass)
		}
		if rec := readRecord(t, f); rec.Level != authority.Strict {
			t.Errorf("level = %s", rec.Level)
		}
	})
	t.Run("a bypass posture implies no level and no grant", func(t *testing.T) {
		_, f := authorityWorkspace(t, "", "")
		prev := provisionInstanceFunc
		provisionInstanceFunc = func(ctx context.Context, r, cwd, p, sep string, n int) (provisionResult, error) {
			res, err := prev(ctx, r, cwd, p, sep, n)
			if err == nil {
				err = workspace.SaveState(res.Path, &workspace.InstanceState{SchemaVersion: workspace.SchemaVersion, InstanceName: res.Name, Root: res.Path, ClaudePermissions: "bypass"})
			}
			return res, err
		}
		var got lineageLaunch
		captureLineageLaunch(f, &got)
		if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v\n%s", err, stderr)
		}
		if !strings.Contains(strings.Join(got.pass, " "), "--permission-mode bypassPermissions") {
			t.Errorf("the posture no longer derives bypass: %v", got.pass)
		}
		if rec := readRecord(t, f); rec.Level != authority.Autonomous || len(rec.Grant) != 0 {
			t.Errorf("record = %+v", rec)
		}
	})
}

func TestDispatchAuthority_LineageAttributes(t *testing.T) {
	cases := []struct {
		name  string
		flags func()
		level string
		grant string
	}{
		{"two authorities", func() { dispatchGrants = []string{"close-issue", "merge-pr"} }, "supervised", "merge-pr/close-issue"},
		{"three authorities", func() { dispatchRoles = []string{"coordinator"} }, "supervised", "merge-pr/close-issue/cut-release"},
		{"empty grant", func() { dispatchLevel = "strict" }, "strict", "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, f := authorityWorkspace(t, r8aExample, "")
			var got lineageLaunch
			captureLineageLaunch(f, &got)
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "team=x")
			tc.flags()
			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v\n%s", err, stderr)
			}
			for name, value := range map[string]string{"settings document": got.settings, "launch environment": got.env} {
				attrs, _ := attributeMap(t, value)
				if attrs[attrAuthorityLevel] != tc.level || attrs[attrAuthorityGrant] != tc.grant {
					t.Errorf("%s: level %q grant %q, want %q and %q", name, attrs[attrAuthorityLevel], attrs[attrAuthorityGrant], tc.level, tc.grant)
				}
				for _, other := range []string{attrDispatchID, attrBriefID, attrWorkspace, "team"} {
					if attrs[other] == "" {
						t.Errorf("%s dropped %s: %q", name, other, value)
					}
				}
			}
		})
	}
}

// settingsEnvKeys returns the keys of the launch settings document's env
// object in the order the document writes them.
func settingsEnvKeys(t *testing.T, passthrough []string) []string {
	t.Helper()
	for i := 0; i+1 < len(passthrough); i++ {
		if passthrough[i] != "--settings" {
			continue
		}
		var doc struct {
			Env json.RawMessage `json:"env"`
		}
		if err := json.Unmarshal([]byte(passthrough[i+1]), &doc); err != nil {
			t.Fatalf("settings document is not JSON: %v", err)
		}
		dec := json.NewDecoder(bytes.NewReader(doc.Env))
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			t.Fatalf("settings env is not a JSON object")
		}
		var keys []string
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				t.Fatalf("settings env: %v", err)
			}
			keys = append(keys, tok.(string))
			var skip string
			if err := dec.Decode(&skip); err != nil {
				t.Fatalf("settings env %s: %v", tok, err)
			}
		}
		return keys
	}
	t.Fatalf("no settings document in the launch")
	return nil
}

// TestDispatchAuthority_SettingsEnvCarriesAuthorityPassthroughAndReapKey: a
// dispatch with --grant merge-pr in a workspace whose ceiling allows it, and
// a [session.dispatch_env] pass-through name that is set, gives the worker one
// settings env holding all of it together: OTEL_RESOURCE_ATTRIBUTES with the
// lineage attributes, the dispatcher's own attribute and the two authority
// attributes, then the pass-through value, then NIWA_REAP_KEY set to the
// dispatch id. The launch environment carries the same. Neither the reap
// key's value nor the whole environment is ever printed.
func TestDispatchAuthority_SettingsEnvCarriesAuthorityPassthroughAndReapKey(t *testing.T) {
	_, f := authorityWorkspace(t, r8aExample+"\n[session.dispatch_env]\npassthrough = [\"PASS_MARK\"]\n", "")
	var reqs []launchRequest
	captureLaunchRequest(f, &reqs)
	dispatchGrants = []string{"merge-pr"}
	dispatchName = "env-check"
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "team=x")
	t.Setenv("PASS_MARK", "carried-value")
	t.Setenv(config.ReapKeyEnv, "inherited-fake-key")

	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if len(reqs) != 1 {
		t.Fatalf("launches = %d, want 1", len(reqs))
	}
	req := reqs[0]
	dispatchID := readRecord(t, f).DispatchID
	if !validReapKey(dispatchID) {
		t.Fatal("the authority record holds no usable dispatch id")
	}

	env, ok := settingsEnvOf(t, req.Passthrough)
	if !ok {
		t.Fatal("no settings document in the launch")
	}
	wantKeys := []string{"OTEL_RESOURCE_ATTRIBUTES", "PASS_MARK", config.ReapKeyEnv, config.DispatchIDEnv, config.WorkspaceEnv}
	if keys := settingsEnvKeys(t, req.Passthrough); !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("settings env keys = %v, want %v", keys, wantKeys)
	}

	launchOTEL, _ := envValue(t, req.Env, "OTEL_RESOURCE_ATTRIBUTES")
	for where, value := range map[string]string{"settings env": env["OTEL_RESOURCE_ATTRIBUTES"], "launch environment": launchOTEL} {
		attrs, _ := attributeMap(t, value)
		if attrs[attrDispatchID] != dispatchID {
			t.Errorf("%s: %s is not the dispatch id", where, attrDispatchID)
		}
		for _, name := range []string{attrDispatchSlug, attrBriefID, attrWorkspace} {
			if attrs[name] == "" {
				t.Errorf("%s: lineage attribute %s is missing", where, name)
			}
		}
		if attrs["team"] != "x" {
			t.Errorf("%s: the dispatcher's own attribute team=x is missing", where)
		}
		if attrs[attrAuthorityLevel] != "supervised" || attrs[attrAuthorityGrant] != "merge-pr" {
			t.Errorf("%s: level %q grant %q, want supervised and merge-pr", where, attrs[attrAuthorityLevel], attrs[attrAuthorityGrant])
		}
	}

	if env["PASS_MARK"] != "carried-value" {
		t.Errorf("settings PASS_MARK = %q, want carried-value", env["PASS_MARK"])
	}
	if v, ok := envValue(t, req.Env, "PASS_MARK"); !ok || v != "carried-value" {
		t.Errorf("launch PASS_MARK = %q (set %v), want carried-value", v, ok)
	}
	if env[config.ReapKeyEnv] != dispatchID {
		t.Errorf("settings %s is not the dispatch id", config.ReapKeyEnv)
	}
	if v, ok := envValue(t, req.Env, config.ReapKeyEnv); !ok || v != dispatchID {
		t.Errorf("launch %s is not the dispatch id (set %v)", config.ReapKeyEnv, ok)
	}
}

func TestDispatchAuthority_CodexGetsNoAuthorityAttributes(t *testing.T) {
	records := map[string]authority.Record{}
	for _, harness := range []string{"claude", "codex"} {
		_, f := authorityWorkspace(t, r8aExample, "")
		dispatchHarness = harness
		dispatchGrants = []string{"merge-pr"}
		var env []string
		dispatchLaunch = func(_ context.Context, req launchRequest) error {
			f.launchCalled++
			env = req.Env
			return nil
		}
		if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("%s dispatch: %v\n%s", harness, err, stderr)
		}
		if harness == "codex" {
			for _, kv := range append(env, os.Environ()...) {
				if strings.Contains(kv, "niwa.authority.") {
					t.Errorf("a Codex launch environment carries %q", kv)
				}
			}
		}
		rec := readRecord(t, f)
		rec.DispatchID = ""
		records[harness] = rec
	}
	if !reflect.DeepEqual(records["claude"], records["codex"]) {
		t.Errorf("records differ by agent:\nclaude %+v\ncodex  %+v", records["claude"], records["codex"])
	}
}

// stripAuthorityAttributes removes the two authority lineage entries from
// every OTEL_RESOURCE_ATTRIBUTES value in a pass-through, and blanks the
// dispatch id, which differs per dispatch, both in the lineage value and as
// the NIWA_REAP_KEY and NIWA_DISPATCH_ID settings env values.
func stripAuthorityAttributes(pass []string) []string {
	re := regexp.MustCompile(`,?niwa\.authority\.(level|grant)=[^,"]*`)
	id := regexp.MustCompile(`niwa\.dispatch\.id=[0-9a-f-]{36}`)
	envIDs := regexp.MustCompile(`"(` + config.ReapKeyEnv + `|` + config.DispatchIDEnv + `)":"[^"]*"`)
	out := make([]string, len(pass))
	for i, p := range pass {
		p = id.ReplaceAllString(re.ReplaceAllString(p, ""), "niwa.dispatch.id=<id>")
		out[i] = envIDs.ReplaceAllString(p, `"$1":"<id>"`)
	}
	return out
}

// TestDispatchAuthority_LaunchUnchangedByAuthority: the argv and the settings
// document are the same whatever was granted, apart from the two lineage
// attributes (R28 to R30).
func TestDispatchAuthority_LaunchUnchangedByAuthority(t *testing.T) {
	launch := func(flags func()) []string {
		_, f := authorityWorkspace(t, r8aExample, "")
		var got lineageLaunch
		captureLineageLaunch(f, &got)
		flags()
		if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v\n%s", err, stderr)
		}
		return stripAuthorityAttributes(got.pass)
	}
	none := launch(func() {})
	for name, flags := range map[string]func(){
		"--role coordinator": func() { dispatchRoles = []string{"coordinator"} },
		"--level strict":     func() { dispatchLevel = "strict" },
	} {
		if got := launch(flags); !reflect.DeepEqual(got, none) {
			t.Errorf("%s changed the launch:\n%v\nvs\n%v", name, got, none)
		}
	}
}

func TestAuthorityRefusal_PrefixesEveryLine(t *testing.T) {
	err := authorityRefusal{errors.New("one\ntwo")}
	if got := err.Error(); got != "niwa: error: one\nniwa: error: two" {
		t.Errorf("got %q", got)
	}
}
