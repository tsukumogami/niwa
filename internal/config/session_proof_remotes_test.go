package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSessionRemovalProofRemotes(t *testing.T) {
	result, err := Parse([]byte(removalTestHeader))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Config.Session.Removal.ProofRemotes) != 0 {
		t.Fatalf("proof_remotes = %q by default, want empty", result.Config.Session.Removal.ProofRemotes)
	}

	body := "[session.removal]\nproof_remotes = [\"https://github.com/dana/\", \"ssh://git@host:2222/team/\"]\n"
	result, err = Parse([]byte(removalTestHeader + body))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://github.com/dana/", "ssh://git@host:2222/team/"}
	if got := result.Config.Session.Removal.ProofRemotes; !slices.Equal(got, want) {
		t.Fatalf("proof_remotes = %q, want %q", got, want)
	}
}

func TestSessionRemovalProofRemotesRefusesABadPrefix(t *testing.T) {
	for _, c := range []struct{ prefix, reason string }{
		{"https://github.com/dana", "end in /"},
		{"https://github.com/", "at least one path segment"},
		{"git@github.com:dana/", "https:// or ssh://"},
		{"http://github.com/dana/", "scheme not allowed"},
		{"https://github.com/dana/../mallory/", "dot segment"},
		{"https://dana:hunter2@github.com/dana/", "userinfo"},
	} {
		body := "[session.removal]\nproof_remotes = [\"" + c.prefix + "\"]\n"
		_, err := Parse([]byte(removalTestHeader + body))
		if err == nil {
			t.Errorf("proof_remotes %q parsed; want a configuration error", c.prefix)
			continue
		}
		var rse *RemovalSettingsError
		if !errors.As(err, &rse) {
			t.Errorf("proof_remotes %q: error %v is not a removal settings error", c.prefix, err)
		}
		if !strings.Contains(err.Error(), "session.removal.proof_remotes[0]") || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("proof_remotes %q: error %q, want the field and %q", c.prefix, err, c.reason)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("proof_remotes error repeats the userinfo: %q", err)
		}
	}
}
