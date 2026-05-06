package workspace

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateInitName_Accepts(t *testing.T) {
	t.Parallel()
	valid := []string{
		"my-ws",
		"my_ws",
		"my.ws",
		"MyWS123",
		"a",
		"workspace-1",
		"workspace.config.v2",
		"a-b_c.d",
	}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateInitName(name); err != nil {
				t.Fatalf("ValidateInitName(%q) returned error %v; want nil", name, err)
			}
		})
	}
}

func TestValidateInitName_RejectsCharSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
	}{
		{"whitespace", "foo bar"},
		{"forward_slash", "foo/bar"},
		{"backslash", "foo\\bar"},
		{"colon", "foo:bar"},
		{"asterisk", "foo*bar"},
		{"hash", "foo#bar"},
		{"newline", "foo\nbar"},
		{"tab", "foo\tbar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateInitName(tc.in)
			if err == nil {
				t.Fatalf("ValidateInitName(%q) returned nil; want error", tc.in)
			}
			msg := err.Error()
			quoted := fmt.Sprintf("%q", tc.in)
			if !strings.Contains(msg, quoted) {
				t.Errorf("error %q does not contain quoted input %s", msg, quoted)
			}
			if !strings.Contains(msg, "alphanumerics") {
				t.Errorf("error %q does not describe allowed character set", msg)
			}
		})
	}
}

func TestValidateInitName_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		mustHave []string
	}{
		{".", []string{`"."`, "path-traversal", "alphanumerics"}},
		{"..", []string{`".."`, "path-traversal", "alphanumerics"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			err := ValidateInitName(tc.in)
			if err == nil {
				t.Fatalf("ValidateInitName(%q) returned nil; want error", tc.in)
			}
			msg := err.Error()
			for _, want := range tc.mustHave {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q missing %q", msg, want)
				}
			}
		})
	}
}

func TestValidateInitName_RejectsNiwaMarker(t *testing.T) {
	t.Parallel()
	err := ValidateInitName(".niwa")
	if err == nil {
		t.Fatalf(`ValidateInitName(".niwa") returned nil; want error`)
	}
	msg := err.Error()
	for _, want := range []string{`".niwa"`, "niwa", "alphanumerics"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestValidateInitName_RejectsEmpty(t *testing.T) {
	t.Parallel()
	err := ValidateInitName("")
	if err == nil {
		t.Fatal(`ValidateInitName("") returned nil; want error`)
	}
	msg := err.Error()
	if !strings.Contains(msg, "empty") {
		t.Errorf("error %q does not state the name cannot be empty", msg)
	}
	if !strings.Contains(msg, "alphanumerics") {
		t.Errorf("error %q does not describe allowed character set", msg)
	}
}
