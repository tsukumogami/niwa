package cli

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func rcBoolPtr(v bool) *bool { return &v }

func TestResolveDispatchRemoteControl(t *testing.T) {
	const apiKey = "ANTHROPIC_API_KEY=sk-ant-xxx"
	cleanEnv := []string{"PATH=/usr/bin", "HOME=/home/dev"}
	apiKeyEnv := append([]string{apiKey}, cleanEnv...)

	// host {unset,false,true} × downstream {nil,false,true} × API key {absent,present}.
	cases := []struct {
		name        string
		host        *bool
		downstream  *bool
		env         []string
		wantInject  bool
		wantWarning bool
	}{
		// Host unset -> never inject, regardless of anything else (today's behavior).
		{"host-unset", nil, nil, cleanEnv, false, false},
		{"host-unset/downstream-true", nil, rcBoolPtr(true), cleanEnv, false, false},
		{"host-unset/api-key", nil, nil, apiKeyEnv, false, false},
		// Host false -> never inject.
		{"host-false", rcBoolPtr(false), nil, cleanEnv, false, false},
		{"host-false/downstream-false", rcBoolPtr(false), rcBoolPtr(false), cleanEnv, false, false},
		// Host true, downstream unset, clean env -> inject.
		{"host-true/unset/clean", rcBoolPtr(true), nil, cleanEnv, true, false},
		// Host true, downstream decided -> never inject (worker honors its own settings.json).
		{"host-true/downstream-false", rcBoolPtr(true), rcBoolPtr(false), cleanEnv, false, false},
		{"host-true/downstream-true", rcBoolPtr(true), rcBoolPtr(true), cleanEnv, false, false},
		// Host true, downstream unset, API key present -> warn, do not inject.
		{"host-true/unset/api-key", rcBoolPtr(true), nil, apiKeyEnv, false, true},
		// Downstream decided wins even with an API key present (no warning: niwa is not enabling RC).
		{"host-true/downstream-false/api-key", rcBoolPtr(true), rcBoolPtr(false), apiKeyEnv, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			global := config.GlobalSettings{RemoteControlOnDispatch: tc.host}
			var inst *instanceSettings
			if tc.downstream != nil {
				inst = &instanceSettings{RemoteControlAtStartup: tc.downstream}
			} else {
				inst = &instanceSettings{}
			}
			inject, warning := resolveDispatchRemoteControl(global, inst, tc.env)
			if inject != tc.wantInject {
				t.Errorf("inject = %v, want %v", inject, tc.wantInject)
			}
			if (warning != "") != tc.wantWarning {
				t.Errorf("warning = %q, want warning present = %v", warning, tc.wantWarning)
			}
		})
	}
}

// A nil instanceSettings (settings unreadable) is treated as "downstream unset".
func TestResolveDispatchRemoteControl_NilInstance(t *testing.T) {
	global := config.GlobalSettings{RemoteControlOnDispatch: rcBoolPtr(true)}
	inject, warning := resolveDispatchRemoteControl(global, nil, []string{"PATH=/usr/bin"})
	if !inject || warning != "" {
		t.Fatalf("nil instance with host-on/clean-env: inject=%v warning=%q, want inject=true warning=\"\"", inject, warning)
	}
}

func TestApiKeyAuthForced(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want bool
	}{
		{"absent", []string{"PATH=/usr/bin"}, false},
		{"present", []string{"ANTHROPIC_API_KEY=sk-ant"}, true},
		{"present-empty", []string{"ANTHROPIC_API_KEY="}, false},
		{"prefix-collision", []string{"ANTHROPIC_API_KEY_EXTRA=x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := apiKeyAuthForced(tc.env); got != tc.want {
				t.Errorf("apiKeyAuthForced(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
