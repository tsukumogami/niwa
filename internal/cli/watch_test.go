package cli

import (
	"path/filepath"
	"testing"
)

func TestOwnerRepoFromGitURL(t *testing.T) {
	cases := []struct {
		in          string
		owner, repo string
		ok          bool
	}{
		{"git@github.com:acme/api.git", "acme", "api", true},
		{"https://github.com/acme/api.git", "acme", "api", true},
		{"https://github.com/acme/api", "acme", "api", true},
		{"git@ghe.example.com:org/sub-repo.git", "org", "sub-repo", true},
		{"", "", "", false},
		{"not-a-url", "", "", false},
	}
	for _, tc := range cases {
		owner, repo, ok := ownerRepoFromGitURL(tc.in)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo {
			t.Errorf("ownerRepoFromGitURL(%q) = (%q,%q,%v) want (%q,%q,%v)",
				tc.in, owner, repo, ok, tc.owner, tc.repo, tc.ok)
		}
	}
}

func TestValidateDraftPath(t *testing.T) {
	root := filepath.FromSlash("/ws")
	good := filepath.Join(root, "inst+watch-a-b-1-deadbeef", "watch-review-draft.md")
	if err := validateDraftPath(root, good); err != nil {
		t.Errorf("expected valid draft path to pass: %v", err)
	}

	bad := []string{
		filepath.FromSlash("/etc/passwd"),                         // outside root
		filepath.Join(root, "inst", "other.md"),                   // wrong basename
		filepath.Join(root, "..", "etc", "watch-review-draft.md"), // traversal out
		filepath.FromSlash("/wsother/inst/watch-review-draft.md"), // prefix-but-not-child
	}
	for _, p := range bad {
		if err := validateDraftPath(root, p); err == nil {
			t.Errorf("expected draft path %q to be rejected", p)
		}
	}
}
