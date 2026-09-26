package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateApplyProfileArgs(t *testing.T) {
	realFile := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(realFile, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seeding temp bwrap: %v", err)
	}

	cases := []struct {
		name      string
		isRoot    bool
		bwrapPath string
		wantErr   bool
	}{
		// The elevated child must be root -- a non-root child is a misuse.
		{"not root", false, realFile, true},
		// Root but no path: nothing to install for.
		{"root empty path", true, "", true},
		// Root and a nonexistent path: the parent-resolved binary must exist.
		{"root nonexistent path", true, filepath.Join(t.TempDir(), "missing"), true},
		// Root and a real file: the only valid case.
		{"root real file", true, realFile, false},
	}
	for _, tc := range cases {
		err := validateApplyProfileArgs(tc.isRoot, tc.bwrapPath)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: validateApplyProfileArgs(%v, %q) err = %v, wantErr = %v",
				tc.name, tc.isRoot, tc.bwrapPath, err, tc.wantErr)
		}
	}
}

func TestApparmorBwrapProfile(t *testing.T) {
	p := apparmorBwrapProfile("/usr/bin/bwrap")
	for _, want := range []string{
		"/usr/bin/bwrap",     // the resolved binary path is embedded
		"flags=(unconfined)", // profile mode
		"userns,",            // the capability being granted
		"profile niwa-bwrap", // named, niwa-owned profile
		"niwa setup-sandbox", // provenance comment
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q; got:\n%s", want, p)
		}
	}
	// It must NOT reach for the global sysctl hammer.
	if strings.Contains(p, "apparmor_restrict_unprivileged_userns=0") {
		t.Errorf("profile should be a per-binary grant, not the global sysctl override")
	}
}
