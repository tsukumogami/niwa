package config

import "testing"

// TestEffectiveWorktreeSetup_DefaultsOff is the safety property of the whole
// feature, not a formality.
//
// Every setup script that exists was written before worktrees ran one, and a
// script that walks up from its working directory lands on the instance root
// from a clone and on the instance's internal directory from a worktree -- a
// path that exists and is writable, so it writes to the wrong place and exits
// 0. If this defaulted on, turning the feature on would break those scripts
// silently, which is the failure class the feature exists to remove.
//
// Note the inversion from EffectiveReadEnvExample, whose cascade this otherwise
// mirrors exactly: that one defaults ON. Copying its final `return true` is the
// single most likely way to get this wrong.
func TestEffectiveWorktreeSetup_DefaultsOff(t *testing.T) {
	ws := &WorkspaceConfig{}
	if EffectiveWorktreeSetup(ws, "alpha") {
		t.Error("worktree setup must default OFF with nothing configured")
	}

	if EffectiveWorktreeSetup(nil, "alpha") {
		t.Error("a nil config must resolve to OFF")
	}
}

func TestEffectiveWorktreeSetup_Cascade(t *testing.T) {
	tests := []struct {
		name      string
		workspace *bool
		repo      *bool
		want      bool
	}{
		{"both unset", nil, nil, false},
		{"workspace on", boolPtr(true), nil, true},
		{"workspace off", boolPtr(false), nil, false},
		{"repo on over workspace unset", nil, boolPtr(true), true},
		{"repo on over workspace off", boolPtr(false), boolPtr(true), true},
		{"repo off over workspace on", boolPtr(true), boolPtr(false), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := &WorkspaceConfig{
				Workspace: WorkspaceMeta{WorktreeSetup: tc.workspace},
				Repos:     map[string]RepoOverride{"alpha": {WorktreeSetup: tc.repo}},
			}
			if got := EffectiveWorktreeSetup(ws, "alpha"); got != tc.want {
				t.Errorf("EffectiveWorktreeSetup = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEffectiveWorktreeSetup_IsPerRepo covers the isolation requirement: an
// opt-in on one repo must not turn the feature on for another. This is what
// makes a mixed workspace -- some repos needing per-tree setup, others whose
// setup writes shared state -- expressible at all.
func TestEffectiveWorktreeSetup_IsPerRepo(t *testing.T) {
	ws := &WorkspaceConfig{
		Repos: map[string]RepoOverride{
			"opted-in": {WorktreeSetup: boolPtr(true)},
		},
	}

	if !EffectiveWorktreeSetup(ws, "opted-in") {
		t.Error("the opted-in repo should resolve on")
	}
	if EffectiveWorktreeSetup(ws, "not-mentioned") {
		t.Error("a repo with no override must not inherit another repo's opt-in")
	}
}
