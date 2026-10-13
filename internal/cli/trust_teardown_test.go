package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/claudetrust"
	"github.com/tsukumogami/niwa/internal/config"
)

// These tests check that every route that tears an instance down removes the
// Claude trust grant its state records, through the real DestroyInstance, and
// nothing a grant doesn't name. HOME is a t.TempDir, so the config file they
// write is never the developer's own.

// handMadeKey is a projects entry no instance owns; every teardown must leave
// it alone.
const handMadeKey = "/elsewhere/hand-made"

// seedReviewGrant writes a review grant for inst into home's Claude config,
// as resolveAskPosture would, records it in inst's state, and returns its key.
func seedReviewGrant(t *testing.T, home, inst string) string {
	t.Helper()
	out := claudetrust.Ensure(claudetrust.Env{Home: home}, inst, nil, claudetrust.SourceReview)
	if out.Grant == nil || !out.Changed || out.Warning != "" {
		t.Fatalf("seeding a review grant for %s: %+v", inst, out)
	}
	recordGrant(t, inst, out.Grant)
	assertTrusted(t, out.Grant.ConfigFile, out.Grant.Key)
	return out.Grant.Key
}

func assertInstanceGone(t *testing.T, inst string) {
	t.Helper()
	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Errorf("instance %s survived the teardown (stat err %v)", inst, err)
	}
}

// TestTeardownRoutesWithdrawRecordedGrant drives each route that destroys an
// instance -- niwa reset, dispatch's cleanup after a failed launch, and both
// reap sweeps -- with the real DestroyInstance behind it.
func TestTeardownRoutesWithdrawRecordedGrant(t *testing.T) {
	routes := []struct {
		name string
		// run provisions an instance, calls seed on it, and tears it down
		// through the route.
		run func(t *testing.T, seed func(inst string))
	}{
		{"niwa reset", func(t *testing.T, seed func(string)) {
			t.Setenv("GITHUB_TOKEN", "test-token")
			root := canonicalTempDir(t)
			niwaDir := filepath.Join(root, config.ConfigDir)
			writeTestFile(t, filepath.Join(niwaDir, config.ConfigFile), "[workspace]\nname = \"test-ws\"\n")
			// A .git directory marks the config as cloned, which reset requires.
			if err := os.MkdirAll(filepath.Join(niwaDir, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			inst := makeReapInstance(t, root, "test-ws")
			seed(inst)
			chdir(t, root)
			prevForce := resetForce
			resetForce = true
			t.Cleanup(func() { resetForce = prevForce })

			var stdout, stderr bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetContext(context.Background())
			if err := runReset(cmd, []string{"test-ws"}); err != nil {
				t.Fatalf("runReset: %v\nstderr:\n%s", err, stderr.String())
			}
			// The rebuilt instance is a new one with the option off: it
			// carries no grant forward.
			if g := recordedGrant(t, inst); g != nil {
				t.Errorf("rebuilt instance records %+v, want no grant", g)
			}
		}},
		{"dispatch failed launch", func(t *testing.T, seed func(string)) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			f := installDispatchFakes(t, root)
			destroyInstanceFunc = realDestroyInstance // installDispatchFakes restores the original
			inner := provisionInstanceFunc
			provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string, n int) (provisionResult, error) {
				res, err := inner(ctx, r, cwd, namePrefix, sep, n)
				if err == nil {
					seed(res.Path)
				}
				return res, err
			}
			dispatchLaunch = func(context.Context, launchRequest) error { return errors.New("launch boom") }

			if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
				t.Fatal("dispatch succeeded, want the launch error")
			}
			assertInstanceGone(t, f.instancePath)
		}},
		{"reap mapped sweep", func(t *testing.T, seed func(string)) {
			root := setupHookWorkspace(t, true)
			inst := makeReapInstance(t, root, "test-ws-trust")
			mapEphemeral(t, root, reapDeadSessionID, inst, true)
			seed(inst)

			n, err := reapWorkspace(root, t.TempDir(), time.Now())
			if err != nil || n != 1 {
				t.Fatalf("reapWorkspace = %d, %v; want 1, nil", n, err)
			}
			assertInstanceGone(t, inst)
		}},
		{"reap backstop sweep", func(t *testing.T, seed func(string)) {
			root := setupHookWorkspace(t, true)
			now := time.Now()
			inst := makeReapInstance(t, root, dispInstOld)
			writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
			seed(inst)

			n, err := reapBackstop(root, t.TempDir(), now)
			if err != nil || n != 1 {
				t.Fatalf("reapBackstop = %d, %v; want 1, nil", n, err)
			}
			assertInstanceGone(t, inst)
		}},
	}
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			home, file := stubReviewHome(t)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			writeTestFile(t, file, `{"projects": {"`+handMadeKey+`": {"hasTrustDialogAccepted": true}}}`)

			var key string
			r.run(t, func(inst string) { key = seedReviewGrant(t, home, inst) })
			if key == "" {
				t.Fatal("the route never reached the point where the grant is seeded")
			}
			assertNoEntry(t, file, key)
			assertTrusted(t, file, handMadeKey)
		})
	}
}

// TestReap_LeavesUnrecordedEntryAlone: an entry at a reclaimed instance's path
// that no grant records isn't niwa's, so reap leaves the config file
// byte-identical.
func TestReap_LeavesUnrecordedEntryAlone(t *testing.T) {
	_, file := stubReviewHome(t)
	root := setupHookWorkspace(t, true)
	inst := makeReapInstance(t, root, "test-ws-unrecorded")
	mapEphemeral(t, root, reapDeadSessionID, inst, true)
	keyJSON, err := json.Marshal(resolvedKey(t, inst))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, file, `{"projects": {`+string(keyJSON)+`: {"hasTrustDialogAccepted": true}}}`)
	before := statFile(t, file)

	n, err := reapWorkspace(root, t.TempDir(), time.Now())
	if err != nil || n != 1 {
		t.Fatalf("reapWorkspace = %d, %v; want 1, nil", n, err)
	}
	assertInstanceGone(t, inst)
	assertUnchanged(t, file, before)
}
