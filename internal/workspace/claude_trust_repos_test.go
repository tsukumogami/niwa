package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/claudetrust"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// These tests cover Claude trust for the repository directories niwa clones
// into an instance: one grant per repository, recorded beside the instance
// root's, and the approval list each repository's settings document carries
// for the servers the instance root's .mcp.json holds. Like the root's tests,
// every run writes into a temporary developer home.

// libRepo is the second repository newRepoTrustFixture adds, by the
// instance-relative path its grant is recorded under.
const libRepo = "tools/lib"

// libGroupTOML puts libRepo in the workspace's config.
const libGroupTOML = "\n[groups.tools]\nrepos = [\"lib\"]\n"

// libClaudeOff turns Claude off for libRepo.
const libClaudeOff = "\n[repos.lib.claude]\nenabled = false\n"

// docsServerTOML declares one MCP server, which the instance root's .mcp.json
// carries.
const docsServerTOML = "\n[mcp.servers.docs]\nurl = \"https://docs.example.com\"\n"

// newRepoTrustFixture is a trustFixture whose organization also lists lib,
// already "cloned" under tools/ in the default instance. A config includes it
// by adding libGroupTOML.
func newRepoTrustFixture(t *testing.T) *trustFixture {
	t.Helper()
	f := newTrustFixture(t)
	f.applier.GitHubClient = &mockGitHubClient{repos: map[string][]github.Repo{
		"testorg": {
			{Name: "app", SSHURL: "git@github.com:testorg/app.git"},
			{Name: "lib", SSHURL: "git@github.com:testorg/lib.git"},
		},
	}}
	if err := os.MkdirAll(filepath.Join(f.dirOf(libRepo), ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// dirOf returns the directory of the repository recorded as rel in the
// default instance.
func (f *trustFixture) dirOf(rel string) string {
	return filepath.Join(f.instanceRoot, filepath.FromSlash(rel))
}

// repoSettingsPath is the Claude settings document niwa writes into dir.
func repoSettingsPath(dir string) string {
	return filepath.Join(dir, ".claude", "settings.local.json")
}

// repoApprovals returns the approval list in dir's settings document and
// whether the key is there; a document that doesn't exist approves nothing.
func repoApprovals(t *testing.T, dir string) ([]string, bool) {
	t.Helper()
	data, err := os.ReadFile(repoSettingsPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return approvedServersIn(t, data)
}

// readOrNil returns path's bytes, or nil when it doesn't exist.
func readOrNil(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestClaudeTrustRepos_CreateRecordsEachRepo checks create trusts each repo
// root and records one grant per repository by its instance-relative path:
// created where niwa added the entry, flag-only where Claude Code already had
// one. Each repository's settings document approves the declared server.
func TestClaudeTrustRepos_CreateRecordsEachRepo(t *testing.T) {
	f := newRepoTrustFixture(t)
	appKey := trustKey(t, f.dirOf(appRepo))
	libKey := trustKey(t, f.dirOf(libRepo))
	writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
		libKey: map[string]any{"allowedTools": []any{"Read"}},
	}})

	f.mustCreate(t, f.writeConfig(t, trustOn+libGroupTOML+docsServerTOML))

	want := map[string]*claudetrust.Grant{
		appRepo: wantGrant(f.claudeJSON, appKey, claudetrust.ModeCreated),
		libRepo: wantGrant(f.claudeJSON, libKey, claudetrust.ModeFlagOnly),
	}
	if gs := repoGrantsOf(t, f.instanceRoot); !reflect.DeepEqual(gs, want) {
		t.Errorf("repository grants = %+v, want %+v", gs, want)
	}
	if e, _ := entryAt(t, f.claudeJSON, appKey); !reflect.DeepEqual(e, map[string]any{"hasTrustDialogAccepted": true}) {
		t.Errorf("app entry = %v", e)
	}
	if e, _ := entryAt(t, f.claudeJSON, libKey); !reflect.DeepEqual(e, map[string]any{"allowedTools": []any{"Read"}, "hasTrustDialogAccepted": true}) {
		t.Errorf("lib entry = %v, want the flag added beside the existing keys", e)
	}
	for _, rel := range []string{appRepo, libRepo} {
		if names, ok := repoApprovals(t, f.dirOf(rel)); !ok || !reflect.DeepEqual(names, []string{"docs"}) {
			t.Errorf("%s approvals = %v (present %v), want [docs]", rel, names, ok)
		}
	}
}

// TestClaudeTrustRepos_ClaudeDisabledRepoSkipped checks a repository with
// Claude turned off gets no trust and no settings document, and that turning
// Claude off for a repository that had a grant retracts it.
func TestClaudeTrustRepos_ClaudeDisabledRepoSkipped(t *testing.T) {
	f := newRepoTrustFixture(t)
	base := trustOn + libGroupTOML + docsServerTOML
	libKey := trustKey(t, f.dirOf(libRepo))

	f.mustCreate(t, f.writeConfig(t, base+libClaudeOff))
	gs := repoGrantsOf(t, f.instanceRoot)
	if _, ok := gs[libRepo]; ok || gs[appRepo] == nil {
		t.Errorf("repository grants = %+v, want only %s", gs, appRepo)
	}
	if _, ok := entryAt(t, f.claudeJSON, libKey); ok {
		t.Errorf("an entry was written for %s, whose Claude is off", libKey)
	}
	assertNoFile(t, repoSettingsPath(f.dirOf(libRepo)))

	f.mustApply(t, f.writeConfig(t, base))
	if repoGrantsOf(t, f.instanceRoot)[libRepo] == nil {
		t.Fatal("no grant for lib once Claude is on for it")
	}

	f.mustApply(t, f.writeConfig(t, base+libClaudeOff))
	if _, ok := repoGrantsOf(t, f.instanceRoot)[libRepo]; ok {
		t.Error("lib's grant survived turning Claude off for it")
	}
	if e, _ := entryAt(t, f.claudeJSON, libKey); e["hasTrustDialogAccepted"] != nil {
		t.Errorf("lib entry = %v, want the flag retracted", e)
	}
}

// TestClaudeTrustRepos_ApprovalsOnlyWithTrust checks the repository settings
// document carries the approval list only while trust_instances is on, and is
// byte-identical to a never-opted-in workspace's otherwise.
func TestClaudeTrustRepos_ApprovalsOnlyWithTrust(t *testing.T) {
	f := newTrustFixture(t)
	settings := repoSettingsPath(f.repoDir())

	f.mustCreate(t, f.writeConfig(t, docsServerTOML))
	never := readOrNil(t, settings)
	if _, ok := repoApprovals(t, f.repoDir()); ok {
		t.Fatal("approvals written without trust_instances")
	}

	f.mustApply(t, f.writeConfig(t, trustOff+docsServerTOML))
	if got := readOrNil(t, settings); !bytes.Equal(got, never) {
		t.Errorf("settings with trust off:\n%s\nwant the never-opted-in document:\n%s", got, never)
	}

	f.mustApply(t, f.writeConfig(t, trustOn+docsServerTOML))
	if names, ok := repoApprovals(t, f.repoDir()); !ok || !reflect.DeepEqual(names, []string{"docs"}) {
		t.Errorf("approvals with trust on = %v (present %v), want [docs]", names, ok)
	}

	f.mustApply(t, f.writeConfig(t, trustOff+docsServerTOML))
	if got := readOrNil(t, settings); !bytes.Equal(got, never) {
		t.Errorf("settings after turning trust off:\n%s\nwant the never-opted-in document:\n%s", got, never)
	}
}

// TestSettingsMaterializer_RepoApprovalsNeedTrust drives the repository
// settings materializer alone: an approval list is enough to write the
// document with trust on, and with trust off nothing is written.
func TestSettingsMaterializer_RepoApprovalsNeedTrust(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name  string
		trust *bool
		want  []string
	}{
		{name: "on", trust: &on, want: []string{"docs", "search"}},
		{name: "off", trust: &off},
		{name: "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir, repoDir := newApprovalDirs(t)
			cfg := approvalWorkspace("search", "docs")
			cfg.Claude.TrustInstances = tc.trust
			written, err := (&SettingsMaterializer{}).Materialize(&MaterializeContext{
				Config:    cfg,
				ConfigDir: configDir,
				RepoDir:   repoDir,
				RepoIndex: map[string]string{},
			})
			if err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			if tc.want == nil {
				if len(written) != 0 {
					t.Errorf("wrote %v, want nothing", written)
				}
				assertNoFile(t, repoSettingsPath(repoDir))
				return
			}
			names, ok := repoApprovals(t, repoDir)
			if !ok || !reflect.DeepEqual(names, tc.want) {
				t.Errorf("approvals = %v (present %v), want %v", names, ok, tc.want)
			}
		})
	}
}

// TestApplyToWorktree_RepoApprovalsFollowTrust checks a worktree's settings
// document, written by the same materializer as the clone's, carries the
// approval list with trust_instances on and none with it off. niwa doesn't
// trust worktree directories; the list only takes effect once something
// does.
func TestApplyToWorktree_RepoApprovalsFollowTrust(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name  string
		trust *bool
		want  bool
	}{
		{name: "on", trust: &on, want: true},
		{name: "off", trust: &off},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
			cfg.MCP.Servers = map[string]config.MCPServerConfig{"docs": mcpServerDecl("docs")}
			cfg.Claude.TrustInstances = tc.trust
			if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
				t.Fatalf("ApplyToWorktree: %v", err)
			}
			names, ok := repoApprovals(t, worktreePath)
			if ok != tc.want || (tc.want && !reflect.DeepEqual(names, []string{"docs"})) {
				t.Errorf("worktree approvals = %v (present %v), want present %v", names, ok, tc.want)
			}
		})
	}
}

// TestClaudeTrustRepos_SecondApplyNoRewrite checks an apply that changes
// nothing leaves the Claude config alone and the repository documents as
// they were.
func TestClaudeTrustRepos_SecondApplyNoRewrite(t *testing.T) {
	f := newRepoTrustFixture(t)
	on := f.writeConfig(t, trustOn+libGroupTOML+docsServerTOML)
	f.mustCreate(t, on)
	before := snapFile(t, f.claudeJSON)
	grants := repoGrantsOf(t, f.instanceRoot)
	docs := map[string][]byte{}
	for _, rel := range []string{appRepo, libRepo} {
		docs[rel] = mustRead(t, repoSettingsPath(f.dirOf(rel)))
	}

	f.mustApply(t, on)
	assertFileUntouched(t, f.claudeJSON, before)
	if gs := repoGrantsOf(t, f.instanceRoot); !reflect.DeepEqual(gs, grants) {
		t.Errorf("repository grants = %+v, want them kept as %+v", gs, grants)
	}
	for rel, want := range docs {
		if got := mustRead(t, repoSettingsPath(f.dirOf(rel))); !bytes.Equal(got, want) {
			t.Errorf("%s settings changed:\n%s\nwant:\n%s", rel, got, want)
		}
	}
}

// TestClaudeTrustRepos_OffSwitchRetractsAll checks turning the option off
// retracts every repository's flag once, keeps the keys Claude Code added,
// clears the records and drops the approval lists; a second apply with the
// option off touches nothing.
func TestClaudeTrustRepos_OffSwitchRetractsAll(t *testing.T) {
	for name, offBody := range map[string]string{"false": trustOff, "absent": ""} {
		t.Run(name, func(t *testing.T) {
			f := newRepoTrustFixture(t)
			f.mustCreate(t, f.writeConfig(t, trustOn+libGroupTOML+docsServerTOML))
			key := trustKey(t, f.instanceRoot)
			appKey := trustKey(t, f.dirOf(appRepo))
			libKey := trustKey(t, f.dirOf(libRepo))
			writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
				key:    map[string]any{"hasTrustDialogAccepted": true},
				appKey: map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []any{"Bash(ls)"}},
				libKey: map[string]any{"hasTrustDialogAccepted": true},
			}})

			off := f.writeConfig(t, offBody+libGroupTOML+docsServerTOML)
			f.mustApply(t, off)
			if e, ok := entryAt(t, f.claudeJSON, appKey); !ok || !reflect.DeepEqual(e, map[string]any{"allowedTools": []any{"Bash(ls)"}}) {
				t.Errorf("app entry = %v (present %v), want only the flag removed", e, ok)
			}
			if e, ok := entryAt(t, f.claudeJSON, libKey); !ok || len(e) != 0 {
				t.Errorf("lib entry = %v (present %v), want it kept without the flag", e, ok)
			}
			if gs := repoGrantsOf(t, f.instanceRoot); gs != nil {
				t.Errorf("repository grants = %+v, want them cleared", gs)
			}
			for _, rel := range []string{appRepo, libRepo} {
				if names, ok := repoApprovals(t, f.dirOf(rel)); ok {
					t.Errorf("%s still approves %v with the option off", rel, names)
				}
			}

			before := snapFile(t, f.claudeJSON)
			if out := f.mustApply(t, off); strings.Contains(out, "Claude trust") {
				t.Errorf("second off apply warned about Claude trust:\n%s", out)
			}
			assertFileUntouched(t, f.claudeJSON, before)
		})
	}
}

// TestClaudeTrustRepos_RemovedRepoWithdrawn checks a repository that leaves
// the config has its flag retracted and its record dropped, and one whose
// directory is already gone has its record dropped with a warning and its
// entry left alone.
func TestClaudeTrustRepos_RemovedRepoWithdrawn(t *testing.T) {
	t.Run("directory present", func(t *testing.T) {
		f := newRepoTrustFixture(t)
		f.mustCreate(t, f.writeConfig(t, trustOn+libGroupTOML))
		libKey := trustKey(t, f.dirOf(libRepo))
		appGrant := repoGrantsOf(t, f.instanceRoot)[appRepo]

		f.mustApply(t, f.writeConfig(t, trustOn))
		want := map[string]*claudetrust.Grant{appRepo: appGrant}
		if gs := repoGrantsOf(t, f.instanceRoot); !reflect.DeepEqual(gs, want) {
			t.Errorf("repository grants = %+v, want only %s's", gs, appRepo)
		}
		if e, ok := entryAt(t, f.claudeJSON, libKey); !ok || e["hasTrustDialogAccepted"] != nil {
			t.Errorf("lib entry = %v (present %v), want its flag retracted", e, ok)
		}
	})

	t.Run("directory gone", func(t *testing.T) {
		f := newRepoTrustFixture(t)
		f.mustCreate(t, f.writeConfig(t, trustOn+libGroupTOML))
		libGrant := repoGrantsOf(t, f.instanceRoot)[libRepo]
		if err := os.RemoveAll(f.dirOf(libRepo)); err != nil {
			t.Fatal(err)
		}
		before := snapFile(t, f.claudeJSON)

		out := f.mustApply(t, f.writeConfig(t, trustOn))
		if !strings.Contains(out, "warning:") || !strings.Contains(out, libGrant.Key) || !strings.Contains(out, "no longer exists") {
			t.Errorf("no warning naming %s:\n%s", libGrant.Key, out)
		}
		assertFileUntouched(t, f.claudeJSON, before)
		if _, ok := repoGrantsOf(t, f.instanceRoot)[libRepo]; ok {
			t.Error("the record for the removed directory was kept")
		}
	})
}

// TestClaudeTrustRepos_SaveFailureUndoesAllWrites checks a failed state save
// takes back every write the run made, the root's and each repository's, and
// only those.
func TestClaudeTrustRepos_SaveFailureUndoesAllWrites(t *testing.T) {
	failSave := func(string, *InstanceState) error { return errors.New("injected save failure") }

	t.Run("create", func(t *testing.T) {
		f := newRepoTrustFixture(t)
		libKey := trustKey(t, f.dirOf(libRepo))
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
			libKey: map[string]any{"allowedTools": []any{"Read"}},
		}})
		f.applier.saveState = failSave
		if _, err := f.createNamed(t, f.writeConfig(t, trustOn+libGroupTOML), "posture-ws"); err == nil {
			t.Fatal("Create succeeded despite the injected save failure")
		}
		want := map[string]any{libKey: map[string]any{"allowedTools": []any{"Read"}}}
		if got := projectsOf(t, readClaudeConfig(t, f.claudeJSON)); !reflect.DeepEqual(got, want) {
			t.Errorf("projects = %v, want only the untouched lib entry %v", got, want)
		}
	})

	t.Run("apply re-setting removed flags", func(t *testing.T) {
		f := newRepoTrustFixture(t)
		on := f.writeConfig(t, trustOn+libGroupTOML)
		f.mustCreate(t, on)
		key := trustKey(t, f.instanceRoot)
		appKey := trustKey(t, f.dirOf(appRepo))
		libKey := trustKey(t, f.dirOf(libRepo))
		// Every flag removed by hand: the apply sets each one again.
		writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
			key: map[string]any{}, appKey: map[string]any{}, libKey: map[string]any{"x": 1},
		}})
		before := readClaudeConfig(t, f.claudeJSON)

		f.applier.saveState = failSave
		if _, err := f.applyAt(t, on, f.instanceRoot); err == nil {
			t.Fatal("Apply succeeded despite the injected save failure")
		}
		if got := readClaudeConfig(t, f.claudeJSON); !reflect.DeepEqual(got, before) {
			t.Errorf("Claude config = %v, want it back to %v", got, before)
		}
	})
}

// TestClaudeTrustRepos_DestroyWithdrawsAll checks destroy removes what each
// repository grant added: the whole entry for a created grant, only the flag
// for a flag-only one.
func TestClaudeTrustRepos_DestroyWithdrawsAll(t *testing.T) {
	f := newRepoTrustFixture(t)
	libKey := trustKey(t, f.dirOf(libRepo))
	writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
		libKey:     map[string]any{"allowedTools": []any{"Read"}},
		"/another": map[string]any{"hasTrustDialogAccepted": true},
	}})
	f.mustCreate(t, f.writeConfig(t, trustOn+libGroupTOML))
	if n := len(repoGrantsOf(t, f.instanceRoot)); n != 2 {
		t.Fatalf("%d repository grants recorded, want 2", n)
	}

	f.freshOutput()
	if err := DestroyInstance(f.instanceRoot, WithDestroyReporter(f.applier.Reporter), WithPluginRecordBaseDir(f.home)); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	if strings.Contains(f.out.String(), "warning") {
		t.Errorf("destroy warned:\n%s", f.out.String())
	}
	want := map[string]any{
		libKey:     map[string]any{"allowedTools": []any{"Read"}},
		"/another": map[string]any{"hasTrustDialogAccepted": true},
	}
	if got := projectsOf(t, readClaudeConfig(t, f.claudeJSON)); !reflect.DeepEqual(got, want) {
		t.Errorf("projects after destroy = %v, want %v", got, want)
	}
}

// TestClaudeTrustRepos_DestroyRefusesCraftedPaths checks a recorded
// repository path that is absolute, climbs out with "..", isn't clean, or
// resolves through a symlink to a directory outside the instance is refused
// with a warning, so a crafted record can't withdraw another project's trust.
// A recorded directory that is already gone is refused too, and the
// legitimate grant beside them is still withdrawn.
func TestClaudeTrustRepos_DestroyRefusesCraftedPaths(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")
	victim := filepath.Join(root, "victim")
	for _, d := range []string{victim, filepath.Join(inst, "g", "r")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(victim, filepath.Join(inst, "g", "link")); err != nil {
		t.Fatal(err)
	}
	victimKey := trustKey(t, victim)
	legitKey := trustKey(t, filepath.Join(inst, "g", "r"))
	goneKey := filepath.Join(trustKey(t, inst), "g", "gone")

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	writeClaudeConfig(t, cfgPath, map[string]any{"projects": map[string]any{
		victimKey: map[string]any{"hasTrustDialogAccepted": true},
		legitKey:  map[string]any{"hasTrustDialogAccepted": true},
		goneKey:   map[string]any{"hasTrustDialogAccepted": true},
	}})
	grant := func(key string) *claudetrust.Grant {
		return &claudetrust.Grant{ConfigFile: cfgPath, Key: key, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace}
	}
	crafted := []string{"../victim", victim, "g/../../victim", "./g/link", "g/link"}
	grants := map[string]*claudetrust.Grant{"g/r": grant(legitKey), "g/gone": grant(goneKey)}
	for _, rel := range crafted {
		grants[rel] = grant(victimKey)
	}
	recordRepoClaudeTrust(t, inst, grants)

	var buf bytes.Buffer
	if err := DestroyInstance(inst, WithDestroyReporter(NewReporterWithTTY(&buf, false))); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	out := buf.String()
	for _, rel := range crafted {
		if !strings.Contains(out, "refusing the record for repository "+`"`+rel+`"`) {
			t.Errorf("no refusal for %q:\n%s", rel, out)
		}
	}
	if !strings.Contains(out, goneKey) {
		t.Errorf("no warning naming the entry %s left for the gone directory:\n%s", goneKey, out)
	}
	want := map[string]any{
		victimKey: map[string]any{"hasTrustDialogAccepted": true},
		goneKey:   map[string]any{"hasTrustDialogAccepted": true},
	}
	if got := projectsOf(t, readClaudeConfig(t, cfgPath)); !reflect.DeepEqual(got, want) {
		t.Errorf("projects = %v, want %v", got, want)
	}
}

// TestClaudeTrustRepos_ApplyDropsCraftedRecord checks an apply drops a
// crafted repository record with a warning and leaves the entry it names
// alone.
func TestClaudeTrustRepos_ApplyDropsCraftedRecord(t *testing.T) {
	f := newTrustFixture(t)
	on := f.writeConfig(t, trustOn)
	f.mustCreate(t, on)
	victim := filepath.Join(f.workspaceRoot, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	victimKey := trustKey(t, victim)
	grants := repoGrantsOf(t, f.instanceRoot)
	grants["../victim"] = wantGrant(f.claudeJSON, victimKey, claudetrust.ModeCreated)
	recordRepoClaudeTrust(t, f.instanceRoot, grants)
	doc := readClaudeConfig(t, f.claudeJSON)
	projectsOf(t, doc)[victimKey] = map[string]any{"hasTrustDialogAccepted": true}
	writeClaudeConfig(t, f.claudeJSON, doc)
	before := snapFile(t, f.claudeJSON)

	out := f.mustApply(t, on)
	if !strings.Contains(out, "warning:") || !strings.Contains(out, `repository "../victim"`) {
		t.Errorf("no warning about the crafted record:\n%s", out)
	}
	assertFileUntouched(t, f.claudeJSON, before)
	if _, ok := repoGrantsOf(t, f.instanceRoot)["../victim"]; ok {
		t.Error("the crafted record was kept")
	}
}

// TestRepoTrustKey covers the recorded-path checks on their own.
func TestRepoTrustKey(t *testing.T) {
	ws := t.TempDir()
	inst := filepath.Join(ws, "inst")
	outside := filepath.Join(ws, "outside")
	for _, d := range []string{filepath.Join(inst, "g", "r"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(inst, "g", "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(inst, "g", "r"), filepath.Join(inst, "g", "alias")); err != nil {
		t.Fatal(err)
	}
	instKey := trustKey(t, inst)
	rKey := trustKey(t, filepath.Join(inst, "g", "r"))

	for _, tc := range []struct {
		rel     string
		want    string
		refused bool
	}{
		{rel: "g/r", want: rKey},
		{rel: "g/alias", want: rKey},
		{rel: "g/missing", want: ""},
		{rel: "", refused: true},
		{rel: "/etc", refused: true},
		{rel: "..", refused: true},
		{rel: "../outside", refused: true},
		{rel: "g/../../outside", refused: true},
		{rel: "g/./r", refused: true},
		{rel: "g/r/", refused: true},
		{rel: ".", refused: true},
		{rel: "g/out", refused: true},
	} {
		t.Run(tc.rel, func(t *testing.T) {
			got, err := repoTrustKey(inst, instKey, tc.rel)
			if tc.refused {
				if !errors.Is(err, errRepoTrustPathRefused) {
					t.Errorf("repoTrustKey(%q) = %q, %v; want a refusal", tc.rel, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("repoTrustKey(%q) = %q, %v; want %q", tc.rel, got, err, tc.want)
			}
		})
	}
}

// TestClaudeTrustRepos_OverBroadKeyRefused checks the over-broad-key refusal
// covers repository directories: with the developer home at a repository's
// root, neither that repository nor the instance root above it is trusted.
func TestClaudeTrustRepos_OverBroadKeyRefused(t *testing.T) {
	f := newTrustFixture(t)
	on := f.writeConfig(t, trustOn)
	f.instance(t, on.Workspace.Name)
	f.applier.DeveloperHome = f.repoDir()
	repoKey := trustKey(t, f.repoDir())

	out := f.mustCreate(t, on)
	if !strings.Contains(out, repoKey) || !strings.Contains(out, "is the home directory") {
		t.Errorf("no refusal naming %s:\n%s", repoKey, out)
	}
	if gs := repoGrantsOf(t, f.instanceRoot); gs != nil {
		t.Errorf("repository grants = %+v, want none", gs)
	}
	assertNoFile(t, filepath.Join(f.repoDir(), ".claude.json"))
}

// TestClaudeTrustRepos_MovedInstance checks a moved instance's repository
// records are replaced by fresh grants at the new paths, with the old entries
// left alone and named in a warning.
func TestClaudeTrustRepos_MovedInstance(t *testing.T) {
	f := newTrustFixture(t)
	on := f.writeConfig(t, trustOn)
	f.mustCreate(t, on)
	oldKey := trustKey(t, f.repoDir())
	oldEntry, _ := entryAt(t, f.claudeJSON, oldKey)

	moved := filepath.Join(f.workspaceRoot, "posture-ws-moved")
	if err := os.Rename(f.instanceRoot, moved); err != nil {
		t.Fatal(err)
	}
	out, err := f.applyAt(t, on, moved)
	if err != nil {
		t.Fatalf("Apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, oldKey) {
		t.Errorf("no warning naming %s:\n%s", oldKey, out)
	}
	if e, ok := entryAt(t, f.claudeJSON, oldKey); !ok || !reflect.DeepEqual(e, oldEntry) {
		t.Errorf("old entry = %v, want it left as %v", e, oldEntry)
	}
	newKey := trustKey(t, filepath.Join(moved, filepath.FromSlash(appRepo)))
	want := map[string]*claudetrust.Grant{appRepo: wantGrant(f.claudeJSON, newKey, claudetrust.ModeCreated)}
	if gs := repoGrantsOf(t, moved); !reflect.DeepEqual(gs, want) {
		t.Errorf("repository grants = %+v, want %+v", gs, want)
	}
}

// TestClaudeTrustRepos_EmptyDeveloperHomeCarriesRecords checks an applier
// with no developer home carries the repository records forward untouched.
func TestClaudeTrustRepos_EmptyDeveloperHomeCarriesRecords(t *testing.T) {
	f := newTrustFixture(t)
	f.mustCreate(t, f.writeConfig(t, trustOn))
	recorded := repoGrantsOf(t, f.instanceRoot)
	before := snapFile(t, f.claudeJSON)

	f.applier.DeveloperHome = ""
	f.mustApply(t, f.writeConfig(t, trustOff))
	assertFileUntouched(t, f.claudeJSON, before)
	if gs := repoGrantsOf(t, f.instanceRoot); !reflect.DeepEqual(gs, recorded) {
		t.Errorf("repository grants = %+v, want them carried forward as %+v", gs, recorded)
	}
}

// TestClaudeTrustRepos_OverlayNoticeNamesRepos checks the overlay notice says
// the repository directories are trusted and what that lets their own
// committed settings do.
func TestClaudeTrustRepos_OverlayNoticeNamesRepos(t *testing.T) {
	f := newTrustFixture(t)
	f.withOverlay(t, "[claude]\ntrust_instances = true\n")
	out := f.mustCreate(t, f.writeConfig(t, ""))
	for _, want := range []string{overlayNoticeMarker, "and 1 repository directory in it trusted", "own committed .claude settings and hooks"} {
		if !strings.Contains(out, want) {
			t.Errorf("notice lacks %q:\n%s", want, out)
		}
	}
}

// TestStateWithClaudeTrustReposRoundTrips checks the repository records
// survive a save and load, and that a state without them writes no key.
func TestStateWithClaudeTrustReposRoundTrips(t *testing.T) {
	dir := t.TempDir()
	grants := map[string]*claudetrust.Grant{
		"g/r": {ConfigFile: "/home/dev/.claude.json", Key: "/work/inst/g/r", Mode: claudetrust.ModeFlagOnly, Source: claudetrust.SourceWorkspace},
	}
	if err := SaveState(dir, &InstanceState{SchemaVersion: SchemaVersion, ClaudeTrustRepos: grants}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(mustRead(t, filepath.Join(dir, StateDir, StateFile)), []byte(`"claude_trust_repos"`)) {
		t.Error("the state file has no claude_trust_repos key")
	}
	if gs := repoGrantsOf(t, dir); !reflect.DeepEqual(gs, grants) {
		t.Errorf("loaded %+v, want %+v", gs, grants)
	}

	if err := SaveState(dir, &InstanceState{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(mustRead(t, filepath.Join(dir, StateDir, StateFile)), []byte("claude_trust_repos")) {
		t.Error("a state without repository records still writes the key")
	}
}
