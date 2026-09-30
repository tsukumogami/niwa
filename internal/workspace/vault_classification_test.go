package workspace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
)

// lapsedCommander stands in for an `infisical` CLI whose stored login
// has lapsed: every export fails with the logged-out wording, and the
// session probe lists no session. It satisfies the infisical package's
// unexported commander interface structurally, like fakeExportCommander.
type lapsedCommander struct {
	mu              sync.Mutex
	exports, probes int
}

func (c *lapsedCommander) Run(_ context.Context, _ string, args []string) ([]byte, []byte, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(args) >= 2 && args[0] == "login" && args[1] == "status" {
		c.probes++
		return []byte(`{"sessions":[]}`), nil, 0, nil
	}
	c.exports++
	return nil, []byte("error: No valid login session found, cannot perform this action. Please run [infisical login] manually\n"), 1, nil
}

func (c *lapsedCommander) counts() (exports, probes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exports, c.probes
}

// R5: a credential-sync lookup whose provider fails with a lapsed login
// takes the existing soft path. The classified error still matches
// ErrProviderUnreachable, so the pool records the observation that
// drives the existing "personal vault provider ... unreachable" warning,
// and injectProviderTokens goes on without injecting a token. No change
// to credentialpool.go is needed for this.
func TestCredentialSync_LapsedLoginTakesSoftPath(t *testing.T) {
	t.Setenv("INFISICAL_TOKEN", "")
	cmd := &lapsedCommander{}
	bundle, prov, err := openCredentialSyncProvider(context.Background(), vault.ProviderSpec{
		Name: "personal",
		Kind: "infisical",
		Config: vault.ProviderConfig{
			"project":    "sync-project",
			"_commander": cmd,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.CloseAll()

	pool := NewCredentialPool(nil, &vaultCredLoader{
		Provider:     prov,
		ProviderName: "personal",
		PathPrefix:   CredentialSyncPathPrefix,
		SelfKind:     "infisical",
		SelfProject:  "sync-project",
	})
	vr := &config.VaultRegistry{Provider: &config.VaultProviderConfig{
		Kind:   "infisical",
		Config: map[string]any{"project": "uuid-X"},
	}}
	if err := injectProviderTokens(context.Background(), pool, vr); err != nil {
		t.Fatalf("injectProviderTokens = %v, want the soft path", err)
	}
	if _, ok := vr.Provider.Config["token"]; ok {
		t.Error("a token was injected although the lookup failed")
	}

	obs := pool.VaultUnreachableObservations()
	if len(obs) != 1 || obs[0].Kind != "infisical" || obs[0].Project != "uuid-X" || obs[0].ProviderName != "personal" {
		t.Fatalf("observations = %+v, want one for infisical/uuid-X via personal", obs)
	}
	var class *vault.FailureClass
	if !errors.As(&obs[0], &class) || class.Class != vault.ClassUnauthenticated || class.Reason != vault.ReasonLoggedOut {
		t.Errorf("observation lost its classification: %v", obs[0].Error())
	}
	if !strings.Contains(obs[0].Error(), "infisical: export exited 1: error: No valid login session found") {
		t.Errorf("observation text = %q, want today's export error", obs[0].Error())
	}
}

// Onboard's CheckProviderAuth attaches no run state, so it behaves as
// before apart from the classified error: a lapsed login is a
// per-pair vault-unreachable error with today's text, and a second pair
// on the same domain still runs its own export rather than being
// skipped.
func TestCheckProviderAuth_LapsedLoginWithoutRunState(t *testing.T) {
	setIsolatedNiwaConfigDir(t)
	t.Setenv("INFISICAL_TOKEN", "")
	cmd := &lapsedCommander{}
	override := &config.GlobalConfigOverride{Global: config.GlobalOverride{
		Vault: &config.VaultRegistry{Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "sync-project", "_commander": cmd},
		}},
	}}
	teamVault := &config.VaultRegistry{Provider: &config.VaultProviderConfig{
		Kind:   "infisical",
		Config: map[string]any{"project": "other-project"},
	}}

	result, err := CheckProviderAuth(context.Background(), override, teamVault, nil, "infisical", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if !errors.Is(result.Target.Err, vault.ErrProviderUnreachable) {
		t.Fatalf("target err = %v, want it to wrap ErrProviderUnreachable", result.Target.Err)
	}
	if strings.Contains(result.Target.Err.Error(), "skipped") ||
		!strings.Contains(result.Target.Err.Error(), "infisical: export exited 1: error: No valid login session found") {
		t.Errorf("target err = %q, want today's export error text", result.Target.Err.Error())
	}
	if len(result.OtherFailures) != 0 {
		t.Errorf("other failures = %+v, want the unreachable pair tolerated", result.OtherFailures)
	}
	if exports, probes := cmd.counts(); exports != 2 || probes != 2 {
		t.Errorf("exports=%d probes=%d, want 2 and 2 (no run state, nothing skipped)", exports, probes)
	}
}
