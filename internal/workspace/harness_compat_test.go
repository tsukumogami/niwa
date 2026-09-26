package workspace

import "testing"

func TestSupportsWorktreeHooks(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		// At baseline: supported.
		{name: "at baseline", output: "2.1.183 (Claude Code)", want: true},
		{name: "at baseline bare", output: "2.1.183", want: true},

		// Above baseline: supported.
		{name: "above baseline patch", output: "2.1.184 (Claude Code)", want: true},
		{name: "above baseline minor", output: "2.2.0 (Claude Code)", want: true},
		{name: "above baseline major", output: "3.0.0 (Claude Code)", want: true},

		// Below baseline: unsupported.
		{name: "below baseline patch", output: "2.1.182 (Claude Code)", want: false},
		{name: "below baseline minor", output: "2.0.0 (Claude Code)", want: false},
		{name: "below baseline major", output: "1.9.9 (Claude Code)", want: false},

		// Unparseable / empty: optimistic — supported.
		{name: "empty", output: "", want: true},
		{name: "whitespace only", output: "   \n", want: true},
		{name: "no semver", output: "Claude Code", want: true},
		{name: "partial version", output: "2.1 (Claude Code)", want: true},

		// Defensive parsing tolerates surrounding text and whitespace.
		{name: "leading whitespace", output: "  2.1.183 (Claude Code)\n", want: true},
		{name: "prefixed text below baseline", output: "version 2.0.5 build", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supportsWorktreeHooks(tt.output); got != tt.want {
				t.Errorf("supportsWorktreeHooks(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func TestParseClaudeVersion(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOK    bool
		wantMajor int
		wantMinor int
		wantPatch int
	}{
		{name: "standard output", input: "2.1.183 (Claude Code)", wantOK: true, wantMajor: 2, wantMinor: 1, wantPatch: 183},
		{name: "bare semver", input: "3.0.0", wantOK: true, wantMajor: 3, wantMinor: 0, wantPatch: 0},
		{name: "with whitespace", input: "  2.1.184  \n", wantOK: true, wantMajor: 2, wantMinor: 1, wantPatch: 184},
		{name: "prefixed text", input: "version 2.0.5 build", wantOK: true, wantMajor: 2, wantMinor: 0, wantPatch: 5},
		{name: "empty", input: "", wantOK: false},
		{name: "no semver", input: "Claude Code", wantOK: false},
		{name: "two components only", input: "2.1", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseClaudeVersion(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("parseClaudeVersion(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.major != tt.wantMajor || got.minor != tt.wantMinor || got.patch != tt.wantPatch {
				t.Errorf("parseClaudeVersion(%q) = %+v, want {%d %d %d}",
					tt.input, got, tt.wantMajor, tt.wantMinor, tt.wantPatch)
			}
		})
	}
}

func TestCompareClaudeVersions(t *testing.T) {
	tests := []struct {
		name string
		a    claudeVersion
		b    claudeVersion
		want int
	}{
		{name: "equal", a: claudeVersion{2, 1, 183}, b: claudeVersion{2, 1, 183}, want: 0},
		{name: "patch greater", a: claudeVersion{2, 1, 184}, b: claudeVersion{2, 1, 183}, want: 1},
		{name: "patch lesser", a: claudeVersion{2, 1, 182}, b: claudeVersion{2, 1, 183}, want: -1},
		{name: "minor greater", a: claudeVersion{2, 2, 0}, b: claudeVersion{2, 1, 183}, want: 1},
		{name: "minor lesser", a: claudeVersion{2, 0, 999}, b: claudeVersion{2, 1, 0}, want: -1},
		{name: "major greater", a: claudeVersion{3, 0, 0}, b: claudeVersion{2, 9, 9}, want: 1},
		{name: "major lesser", a: claudeVersion{1, 9, 9}, b: claudeVersion{2, 0, 0}, want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareClaudeVersions(tt.a, tt.b); got != tt.want {
				t.Errorf("compareClaudeVersions(%+v, %+v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
