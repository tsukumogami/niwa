package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestComposeCodexContextLayerOrder pins the composition order: the marker on
// the first line, then the layers outermost-first, with the committed context
// file inlined inside the repository layer and the worktree framing last.
func TestComposeCodexContextLayerOrder(t *testing.T) {
	repoDir := t.TempDir()
	committed := filepath.Join(repoDir, "AGENTS.md")
	if err := os.WriteFile(committed, []byte("COMMITTED-SENTINEL\n"), 0o644); err != nil {
		t.Fatalf("writing committed context: %v", err)
	}

	got := ComposeCodexContext(CodexComposeRequest{
		Instance:             "INSTANCE-SENTINEL",
		Group:                "GROUP-SENTINEL",
		Repository:           "REPO-SENTINEL",
		CommittedContextPath: committed,
		Worktree:             "WORKTREE-SENTINEL",
	})

	if got.Refusal != nil {
		t.Fatalf("unexpected refusal: %s", got.Refusal)
	}

	lines := strings.Split(got.Content, "\n")
	if lines[0] != CodexGenerationMarker {
		t.Errorf("first line = %q, want the generation marker", lines[0])
	}

	order := []string{
		"INSTANCE-SENTINEL",
		"GROUP-SENTINEL",
		"REPO-SENTINEL",
		"COMMITTED-SENTINEL",
		"WORKTREE-SENTINEL",
	}
	prev := -1
	for _, sentinel := range order {
		idx := strings.Index(got.Content, sentinel)
		if idx < 0 {
			t.Fatalf("layer %s missing from composed document:\n%s", sentinel, got.Content)
		}
		if idx <= prev {
			t.Errorf("layer %s appears out of order at %d (previous layer at %d):\n%s", sentinel, idx, prev, got.Content)
		}
		prev = idx
	}

	if !strings.HasSuffix(got.Content, "\n") {
		t.Errorf("composed document does not end in a newline: %q", got.Content)
	}
	if got.Empty() {
		t.Error("Empty() reports true for a document with content")
	}
}

// TestComposeCodexContextPartialChain covers the common shapes: a group file
// (instance plus group) and a repository override with no worktree framing.
func TestComposeCodexContextPartialChain(t *testing.T) {
	group := ComposeCodexContext(CodexComposeRequest{
		Instance: "INSTANCE-SENTINEL",
		Group:    "GROUP-SENTINEL",
	})
	if !strings.Contains(group.Content, "INSTANCE-SENTINEL") || !strings.Contains(group.Content, "GROUP-SENTINEL") {
		t.Errorf("group composition lost a layer:\n%s", group.Content)
	}
	if strings.Index(group.Content, "INSTANCE-SENTINEL") > strings.Index(group.Content, "GROUP-SENTINEL") {
		t.Errorf("group composition is not outermost-first:\n%s", group.Content)
	}

	repo := ComposeCodexContext(CodexComposeRequest{
		Instance:   "INSTANCE-SENTINEL",
		Repository: "REPO-SENTINEL",
	})
	if !strings.Contains(repo.Content, "REPO-SENTINEL") {
		t.Errorf("repository layer missing with no group configured:\n%s", repo.Content)
	}
}

// TestComposeCodexContextNoContentWritesNothing is the never-empty rule: a
// chain with nothing in it produces no document at all, so the caller writes no
// file and the repository's own committed context file keeps the directory's
// single context slot.
func TestComposeCodexContextNoContentWritesNothing(t *testing.T) {
	got := ComposeCodexContext(CodexComposeRequest{})

	if got.Content != "" {
		t.Errorf("empty chain produced a document: %q", got.Content)
	}
	if !got.Empty() {
		t.Error("Empty() reports false for an empty chain")
	}
	if strings.Contains(got.Content, CodexGenerationMarker) {
		t.Error("the generation marker was emitted without any layer content")
	}
}

// TestComposeCodexContextWhitespaceOnlyIsEmpty pins whitespace-only layer
// content as no content. A document built from it would claim the context slot
// while saying nothing, suppressing the repository's own file.
func TestComposeCodexContextWhitespaceOnlyIsEmpty(t *testing.T) {
	repoDir := t.TempDir()
	committed := filepath.Join(repoDir, "AGENTS.md")
	if err := os.WriteFile(committed, []byte("   \n\t\n"), 0o644); err != nil {
		t.Fatalf("writing committed context: %v", err)
	}

	got := ComposeCodexContext(CodexComposeRequest{
		Instance:             "   ",
		Group:                "\n\n",
		Repository:           "\t",
		CommittedContextPath: committed,
		Worktree:             " \n ",
	})

	if got.Content != "" {
		t.Errorf("whitespace-only layers produced a document: %q", got.Content)
	}
	if got.Refusal != nil {
		t.Errorf("unexpected refusal for a readable regular file: %s", got.Refusal)
	}
}

// TestComposeCodexContextWhitespaceOnlyDoesNotSuppressOthers checks that an
// empty layer drops out without taking the rest of the chain with it.
func TestComposeCodexContextWhitespaceOnlyDoesNotSuppressOthers(t *testing.T) {
	got := ComposeCodexContext(CodexComposeRequest{
		Instance:   "  \n ",
		Repository: "REPO-SENTINEL",
	})

	if !strings.Contains(got.Content, "REPO-SENTINEL") {
		t.Fatalf("a whitespace-only outer layer suppressed the chain:\n%q", got.Content)
	}
	want := CodexGenerationMarker + "\n\nREPO-SENTINEL\n"
	if got.Content != want {
		t.Errorf("composed document =\n%q\nwant\n%q", got.Content, want)
	}
}

// TestComposeCodexContextInlinesRegularFileVerbatim checks that a regular
// committed context file reaches the composed document byte-for-byte.
func TestComposeCodexContextInlinesRegularFileVerbatim(t *testing.T) {
	repoDir := t.TempDir()
	committed := filepath.Join(repoDir, "AGENTS.md")
	body := "# Repo rules\n\n- one *emphasized* rule\n- another {not a template var}\n"
	if err := os.WriteFile(committed, []byte(body), 0o644); err != nil {
		t.Fatalf("writing committed context: %v", err)
	}

	got := ComposeCodexContext(CodexComposeRequest{
		Instance:             "INSTANCE-SENTINEL",
		CommittedContextPath: committed,
	})

	if got.Refusal != nil {
		t.Fatalf("unexpected refusal: %s", got.Refusal)
	}
	if !strings.Contains(got.Content, strings.TrimRight(body, "\n")) {
		t.Errorf("committed content was not inlined verbatim:\n%s", got.Content)
	}

	// The committed file itself is never modified.
	after, err := os.ReadFile(committed)
	if err != nil {
		t.Fatalf("re-reading committed context: %v", err)
	}
	if string(after) != body {
		t.Errorf("committed file changed: %q", string(after))
	}
}

// TestComposeCodexContextMissingCommittedFileIsNotARefusal covers the ordinary
// case: most repositories commit no context file, which is not a refusal.
func TestComposeCodexContextMissingCommittedFileIsNotARefusal(t *testing.T) {
	got := ComposeCodexContext(CodexComposeRequest{
		Instance:             "INSTANCE-SENTINEL",
		CommittedContextPath: filepath.Join(t.TempDir(), "AGENTS.md"),
	})

	if got.Refusal != nil {
		t.Errorf("missing committed file reported as a refusal: %s", got.Refusal)
	}
	if !strings.Contains(got.Content, "INSTANCE-SENTINEL") {
		t.Errorf("workspace layers did not compose:\n%s", got.Content)
	}
}

// TestComposeCodexContextRefusesSymlinkedCommittedFile is the load-bearing
// security case. A repository can commit its AGENTS.md as a symlink to any
// absolute path -- the developer's own agent credentials being the obvious
// target. The open must fail, nothing from the target may appear in the output,
// the workspace layers must still compose, and the refusal must reach the
// caller.
func TestComposeCodexContextRefusesSymlinkedCommittedFile(t *testing.T) {
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "credentials.json")
	if err := os.WriteFile(secret, []byte(`{"token":"CREDENTIAL-SENTINEL"}`), 0o600); err != nil {
		t.Fatalf("writing fake credential: %v", err)
	}

	repoDir := t.TempDir()
	committed := filepath.Join(repoDir, "AGENTS.md")
	if err := os.Symlink(secret, committed); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := ComposeCodexContext(CodexComposeRequest{
		Instance:             "INSTANCE-SENTINEL",
		Group:                "GROUP-SENTINEL",
		CommittedContextPath: committed,
	})

	if strings.Contains(got.Content, "CREDENTIAL-SENTINEL") {
		t.Fatalf("symlink target was read into the composed document:\n%s", got.Content)
	}
	if got.Refusal == nil {
		t.Fatal("symlinked committed context file did not produce a refusal")
	}
	if got.Refusal.Path != committed {
		t.Errorf("refusal path = %q, want %q", got.Refusal.Path, committed)
	}
	if !strings.Contains(got.Refusal.String(), committed) {
		t.Errorf("refusal report does not name the file: %s", got.Refusal)
	}
	if !strings.Contains(got.Content, "INSTANCE-SENTINEL") || !strings.Contains(got.Content, "GROUP-SENTINEL") {
		t.Errorf("refusal was not scoped to the inline; workspace layers are missing:\n%s", got.Content)
	}
	if !HasCodexGenerationMarker([]byte(got.Content)) {
		t.Error("composed document is missing the generation marker after a refusal")
	}
}

// TestComposeCodexContextRefusesSymlinkToInTreeFile checks that refusal is
// unconditional: an in-tree symlink target is refused on the same path, since
// resolving links at all is what the design rejected.
func TestComposeCodexContextRefusesSymlinkToInTreeFile(t *testing.T) {
	repoDir := t.TempDir()
	target := filepath.Join(repoDir, "docs.md")
	if err := os.WriteFile(target, []byte("IN-TREE-SENTINEL\n"), 0o644); err != nil {
		t.Fatalf("writing target: %v", err)
	}
	committed := filepath.Join(repoDir, "AGENTS.md")
	if err := os.Symlink(target, committed); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := ComposeCodexContext(CodexComposeRequest{
		Instance:             "INSTANCE-SENTINEL",
		CommittedContextPath: committed,
	})

	if got.Refusal == nil {
		t.Fatal("in-tree symlink was not refused")
	}
	if strings.Contains(got.Content, "IN-TREE-SENTINEL") {
		t.Errorf("symlink target was inlined:\n%s", got.Content)
	}
}

// TestComposeCodexContextRefusesDirectory covers the other non-regular file
// shape reachable without special privileges: the open succeeds, the type check
// on the open descriptor refuses.
func TestComposeCodexContextRefusesDirectory(t *testing.T) {
	repoDir := t.TempDir()
	committed := filepath.Join(repoDir, "AGENTS.md")
	if err := os.Mkdir(committed, 0o755); err != nil {
		t.Fatalf("creating directory: %v", err)
	}

	got := ComposeCodexContext(CodexComposeRequest{
		Instance:             "INSTANCE-SENTINEL",
		CommittedContextPath: committed,
	})

	if got.Refusal == nil {
		t.Fatal("a directory at the committed context path was not refused")
	}
	if !strings.Contains(got.Content, "INSTANCE-SENTINEL") {
		t.Errorf("workspace layers did not compose after refusal:\n%s", got.Content)
	}
}

// TestComposeCodexContextRefusalAloneWritesNoFile pins the interaction between
// the two rules: a refused inline with no workspace content still produces no
// document, so the caller writes nothing and reports the refusal.
func TestComposeCodexContextRefusalAloneWritesNoFile(t *testing.T) {
	repoDir := t.TempDir()
	committed := filepath.Join(repoDir, "AGENTS.md")
	if err := os.Mkdir(committed, 0o755); err != nil {
		t.Fatalf("creating directory: %v", err)
	}

	got := ComposeCodexContext(CodexComposeRequest{CommittedContextPath: committed})

	if got.Content != "" {
		t.Errorf("a refusal with no other content produced a document: %q", got.Content)
	}
	if got.Refusal == nil {
		t.Error("refusal was not reported")
	}
}

func TestHasCodexGenerationMarker(t *testing.T) {
	composed := ComposeCodexContext(CodexComposeRequest{Instance: "INSTANCE-SENTINEL"})

	tests := []struct {
		name string
		data string
		want bool
	}{
		{"composed document", composed.Content, true},
		{"marker with edits appended", composed.Content + "\nhand-edited\n", true},
		{"crlf line ending", CodexGenerationMarker + "\r\nbody\n", true},
		{"foreign file", "# A repository's own override\n", false},
		{"empty file", "", false},
		{"marker not on the first line", "preamble\n" + CodexGenerationMarker + "\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasCodexGenerationMarker([]byte(tt.data)); got != tt.want {
				t.Errorf("HasCodexGenerationMarker(%q) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

// TestCodexGenerationMarkerIsNotAComment guards the property the conflict rule
// depends on: the marker is document text, not a comment a reader could strip.
func TestCodexGenerationMarkerIsNotAComment(t *testing.T) {
	if strings.HasPrefix(CodexGenerationMarker, "<!--") || strings.HasPrefix(CodexGenerationMarker, "#") {
		t.Errorf("generation marker is a comment: %q", CodexGenerationMarker)
	}
	if strings.Contains(CodexGenerationMarker, "\n") {
		t.Errorf("generation marker spans more than one line: %q", CodexGenerationMarker)
	}
}
