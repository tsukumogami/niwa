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
		{"ErrClientNotInstalled", vault.ErrClientNotInstalled},
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

// TestClientNotInstalledNarrowsUnreachable pins the one-way
// relationship between the two unreachability sentinels: an absent
// client binary IS a form of unreachability, but not every
// unreachability is an absent client binary.
//
// The asymmetry is what makes the two remedies separable, and it is
// also why any code branching on both must test the narrower one
// first.
func TestClientNotInstalledNarrowsUnreachable(t *testing.T) {
	if !errors.Is(vault.ErrClientNotInstalled, vault.ErrProviderUnreachable) {
		t.Error("ErrClientNotInstalled must satisfy errors.Is for ErrProviderUnreachable " +
			"so existing unreachability checks keep matching")
	}
	if errors.Is(vault.ErrProviderUnreachable, vault.ErrClientNotInstalled) {
		t.Error("ErrProviderUnreachable must NOT satisfy errors.Is for ErrClientNotInstalled: " +
			"an auth failure means the client ran")
	}

	wrapped := fmt.Errorf("resolving KEY: %w", vault.ErrClientNotInstalled)
	if !errors.Is(wrapped, vault.ErrProviderUnreachable) {
		t.Error("the narrow sentinel must survive a wrap as the broad one")
	}
	if !errors.Is(wrapped, vault.ErrClientNotInstalled) {
		t.Error("the narrow sentinel must survive a wrap as itself")
	}
}
