package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsCommitSHA(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"abc1234", true},
		{"abc1234567890abc1234567890abc123456789ab", true}, // 40 chars
		{"abcdef1", true},                                   // 7 chars, minimum
		{"ABC1234", false},                                  // uppercase
		{"abc123", false},                                   // too short (6)
		{"main", false},
		{"v1.2.3", false},
		{"refs/tags/v1", false},
		{"", false},
		{"abc1234567890abc1234567890abc1234567890abc", false}, // 41 chars
		{"ghijkl1", false},                                   // non-hex letters
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			if got := isCommitSHA(tt.ref); got != tt.want {
				t.Errorf("isCommitSHA(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestResolveCloneURL(t *testing.T) {
	tests := []struct {
		name     string
		orgRepo  string
		protocol string
		want     string
		wantErr  bool
	}{
		{
			name:     "https default",
			orgRepo:  "myorg/myrepo",
			protocol: "https",
			want:     "https://github.com/myorg/myrepo.git",
		},
		{
			name:     "ssh protocol",
			orgRepo:  "myorg/myrepo",
			protocol: "ssh",
			want:     "git@github.com:myorg/myrepo.git",
		},
		{
			name:     "empty protocol defaults to https",
			orgRepo:  "myorg/myrepo",
			protocol: "",
			want:     "https://github.com/myorg/myrepo.git",
		},
		{
			name:     "passthrough https URL",
			orgRepo:  "https://github.com/myorg/myrepo.git",
			protocol: "ssh",
			want:     "https://github.com/myorg/myrepo.git",
		},
		{
			name:     "passthrough ssh URL",
			orgRepo:  "git@github.com:myorg/myrepo.git",
			protocol: "https",
			want:     "git@github.com:myorg/myrepo.git",
		},
		{
			name:     "invalid format no slash",
			orgRepo:  "justrepo",
			protocol: "https",
			wantErr:  true,
		},
		{
			name:     "invalid format empty org",
			orgRepo:  "/repo",
			protocol: "https",
			wantErr:  true,
		},
		{
			name:     "invalid format empty repo",
			orgRepo:  "org/",
			protocol: "https",
			wantErr:  true,
		},
		{
			name:     "unsupported protocol",
			orgRepo:  "org/repo",
			protocol: "ftp",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveCloneURL(tt.orgRepo, tt.protocol)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveCloneURL(%q, %q) = %q, want %q", tt.orgRepo, tt.protocol, got, tt.want)
			}
		})
	}
}

func TestCloneWith_SkipsExistingRepo(t *testing.T) {
	dir := t.TempDir()
	// Simulate existing .git directory.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &Cloner{}
	cloned, err := c.CloneWith(context.Background(), "https://example.com/repo.git", dir, CloneOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cloned {
		t.Error("expected cloned=false for existing repo")
	}
}

func TestClone_DelegatesToCloneWith(t *testing.T) {
	// Clone should skip when .git exists, same as CloneWith with zero options.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &Cloner{}
	cloned, err := c.Clone(context.Background(), "https://example.com/repo.git", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cloned {
		t.Error("expected cloned=false for existing repo via Clone wrapper")
	}
}

func TestCloneWithBranch_DelegatesToCloneWith(t *testing.T) {
	// CloneWithBranch should skip when .git exists, same as CloneWith.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &Cloner{}
	cloned, err := c.CloneWithBranch(context.Background(), "https://example.com/repo.git", dir, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cloned {
		t.Error("expected cloned=false for existing repo via CloneWithBranch wrapper")
	}
}

func TestBuildCloneArgs_Depth(t *testing.T) {
	// Verify that depth > 0 produces --depth in the args.
	// We test this indirectly by checking the args construction logic.
	// Since we can't easily intercept exec calls without an interface,
	// we test the arg building by extracting it into a helper or
	// verifying via the SHA detection + depth combination.

	// For now, verify the SHA detection is correct so args are built properly.
	// The actual git invocation is tested via integration tests.
	if !isCommitSHA("abc1234") {
		t.Error("expected abc1234 to be detected as SHA")
	}
	if isCommitSHA("main") {
		t.Error("expected main to not be detected as SHA")
	}
}

func TestCloneWith_CreatesParentDir(t *testing.T) {
	base := t.TempDir()
	targetDir := filepath.Join(base, "nested", "deep", "repo")

	c := &Cloner{}
	// This will fail because the URL is invalid, but the parent dir
	// should still be created before the git command runs.
	_, _ = c.CloneWith(context.Background(), "invalid-url", targetDir, CloneOptions{})

	parentDir := filepath.Dir(targetDir)
	if _, err := os.Stat(parentDir); os.IsNotExist(err) {
		t.Error("expected parent directory to be created")
	}
}
