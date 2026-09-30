package cli

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// lapsedInfisical stands in for an `infisical` CLI whose stored login
// has lapsed: exports fail with the logged-out wording and the session
// probe lists no session. It satisfies the infisical package's
// unexported commander interface structurally.
type lapsedInfisical struct {
	exports, probes atomic.Int32
}

func (c *lapsedInfisical) Run(_ context.Context, _ string, args []string) ([]byte, []byte, int, error) {
	if len(args) >= 2 && args[0] == "login" && args[1] == "status" {
		c.probes.Add(1)
		return []byte(`{"sessions":[]}`), nil, 0, nil
	}
	c.exports.Add(1)
	return nil, []byte("error: No valid login session found, cannot perform this action. Please run [infisical login] manually\n"), 1, nil
}

// `niwa status --check-vault` attaches no run state, so a lapsed login
// changes nothing for it but the error's classification: every recorded
// source is still re-resolved with its own export (none is skipped),
// and each failure reads exactly as before.
func TestDetectVaultRotations_LapsedLoginWithoutRunState(t *testing.T) {
	t.Setenv("INFISICAL_TOKEN", "")
	reg := vault.NewRegistry()
	if err := reg.Register(infisical.NewFactory()); err != nil {
		t.Fatal(err)
	}
	cmd := &lapsedInfisical{}
	ctx := context.Background()
	bundle, err := reg.Build(ctx, []vault.ProviderSpec{{
		Name: "team", Kind: "infisical",
		Config: vault.ProviderConfig{"name": "team", "project": "proj", "_commander": cmd},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.CloseAll()

	state := &workspace.InstanceState{ManagedFiles: []workspace.ManagedFile{{
		Path: "/tmp/lapsed.env",
		Sources: []workspace.SourceEntry{
			{Kind: workspace.SourceKindVault, SourceID: "team/KEY_A", VersionToken: "a"},
			{Kind: workspace.SourceKindVault, SourceID: "team/KEY_B", VersionToken: "b"},
		},
	}}}

	rotations := detectVaultRotations(ctx, state, bundle)
	if len(rotations) != 1 || len(rotations[0].ChangedSources) != 2 {
		t.Fatalf("rotations = %+v, want one file with two failed sources", rotations)
	}
	const want = "infisical: export exited 1: error: No valid login session found, cannot perform this action. Please run [infisical login] manually"
	for _, cs := range rotations[0].ChangedSources {
		if cs.Err == nil || cs.Err.Error() != want {
			t.Errorf("%s: err = %v, want %q", cs.SourceID, cs.Err, want)
		}
	}
	if cmd.exports.Load() != 2 {
		t.Errorf("exports = %d, want 2 (nothing skipped without a run state)", cmd.exports.Load())
	}
}
