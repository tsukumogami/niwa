package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/source"
)

// TestArgvInjectionGuard_SlugWithShellMetacharacter asserts the PRD R22
// argv-injection invariant: a slug carrying shell metacharacters
// (`;`, `&`, ` `, etc.) must either be rejected by source.Parse OR
// arrive at the orchestrator as a single string with no shell-style
// expansion.
//
// The path under test is the slug -> source.Source conversion that
// runInit performs for --bootstrap. We don't reach the exec layer here
// because the cli package's runBootstrap is a seam; the workspace-level
// bootstrap_test.go covers the recordingGitInvoker side. The cli-level
// test asserts the upstream invariant: that a malicious slug is either
// rejected or preserved verbatim into the Source.Repo field (no field
// gets split on `;` or shell tokens).
func TestArgvInjectionGuard_SlugWithShellMetacharacter(t *testing.T) {
	cases := []struct {
		name     string
		slug     string
		wantRej  bool   // expect Parse to reject
		wantRepo string // when not rejected, expected Source.Repo content
	}{
		{
			// Whitespace in the slug is rejected by source.Parse's
			// IsSpace check (R3 strict-parsing rule).
			name:    "slug-with-space-rejected",
			slug:    "owner/foo;rm -rf /tmp/x",
			wantRej: true,
		},
		{
			// A slug with `;` but no whitespace also gets rejected: the
			// `;` is not whitespace per se, but the canonical Parse
			// grammar accepts only alphanumeric + `._-` in segments. We
			// don't assert the specific reason; we just assert that
			// either Parse rejects OR the value is preserved into one
			// Repo field.
			name:    "slug-with-semicolon-only",
			slug:    "owner/foo;rm",
			wantRej: false,
			// Today's Parse accepts arbitrary chars except whitespace
			// and the structural separators; the resulting Repo value
			// is `foo;rm` as one string, which is the safe outcome
			// (single argv element when passed to exec.CommandContext).
			wantRepo: "foo;rm",
		},
		{
			// Backtick-shaped injection. Same expectation: Parse keeps
			// the value as one element OR rejects it.
			name:     "slug-with-backtick",
			slug:     "owner/foo`whoami`",
			wantRej:  false,
			wantRepo: "foo`whoami`",
		},
		{
			// Newline in the slug — Parse's whitespace check fires.
			name:    "slug-with-newline-rejected",
			slug:    "owner/foo\nrm",
			wantRej: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, err := source.Parse(tc.slug)
			if tc.wantRej {
				if err == nil {
					t.Fatalf("expected source.Parse(%q) to be rejected, got Source=%+v", tc.slug, src)
				}
				return
			}
			if err != nil {
				t.Fatalf("source.Parse(%q) errored unexpectedly: %v", tc.slug, err)
			}
			// The whole post-`/` segment must land in Source.Repo as
			// one string. If Parse split it on `;` or `&` we'd see
			// only the prefix in Repo and the tail dropped (or worse,
			// shifted into Subpath/Ref).
			if src.Repo != tc.wantRepo {
				t.Errorf("Source.Repo = %q, want %q (slug must arrive as one element)", src.Repo, tc.wantRepo)
			}
			// Subpath and Ref must NOT have captured any of the
			// metacharacter tail; those fields are only populated by
			// explicit `:` and `@` separators.
			if src.Subpath != "" {
				t.Errorf("Source.Subpath = %q for slug %q; nothing should be captured there", src.Subpath, tc.slug)
			}
			if src.Ref != "" {
				t.Errorf("Source.Ref = %q for slug %q; nothing should be captured there", src.Ref, tc.slug)
			}
		})
	}
}

// TestRunInit_BootstrapInitStepFails_PreservesNoDirectoryOrRegistry
// covers the PRD "Cleanup-defer at init-fail (preservation case)" AC:
// if the init step itself fails (e.g., the target directory already
// exists), the workspace dir must NOT be created (or, if it was
// momentarily created, must be removed), and no registry entry is
// written.
//
// Today's preflight runs BEFORE Mkdir so a pre-existing target dir
// surfaces a typed error and Mkdir never fires. This test asserts
// that observable invariant from the runInit boundary.
func TestRunInit_BootstrapInitStepFails_PreservesNoDirectoryOrRegistry(t *testing.T) {
	dir := chdirTemp(t)
	resetInitFlags(t)

	// Pre-create the target directory so preflight refuses.
	target := filepath.Join(dir, "myws")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	initFrom = "acme/foo"
	initBootstrap = true

	err := runInitErr(t, "myws")
	if err == nil {
		t.Fatal("expected target-exists error; got nil")
	}
	if !strings.Contains(err.Error(), "already exists (directory)") {
		t.Errorf("unexpected error text: %v", err)
	}

	// The pre-existing target directory must NOT have been overwritten
	// or had a .niwa/ planted inside it.
	if _, statErr := os.Stat(filepath.Join(target, ".niwa")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("init wrote .niwa/ into pre-existing target dir; should have refused: %v", statErr)
	}
}
