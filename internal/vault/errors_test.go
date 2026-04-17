package vault_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// TestSentinelErrorsPresent asserts AC: the sentinel error catalog
// exists and each sentinel is a distinct, non-nil error value.
func TestSentinelErrorsPresent(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"ErrKeyNotFound", vault.ErrKeyNotFound},
		{"ErrProviderUnreachable", vault.ErrProviderUnreachable},
		{"ErrProviderNameCollision", vault.ErrProviderNameCollision},
		{"ErrTeamOnlyLocked", vault.ErrTeamOnlyLocked},
	}
	seen := map[string]struct{}{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.err == nil {
				t.Fatalf("%s is nil", c.name)
			}
			msg := c.err.Error()
			if !strings.HasPrefix(msg, "vault:") {
				t.Fatalf("%s.Error() = %q, want vault: prefix", c.name, msg)
			}
			if _, dup := seen[msg]; dup {
				t.Fatalf("%s has duplicate message %q", c.name, msg)
			}
			seen[msg] = struct{}{}
		})
	}
}

// TestSentinelsWrapViaErrorsIs verifies the common pattern of
// wrapping a sentinel with fmt.Errorf("...: %w", vault.ErrKeyNotFound)
// and recovering it via errors.Is.
func TestSentinelsWrapViaErrorsIs(t *testing.T) {
	wrapped := fmt.Errorf("resolve foo: %w", vault.ErrKeyNotFound)
	if !errors.Is(wrapped, vault.ErrKeyNotFound) {
		t.Fatalf("errors.Is did not find ErrKeyNotFound through wrap")
	}
	if errors.Is(wrapped, vault.ErrProviderUnreachable) {
		t.Fatalf("errors.Is matched wrong sentinel")
	}
}
