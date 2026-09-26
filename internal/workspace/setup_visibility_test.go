package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

// writeSetupScript creates repoDir/scripts/setup/name with the given body and
// mode 0755, creating parents as needed.
func writeSetupScript(t *testing.T, repoDir, name, body string) {
	t.Helper()
	dir := filepath.Join(repoDir, "scripts", "setup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating setup dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatalf("writing script %s: %v", name, err)
	}
}

// TestSetupScriptOutputIsDurable is the regression test for issue #239: a
// failing setup script's own explanation of why it failed must survive to the
// operator in both run modes. Before the fix, script output was routed through
// Reporter.Status — a no-op off a TTY, and a transient spinner frame on one —
// so permanent output was empty in both modes.
func TestSetupScriptOutputIsDurable(t *testing.T) {
	const marker = "NIWA-SETUP-MARKER: dependency 'foo' is missing"

	for _, isTTY := range []bool{false, true} {
		t.Run(fmt.Sprintf("isTTY=%v", isTTY), func(t *testing.T) {
			repoDir := t.TempDir()
			// Emit a run of lines so the marker is not the single line
			// that a TTY spinner frame happens to leave behind.
			body := "#!/bin/sh\n" +
				"i=0; while [ $i -lt 20 ]; do echo \"filler line $i\"; i=$((i+1)); done\n" +
				"echo \"" + marker + "\" >&2\n" +
				"i=0; while [ $i -lt 20 ]; do echo \"more filler $i\"; i=$((i+1)); done\n" +
				"exit 1\n"
			writeSetupScript(t, repoDir, "01-fails.sh", body)

			var buf syncBuffer
			r := NewReporterWithTTY(&buf, isTTY)
			result := RunSetupScripts(repoDir, "scripts/setup", r, nil)
			r.Log("end") // tear the spinner down so the stream is final

			if len(result.Scripts) != 1 || result.Scripts[0].Error == nil {
				t.Fatalf("expected one failing script result, got %+v", result.Scripts)
			}

			perm := permanentOutput(buf.String())
			if !strings.Contains(perm, marker) {
				t.Errorf("failing script's stderr did not reach permanent output.\npermanent output was:\n%s", perm)
			}
		})
	}
}

// TestSetupScriptOutputPrefixAndAnnouncement verifies that each script is
// announced before it runs and that its output carries the [<repo>/<script>]
// prefix that distinguishes it from niwa's own lines.
func TestSetupScriptOutputPrefixAndAnnouncement(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("creating repo dir: %v", err)
	}
	writeSetupScript(t, repoDir, "01-hello.sh", "#!/bin/sh\necho hello from the script\n")

	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)
	RunSetupScripts(repoDir, "scripts/setup", r, nil)

	perm := permanentOutput(buf.String())
	if !strings.Contains(perm, "running setup script myapp/01-hello.sh") {
		t.Errorf("missing per-script announcement in:\n%s", perm)
	}
	if !strings.Contains(perm, "[myapp/01-hello.sh] hello from the script") {
		t.Errorf("missing prefixed script output in:\n%s", perm)
	}
}

// TestSetupScriptStopsOnFirstErrorWithOutput strengthens the existing
// stop-on-first-error test: the third script must not run, and the output must
// show the first two and not the third.
func TestSetupScriptStopsOnFirstErrorWithOutput(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("creating repo dir: %v", err)
	}
	writeSetupScript(t, repoDir, "01-ok.sh", "#!/bin/sh\necho FIRST-RAN\n")
	writeSetupScript(t, repoDir, "02-fails.sh", "#!/bin/sh\necho SECOND-RAN\nexit 3\n")
	writeSetupScript(t, repoDir, "03-never.sh", "#!/bin/sh\necho THIRD-RAN\n")

	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)
	result := RunSetupScripts(repoDir, "scripts/setup", r, nil)

	if len(result.Scripts) != 2 {
		t.Fatalf("expected 2 script results (stopped after the failure), got %d: %+v",
			len(result.Scripts), result.Scripts)
	}
	if result.Scripts[1].Error == nil {
		t.Error("expected an error on the second script")
	}

	perm := permanentOutput(buf.String())
	if !strings.Contains(perm, "FIRST-RAN") {
		t.Errorf("first script's output missing from:\n%s", perm)
	}
	if !strings.Contains(perm, "SECOND-RAN") {
		t.Errorf("failing script's output missing from:\n%s", perm)
	}
	if strings.Contains(perm, "THIRD-RAN") {
		t.Errorf("third script ran after a failure; output was:\n%s", perm)
	}
}

// TestSetupScriptsContinueToNextRepo verifies that a repo whose setup fails
// does not stop the next repo from running its scripts, and that the second
// repo's output still reaches the operator.
func TestSetupScriptsContinueToNextRepo(t *testing.T) {
	base := t.TempDir()
	alpha := filepath.Join(base, "alpha")
	beta := filepath.Join(base, "beta")
	for _, d := range []string{alpha, beta} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("creating repo dir: %v", err)
		}
	}
	writeSetupScript(t, alpha, "01-fails.sh", "#!/bin/sh\necho ALPHA-RAN\nexit 1\n")
	writeSetupScript(t, beta, "01-ok.sh", "#!/bin/sh\necho BETA-RAN\n")

	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)

	// Mirrors Step 6.75: each repo gets its turn regardless of the previous
	// repo's outcome.
	alphaResult := RunSetupScripts(alpha, "scripts/setup", r, nil)
	betaResult := RunSetupScripts(beta, "scripts/setup", r, nil)

	if len(alphaResult.Scripts) != 1 || alphaResult.Scripts[0].Error == nil {
		t.Fatalf("expected alpha's script to fail, got %+v", alphaResult.Scripts)
	}
	if len(betaResult.Scripts) != 1 || betaResult.Scripts[0].Error != nil {
		t.Fatalf("expected beta's script to succeed, got %+v", betaResult.Scripts)
	}

	perm := permanentOutput(buf.String())
	if !strings.Contains(perm, "[alpha/01-fails.sh] ALPHA-RAN") {
		t.Errorf("alpha's output missing from:\n%s", perm)
	}
	if !strings.Contains(perm, "[beta/01-ok.sh] BETA-RAN") {
		t.Errorf("beta's output missing from:\n%s", perm)
	}
}

// TestSetupScriptOutputIsScrubbed verifies that a registered secret echoed by
// a setup script is replaced by the redactor's placeholder before the line is
// printed.
func TestSetupScriptOutputIsScrubbed(t *testing.T) {
	const plaintext = "super-secret-token-value"

	base := t.TempDir()
	repoDir := filepath.Join(base, "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("creating repo dir: %v", err)
	}
	writeSetupScript(t, repoDir, "01-leaks.sh", "#!/bin/sh\necho \"API_KEY="+plaintext+"\"\n")

	red := secret.NewRedactor()
	red.Register([]byte(plaintext))

	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)
	RunSetupScripts(repoDir, "scripts/setup", r, red)

	perm := permanentOutput(buf.String())
	if strings.Contains(perm, plaintext) {
		t.Errorf("secret plaintext reached the operator:\n%s", perm)
	}
	if !strings.Contains(perm, "API_KEY=***") {
		t.Errorf("expected the redacted placeholder in:\n%s", perm)
	}
}

// TestSetupScriptOutputScrubbedThroughInterleavedEscape proves the ordering
// inside the scanner loop is load-bearing: a secret split by an ANSI escape
// sequence renders contiguously in a terminal, so it must be stripped before
// the redactor's substring match runs.
func TestSetupScriptOutputScrubbedThroughInterleavedEscape(t *testing.T) {
	const plaintext = "super-secret-token-value"

	base := t.TempDir()
	repoDir := filepath.Join(base, "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("creating repo dir: %v", err)
	}
	// A colorized `set -x` trace can interleave an escape sequence inside a
	// token exactly like this.
	writeSetupScript(t, repoDir, "01-leaks.sh",
		"#!/bin/sh\nprintf 'API_KEY=super-secret\\033[0m-token-value\\n'\n")

	red := secret.NewRedactor()
	red.Register([]byte(plaintext))

	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)
	RunSetupScripts(repoDir, "scripts/setup", r, red)

	perm := permanentOutput(buf.String())
	if strings.Contains(perm, plaintext) {
		t.Errorf("secret survived the escape-interleaved line:\n%s", perm)
	}
	if !strings.Contains(perm, "API_KEY=***") {
		t.Errorf("expected the redacted placeholder in:\n%s", perm)
	}
}

// TestSetupScriptFilenameIsSanitized verifies that a repo-controlled script
// filename cannot inject control bytes into the announcement or the prefix.
func TestSetupScriptFilenameIsSanitized(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "myapp")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("creating repo dir: %v", err)
	}
	// A filename carrying a carriage return and a CSI sequence.
	writeSetupScript(t, repoDir, "01-\rok\x1b[2K.sh", "#!/bin/sh\necho ran\n")

	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)
	RunSetupScripts(repoDir, "scripts/setup", r, nil)

	perm := permanentOutput(buf.String())
	if strings.ContainsAny(perm, "\r\x1b") {
		t.Errorf("control bytes from the filename reached output: %q", perm)
	}
	if !strings.Contains(perm, "[myapp/01-ok.sh] ran") {
		t.Errorf("expected the sanitized prefix in:\n%s", perm)
	}
}
