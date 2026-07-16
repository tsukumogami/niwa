package cli

import (
	"testing"
)

func kaBoolPtr(v bool) *bool { return &v }

// TestDispatchCmd_HasKeepAliveFlag pins the tri-state flag registration: the
// flag exists and its NoOptDefVal makes the bare `--keep-alive` form mean
// explicit true (without it, a valueless bool-shaped Var flag would reject
// `--keep-alive`).
func TestDispatchCmd_HasKeepAliveFlag(t *testing.T) {
	flag := dispatchCmd.Flags().Lookup("keep-alive")
	if flag == nil {
		t.Fatal("expected --keep-alive flag to be registered on dispatch")
	}
	if flag.NoOptDefVal != "true" {
		t.Errorf("NoOptDefVal = %q, want \"true\" so bare --keep-alive means on", flag.NoOptDefVal)
	}
}

// TestTriBoolValue covers the tri-state mechanics: the target stays nil until
// Set, Set records an explicit true or false, and a non-boolean value errors.
func TestTriBoolValue(t *testing.T) {
	var target *bool
	v := triBoolValue{&target}

	if got := v.String(); got != "" {
		t.Errorf("String() before Set = %q, want \"\" (unset)", got)
	}
	if target != nil {
		t.Fatal("target must stay nil until Set")
	}

	if err := v.Set("true"); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	if target == nil || !*target {
		t.Fatalf("Set(true) recorded %v, want explicit true", target)
	}
	if got := v.String(); got != "true" {
		t.Errorf("String() after Set(true) = %q, want \"true\"", got)
	}

	if err := v.Set("false"); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	if target == nil || *target {
		t.Fatalf("Set(false) recorded %v, want explicit false", target)
	}

	if err := v.Set("maybe"); err == nil {
		t.Error("Set(maybe) should error for a non-boolean value")
	}
}

// TestRemoteControlEnabled covers the RC gate keep-alive arms behind: injected
// this dispatch, decided downstream, or neither.
func TestRemoteControlEnabled(t *testing.T) {
	cases := []struct {
		name       string
		rcInjected bool
		inst       *instanceSettings
		want       bool
	}{
		{"injected", true, nil, true},
		{"downstream-true", false, &instanceSettings{RemoteControlAtStartup: kaBoolPtr(true)}, true},
		{"downstream-false", false, &instanceSettings{RemoteControlAtStartup: kaBoolPtr(false)}, false},
		{"downstream-unset", false, &instanceSettings{}, false},
		{"nil-instance", false, nil, false},
		{"injected-wins-over-downstream-false", true, &instanceSettings{RemoteControlAtStartup: kaBoolPtr(false)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := remoteControlEnabled(tc.rcInjected, tc.inst); got != tc.want {
				t.Errorf("remoteControlEnabled(%v, %+v) = %v, want %v", tc.rcInjected, tc.inst, got, tc.want)
			}
		})
	}
}
