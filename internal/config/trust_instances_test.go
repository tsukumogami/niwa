package config

import (
	"strings"
	"testing"
)

// TestParseTrustInstances covers [claude] trust_instances in the workspace
// config: absent is nil, so the overlay can still decide; a boolean is kept
// as given, false included, so the base can refuse the overlay's choice.
func TestParseTrustInstances(t *testing.T) {
	tests := []struct {
		name string
		body string
		want *bool
	}{
		{name: "absent", body: ""},
		{name: "true", body: "\n[claude]\ntrust_instances = true\n", want: boolPtr(true)},
		{name: "false", body: "\n[claude]\ntrust_instances = false\n", want: boolPtr(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Parse([]byte("[workspace]\nname = \"ws\"\n" + tt.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := result.Config.Claude.TrustInstances
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("TrustInstances = %v, want %v", fmtBoolPtr(got), fmtBoolPtr(tt.want))
			}
			for _, w := range result.Warnings {
				if strings.Contains(w, "trust_instances") {
					t.Errorf("unexpected warning about the key: %s", w)
				}
			}
		})
	}
}

// TestParseTrustInstancesRejectsNonBoolean holds the key to a TOML boolean in
// both files that can set it, and checks the error names the key so the user
// can find the line.
func TestParseTrustInstancesRejectsNonBoolean(t *testing.T) {
	for _, value := range []string{`"yes"`, `1`, `"true"`} {
		t.Run("base "+value, func(t *testing.T) {
			_, err := Parse([]byte("[workspace]\nname = \"ws\"\n\n[claude]\ntrust_instances = " + value + "\n"))
			if err == nil {
				t.Fatal("Parse accepted a non-boolean trust_instances")
			}
			if !strings.Contains(err.Error(), "trust_instances") {
				t.Errorf("error %q does not name trust_instances", err)
			}
		})
		t.Run("overlay "+value, func(t *testing.T) {
			_, err := ParseOverlay(writeOverlayFile(t, "[claude]\ntrust_instances = "+value+"\n"))
			if err == nil {
				t.Fatal("ParseOverlay accepted a non-boolean trust_instances")
			}
			if !strings.Contains(err.Error(), "trust_instances") {
				t.Errorf("error %q does not name trust_instances", err)
			}
		})
	}
}

// TestParseOverlayTrustInstances checks the overlay decodes the key.
func TestParseOverlayTrustInstances(t *testing.T) {
	o, err := ParseOverlay(writeOverlayFile(t, "[claude]\ntrust_instances = true\n"))
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}
	if o.Claude.TrustInstances == nil || !*o.Claude.TrustInstances {
		t.Errorf("overlay TrustInstances = %v, want true", fmtBoolPtr(o.Claude.TrustInstances))
	}

	o, err = ParseOverlay(writeOverlayFile(t, "[claude]\nplugins = [\"p\"]\n"))
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}
	if o.Claude.TrustInstances != nil {
		t.Errorf("overlay without the key: TrustInstances = %v, want nil", *o.Claude.TrustInstances)
	}
}

func fmtBoolPtr(b *bool) string {
	if b == nil {
		return "nil"
	}
	if *b {
		return "true"
	}
	return "false"
}
