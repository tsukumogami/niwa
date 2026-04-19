package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEnvExample writes content to a .env.example file in a temp dir and
// returns the file path. The test fails immediately if the write fails.
func writeEnvExample(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.example")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeEnvExample: %v", err)
	}
	return path
}

// TestParseDotEnvExampleBasicSyntax covers the Node-style syntax variants
// (scenario-4): single-quoted, double-quoted, unquoted, export prefix,
// CRLF, blank/comment skipping, and duplicate-key last-wins.
func TestParseDotEnvExampleBasicSyntax(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantVars map[string]string
	}{
		{
			name:     "unquoted value",
			input:    "KEY=value\n",
			wantVars: map[string]string{"KEY": "value"},
		},
		{
			name:     "single-quoted literal",
			input:    "KEY='hello world'\n",
			wantVars: map[string]string{"KEY": "hello world"},
		},
		{
			name:     "single-quoted preserves backslash",
			input:    "KEY='no\\nescape'\n",
			wantVars: map[string]string{"KEY": `no\nescape`},
		},
		{
			name:     "double-quoted newline escape",
			input:    `KEY="line1\nline2"` + "\n",
			wantVars: map[string]string{"KEY": "line1\nline2"},
		},
		{
			name:     "double-quoted tab escape",
			input:    `KEY="col1\tcol2"` + "\n",
			wantVars: map[string]string{"KEY": "col1\tcol2"},
		},
		{
			name:     "double-quoted escaped quote",
			input:    `KEY="say \"hi\""` + "\n",
			wantVars: map[string]string{"KEY": `say "hi"`},
		},
		{
			name:     "double-quoted escaped backslash",
			input:    `KEY="back\\slash"` + "\n",
			wantVars: map[string]string{"KEY": `back\slash`},
		},
		{
			name:     "export prefix stripped",
			input:    "export KEY=value\n",
			wantVars: map[string]string{"KEY": "value"},
		},
		{
			name:     "export prefix with single-quoted value",
			input:    "export KEY='quoted'\n",
			wantVars: map[string]string{"KEY": "quoted"},
		},
		{
			name:     "CRLF normalised",
			input:    "KEY=value\r\n",
			wantVars: map[string]string{"KEY": "value"},
		},
		{
			name:     "blank lines skipped",
			input:    "\nKEY=value\n\n",
			wantVars: map[string]string{"KEY": "value"},
		},
		{
			name:     "comment lines skipped",
			input:    "# this is a comment\nKEY=value\n",
			wantVars: map[string]string{"KEY": "value"},
		},
		{
			name:     "inline comment stripped from unquoted",
			input:    "KEY=value # comment\n",
			wantVars: map[string]string{"KEY": "value"},
		},
		{
			name:     "empty value included",
			input:    "KEY=\n",
			wantVars: map[string]string{"KEY": ""},
		},
		{
			name:     "duplicate key last wins",
			input:    "KEY=first\nKEY=second\n",
			wantVars: map[string]string{"KEY": "second"},
		},
		{
			name:  "multiple keys",
			input: "A=1\nB=2\nC=3\n",
			wantVars: map[string]string{
				"A": "1",
				"B": "2",
				"C": "3",
			},
		},
		{
			name:     "key with digits and underscore",
			input:    "MY_KEY_2=value\n",
			wantVars: map[string]string{"MY_KEY_2": "value"},
		},
		{
			name:     "export with double-quoted escapes",
			input:    `export MSG="hello\tworld"` + "\n",
			wantVars: map[string]string{"MSG": "hello\tworld"},
		},
		{
			name:     "unclosed single quote treated as literal",
			input:    "KEY='unclosed\n",
			wantVars: map[string]string{"KEY": "unclosed"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeEnvExample(t, tc.input)
			vars, warnings, err := parseDotEnvExample(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(vars) != len(tc.wantVars) {
				t.Fatalf("vars count = %d, want %d; got %v", len(vars), len(tc.wantVars), vars)
			}
			for k, want := range tc.wantVars {
				got, ok := vars[k]
				if !ok {
					t.Errorf("key %q missing from result", k)
					continue
				}
				if got != want {
					t.Errorf("vars[%q] = %q, want %q", k, got, want)
				}
			}
			_ = warnings
		})
	}
}

// TestParseDotEnvExamplePerLineWarnings covers per-line tolerance (scenario-5):
// invalid key characters, missing '=', and unknown double-quote escapes each
// produce a per-line warning with no value text; parsing continues; error is nil.
func TestParseDotEnvExamplePerLineWarnings(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantWarnSubs []string // substrings that must appear in some warning
		wantNoSubs   []string // substrings that must NOT appear in any warning
		wantVarKeys  []string // keys that should be present in the result
	}{
		{
			name:         "invalid key character hyphen",
			input:        "MY-KEY=value\n",
			wantWarnSubs: []string{"invalid key name"},
			wantNoSubs:   []string{"value"},
		},
		{
			name:         "invalid key character space",
			input:        "MY KEY=value\n",
			wantWarnSubs: []string{"invalid key name"},
			wantNoSubs:   []string{"value"},
		},
		{
			name:         "invalid key character dollar",
			input:        "$KEY=secret123\n",
			wantWarnSubs: []string{"invalid key name"},
			wantNoSubs:   []string{"secret123"},
		},
		{
			name:         "missing equals separator",
			input:        "NODIVIDER\n",
			wantWarnSubs: []string{"missing '=' separator"},
		},
		{
			name:         "unknown double-quote escape",
			input:        `KEY="bad\xescape"` + "\n",
			wantWarnSubs: []string{"unrecognised escape sequence"},
			wantNoSubs:   []string{"bad", "xescape"},
		},
		{
			name:  "bad line followed by good line",
			input: "BAD-KEY=ignore\nGOOD=kept\n",
			// warning for the bad line
			wantWarnSubs: []string{"invalid key name"},
			// good key must survive
			wantVarKeys: []string{"GOOD"},
			wantNoSubs:  []string{"ignore"},
		},
		{
			name:  "multiple bad lines parse continues",
			input: "BAD-KEY=a\nNODIVIDER\nGOOD=b\n",
			// two warnings
			wantWarnSubs: []string{"invalid key name", "missing '=' separator"},
			wantVarKeys:  []string{"GOOD"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeEnvExample(t, tc.input)
			vars, warnings, err := parseDotEnvExample(path)
			if err != nil {
				t.Fatalf("unexpected whole-file error: %v", err)
			}

			// Check expected warning substrings appear.
			for _, sub := range tc.wantWarnSubs {
				found := false
				for _, w := range warnings {
					if strings.Contains(w, sub) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected warning containing %q; got warnings: %v", sub, warnings)
				}
			}

			// Check forbidden substrings do not appear in any warning.
			for _, sub := range tc.wantNoSubs {
				for _, w := range warnings {
					if strings.Contains(w, sub) {
						t.Errorf("warning %q contains forbidden substring %q", w, sub)
					}
				}
			}

			// Check expected keys are present.
			for _, k := range tc.wantVarKeys {
				if _, ok := vars[k]; !ok {
					t.Errorf("expected key %q in vars, got %v", k, vars)
				}
			}
		})
	}
}

// TestParseDotEnvExampleWarningFormat verifies that per-line warnings follow the
// "file:line:problem" format and contain no value text.
func TestParseDotEnvExampleWarningFormat(t *testing.T) {
	input := "NODIVIDER\n"
	path := writeEnvExample(t, input)
	_, warnings, err := parseDotEnvExample(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning")
	}
	for _, w := range warnings {
		// Must contain the file path.
		if !strings.Contains(w, path) {
			t.Errorf("warning %q does not contain file path %q", w, path)
		}
		// Must contain a line number (":1:" for line 1).
		if !strings.Contains(w, ":1:") {
			t.Errorf("warning %q does not contain line number ':1:'", w)
		}
	}
}

// TestParseDotEnvExampleWholeFileErrors covers whole-file failure modes (scenario-6).
// Precondition: tests only use paths that exist (permission-denied test creates then
// restricts access; binary and size tests create the file before calling the function).
func TestParseDotEnvExampleWholeFileErrors(t *testing.T) {
	t.Run("permission denied", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root bypasses permission checks")
		}
		path := writeEnvExample(t, "KEY=value\n")
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

		vars, _, err := parseDotEnvExample(path)
		if err == nil {
			t.Fatal("expected error for permission denied, got nil")
		}
		if vars != nil {
			t.Errorf("expected nil vars on whole-file error, got %v", vars)
		}
	})

	t.Run("binary content", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env.example")
		// Write content with NUL bytes.
		content := []byte("KEY=value\x00binary\n")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		vars, _, err := parseDotEnvExample(path)
		if err == nil {
			t.Fatal("expected error for binary content, got nil")
		}
		if vars != nil {
			t.Errorf("expected nil vars on whole-file error, got %v", vars)
		}
	})

	t.Run("exceeds 512 KB", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env.example")
		// Write 513 KB of valid-looking content.
		chunk := []byte("# comment line padding\n")
		var buf []byte
		for len(buf) <= maxEnvExampleSize {
			buf = append(buf, chunk...)
		}
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		vars, _, err := parseDotEnvExample(path)
		if err == nil {
			t.Fatal("expected error for oversized file, got nil")
		}
		if vars != nil {
			t.Errorf("expected nil vars on whole-file error, got %v", vars)
		}
	})
}

// TestParseDotEnvExampleNoValueInWarnings is a meta-test asserting that none of
// the per-line warning strings produced by the parser contain the literal value
// text from the input. This enforces R22 for the parser layer.
func TestParseDotEnvExampleNoValueInWarnings(t *testing.T) {
	// Each entry pairs an input line with the secret value that must not appear
	// in any warning string.
	cases := []struct {
		name        string
		input       string
		secretValue string
	}{
		{
			name:        "invalid key hides value",
			input:       "BAD-KEY=s3cr3t_p@ssw0rd\n",
			secretValue: "s3cr3t_p@ssw0rd",
		},
		{
			name:        "unknown escape hides value",
			input:       `KEY="prefix\qsuffix_topsecret"` + "\n",
			secretValue: "topsecret",
		},
		{
			name:        "invalid key dollar hides value",
			input:       "$MYKEY=abcdefghijklmnop\n",
			secretValue: "abcdefghijklmnop",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeEnvExample(t, tc.input)
			_, warnings, err := parseDotEnvExample(path)
			if err != nil {
				t.Fatalf("unexpected whole-file error: %v", err)
			}
			for _, w := range warnings {
				if strings.Contains(w, tc.secretValue) {
					t.Errorf("warning %q contains secret value substring %q", w, tc.secretValue)
				}
			}
		})
	}
}
