package functional

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runFakeInfisical shells out to the stub binary written by
// writeFakeInfisical, mirroring exactly how internal/vault/infisical's
// own subprocess calls invoke it (argv shape, stdin-fed body).
func runFakeInfisical(t *testing.T, binDir, storeDir string, stdin []byte, args ...string) (stdout, stderr []byte, exitCode int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "infisical"), args...)
	cmd.Env = append(os.Environ(), "INFISICAL_STUB_STORE_DIR="+storeDir)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitCode()
		}
		t.Fatalf("running fake infisical: %v", err)
	}
	return outBuf.Bytes(), errBuf.Bytes(), 0
}

// TestWriteFakeInfisical_SecretsSetThenExportRoundTrips pins the
// stub extension the onboard functional scenarios depend on: the
// wizard-end verification (R11) reads back, via `infisical export`,
// exactly what the individual pipeline just wrote via `infisical
// secrets set` -- both against this one hermetic stub, no REST double
// involved (that leg is a separate provider entirely: the credential-
// sync read goes through the CLI, not infisicalFakeServer).
func TestWriteFakeInfisical_SecretsSetThenExportRoundTrips(t *testing.T) {
	binDir := t.TempDir()
	if err := writeFakeInfisical(binDir); err != nil {
		t.Fatalf("writeFakeInfisical: %v", err)
	}
	storeDir := t.TempDir()

	body := "version = \"1\"\nclient_id = \"client-abc\"\nclient_secret = \"s3cr3t\\\"quote\"\n"
	_, stderr, exitCode := runFakeInfisical(t, binDir, storeDir, []byte(body),
		"secrets", "set", "p-proj-1=@/dev/stdin",
		"--path", "/niwa/provider-auth/infisical",
		"--env", "dev",
		"--projectId", "personal-proj",
	)
	if exitCode != 0 {
		t.Fatalf("secrets set failed (exit %d): %s", exitCode, stderr)
	}

	stdout, stderr, exitCode := runFakeInfisical(t, binDir, storeDir, nil,
		"export",
		"--projectId", "personal-proj",
		"--env", "dev",
		"--path", "/niwa/provider-auth/infisical",
		"--format", "json",
	)
	if exitCode != 0 {
		t.Fatalf("export failed (exit %d): %s", exitCode, stderr)
	}

	var decoded map[string]string
	if err := json.Unmarshal(stdout, &decoded); err != nil {
		t.Fatalf("export output is not valid JSON: %v (output: %q)", err, stdout)
	}
	got, ok := decoded["p-proj-1"]
	if !ok {
		t.Fatalf("export output missing key \"p-proj-1\": %v", decoded)
	}
	// The stub's line-based JSON encoder drops a single trailing
	// newline (awk's line-splitting has no way to distinguish "ended
	// with \n" from "didn't"); a TOML parser tolerates a missing
	// final newline, so this approximation is fine for what R11's
	// verification actually needs -- comparing against the trimmed
	// body, not the exact byte-for-byte original.
	wantTrimmed := body[:len(body)-1]
	if got != wantTrimmed {
		t.Errorf("round-tripped body = %q, want %q", got, wantTrimmed)
	}
}

// TestWriteFakeInfisical_ExportWithNoStoredSecretsIsEmptyObject pins
// the pre-existing default (no scenario has stored anything at this
// path yet): export must still return `{}`, not an error and not a
// stale entry from an unrelated (project, env, path).
func TestWriteFakeInfisical_ExportWithNoStoredSecretsIsEmptyObject(t *testing.T) {
	binDir := t.TempDir()
	if err := writeFakeInfisical(binDir); err != nil {
		t.Fatalf("writeFakeInfisical: %v", err)
	}
	storeDir := t.TempDir()

	stdout, stderr, exitCode := runFakeInfisical(t, binDir, storeDir, nil,
		"export",
		"--projectId", "nonexistent-proj",
		"--env", "dev",
		"--path", "/niwa/provider-auth/infisical",
		"--format", "json",
	)
	if exitCode != 0 {
		t.Fatalf("export failed (exit %d): %s", exitCode, stderr)
	}
	if string(bytes.TrimSpace(stdout)) != "{}" {
		t.Errorf("stdout = %q, want {}", stdout)
	}
}

// TestWriteFakeInfisical_SecretsSetFailDoesNotPersist confirms an
// induced store-write failure (INFISICAL_STUB_SECRETS_SET_FAIL) still
// leaves no entry behind -- a scenario asserting a store failure must
// not find a stale value if it then probes export.
func TestWriteFakeInfisical_SecretsSetFailDoesNotPersist(t *testing.T) {
	binDir := t.TempDir()
	if err := writeFakeInfisical(binDir); err != nil {
		t.Fatalf("writeFakeInfisical: %v", err)
	}
	storeDir := t.TempDir()

	cmd := exec.Command(filepath.Join(binDir, "infisical"),
		"secrets", "set", "p-proj-1=@/dev/stdin",
		"--path", "/niwa/provider-auth/infisical",
		"--env", "dev",
		"--projectId", "personal-proj",
	)
	cmd.Env = append(os.Environ(),
		"INFISICAL_STUB_STORE_DIR="+storeDir,
		"INFISICAL_STUB_SECRETS_SET_FAIL=1",
	)
	cmd.Stdin = bytes.NewReader([]byte("version = \"1\"\n"))
	if err := cmd.Run(); err == nil {
		t.Fatal("want a non-zero exit from the induced store-write failure")
	}

	stdout, _, exitCode := runFakeInfisical(t, binDir, storeDir, nil,
		"export",
		"--projectId", "personal-proj",
		"--env", "dev",
		"--path", "/niwa/provider-auth/infisical",
		"--format", "json",
	)
	if exitCode != 0 {
		t.Fatalf("export failed unexpectedly (exit %d)", exitCode)
	}
	if string(bytes.TrimSpace(stdout)) != "{}" {
		t.Errorf("stdout = %q, want {} (a failed set must not persist)", stdout)
	}
}
