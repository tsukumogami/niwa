package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/claudetrust"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// These tests cover the rule that a review instance gets no
// repository-directory Claude trust: niwa watch withdraws the grants the
// instance was created with, records the instance as a review instance, and
// trusts only its root.

// reviewRepoGrants gives the review instance at inst two repository
// directories with workspace grants, as a create with trust_instances on
// leaves them: g/new has an entry niwa created, g/old a flag niwa set on an
// entry that already held allowedTools. It returns their keys.
func reviewRepoGrants(t *testing.T, home, file, inst string) (newKey, oldKey string) {
	t.Helper()
	for _, rel := range []string{"g/new", "g/old"} {
		if err := os.MkdirAll(filepath.Join(inst, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	newKey = resolvedKey(t, filepath.Join(inst, "g", "new"))
	oldKey = resolvedKey(t, filepath.Join(inst, "g", "old"))
	oldJSON, _ := json.Marshal(oldKey)
	writeTestFile(t, file, `{"projects": {`+string(oldJSON)+`: {"allowedTools": ["Read"]}}}`)

	env := claudetrust.Env{Home: home}
	grants := map[string]*claudetrust.Grant{}
	for rel, dir := range map[string]string{"g/new": newKey, "g/old": oldKey} {
		out := claudetrust.Ensure(env, dir, nil, claudetrust.SourceWorkspace)
		if out.Grant == nil || out.Warning != "" {
			t.Fatalf("seeding a workspace grant for %s: %+v", rel, out)
		}
		grants[rel] = out.Grant
	}
	if grants["g/new"].Mode != claudetrust.ModeCreated || grants["g/old"].Mode != claudetrust.ModeFlagOnly {
		t.Fatalf("seeded grants = %+v, want created and flag-only", grants)
	}
	state, err := workspace.LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	state.ClaudeTrustRepos = grants
	if err := workspace.SaveState(inst, state); err != nil {
		t.Fatal(err)
	}
	return newKey, oldKey
}

// assertRepoTrustWithdrawn checks what destroying the instance would leave
// of the two grants reviewRepoGrants seeded: no entry for g/new, and g/old's
// entry without the flag but with its own keys, and no record for either.
func assertRepoTrustWithdrawn(t *testing.T, file, inst, newKey, oldKey string) {
	t.Helper()
	assertNoEntry(t, file, newKey)
	e, ok := claudeEntry(t, file, oldKey)
	if !ok || e["hasTrustDialogAccepted"] != nil || e["allowedTools"] == nil {
		t.Errorf("projects[%s] = %v (present %v), want allowedTools kept and the flag removed", oldKey, e, ok)
	}
	state, err := workspace.LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	if state.ClaudeTrustRepos != nil {
		t.Errorf("repository records = %+v, want none", state.ClaudeTrustRepos)
	}
}

// TestResolveAskPosture_WithdrawsRepoGrants checks seeding a sandboxed
// review's trust withdraws every workspace grant for a repository directory
// and clears the records, while trusting the root.
func TestResolveAskPosture_WithdrawsRepoGrants(t *testing.T) {
	home, file := stubReviewHome(t)
	inst := makeReviewInstance(t)
	newKey, oldKey := reviewRepoGrants(t, home, file, inst)

	var stderr bytes.Buffer
	if !resolveAskPosture(&stderr, inst, true) {
		t.Fatalf("resolveAskPosture = false, want true; stderr: %s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected warnings: %s", stderr.String())
	}
	assertTrusted(t, file, resolvedKey(t, inst))
	assertRepoTrustWithdrawn(t, file, inst, newKey, oldKey)
	if g := recordedGrant(t, inst); g == nil || g.Source != claudetrust.SourceReview {
		t.Errorf("root record = %+v, want a review grant", g)
	}
}

// TestMarkReviewInstance covers the step every staged review runs, sandboxed
// or not, before the pull request is fetched.
func TestMarkReviewInstance(t *testing.T) {
	t.Run("marks and withdraws", func(t *testing.T) {
		home, file := stubReviewHome(t)
		inst := makeReviewInstance(t)
		newKey, oldKey := reviewRepoGrants(t, home, file, inst)

		var stderr bytes.Buffer
		if err := markReviewInstance(&stderr, inst); err != nil {
			t.Fatalf("markReviewInstance: %v", err)
		}
		if stderr.Len() != 0 {
			t.Errorf("unexpected warnings: %s", stderr.String())
		}
		assertRepoTrustWithdrawn(t, file, inst, newKey, oldKey)
		state, err := workspace.LoadState(inst)
		if err != nil || !state.ReviewInstance {
			t.Errorf("review_instance not recorded (state %+v, err %v)", state, err)
		}
	})

	t.Run("refused record is dropped with a warning", func(t *testing.T) {
		_, file := stubReviewHome(t)
		inst := makeReviewInstance(t)
		victim := filepath.Join(filepath.Dir(inst), "victim")
		if err := os.MkdirAll(victim, 0o755); err != nil {
			t.Fatal(err)
		}
		victimKey := resolvedKey(t, victim)
		victimJSON, _ := json.Marshal(victimKey)
		writeTestFile(t, file, `{"projects": {`+string(victimJSON)+`: {"hasTrustDialogAccepted": true}}}`)
		before := statFile(t, file)
		state, err := workspace.LoadState(inst)
		if err != nil {
			t.Fatal(err)
		}
		state.ClaudeTrustRepos = map[string]*claudetrust.Grant{
			"../victim": {ConfigFile: file, Key: victimKey, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace},
		}
		if err := workspace.SaveState(inst, state); err != nil {
			t.Fatal(err)
		}

		var stderr bytes.Buffer
		if err := markReviewInstance(&stderr, inst); err != nil {
			t.Fatalf("markReviewInstance: %v", err)
		}
		if !strings.Contains(stderr.String(), `refusing the record for repository "../victim"`) {
			t.Errorf("stderr = %q, want the crafted record refused", stderr.String())
		}
		assertUnchanged(t, file, before)
		if state, _ := workspace.LoadState(inst); state.ClaudeTrustRepos != nil || !state.ReviewInstance {
			t.Errorf("state = %+v, want the record dropped and the instance marked", state)
		}
	})

	t.Run("skipped withdrawal fails the stage", func(t *testing.T) {
		home, file := stubReviewHome(t)
		inst := makeReviewInstance(t)
		reviewRepoGrants(t, home, file, inst)
		// A config file niwa can't parse makes it skip the withdrawal and
		// keep the records, as a lock held too long would.
		writeTestFile(t, file, `{"projects": `)

		var stderr bytes.Buffer
		err := markReviewInstance(&stderr, inst)
		if err == nil || !strings.Contains(err.Error(), "repository directory g/new is still recorded") {
			t.Errorf("markReviewInstance = %v, want the stage failed naming g/new", err)
		}
		state, lerr := workspace.LoadState(inst)
		if lerr != nil || !state.ReviewInstance || len(state.ClaudeTrustRepos) != 2 {
			t.Errorf("state = %+v (err %v), want the instance marked and both records kept for destroy", state, lerr)
		}
	})

	t.Run("no instance state", func(t *testing.T) {
		inst := t.TempDir()
		if err := markReviewInstance(io.Discard, inst); err != nil {
			t.Errorf("markReviewInstance with no state = %v, want nil", err)
		}
		if _, err := os.Lstat(filepath.Join(inst, workspace.StateDir)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a state directory was created (err %v)", err)
		}
	})

	t.Run("save failure fails the stage", func(t *testing.T) {
		stubReviewHome(t)
		inst := makeReviewInstance(t)
		prev := saveReviewState
		saveReviewState = func(string, *workspace.InstanceState) error { return errors.New("disk full") }
		t.Cleanup(func() { saveReviewState = prev })

		err := markReviewInstance(io.Discard, inst)
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Errorf("markReviewInstance = %v, want the save error", err)
		}
	})

	t.Run("already marked saves nothing", func(t *testing.T) {
		stubReviewHome(t)
		inst := makeReviewInstance(t)
		state, err := workspace.LoadState(inst)
		if err != nil {
			t.Fatal(err)
		}
		state.ReviewInstance = true
		if err := workspace.SaveState(inst, state); err != nil {
			t.Fatal(err)
		}
		prev := saveReviewState
		saveReviewState = func(string, *workspace.InstanceState) error { return errors.New("unexpected save") }
		t.Cleanup(func() { saveReviewState = prev })

		if err := markReviewInstance(io.Discard, inst); err != nil {
			t.Errorf("markReviewInstance = %v, want no save and no error", err)
		}
	})
}
