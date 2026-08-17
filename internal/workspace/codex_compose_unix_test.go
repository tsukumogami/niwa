//go:build unix

package workspace

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestComposeCodexContextRefusesFIFO covers the non-regular file that would
// otherwise hang the apply: a read-only open of a FIFO blocks until a writer
// appears, so the open carries O_NONBLOCK and the type check refuses. The test
// completing at all is half of what it asserts.
func TestComposeCodexContextRefusesFIFO(t *testing.T) {
	repoDir := t.TempDir()
	committed := filepath.Join(repoDir, "AGENTS.md")
	if err := syscall.Mkfifo(committed, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	done := make(chan CodexComposition, 1)
	go func() {
		done <- ComposeCodexContext(CodexComposeRequest{
			Instance:             "INSTANCE-SENTINEL",
			CommittedContextPath: committed,
		})
	}()

	select {
	case got := <-done:
		if got.Refusal == nil {
			t.Fatal("a FIFO at the committed context path was not refused")
		}
		if !strings.Contains(got.Content, "INSTANCE-SENTINEL") {
			t.Errorf("workspace layers did not compose after refusal:\n%s", got.Content)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("composing against a FIFO blocked instead of refusing")
	}
}
