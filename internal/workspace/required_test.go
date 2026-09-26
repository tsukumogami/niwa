package workspace

import (
	"io"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
)

// requiredKeyConfig builds the smallest config that declares KEY as
// required in [env.secrets] and gives it the supplied value. A nil
// value means the key has no entry in the Values map at all, which is
// a materially different state from an empty entry.
func requiredKeyConfig(value *config.MaybeSecret) *config.WorkspaceConfig {
	values := map[string]config.MaybeSecret{}
	if value != nil {
		values["KEY"] = *value
	}
	return &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values:   values,
				Required: map[string]string{"KEY": "what the key is for"},
			},
		},
	}
}

// TestCheckRequiredKeysFatalOnlyOnReachableProviderMiss walks the
// states a required key can be in and pins which single one is fatal.
//
// The rule under test is strict-when-reachable: fatality survives
// exactly where it still identifies a fault with a known owner -- a
// provider that answered, and answered that it does not hold the key.
// Every other shortfall is a property of the environment the command
// is running in, and refusing to materialize would not help.
func TestCheckRequiredKeysFatalOnlyOnReachableProviderMiss(t *testing.T) {
	marked := func(cause config.UnresolvedCause) *config.MaybeSecret {
		return &config.MaybeSecret{Unresolved: &config.Unresolved{
			Cause: cause,
			Level: config.LevelRequired,
		}}
	}
	resolved := config.MaybeSecret{Secret: secret.New([]byte("v"), secret.Origin{Key: "KEY"})}

	cases := []struct {
		name      string
		value     *config.MaybeSecret
		wantFatal bool
	}{
		{
			name:      "reachable provider does not hold the key",
			value:     marked(config.CauseKeyNotFound),
			wantFatal: true,
		},
		{
			name:      "provider unreachable",
			value:     marked(config.CauseProviderUnreachable),
			wantFatal: false,
		},
		{
			name:      "client binary not installed",
			value:     marked(config.CauseClientNotInstalled),
			wantFatal: false,
		},
		{
			name:      "reference names a provider nothing declares",
			value:     marked(config.CauseUndeclaredProvider),
			wantFatal: false,
		},
		{
			name:      "no entry in the values map at all",
			value:     nil,
			wantFatal: false,
		},
		{
			name: "empty value with no mark is a deliberate empty",
			// This is what the per-reference ?required=false opt-out
			// leaves behind, and what an author writing KEY = "" means.
			value:     &config.MaybeSecret{},
			wantFatal: false,
		},
		{
			name:      "value present",
			value:     &resolved,
			wantFatal: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkRequiredKeys(requiredKeyConfig(c.value), io.Discard)
			if c.wantFatal && err == nil {
				t.Fatal("expected a fatal required-key error")
			}
			if !c.wantFatal && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}

// TestCheckRequiredKeysErrorNamesKeyScopeAndDescription keeps the
// content of the one surviving fatal message intact: a reader must be
// able to act on it without opening the configuration.
func TestCheckRequiredKeysErrorNamesKeyScopeAndDescription(t *testing.T) {
	cfg := requiredKeyConfig(&config.MaybeSecret{Unresolved: &config.Unresolved{
		Cause:        config.CauseKeyNotFound,
		Level:        config.LevelRequired,
		ProviderKind: "fake",
	}})

	err := checkRequiredKeys(cfg, io.Discard)
	if err == nil {
		t.Fatal("expected a fatal required-key error")
	}
	for _, want := range []string{"KEY", "env.secrets", "what the key is for"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
}
