package workspace

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestCleanRemovedFilesSparesAnExemptPath is the second half of the conflict
// rule, and the half that decides whether the first half means anything.
//
// The sequence it reproduces is the one that actually happens: an apply writes
// niwa's document, the developer commits a file of their own at that name, and
// the next apply refuses to write there. The refused path is now a recorded path
// the current apply did not produce, which is exactly what the cleanup deletes.
// Without the exemption, the step that runs right after the refusal would undo
// it and take the developer's file with it.
func TestCleanRemovedFilesSparesAnExemptPath(t *testing.T) {
	dir := t.TempDir()
	refused := filepath.Join(dir, "AGENTS.override.md")
	stale := filepath.Join(dir, "stale.md")

	const committed = "# the repository's own file\n"
	for path, body := range map[string]string{refused: committed, stale: "gone next apply\n"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a := NewApplier(nil)
	a.Reporter = NewReporterWithTTY(io.Discard, false)

	prior := &InstanceState{ManagedFiles: []ManagedFile{
		{Path: refused, ContentHash: "old"},
		{Path: stale, ContentHash: "old"},
	}}

	// This apply produced neither path: one was refused, the other is genuinely
	// no longer declared.
	a.cleanRemovedFiles(prior, &pipelineResult{exemptPaths: []string{refused}})

	got, err := os.ReadFile(refused)
	if err != nil {
		t.Fatalf("the refused path was deleted by cleanup: %v", err)
	}
	if string(got) != committed {
		t.Errorf("the refused path was rewritten: %q", got)
	}

	// The exemption is narrow: a path that simply stopped being declared is
	// still cleaned up, or the cleanup would stop working the moment any path
	// were exempt.
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a path that is no longer declared survived cleanup: %v", err)
	}
}
