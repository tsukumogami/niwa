package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/source"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestRunInit_FromSlugWithRef covers `--from owner/repo@ref`: the ref
// reaches the materializer intact, and the "Initializing from:" line
// names the clone URL without the ref glued onto the repo name. A ref
// containing `/` (most branch names) used to fail before anything was
// fetched, because the displayed URL was built by re-parsing the raw
// --from string as a bare org/repo.
func TestRunInit_FromSlugWithRef(t *testing.T) {
	cases := []struct {
		name     string
		from     string
		wantRef  string
		wantLine string
	}{
		{
			name:     "ref with slash",
			from:     "acme/cfg@feature/x",
			wantRef:  "feature/x",
			wantLine: "Initializing from: git@github.com:acme/cfg.git (ref feature/x)",
		},
		{
			name:     "ref without slash",
			from:     "acme/cfg@v1.2.0",
			wantRef:  "v1.2.0",
			wantLine: "Initializing from: git@github.com:acme/cfg.git (ref v1.2.0)",
		},
		{
			name:     "explicit host with ref",
			from:     "gitlab.com/acme/cfg@feature/x",
			wantRef:  "feature/x",
			wantLine: "Initializing from: https://gitlab.com/acme/cfg.git (ref feature/x)",
		},
		{
			name:     "no ref",
			from:     "acme/cfg",
			wantRef:  "",
			wantLine: "Initializing from: git@github.com:acme/cfg.git",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chdirTemp(t)
			resetInitFlags(t)
			initFrom = tc.from

			stop := errors.New("stop after materialize")
			var gotSrc source.Source
			called := false
			stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
				called = true
				gotSrc = src
				return 0, stop
			})

			var out bytes.Buffer
			initCmd.SetOut(&out)
			t.Cleanup(func() { initCmd.SetOut(nil) })

			err := runInitErr(t, "ws")
			if !called {
				t.Fatalf("materializer never ran; runInit returned %v", err)
			}
			if gotSrc.Owner != "acme" || gotSrc.Repo != "cfg" || gotSrc.Ref != tc.wantRef {
				t.Errorf("materializer got %+v, want owner=acme repo=cfg ref=%q", gotSrc, tc.wantRef)
			}

			var line string
			for _, l := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(l, "Initializing from:") {
					line = l
				}
			}
			if line != tc.wantLine {
				t.Errorf("printed line mismatch:\n  got:  %q\n  want: %q", line, tc.wantLine)
			}
		})
	}
}

// TestDescribeCloneSource covers the helper shared by init and
// `config set global`: URL input is echoed unchanged, slug input is
// rendered from its parsed fields with the ref kept out of the URL.
func TestDescribeCloneSource(t *testing.T) {
	cases := []struct {
		raw      string
		protocol string
		want     string
	}{
		{"acme/cfg", "https", "https://github.com/acme/cfg.git"},
		{"acme/cfg@release/2.x", "https", "https://github.com/acme/cfg.git (ref release/2.x)"},
		{"acme/cfg:sub/dir@main", "ssh", "git@github.com:acme/cfg.git (ref main)"},
		{"https://github.com/acme/cfg.git", "ssh", "https://github.com/acme/cfg.git"},
		{"git@github.com:acme/cfg.git", "https", "git@github.com:acme/cfg.git"},
	}
	for _, tc := range cases {
		src, err := parseInitSource(tc.raw)
		if err != nil {
			t.Fatalf("parseInitSource(%q): %v", tc.raw, err)
		}
		got, err := describeCloneSource(tc.raw, src, tc.protocol)
		if err != nil {
			t.Fatalf("describeCloneSource(%q): %v", tc.raw, err)
		}
		if got != tc.want {
			t.Errorf("describeCloneSource(%q, %s) = %q, want %q", tc.raw, tc.protocol, got, tc.want)
		}
	}
}
