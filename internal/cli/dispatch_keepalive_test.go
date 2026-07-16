package cli

import (
	"encoding/json"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func kaBoolPtr(v bool) *bool { return &v }

// TestResolveDispatchKeepAlive covers the precedence matrix flag > downstream >
// host-default, default off, mirroring the RC resolver's test shape. Cases are
// curated equivalence classes: once a higher layer decides, the lower layers
// are short-circuited.
func TestResolveDispatchKeepAlive(t *testing.T) {
	cases := []struct {
		name       string
		flag       *bool
		downstream *bool
		host       *bool
		want       bool
	}{
		// Everything unset: default off (today's behavior).
		{"all-unset", nil, nil, nil, false},
		// Host default fills when nothing above decides.
		{"host-true", nil, nil, kaBoolPtr(true), true},
		{"host-false", nil, nil, kaBoolPtr(false), false},
		// Downstream decided: the host default never overrides it.
		{"downstream-true/host-unset", nil, kaBoolPtr(true), nil, true},
		{"downstream-true/host-false", nil, kaBoolPtr(true), kaBoolPtr(false), true},
		{"downstream-false/host-true", nil, kaBoolPtr(false), kaBoolPtr(true), false},
		// The flag wins in BOTH directions over everything below it.
		{"flag-true/host-unset", kaBoolPtr(true), nil, nil, true},
		{"flag-true/host-false", kaBoolPtr(true), nil, kaBoolPtr(false), true},
		{"flag-true/downstream-false", kaBoolPtr(true), kaBoolPtr(false), kaBoolPtr(false), true},
		{"flag-false/host-true", kaBoolPtr(false), nil, kaBoolPtr(true), false},
		{"flag-false/downstream-true", kaBoolPtr(false), kaBoolPtr(true), kaBoolPtr(true), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			global := config.GlobalSettings{KeepAliveOnDispatch: tc.host}
			inst := &instanceSettings{KeepAliveOnDispatch: tc.downstream}
			if got := resolveDispatchKeepAlive(tc.flag, global, inst); got != tc.want {
				t.Errorf("resolveDispatchKeepAlive = %v, want %v", got, tc.want)
			}
		})
	}
}

// A nil instanceSettings (settings unreadable) is treated as "downstream
// unset": the host default still fills.
func TestResolveDispatchKeepAlive_NilInstance(t *testing.T) {
	global := config.GlobalSettings{KeepAliveOnDispatch: kaBoolPtr(true)}
	if !resolveDispatchKeepAlive(nil, global, nil) {
		t.Fatal("nil instance with host-on: want true (downstream treated as unset)")
	}
}

// TestInstanceSettings_KeepAliveTagMatchesKey pins the reader's struct tag --
// a literal Go cannot const-ify -- to config.KeepAliveOnDispatchKey, so a tag
// rename fails here (mirrors TestInstanceSettings_TagMatchesKey for RC).
func TestInstanceSettings_KeepAliveTagMatchesKey(t *testing.T) {
	v := true
	data, err := json.Marshal(instanceSettings{KeepAliveOnDispatch: &v})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m[config.KeepAliveOnDispatchKey]; !ok {
		t.Fatalf("instanceSettings JSON %s lacks key %q; the struct tag drifted from the const", data, config.KeepAliveOnDispatchKey)
	}
}

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
