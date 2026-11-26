package config

import (
	"strings"
	"testing"
)

const removalTestHeader = "[workspace]\nname = \"x\"\n"

func TestSessionRemovalParse(t *testing.T) {
	tests := []struct {
		name             string
		body             string
		wantIgnored      bool
		wantScratch      bool
		wantDisposable   int
		wantErrSubstring string
	}{
		{
			name: "defaults",
			body: "",
		},
		{
			name: "ignored files disposable",
			body: "[session.removal]\nignored_files = \"disposable\"\n",
		},
		{
			name:        "ignored files unique",
			body:        "[session.removal]\nignored_files = \"unique\"\n",
			wantIgnored: true,
		},
		{
			name: "job scratch disposable",
			body: "[session.removal]\njob_scratch = \"disposable\"\n",
		},
		{
			name:        "job scratch unique",
			body:        "[session.removal]\njob_scratch = \"unique\"\n",
			wantScratch: true,
		},
		{
			name:             "ignored files bad value",
			body:             "[session.removal]\nignored_files = \"keep\"\n",
			wantErrSubstring: "session.removal.ignored_files",
		},
		{
			name:             "job scratch bad value",
			body:             "[session.removal]\njob_scratch = \"Unique\"\n",
			wantErrSubstring: "session.removal.job_scratch",
		},
		{
			name:           "disposable entry accepted",
			body:           "[[session.removal.disposable]]\npattern = \"tmp/*.log\"\nreview = \"storage review\"\n",
			wantDisposable: 1,
		},
		{
			name:           "double star pattern accepted",
			body:           "[[session.removal.disposable]]\npattern = \"**/cache/\"\nreview = \"storage review\"\n",
			wantDisposable: 1,
		},
		{
			name:             "disposable entry missing review",
			body:             "[[session.removal.disposable]]\npattern = \"a\"\nreview = \"r\"\n[[session.removal.disposable]]\npattern = \"b\"\nreview = \"r\"\n[[session.removal.disposable]]\npattern = \"c\"\n",
			wantErrSubstring: "session.removal.disposable[2]: review is required",
		},
		{
			name:             "disposable entry empty review",
			body:             "[[session.removal.disposable]]\npattern = \"a\"\nreview = \"   \"\n",
			wantErrSubstring: "session.removal.disposable[0]: review is required",
		},
		{
			name:             "disposable entry missing pattern",
			body:             "[[session.removal.disposable]]\nreview = \"r\"\n",
			wantErrSubstring: "session.removal.disposable[0]: pattern is required",
		},
		{
			name:             "bad glob segment",
			body:             "[[session.removal.disposable]]\npattern = \"build/[a\"\nreview = \"r\"\n",
			wantErrSubstring: "session.removal.disposable[0]: pattern",
		},
		{
			name:             "pattern with dot dot segment",
			body:             "[[session.removal.disposable]]\npattern = \"../x\"\nreview = \"r\"\n",
			wantErrSubstring: "session.removal.disposable[0]: pattern",
		},
		{
			name:             "pattern with empty segment",
			body:             "[[session.removal.disposable]]\npattern = \"a//b\"\nreview = \"r\"\n",
			wantErrSubstring: "session.removal.disposable[0]: pattern",
		},
		{
			name:             "pattern with leading slash",
			body:             "[[session.removal.disposable]]\npattern = \"/abs\"\nreview = \"r\"\n",
			wantErrSubstring: "session.removal.disposable[0]: pattern",
		},
		{
			name:             "pattern with backslash",
			body:             "[[session.removal.disposable]]\npattern = 'a\\b'\nreview = \"r\"\n",
			wantErrSubstring: "session.removal.disposable[0]: pattern",
		},
		{
			name:             "catch-all star",
			body:             "[[session.removal.disposable]]\npattern = \"*\"\nreview = \"r\"\n",
			wantErrSubstring: `session.removal.disposable[0]: pattern "*": matches every path`,
		},
		{
			name:             "catch-all double star",
			body:             "[[session.removal.disposable]]\npattern = \"**\"\nreview = \"r\"\n",
			wantErrSubstring: `session.removal.disposable[0]: pattern "**": matches every path`,
		},
		{
			name:             "catch-all star then double star",
			body:             "[[session.removal.disposable]]\npattern = \"*/**\"\nreview = \"r\"\n",
			wantErrSubstring: `session.removal.disposable[0]: pattern "*/**": matches every path`,
		},
		{
			name:             "catch-all double star directory",
			body:             "[[session.removal.disposable]]\npattern = \"**/\"\nreview = \"r\"\n",
			wantErrSubstring: `session.removal.disposable[0]: pattern "**/": matches every path`,
		},
		{
			name:             "catch-all question marks",
			body:             "[[session.removal.disposable]]\npattern = \"a\"\nreview = \"r\"\n[[session.removal.disposable]]\npattern = \"?*/\"\nreview = \"r\"\n",
			wantErrSubstring: `session.removal.disposable[1]: pattern "?*/": matches every path`,
		},
		{
			name:           "star with a name accepted",
			body:           "[[session.removal.disposable]]\npattern = \"**/*.log\"\nreview = \"r\"\n",
			wantDisposable: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Parse([]byte(removalTestHeader + tc.body))
			if tc.wantErrSubstring != "" {
				if err == nil {
					t.Fatalf("Parse succeeded, want error containing %q", tc.wantErrSubstring)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstring) {
					t.Fatalf("Parse error = %q, want it to contain %q", err, tc.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			r := result.Config.Session.Removal
			if got := r.IgnoredUnique(); got != tc.wantIgnored {
				t.Errorf("IgnoredUnique() = %v, want %v", got, tc.wantIgnored)
			}
			if got := r.JobScratchUnique(); got != tc.wantScratch {
				t.Errorf("JobScratchUnique() = %v, want %v", got, tc.wantScratch)
			}
			if got := len(r.Disposable); got != tc.wantDisposable {
				t.Errorf("len(Disposable) = %d, want %d", got, tc.wantDisposable)
			}
		})
	}
}

func TestRepoBuildTreesParse(t *testing.T) {
	valid := removalTestHeader + "[repos.app]\nbuild_trees = [\"target/\", \"web/dist/\"]\n"
	result, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := result.Config.Repos["app"].BuildTrees
	if len(got) != 2 || got[0] != "target/" || got[1] != "web/dist/" {
		t.Fatalf("BuildTrees = %q, want [target/ web/dist/]", got)
	}

	refused := []struct {
		name  string
		entry string
	}{
		{"empty", `""`},
		{"absolute", `"/abs/"`},
		{"no trailing slash", `"target"`},
		{"just slash", `"/"`},
		{"star", `"out/*/"`},
		{"question mark", `"out?/"`},
		{"bracket", `"out[1]/"`},
		{"brace", `"out{a,b}/"`},
		{"backslash", `'out\x/'`},
		{"empty segment", `"a//b/"`},
		{"dot segment", `"./target/"`},
		{"dot dot segment", `"a/../b/"`},
		{"control character", `"a\tb/"`},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			input := removalTestHeader + "[repos.app]\nbuild_trees = [\"ok/\", " + tc.entry + "]\n"
			_, err := Parse([]byte(input))
			if err == nil {
				t.Fatalf("Parse succeeded for build_trees entry %s, want refusal", tc.entry)
			}
			if !strings.Contains(err.Error(), "repos.app.build_trees[1]: ") {
				t.Fatalf("Parse error = %q, want it to name repos.app.build_trees[1]", err)
			}
		})
	}
}
