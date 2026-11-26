package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/claudetrust"
)

// A review instance the removal rule keeps, where the watch would have
// destroyed it, stays on disk, but its review trust grant is withdrawn as the
// destroy would have withdrawn it: a kept review instance holds the PR's
// content and must not stay trusted while it waits for a later reap.
func TestWatchKeptReviewInstance_WithdrawsItsTrust(t *testing.T) {
	t.Run("rollback_keep", func(t *testing.T) {
		root, home := setupWatchCharEnv(t)
		fakes := installWatchCharFakes(t)
		stubWatchTrust(t, home, false)
		destroyInstanceFunc = realDestroyInstance // installWatchCharFakes restores the original
		fetchPRHeadFunc = func(_ context.Context, _, _, checkoutDir, _ string) error {
			fakePRClone(t, checkoutDir)
			return nil
		}
		claudeJSON := filepath.Join(home, ".claude.json")
		var key string
		dispatchLaunch = func(_ context.Context, req launchRequest) error {
			key = resolvedKey(t, req.InstanceDir)
			if g := recordedGrant(t, req.InstanceDir); g == nil || g.Source != claudetrust.SourceReview {
				t.Errorf("grant recorded before launch = %+v, want a review grant", g)
			}
			assertTrusted(t, claudeJSON, key)
			return errors.New("launch boom")
		}
		client := newPullHeadServer(t)
		cmd, _, _ := newWatchCharCmd()

		if err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{sandbox: true}); err == nil {
			t.Fatal("stageReview succeeded, want the launch error")
		}
		if key == "" {
			t.Fatal("the review was never launched")
		}
		assertInstanceListed(t, root, fakes.provisioned.path)
		assertNoEntry(t, claudeJSON, key)
	})

	t.Run("discard_keep", func(t *testing.T) {
		root, home := setupWatchCharEnv(t)
		installWatchCharFakes(t)
		stubWatchTrust(t, home, false)
		destroyInstanceFunc = realDestroyInstance // installWatchCharFakes restores the original
		rec := stagedReviewInstance(t, root, "the review agent's draft\n")
		if !resolveAskPosture(io.Discard, rec.InstancePath, true) {
			t.Fatal("no review trust was seeded for the staged instance")
		}
		if g := recordedGrant(t, rec.InstancePath); g == nil || g.Source != claudetrust.SourceReview {
			t.Fatalf("grant recorded = %+v, want a review grant", g)
		}
		claudeJSON := filepath.Join(home, ".claude.json")
		key := resolvedKey(t, rec.InstancePath)
		assertTrusted(t, claudeJSON, key)

		var out bytes.Buffer
		if discardStagedRecord(&out, root, rec.Handle, rec, "session no longer live", newRemovalGate(root, removeStepWatchDiscard, &out)) {
			t.Errorf("the discard reports the review discarded although the rule kept its instance:\n%s", out.String())
		}
		assertInstanceListed(t, root, rec.InstancePath)
		assertNoEntry(t, claudeJSON, key)
	})
}
