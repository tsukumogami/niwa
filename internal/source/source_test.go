package source

import (
	"strings"
	"testing"
)

func TestParse_Valid(t *testing.T) {
	cases := []struct {
		name string
		slug string
		want Source
	}{
		{
			name: "bare org/repo",
			slug: "tsukumogami/niwa",
			want: Source{Owner: "tsukumogami", Repo: "niwa"},
		},
		{
			name: "with ref",
			slug: "tsukumogami/niwa@main",
			want: Source{Owner: "tsukumogami", Repo: "niwa", Ref: "main"},
		},
		{
			name: "with subpath",
			slug: "tsukumogami/brain:.niwa",
			want: Source{Owner: "tsukumogami", Repo: "brain", Subpath: ".niwa"},
		},
		{
			name: "with subpath and ref",
			slug: "tsukumogami/brain:.niwa@v1.2.0",
			want: Source{Owner: "tsukumogami", Repo: "brain", Subpath: ".niwa", Ref: "v1.2.0"},
		},
		{
			name: "with explicit host",
			slug: "gitlab.com/group/sub",
			want: Source{Host: "gitlab.com", Owner: "group", Repo: "sub"},
		},
		{
			name: "with host + subpath + ref",
			slug: "gitlab.com/group/sub:dot-niwa@v2",
			want: Source{Host: "gitlab.com", Owner: "group", Repo: "sub", Subpath: "dot-niwa", Ref: "v2"},
		},
		{
			name: "subpath with slashes",
			slug: "org/brain:teams/research",
			want: Source{Owner: "org", Repo: "brain", Subpath: "teams/research"},
		},
		{
			name: "subpath that resolves to a file",
			slug: "org/dot-niwa:niwa.toml",
			want: Source{Owner: "org", Repo: "dot-niwa", Subpath: "niwa.toml"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.slug)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tc.slug, got, tc.want)
			}
		})
	}
}

func TestParse_StrictRejections(t *testing.T) {
	cases := []struct {
		name      string
		slug      string
		wantError string // substring
	}{
		{name: "empty", slug: "", wantError: "empty"},
		{name: "empty subpath after colon (R3a)", slug: "org/repo:", wantError: "empty subpath"},
		{name: "ref before subpath (R3b)", slug: "org/repo@v1:.niwa", wantError: "wrong position"},
		{name: "embedded whitespace (R3c)", slug: "org/repo: .niwa", wantError: "whitespace"},
		{name: "leading whitespace (R3c)", slug: " org/repo", wantError: "whitespace"},
		{name: "tab in slug (R3c)", slug: "org/repo\t", wantError: "whitespace"},
		{name: "multiple colons (R3d)", slug: "org/repo:a:b", wantError: "multiple `:`"},
		{name: "multiple ats (R3e)", slug: "org/repo@v1@v2", wantError: "multiple `@`"},
		{name: "missing owner", slug: "/repo", wantError: "empty owner"},
		{name: "missing repo", slug: "org/", wantError: "empty repo"},
		{name: "single segment", slug: "onlyone", wantError: "owner/repo"},
		{name: "too many segments", slug: "a/b/c/d", wantError: "unexpected path segments"},
		{name: "empty ref after at", slug: "org/repo@", wantError: "empty ref"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.slug)
			if err == nil {
				t.Fatalf("Parse(%q) returned no error; expected %q", tc.slug, tc.wantError)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("Parse(%q) error = %q, want substring %q", tc.slug, err.Error(), tc.wantError)
			}
		})
	}
}

func TestSource_StringRoundTrip(t *testing.T) {
	cases := []string{
		"org/repo",
		"org/repo@main",
		"org/repo:.niwa",
		"org/repo:.niwa@v1.2.0",
		"org/repo:teams/research",
		"gitlab.com/group/sub",
		"gitlab.com/group/sub:dot-niwa@v2",
	}
	for _, slug := range cases {
		t.Run(slug, func(t *testing.T) {
			got, err := Parse(slug)
			if err != nil {
				t.Fatalf("Parse(%q): %v", slug, err)
			}
			if rendered := got.String(); rendered != slug {
				t.Errorf("round-trip mismatch: Parse(%q).String() = %q", slug, rendered)
			}
		})
	}
}

func TestSource_CloneURL(t *testing.T) {
	cases := []struct {
		name     string
		src      Source
		protocol string
		want     string
	}{
		{
			name:     "github https",
			src:      Source{Owner: "org", Repo: "repo"},
			protocol: "https",
			want:     "https://github.com/org/repo.git",
		},
		{
			name:     "github ssh",
			src:      Source{Owner: "org", Repo: "repo"},
			protocol: "ssh",
			want:     "git@github.com:org/repo.git",
		},
		{
			name:     "github default protocol falls back to https",
			src:      Source{Owner: "org", Repo: "repo"},
			protocol: "",
			want:     "https://github.com/org/repo.git",
		},
		{
			name:     "non-github always https",
			src:      Source{Host: "gitlab.com", Owner: "org", Repo: "repo"},
			protocol: "ssh",
			want:     "https://gitlab.com/org/repo.git",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.src.CloneURL(tc.protocol)
			if err != nil {
				t.Fatalf("CloneURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("CloneURL(%q) = %q, want %q", tc.protocol, got, tc.want)
			}
		})
	}
}

func TestSource_TarballURL(t *testing.T) {
	cases := []struct {
		name string
		src  Source
		want string
	}{
		{
			name: "github with ref",
			src:  Source{Owner: "org", Repo: "repo", Ref: "v1.0"},
			want: "https://api.github.com/repos/org/repo/tarball/v1.0",
		},
		{
			name: "github without ref",
			src:  Source{Owner: "org", Repo: "repo"},
			want: "https://api.github.com/repos/org/repo/tarball/HEAD",
		},
		{
			name: "non-github returns empty",
			src:  Source{Host: "gitlab.com", Owner: "org", Repo: "repo"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.src.TarballURL(); got != tc.want {
				t.Errorf("TarballURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSource_CommitsAPIURL(t *testing.T) {
	src := Source{Owner: "org", Repo: "repo", Ref: "main"}
	if got := src.CommitsAPIURL(""); got != "https://api.github.com/repos/org/repo/commits/main" {
		t.Errorf("CommitsAPIURL: %q", got)
	}
	if got := src.CommitsAPIURL("v2"); got != "https://api.github.com/repos/org/repo/commits/v2" {
		t.Errorf("CommitsAPIURL ref override: %q", got)
	}
	noRef := Source{Owner: "org", Repo: "repo"}
	if got := noRef.CommitsAPIURL(""); got != "https://api.github.com/repos/org/repo/commits/HEAD" {
		t.Errorf("CommitsAPIURL no-ref: %q", got)
	}
}

func TestSource_OverlayDerivedSource(t *testing.T) {
	cases := []struct {
		name string
		in   Source
		want Source
	}{
		{
			name: "whole-repo source uses source repo + -overlay",
			in:   Source{Owner: "org", Repo: "dot-niwa"},
			want: Source{Owner: "org", Repo: "dot-niwa-overlay"},
		},
		{
			name: "subpath source uses last segment + -overlay",
			in:   Source{Owner: "org", Repo: "brain", Subpath: ".niwa"},
			want: Source{Owner: "org", Repo: ".niwa-overlay"},
		},
		{
			name: "multi-segment subpath uses last segment only",
			in:   Source{Owner: "org", Repo: "brain", Subpath: "teams/research"},
			want: Source{Owner: "org", Repo: "research-overlay"},
		},
		{
			name: "ref is inherited",
			in:   Source{Owner: "org", Repo: "brain", Subpath: ".niwa", Ref: "v1"},
			want: Source{Owner: "org", Repo: ".niwa-overlay", Ref: "v1"},
		},
		{
			name: "host is inherited",
			in:   Source{Host: "gitlab.com", Owner: "org", Repo: "brain", Subpath: ".niwa"},
			want: Source{Host: "gitlab.com", Owner: "org", Repo: ".niwa-overlay"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.OverlayDerivedSource(); got != tc.want {
				t.Errorf("OverlayDerivedSource() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSource_DisplayRef(t *testing.T) {
	if (Source{Ref: "main"}).DisplayRef() != "main" {
		t.Error("ref-set DisplayRef returned wrong value")
	}
	if (Source{}).DisplayRef() != "(default branch)" {
		t.Error("empty-ref DisplayRef should return '(default branch)'")
	}
}

func TestSource_IsGitHub(t *testing.T) {
	if !(Source{}).IsGitHub() {
		t.Error("empty Host should be IsGitHub")
	}
	if !(Source{Host: "github.com"}).IsGitHub() {
		t.Error("github.com Host should be IsGitHub")
	}
	if (Source{Host: "gitlab.com"}).IsGitHub() {
		t.Error("gitlab.com Host should not be IsGitHub")
	}
}
