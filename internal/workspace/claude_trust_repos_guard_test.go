package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/claudetrust"
	"github.com/tsukumogami/niwa/internal/config"
)

// These tests cover the limits on repository-directory trust: a directory
// that resolves outside the instance, a repository with Claude's own gate
// closed, a review instance, and the one-time notice for a workspace told
// about the instance root before repository directories were trusted.

// libCodexOn turns Codex on for libRepo, so a test with libClaudeOff has
// another agent's gate open where Claude's is closed.
const libCodexOn = "\n[repos.lib.codex]\nenabled = true\n"

// TestClaudeTrustRepos_SymlinkOutsideInstanceSkipped checks a repository
// directory, or the group directory above it, that is a symlink leading out
// of the instance gets no entry and no record, with a warning naming the
// repository, while the other repository is still trusted. A record written
// before the directory was swapped for such a link is dropped the same way.
func TestClaudeTrustRepos_SymlinkOutsideInstanceSkipped(t *testing.T) {
	for _, tc := range []struct {
		name string
		// link replaces part of the instance with a symlink to outside,
		// where lib/.git already exists.
		link func(t *testing.T, f *trustFixture, outside string)
		// prior creates the instance with lib trusted before the link.
		prior bool
	}{
		{name: "repository directory", link: func(t *testing.T, f *trustFixture, outside string) {
			relink(t, f.dirOf(libRepo), filepath.Join(outside, "lib"))
		}},
		{name: "group directory", link: func(t *testing.T, f *trustFixture, outside string) {
			relink(t, filepath.Join(f.instanceRoot, "tools"), outside)
		}},
		{name: "recorded repository directory", prior: true, link: func(t *testing.T, f *trustFixture, outside string) {
			relink(t, f.dirOf(libRepo), filepath.Join(outside, "lib"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRepoTrustFixture(t)
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.MkdirAll(filepath.Join(outside, "lib", ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			outsideKey := trustKey(t, filepath.Join(outside, "lib"))
			cfg := f.writeConfig(t, trustOn+libGroupTOML)

			var out string
			if tc.prior {
				f.mustCreate(t, cfg)
				if repoGrantsOf(t, f.instanceRoot)[libRepo] == nil {
					t.Fatal("no grant for lib before the link")
				}
				tc.link(t, f, outside)
				out = f.mustApply(t, cfg)
			} else {
				tc.link(t, f, outside)
				out = f.mustCreate(t, cfg)
			}

			if !strings.Contains(out, "warning:") || !strings.Contains(out, `repository "tools/lib"`) || !strings.Contains(out, "outside") {
				t.Errorf("no warning naming the repository:\n%s", out)
			}
			if e, ok := entryAt(t, f.claudeJSON, outsideKey); ok {
				t.Errorf("projects[%s] = %v, want no entry outside the instance", outsideKey, e)
			}
			gs := repoGrantsOf(t, f.instanceRoot)
			if _, ok := gs[libRepo]; ok {
				t.Errorf("repository grants = %+v, want no record for %s", gs, libRepo)
			}
			if gs[appRepo] == nil {
				t.Errorf("repository grants = %+v, want %s still trusted", gs, appRepo)
			}
			assertTrustedEntry(t, f.claudeJSON, trustKey(t, f.dirOf(appRepo)))
		})
	}
}

// relink replaces path with a symlink to target.
func relink(t *testing.T, path, target string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

// assertTrustedEntry fails unless the Claude config at path trusts key.
func assertTrustedEntry(t *testing.T, path, key string) {
	t.Helper()
	if e, ok := entryAt(t, path, key); !ok || e["hasTrustDialogAccepted"] != true {
		t.Errorf("projects[%s] = %v (present %v), want it trusted", key, e, ok)
	}
}

// TestClaudeTrustRepos_ClaudeOffCodexOnGetsNone checks a repository with
// Claude turned off gets no Claude trust and no approval list even with
// another agent's gate open for it.
func TestClaudeTrustRepos_ClaudeOffCodexOnGetsNone(t *testing.T) {
	f := newRepoTrustFixture(t)
	libKey := trustKey(t, f.dirOf(libRepo))

	f.mustCreate(t, f.writeConfig(t, trustOn+libGroupTOML+docsServerTOML+libClaudeOff+libCodexOn))
	gs := repoGrantsOf(t, f.instanceRoot)
	if _, ok := gs[libRepo]; ok || gs[appRepo] == nil {
		t.Errorf("repository grants = %+v, want only %s", gs, appRepo)
	}
	if _, ok := entryAt(t, f.claudeJSON, libKey); ok {
		t.Errorf("an entry was written for %s, whose Claude is off", libKey)
	}
	if names, ok := repoApprovals(t, f.dirOf(libRepo)); ok {
		t.Errorf("lib approves %v with Claude off", names)
	}
}

// TestClaudeEnabled_FollowsClaudeGateOnly pins the gate repository trust and
// the repository approval list follow: Claude's own, whatever Codex's says.
// Today it agrees with the gate the settings materializer runs behind, since
// Claude is the only agent that receives hooks; if a second agent ever does,
// this test's first half still holds and the second half is what changes.
func TestClaudeEnabled_FollowsClaudeGateOnly(t *testing.T) {
	yes, no := true, false
	gates := []*bool{nil, &yes, &no}
	for _, wsClaude := range gates {
		for _, repoClaude := range gates {
			for _, repoCodex := range gates {
				cfg := &config.WorkspaceConfig{Repos: map[string]config.RepoOverride{
					"lib": {Claude: &config.ClaudeOverride{Enabled: repoClaude}, Codex: &config.CodexOverride{Enabled: repoCodex}},
				}}
				cfg.Claude.Enabled = wsClaude
				want := AgentEnabled(cfg, "lib", "claude")
				if got := claudeEnabled(cfg, "lib"); got != want {
					t.Errorf("claudeEnabled(claude ws=%v repo=%v, codex repo=%v) = %v, want Claude's gate %v",
						show(wsClaude), show(repoClaude), show(repoCodex), got, want)
				}
				if got := hookOwningAgentsEnabled(cfg, "lib"); got != want {
					t.Errorf("hookOwningAgentsEnabled(claude ws=%v repo=%v, codex repo=%v) = %v, want it equal to Claude's gate %v while Claude alone receives hooks",
						show(wsClaude), show(repoClaude), show(repoCodex), got, want)
				}
			}
		}
	}
}

func show(b *bool) string {
	if b == nil {
		return "unset"
	}
	if *b {
		return "true"
	}
	return "false"
}

// TestRepoMCPApprovals_FollowsClaudeGate checks the repository approval list
// is empty for a repository with Claude off and Codex on, and present for
// one with Claude on.
func TestRepoMCPApprovals_FollowsClaudeGate(t *testing.T) {
	yes, no := true, false
	cfg := approvalWorkspace("docs")
	cfg.Claude.TrustInstances = &yes
	cfg.Repos = map[string]config.RepoOverride{
		"lib": {Claude: &config.ClaudeOverride{Enabled: &no}, Codex: &config.CodexOverride{Enabled: &yes}},
	}
	if names, err := repoMCPApprovals(cfg, "lib"); err != nil || names != nil {
		t.Errorf("repoMCPApprovals(lib) = %v, %v; want none", names, err)
	}
	if names, err := repoMCPApprovals(cfg, "app"); err != nil || !reflect.DeepEqual(names, []string{"docs"}) {
		t.Errorf("repoMCPApprovals(app) = %v, %v; want [docs]", names, err)
	}
}

// TestClaudeTrustRepos_ReviewInstanceGetsNone checks an apply in a review
// instance, marked by the state's review_instance or by a review grant for
// the root, retracts every workspace repository grant, writes none, keeps
// the marker, and touches nothing on the next apply.
func TestClaudeTrustRepos_ReviewInstanceGetsNone(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark func(s *InstanceState)
	}{
		{name: "marker", mark: func(s *InstanceState) { s.ReviewInstance = true }},
		{name: "review root grant", mark: func(s *InstanceState) { s.ClaudeTrust.Source = claudetrust.SourceReview }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRepoTrustFixture(t)
			on := f.writeConfig(t, trustOn+libGroupTOML+docsServerTOML)
			f.mustCreate(t, on)
			if n := len(repoGrantsOf(t, f.instanceRoot)); n != 2 {
				t.Fatalf("%d repository grants after create, want 2", n)
			}
			state, err := LoadState(f.instanceRoot)
			if err != nil {
				t.Fatal(err)
			}
			tc.mark(state)
			if err := SaveState(f.instanceRoot, state); err != nil {
				t.Fatal(err)
			}

			out := f.mustApply(t, on)
			if strings.Contains(out, "Claude trust") {
				t.Errorf("apply warned about Claude trust:\n%s", out)
			}
			for _, rel := range []string{appRepo, libRepo} {
				key := trustKey(t, f.dirOf(rel))
				if e, _ := entryAt(t, f.claudeJSON, key); e["hasTrustDialogAccepted"] != nil {
					t.Errorf("projects[%s] = %v, want the flag retracted", key, e)
				}
			}
			after, err := LoadState(f.instanceRoot)
			if err != nil {
				t.Fatal(err)
			}
			if after.ClaudeTrustRepos != nil {
				t.Errorf("repository grants = %+v, want none", after.ClaudeTrustRepos)
			}
			if !after.isReviewInstance() {
				t.Error("the apply dropped what marks the instance as a review instance")
			}
			assertTrustedEntry(t, f.claudeJSON, trustKey(t, f.instanceRoot))

			before := snapFile(t, f.claudeJSON)
			f.mustApply(t, on)
			assertFileUntouched(t, f.claudeJSON, before)
			if gs := repoGrantsOf(t, f.instanceRoot); gs != nil {
				t.Errorf("repository grants after a second apply = %+v, want none", gs)
			}
		})
	}
}

// TestWithdrawRepoClaudeTrust checks the withdrawal niwa watch runs: each
// workspace repository grant is withdrawn as a destroy would (the whole
// entry niwa created, only the flag it set on an existing one), a crafted
// path and a gone directory are dropped with warnings and their entries left,
// and a review-sourced record is kept.
func TestWithdrawRepoClaudeTrust(t *testing.T) {
	f := newRepoTrustFixture(t)
	libKey := trustKey(t, f.dirOf(libRepo))
	writeClaudeConfig(t, f.claudeJSON, map[string]any{"projects": map[string]any{
		libKey: map[string]any{"allowedTools": []any{"Read"}},
	}})
	f.mustCreate(t, f.writeConfig(t, trustOn+libGroupTOML))
	appKey := trustKey(t, f.dirOf(appRepo))

	victim := filepath.Join(f.workspaceRoot, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	victimKey := trustKey(t, victim)
	goneKey := filepath.Join(trustKey(t, f.instanceRoot), "tools", "gone")
	doc := readClaudeConfig(t, f.claudeJSON)
	projectsOf(t, doc)[victimKey] = map[string]any{"hasTrustDialogAccepted": true}
	projectsOf(t, doc)[goneKey] = map[string]any{"hasTrustDialogAccepted": true}
	writeClaudeConfig(t, f.claudeJSON, doc)

	state, err := LoadState(f.instanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	review := &claudetrust.Grant{ConfigFile: f.claudeJSON, Key: "/elsewhere", Mode: claudetrust.ModeCreated, Source: claudetrust.SourceReview}
	state.ClaudeTrustRepos["../victim"] = wantGrant(f.claudeJSON, victimKey, claudetrust.ModeCreated)
	state.ClaudeTrustRepos["tools/gone"] = wantGrant(f.claudeJSON, goneKey, claudetrust.ModeCreated)
	state.ClaudeTrustRepos["tools/review"] = review

	warnings := WithdrawRepoClaudeTrust(f.instanceRoot, state)
	joined := strings.Join(warnings, "\n")
	if len(warnings) != 2 || !strings.Contains(joined, `refusing the record for repository "../victim"`) ||
		!strings.Contains(joined, "no longer exists; leaving it in place, remove it by hand") {
		t.Errorf("warnings = %q, want one refusing ../victim and one for the gone directory", warnings)
	}
	if want := map[string]*claudetrust.Grant{"tools/review": review}; !reflect.DeepEqual(state.ClaudeTrustRepos, want) {
		t.Errorf("repository records = %+v, want only the review record kept", state.ClaudeTrustRepos)
	}
	if e, ok := entryAt(t, f.claudeJSON, appKey); ok {
		t.Errorf("projects[%s] = %v, want the entry niwa created removed", appKey, e)
	}
	if e, ok := entryAt(t, f.claudeJSON, libKey); !ok || !reflect.DeepEqual(e, map[string]any{"allowedTools": []any{"Read"}}) {
		t.Errorf("projects[%s] = %v (present %v), want only the flag removed", libKey, e, ok)
	}
	for _, key := range []string{victimKey, goneKey} {
		assertTrustedEntry(t, f.claudeJSON, key)
	}
}

// TestClaudeTrustRepos_OverlayRepoNoticeForToldWorkspace checks a workspace
// that was shown the overlay notice before repository directories were
// trusted is told once, when an instance first holds a repository grant,
// that they are trusted too; and that a workspace shown the full notice
// naming repository directories is never shown the second one.
func TestClaudeTrustRepos_OverlayRepoNoticeForToldWorkspace(t *testing.T) {
	const overlay = "[claude]\ntrust_instances = true\n"
	const repoNotice = "now covers the repository directories cloned into each instance"

	t.Run("told about the root only", func(t *testing.T) {
		f := newTrustFixture(t)
		f.withOverlay(t, overlay)
		markDisclosed(t, f.workspaceRoot, noticeClaudeTrustOverlay)

		// No repository grant yet: nothing new to tell.
		if out := f.mustCreate(t, f.writeConfig(t, appClaudeOff)); strings.Contains(out, overlayNoticeMarker) {
			t.Errorf("notice shown with no repository grant:\n%s", out)
		}

		cfg := f.writeConfig(t, "")
		out := f.mustApply(t, cfg)
		for _, want := range []string{repoNotice, "niwa marked 1 repository directory in this instance trusted in " + f.claudeJSON, "own committed .claude settings and hooks"} {
			if !strings.Contains(out, want) {
				t.Errorf("repository notice lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "niwa marked "+trustKey(t, f.instanceRoot)) {
			t.Errorf("the repository notice repeated the root notice:\n%s", out)
		}
		assertDisclosed(t, f.workspaceRoot, noticeClaudeTrustOverlay, noticeClaudeTrustOverlayRepos)

		if again := f.mustApply(t, cfg); strings.Contains(again, overlayNoticeMarker) {
			t.Errorf("the next apply repeated a notice:\n%s", again)
		}
		second, err := f.createNamed(t, cfg, "posture-ws-2")
		if err != nil {
			t.Fatalf("second Create: %v\n%s", err, second)
		}
		if strings.Contains(second, overlayNoticeMarker) {
			t.Errorf("a second instance repeated a notice:\n%s", second)
		}
	})

	t.Run("told about both", func(t *testing.T) {
		f := newTrustFixture(t)
		f.withOverlay(t, overlay)
		cfg := f.writeConfig(t, "")
		first := f.mustCreate(t, cfg)
		if !strings.Contains(first, "and 1 repository directory in it trusted") || strings.Contains(first, repoNotice) {
			t.Errorf("first create: want the full notice only:\n%s", first)
		}
		assertDisclosed(t, f.workspaceRoot, noticeClaudeTrustOverlay, noticeClaudeTrustOverlayRepos)
		if again := f.mustApply(t, cfg); strings.Contains(again, overlayNoticeMarker) {
			t.Errorf("apply after the full notice showed another:\n%s", again)
		}
	})

	t.Run("full notice for the root alone", func(t *testing.T) {
		f := newTrustFixture(t)
		f.withOverlay(t, overlay)
		if out := f.mustCreate(t, f.writeConfig(t, appClaudeOff)); !strings.Contains(out, overlayNoticeMarker) {
			t.Fatalf("create: no notice:\n%s", out)
		}
		assertDisclosed(t, f.workspaceRoot, noticeClaudeTrustOverlay)
		state, err := LoadState(f.workspaceRoot)
		if err != nil {
			t.Fatal(err)
		}
		if sliceContains(state.DisclosedNotices, noticeClaudeTrustOverlayRepos) {
			t.Fatalf("the root-only notice recorded %s", noticeClaudeTrustOverlayRepos)
		}
		if out := f.mustApply(t, f.writeConfig(t, "")); !strings.Contains(out, repoNotice) {
			t.Errorf("the first repository grant didn't get the repository notice:\n%s", out)
		}
	})
}

// markDisclosed records keys as already shown in the workspace root's state.
func markDisclosed(t *testing.T, workspaceRoot string, keys ...string) {
	t.Helper()
	state, err := LoadState(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.DisclosedNotices = append(state.DisclosedNotices, keys...)
	if err := SaveState(workspaceRoot, state); err != nil {
		t.Fatal(err)
	}
}

// assertDisclosed fails unless the workspace root's state records every key.
func assertDisclosed(t *testing.T, workspaceRoot string, keys ...string) {
	t.Helper()
	state, err := LoadState(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if !sliceContains(state.DisclosedNotices, k) {
			t.Errorf("disclosed notices = %v, want %s recorded", state.DisclosedNotices, k)
		}
	}
}
