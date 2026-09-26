package workspace

import "testing"

// TestWorktreeFallbackDisclosure exercises the pure disclosure decision: the
// every-apply warning fires whenever the harness is unsupported; the one-time
// explainer fires only on first encounter (not already disclosed).
func TestWorktreeFallbackDisclosure(t *testing.T) {
	cases := []struct {
		name             string
		supported        bool
		alreadyDisclosed bool
		wantWarn         bool
		wantExplain      bool
	}{
		{
			name:        "supported discloses nothing",
			supported:   true,
			wantWarn:    false,
			wantExplain: false,
		},
		{
			name:      "supported even if previously disclosed stays silent",
			supported: true,
			// alreadyDisclosed has no effect on the supported branch.
			alreadyDisclosed: true,
			wantWarn:         false,
			wantExplain:      false,
		},
		{
			name:        "unsupported first encounter warns and explains",
			supported:   false,
			wantWarn:    true,
			wantExplain: true,
		},
		{
			name:             "unsupported after first encounter warns but does not re-explain",
			supported:        false,
			alreadyDisclosed: true,
			wantWarn:         true,
			wantExplain:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warn, explain := worktreeFallbackDisclosure(tc.supported, tc.alreadyDisclosed)
			if warn != tc.wantWarn {
				t.Errorf("warn = %v, want %v", warn, tc.wantWarn)
			}
			if explain != tc.wantExplain {
				t.Errorf("explain = %v, want %v", explain, tc.wantExplain)
			}
		})
	}
}

// TestWorktreeFallbackDisclosureExplainerFiresOnce simulates the lifecycle: on
// the first unsupported apply the explainer fires and is recorded; on the
// second apply (with the key disclosed) the warning still fires but the
// explainer does not.
func TestWorktreeFallbackDisclosureExplainerFiresOnce(t *testing.T) {
	// First apply: not yet disclosed.
	warn1, explain1 := worktreeFallbackDisclosure(false, noticeDisclosed(nil, noticeWorktreeFallback))
	if !warn1 || !explain1 {
		t.Fatalf("first apply: warn=%v explain=%v, want both true", warn1, explain1)
	}

	// Record the disclosure as the pipeline would.
	state := &InstanceState{DisclosedNotices: []string{noticeWorktreeFallback}}

	// Second apply: the explainer must be suppressed, the warning must persist.
	warn2, explain2 := worktreeFallbackDisclosure(false, noticeDisclosed(state, noticeWorktreeFallback))
	if !warn2 {
		t.Error("second apply: warning must still fire (current-state condition)")
	}
	if explain2 {
		t.Error("second apply: explainer must fire only once")
	}
}
