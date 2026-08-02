package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// A prompt larger than one argv element cannot be written into a feature file:
// runNiwa splits its command string on whitespace, so the payload has to be
// generated here.
//
// It also cannot be delivered as a positional argument at all. The harness
// would have to exec niwa with a 200 KB argument and hits the very limit under
// test -- "argument list too long" -- which is the same wall a developer's own
// shell hits. That is exactly why the capture is the reachable path: niwa
// builds the string itself rather than receiving it through an exec. So this
// drives the pty capture, feeding a bracketed paste past the cap.
func iDispatchAGeneratedPasteOfBytes(ctx context.Context, n int) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}

	// Recognizable head so the excerpt has something identifiable in it, then
	// filler to carry the whole past the exec cap.
	body := "panic: generated failure\n\tmain.go:1\n" + strings.Repeat("x", n)
	s.generatedPrompt = body

	input := "\\e[200~" + body + "\\e[201~\\r"
	return iRunUnderPTYWithInput(ctx, "niwa dispatch --detach", input)
}

// theWorkerReceivedAPointerToASpilledPrompt asserts the whole spill contract at
// the level only an end-to-end run can reach: the argv element names a file
// inside the instance, the fake worker resolved that path from a DIFFERENT
// working directory (so an instance-relative path fails), and the file holds
// the prompt byte for byte.
func theWorkerReceivedAPointerToASpilledPrompt(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}

	argvBytes, err := os.ReadFile(filepath.Join(s.homeDir, "dispatch-launch-argv"))
	if err != nil {
		return fmt.Errorf("reading recorded argv: %w", err)
	}
	argv := string(argvBytes)

	if !strings.Contains(argv, "file: ") {
		return fmt.Errorf("argv carries no spill pointer:\n%s", firstBytes(argv, 400))
	}
	if strings.Contains(argv, strings.Repeat("x", 5000)) {
		return fmt.Errorf("argv still carries the full prompt; it was not spilled")
	}

	// The fake claude read the path from "/" and wrote what it found here.
	resolved, err := os.ReadFile(filepath.Join(s.homeDir, "dispatch-spilled-body"))
	if err != nil {
		return fmt.Errorf("the worker did not resolve the pointer path from another directory: %w", err)
	}
	if string(resolved) != s.generatedPrompt {
		return fmt.Errorf("spilled file holds %d bytes, want the %d-byte prompt byte for byte",
			len(resolved), len(s.generatedPrompt))
	}

	inst := findDispatchInstance(s.workspaceRoot)
	if inst == "" {
		return fmt.Errorf("no dispatch instance found")
	}
	if !strings.Contains(argv, inst) {
		return fmt.Errorf("the spill file is not inside the dispatch instance %s", inst)
	}
	return nil
}

// theSpilledPromptIsGoneAfterTheInstanceIsReclaimed pins that disposal is
// instance reclamation and nothing else.
func theSpilledPromptIsGoneAfterTheInstanceIsReclaimed(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	matches, _ := filepath.Glob(filepath.Join(s.workspaceRoot, "*", ".niwa", "dispatch-prompts", "*"))
	if len(matches) != 0 {
		return fmt.Errorf("spilled prompt survived instance reclamation: %v", matches)
	}
	return nil
}

func firstBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func registerDispatchSpillSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I dispatch a generated paste of (\d+) bytes$`, iDispatchAGeneratedPasteOfBytes)
	ctx.Step(`^the worker received a pointer to a spilled prompt$`, theWorkerReceivedAPointerToASpilledPrompt)
	ctx.Step(`^the spilled prompt is gone after the instance is reclaimed$`, theSpilledPromptIsGoneAfterTheInstanceIsReclaimed)
}
