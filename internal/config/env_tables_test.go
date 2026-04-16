package config

import (
	"strings"
	"testing"
)

// TestEnvVarsReservedKeywordAsScalarRejected covers the case where a
// user writes `required = "..."` (or `recommended`, `optional`) as a
// scalar under an env.vars / env.secrets / claude.env.vars /
// claude.env.secrets table. The three keywords are reserved for
// description sub-tables; accepting the scalar silently would make the
// variable vanish from Values and the mistake would be invisible until
// resolution. The error must name both the offending path and the
// sub-table the user should move to, so either intent (declare a var
// with that name / declare description metadata) can be acted on.
func TestEnvVarsReservedKeywordAsScalarRejected(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantParent   string // e.g. "env.vars"
		wantSubtable string // e.g. "[env.vars.required]"
		wantReserved string // e.g. "\"required\""
	}{
		{
			name: "env.vars required scalar",
			input: `
[workspace]
name = "ws"

[env.vars]
required = "abc"
`,
			wantParent:   "[env.vars]",
			wantSubtable: "[env.vars.required]",
			wantReserved: `"required"`,
		},
		{
			name: "env.secrets optional scalar",
			input: `
[workspace]
name = "ws"

[env.secrets]
optional = "xyz"
`,
			wantParent:   "[env.secrets]",
			wantSubtable: "[env.secrets.optional]",
			wantReserved: `"optional"`,
		},
		{
			name: "claude.env.vars recommended scalar",
			input: `
[workspace]
name = "ws"

[claude.env.vars]
recommended = "hint"
`,
			wantParent:   "[claude.env.vars]",
			wantSubtable: "[claude.env.vars.recommended]",
			wantReserved: `"recommended"`,
		},
		{
			name: "claude.env.secrets required scalar",
			input: `
[workspace]
name = "ws"

[claude.env.secrets]
required = "token"
`,
			wantParent:   "[claude.env.secrets]",
			wantSubtable: "[claude.env.secrets.required]",
			wantReserved: `"required"`,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected error for scalar at reserved key under %s", tt.wantParent)
			}
			msg := err.Error()
			for _, want := range []string{"reserved", tt.wantParent, tt.wantSubtable, tt.wantReserved} {
				if !strings.Contains(msg, want) {
					t.Errorf("error missing %q; got: %v", want, err)
				}
			}
		})
	}
}

// TestEnvVarsReservedSubtableStillAccepted confirms the fix does not
// regress the legitimate use: a TOML table at the reserved key should
// continue to populate Required / Recommended / Optional. Without this
// test the scalar-detection path could be made overly eager and break
// the canonical description-map form.
func TestEnvVarsReservedSubtableStillAccepted(t *testing.T) {
	input := `
[workspace]
name = "ws"

[env.vars]
LOG_LEVEL = "debug"

[env.vars.required]
GH_TOKEN = "GitHub token used by niwa apply"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := result.Config.Env.Vars.Values["LOG_LEVEL"].Plain; got != "debug" {
		t.Errorf("Values[LOG_LEVEL] = %q, want debug", got)
	}
	if got := result.Config.Env.Vars.Required["GH_TOKEN"]; got != "GitHub token used by niwa apply" {
		t.Errorf("Required[GH_TOKEN] = %q, want description", got)
	}
}
