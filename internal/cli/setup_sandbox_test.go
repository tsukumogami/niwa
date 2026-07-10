package cli

import (
	"strings"
	"testing"
)

func TestPlanSetupSandboxLinux(t *testing.T) {
	cases := []struct {
		name                            string
		bwrapOK, socatOK, netnsOK, root bool
		want                            setupAction
	}{
		// Missing deps dominate: setup-sandbox never installs binaries.
		{"no bwrap", false, true, false, true, actionInstallDeps},
		{"no socat", true, false, false, true, actionInstallDeps},
		{"no bwrap even if root and netns", false, true, true, true, actionInstallDeps},
		// Deps present and the namespace works -> already capable, no-op.
		{"capable non-root", true, true, true, false, actionAlreadyCapable},
		{"capable root", true, true, true, true, actionAlreadyCapable},
		// Hardened (netns fails) splits on privilege.
		{"hardened non-root", true, true, false, false, actionNeedRoot},
		{"hardened root", true, true, false, true, actionApplyProfile},
	}
	for _, tc := range cases {
		got := planSetupSandboxLinux(tc.bwrapOK, tc.socatOK, tc.netnsOK, tc.root)
		if got != tc.want {
			t.Errorf("%s: planSetupSandboxLinux(%v,%v,%v,%v) = %d, want %d",
				tc.name, tc.bwrapOK, tc.socatOK, tc.netnsOK, tc.root, got, tc.want)
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
