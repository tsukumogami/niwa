package workspace

import (
	"bytes"
	"strings"
	"testing"
)

// captureReporter wraps a Reporter writing to a bytes.Buffer for
// the I9 emit-line tests. Returns the reporter and the buffer; the
// caller reads buf.String() after EmitR12Lines runs.
func captureReporter() (*Reporter, *bytes.Buffer) {
	var buf bytes.Buffer
	return NewReporter(&buf), &buf
}

// TestEmitR12Lines_Empty confirms an empty trail produces no
// stderr output.
func TestEmitR12Lines_Empty(t *testing.T) {
	rep, buf := captureReporter()
	var trail AuditTrail
	trail.EmitR12Lines(rep)
	if buf.Len() != 0 {
		t.Errorf("empty trail should produce no output; got: %q", buf.String())
	}
}

// TestEmitR12Lines_VaultRow covers PRD AC-13: one
// `auth: <kind>/<project> source=vault:<name>` line per Source=Vault row.
func TestEmitR12Lines_VaultRow(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-A", Source: SourceVault, Provider: "personal"},
	}
	trail.EmitR12Lines(rep)
	got := buf.String()
	want := "auth: infisical/uuid-A source=vault:personal-overlay(personal)\n"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestEmitR12Lines_AnonymousVaultRow covers AC-39: anonymous
// renders as vault:personal-overlay (no name suffix), never bare
// vault: with a trailing colon.
func TestEmitR12Lines_AnonymousVaultRow(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-A", Source: SourceVault, Provider: ""},
	}
	trail.EmitR12Lines(rep)
	got := buf.String()
	if !strings.Contains(got, "source=vault:personal-overlay\n") {
		t.Errorf("anonymous render missing. Got: %q", got)
	}
	if strings.Contains(got, "source=vault:\n") || strings.Contains(got, "source=vault: ") {
		t.Errorf("must not emit bare 'vault:'. Got: %q", got)
	}
}

// TestEmitR12Lines_FileWithFallback covers AC-14: per-pair fallback
// line when local-file overrides vault.
func TestEmitR12Lines_FileWithFallback(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-A", Source: SourceLocalFile, Fallback: "vault:personal-overlay(personal)"},
	}
	trail.EmitR12Lines(rep)
	got := buf.String()
	want := "auth: infisical/uuid-A source=local-file fallback=vault:personal-overlay(personal)\n"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestEmitR12Lines_FileNoFallback covers AC-15 (silent rows): a
// pure local-file row (Fallback empty) emits nothing.
func TestEmitR12Lines_FileNoFallback(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-A", Source: SourceLocalFile, Fallback: ""},
	}
	trail.EmitR12Lines(rep)
	if buf.Len() != 0 {
		t.Errorf("pure local-file row must NOT emit. Got: %q", buf.String())
	}
}

// TestEmitR12Lines_CLISession covers AC-15: cli-session rows are
// silent.
func TestEmitR12Lines_CLISession(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-A", Source: SourceCLISession},
	}
	trail.EmitR12Lines(rep)
	if buf.Len() != 0 {
		t.Errorf("cli-session row must NOT emit. Got: %q", buf.String())
	}
}

// TestEmitR12Lines_None covers the should-not-happen case: a
// SourceNone row in the trail (currently never produced; reserved
// for future apply-orchestrator wiring per I8) is silent — the
// apply will already fail at the backend's auth call, no need for
// a duplicate per-pair line.
func TestEmitR12Lines_None(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-A", Source: SourceNone},
	}
	trail.EmitR12Lines(rep)
	if buf.Len() != 0 {
		t.Errorf("none row must NOT emit. Got: %q", buf.String())
	}
}

// TestEmitR12Lines_Sorted confirms KIND-then-PROJECT ordering.
func TestEmitR12Lines_Sorted(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-z", Source: SourceVault, Provider: "personal"},
		{Kind: "infisical", Project: "uuid-a", Source: SourceVault, Provider: "personal"},
		{Kind: "infisical", Project: "uuid-m", Source: SourceVault, Provider: "personal"},
	}
	trail.EmitR12Lines(rep)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), buf.String())
	}
	for i, want := range []string{"uuid-a", "uuid-m", "uuid-z"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d = %q, want it to contain %q", i, lines[i], want)
		}
	}
}

// TestEmitR12Lines_LastWriteWins matches AsMap's per-pair semantic:
// when the same (kind, project) appears multiple times in the
// trail, the last record's Source/Fallback decides the emitted
// line.
func TestEmitR12Lines_LastWriteWins(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-X", Source: SourceCLISession},        // earlier
		{Kind: "infisical", Project: "uuid-X", Source: SourceVault, Provider: "personal"}, // later wins
	}
	trail.EmitR12Lines(rep)
	got := buf.String()
	if !strings.Contains(got, "source=vault:personal-overlay(personal)") {
		t.Errorf("last-write-wins violation; expected vault:personal-overlay(personal). Got: %q", got)
	}
	if strings.Contains(got, "source=cli-session") {
		t.Errorf("earlier cli-session record leaked into emit. Got: %q", got)
	}
}

// TestEmitR12Lines_NilReporter is a defensive guard: a nil
// reporter is a no-op. (Production callers always pass a real
// Reporter; this protects future test harnesses.)
func TestEmitR12Lines_NilReporter(t *testing.T) {
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-A", Source: SourceVault, Provider: "personal"},
	}
	// Should not panic.
	trail.EmitR12Lines(nil)
}

// TestEmitR12Lines_MixedTrail covers a realistic trail with one
// vault row, one file-with-fallback row, one silent local-file row,
// and one cli-session row. Two emitted lines, in stable order.
func TestEmitR12Lines_MixedTrail(t *testing.T) {
	rep, buf := captureReporter()
	trail := AuditTrail{
		{Kind: "infisical", Project: "uuid-vault", Source: SourceVault, Provider: "personal"},
		{Kind: "infisical", Project: "uuid-fallback", Source: SourceLocalFile, Fallback: "vault:personal-overlay(personal)"},
		{Kind: "infisical", Project: "uuid-pure", Source: SourceLocalFile, Fallback: ""},
		{Kind: "infisical", Project: "uuid-cli", Source: SourceCLISession},
	}
	trail.EmitR12Lines(rep)
	got := buf.String()
	lineCount := strings.Count(got, "\n")
	if lineCount != 2 {
		t.Errorf("expected 2 lines, got %d: %q", lineCount, got)
	}
	if !strings.Contains(got, "auth: infisical/uuid-vault source=vault:personal-overlay(personal)") {
		t.Errorf("vault row missing. Got: %q", got)
	}
	if !strings.Contains(got, "auth: infisical/uuid-fallback source=local-file fallback=vault:personal-overlay(personal)") {
		t.Errorf("fallback row missing. Got: %q", got)
	}
	if strings.Contains(got, "uuid-pure") {
		t.Errorf("pure local-file row leaked into emit. Got: %q", got)
	}
	if strings.Contains(got, "uuid-cli") {
		t.Errorf("cli-session row leaked into emit. Got: %q", got)
	}
}
