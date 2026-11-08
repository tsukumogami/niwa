package infisical

// Tests for how a minted token reaches the `infisical export` child.
// Process arguments are visible to every local user in the process
// table, so the token must never be one of them. The CLI also reads
// the token from INFISICAL_UNIVERSAL_AUTH_ACCESS_TOKEN, the first
// environment variable it checks, and that is the only route niwa
// uses. Every subprocess here is a /bin/sh stub on a test-controlled
// PATH; the tokens are fakes.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	mintedTokenEnvVar = "INFISICAL_UNIVERSAL_AUTH_ACCESS_TOKEN"
	fakeMintedToken   = "fake-minted-token-for-argv-test"
	fakeInheritedUA   = "fake-inherited-ua-token"
	fakeInheritedTok  = "fake-inherited-infisical-token"
)

// exportRecord is what the stub saw when it ran as the export child.
type exportRecord struct {
	argv []string
	// tokenEnv is "minted", "inherited", "empty" or "other": which value
	// INFISICAL_UNIVERSAL_AUTH_ACCESS_TOKEN held. The stub compares; it
	// never writes the value itself.
	tokenEnv string
	// tokenEnvEntries counts the INFISICAL_UNIVERSAL_AUTH_ACCESS_TOKEN
	// entries in the child's environment.
	tokenEnvEntries string
	// infisicalToken is "inherited", "empty" or "other" for INFISICAL_TOKEN.
	infisicalToken string
}

// runExportThroughStub runs runInfisicalExport through the production
// commander against a stub "infisical" on PATH and returns what the
// export child saw. The caller's environment carries fake inherited
// values for both token variables, so the test can tell an inherited
// value from the one niwa passes.
func runExportThroughStub(t *testing.T, token string) exportRecord {
	t.Helper()
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	envFile := filepath.Join(dir, "env")
	stub := writeInfisicalStub(t, `
if [ "$1" = login ]; then echo '{"sessions":[]}'; exit 0; fi
for a in "$@"; do printf '%s\n' "$a"; done > '`+argvFile+`'
case "$`+mintedTokenEnvVar+`" in
  '`+fakeMintedToken+`') ua=minted ;;
  '`+fakeInheritedUA+`') ua=inherited ;;
  '') ua=empty ;;
  *) ua=other ;;
esac
case "$INFISICAL_TOKEN" in
  '`+fakeInheritedTok+`') it=inherited ;;
  '') it=empty ;;
  *) it=other ;;
esac
n=$(env | grep -c '^`+mintedTokenEnvVar+`=')
printf '%s\n%s\n%s\n' "$ua" "$n" "$it" > '`+envFile+`'
echo '{"K":"value-long-enough"}'
`)
	putStubOnPath(t, stub)
	t.Setenv(mintedTokenEnvVar, fakeInheritedUA)
	t.Setenv("INFISICAL_TOKEN", fakeInheritedTok)

	values, _, err := runInfisicalExport(context.Background(), defaultCommander{}, "proj-1", "dev", "/", token)
	if err != nil {
		t.Fatalf("runInfisicalExport: %v", err)
	}
	if _, ok := values["K"]; !ok {
		t.Fatalf("export result has no K: the stub's output was not read")
	}

	argvData, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading the child's argv: %v", err)
	}
	envData, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("reading the child's env record: %v", err)
	}
	envLines := strings.Split(strings.TrimRight(string(envData), "\n"), "\n")
	if len(envLines) != 3 {
		t.Fatalf("env record = %q, want three lines", envData)
	}
	return exportRecord{
		argv:            strings.Split(strings.TrimRight(string(argvData), "\n"), "\n"),
		tokenEnv:        envLines[0],
		tokenEnvEntries: strings.TrimSpace(envLines[1]),
		infisicalToken:  envLines[2],
	}
}

// maskedArgv renders argv for a failure message with the fake token
// replaced, so the message reads the same whatever the fake is.
func maskedArgv(argv []string) string {
	return strings.ReplaceAll(strings.Join(argv, " "), fakeMintedToken, "<minted-token>")
}

func TestMintedTokenNeverInExportArgv(t *testing.T) {
	rec := runExportThroughStub(t, fakeMintedToken)

	for _, a := range rec.argv {
		if a == "--token" || strings.HasPrefix(a, "--token=") || strings.Contains(a, fakeMintedToken) {
			t.Fatalf("the export child's argv carries the token (--token): %s", maskedArgv(rec.argv))
		}
	}
	if rec.tokenEnv != "minted" {
		t.Errorf("%s in the export child = %s value, want the minted token", mintedTokenEnvVar, rec.tokenEnv)
	}
	if rec.tokenEnvEntries != "1" {
		t.Errorf("the export child's environment has %s entries of %s, want 1", rec.tokenEnvEntries, mintedTokenEnvVar)
	}
	if rec.infisicalToken != "inherited" {
		t.Errorf("INFISICAL_TOKEN in the export child = %s value, want the inherited one unchanged", rec.infisicalToken)
	}
	if got := os.Getenv(mintedTokenEnvVar); got != fakeInheritedUA {
		t.Errorf("niwa's own %s changed after the export", mintedTokenEnvVar)
	}
}

func TestNoTokenLeavesExportChildUnchanged(t *testing.T) {
	rec := runExportThroughStub(t, "")

	want := []string{"export", "--projectId", "proj-1", "--env", "dev", "--path", "/", "--format", "json"}
	if strings.Join(rec.argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("the export child's argv = %q, want %q", rec.argv, want)
	}
	if rec.tokenEnv != "inherited" {
		t.Errorf("%s in the export child = %s value, want the inherited one unchanged", mintedTokenEnvVar, rec.tokenEnv)
	}
	if rec.tokenEnvEntries != "1" {
		t.Errorf("the export child's environment has %s entries of %s, want 1", rec.tokenEnvEntries, mintedTokenEnvVar)
	}
	if rec.infisicalToken != "inherited" {
		t.Errorf("INFISICAL_TOKEN in the export child = %s value, want the inherited one unchanged", rec.infisicalToken)
	}
}
