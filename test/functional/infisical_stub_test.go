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

// TestWriteFakeInfisical_ExportFailKnob pins the opt-in export failure: each
// named failure exits 1 with its fixed stderr text and prints nothing on
// stdout, whatever the store holds.
func TestWriteFakeInfisical_ExportFailKnob(t *testing.T) {
	binDir := t.TempDir()
	if err := writeFakeInfisical(binDir); err != nil {
		t.Fatalf("writeFakeInfisical: %v", err)
	}
	cases := map[string]string{
		"no-valid-session":     "No valid login session found",
		"could-not-find-login": "we couldn't find your logged in details",
		"session-expired":      "Your login session has expired",
		"response-401":         "\nResponse Code: 401\n",
		"response-403":         "\nResponse Code: 403\n",
		"response-404":         "\nResponse Code: 404\n",
		"response-500":         "\nResponse Code: 500\n",
		"connection-refused":   "connect: connection refused",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(filepath.Join(binDir, "infisical"),
				"export", "--projectId", "p", "--env", "dev", "--path", "/", "--format", "json")
			cmd.Env = append(os.Environ(),
				"INFISICAL_STUB_STORE_DIR="+t.TempDir(),
				"INFISICAL_STUB_EXPORT_FAIL="+name,
			)
			var outBuf, errBuf bytes.Buffer
			cmd.Stdout = &outBuf
			cmd.Stderr = &errBuf
			err := cmd.Run()
			exitErr, ok := err.(*exec.ExitError)
			if !ok || exitErr.ExitCode() != 1 {
				t.Fatalf("want exit 1, got %v", err)
			}
			if outBuf.Len() != 0 {
				t.Errorf("stdout = %q, want empty", outBuf.String())
			}
			if !bytes.Contains(errBuf.Bytes(), []byte(want)) {
				t.Errorf("stderr = %q, want it to contain %q", errBuf.String(), want)
			}
		})
	}
}

// TestWriteFakeInfisical_InvocationLog pins the opt-in invocation log: one
// line per call holding the argv, the --token value masked and stdin never
// recorded.
func TestWriteFakeInfisical_InvocationLog(t *testing.T) {
	binDir := t.TempDir()
	if err := writeFakeInfisical(binDir); err != nil {
		t.Fatalf("writeFakeInfisical: %v", err)
	}
	storeDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "invocations.log")
	run := func(stdin string, args ...string) {
		t.Helper()
		cmd := exec.Command(filepath.Join(binDir, "infisical"), args...)
		cmd.Env = append(os.Environ(),
			"INFISICAL_STUB_STORE_DIR="+storeDir,
			"INFISICAL_STUB_INVOCATION_LOG="+logPath,
		)
		cmd.Stdin = bytes.NewReader([]byte(stdin))
		if err := cmd.Run(); err != nil {
			t.Fatalf("running fake infisical %v: %v", args, err)
		}
	}
	run("stdin-body-must-not-be-logged", "secrets", "set", "K=@/dev/stdin", "--projectId", "p", "--env", "dev", "--path", "/")
	run("", "export", "--projectId", "p", "--env", "dev", "--path", "/", "--format", "json", "--token", "jwt-must-not-be-logged")
	run("", "login", "status", "--json")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading invocation log: %v", err)
	}
	want := "secrets set K=@/dev/stdin --projectId p --env dev --path /\n" +
		"export --projectId p --env dev --path / --format json --token ***\n" +
		"login status --json\n"
	if string(data) != want {
		t.Errorf("invocation log = %q, want %q", data, want)
	}
}

// TestWriteFakeInfisical_ProbeModes pins the session probe's answers the
// fallback scenarios select with INFISICAL_STUB_LOGIN_STATUS, and that the
// probe token appears only in the probe's own stdout: never in an export's
// output or the invocation log.
func TestWriteFakeInfisical_ProbeModes(t *testing.T) {
	binDir := t.TempDir()
	if err := writeFakeInfisical(binDir); err != nil {
		t.Fatalf("writeFakeInfisical: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "invocations.log")
	const token = "probe-token-under-test"
	run := func(env []string, args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(filepath.Join(binDir, "infisical"), args...)
		cmd.Env = append(os.Environ(),
			"INFISICAL_STUB_STORE_DIR="+t.TempDir(),
			"INFISICAL_STUB_INVOCATION_LOG="+logPath,
			"INFISICAL_STUB_PROBE_TOKEN="+token,
		)
		cmd.Env = append(cmd.Env, env...)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		_ = cmd.Run()
		return out.String(), errOut.String()
	}
	type session struct {
		Status       string `json:"status"`
		Domain       string `json:"domain"`
		Token        string `json:"token"`
		Verification struct {
			State string `json:"state"`
		} `json:"verification"`
	}
	decode := func(out string) []session {
		t.Helper()
		var v struct {
			Sessions []session `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("probe output %q is not JSON: %v", out, err)
		}
		return v.Sessions
	}

	out, _ := run([]string{"INFISICAL_STUB_LOGIN_STATUS=verified"}, "login", "status", "--json")
	if s := decode(out); len(s) != 1 || s[0].Status != "authenticated" || s[0].Verification.State != "verified" ||
		s[0].Domain != "https://app.infisical.com" || s[0].Token != token {
		t.Errorf("verified probe = %+v", s)
	}
	out, _ = run(nil, "login", "status", "--json")
	if s := decode(out); len(s) != 1 || s[0].Verification.State != "" || s[0].Token != token {
		t.Errorf("default probe = %+v", s)
	}
	out, _ = run([]string{"INFISICAL_STUB_LOGIN_STATUS=none"}, "login", "status", "--json")
	if s := decode(out); len(s) != 0 {
		t.Errorf("none probe = %+v", s)
	}
	out, _ = run([]string{"INFISICAL_STUB_LOGIN_STATUS=json", `INFISICAL_STUB_LOGIN_STATUS_JSON={"sessions":[{"status":"expired"}]}`}, "login", "status", "--json")
	if s := decode(out); len(s) != 1 || s[0].Status != "expired" {
		t.Errorf("json probe = %+v", s)
	}
	out, _ = run([]string{"INFISICAL_STUB_LOGIN_STATUS=non-json"}, "login", "status", "--json")
	if json.Valid([]byte(out)) {
		t.Errorf("non-json probe printed JSON: %q", out)
	}

	exportOut, exportErr := run([]string{"INFISICAL_STUB_EXPORT_FAIL=response-403"}, "export", "--projectId", "p", "--env", "dev", "--path", "/", "--format", "json")
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"export stdout": exportOut, "export stderr": exportErr, "invocation log": string(log)} {
		if bytes.Contains([]byte(text), []byte(token)) {
			t.Errorf("%s carries the probe token: %q", name, text)
		}
	}
}
