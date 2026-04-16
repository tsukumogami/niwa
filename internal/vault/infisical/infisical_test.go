package infisical

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
)

// fakeCommander is a test double for the Infisical subprocess. It
// implements the package-internal commander interface.
//
// Each instance records the argv passed to it so argv-hygiene tests
// can assert that no secret values reach argv.
type fakeCommander struct {
	stdout   []byte
	stderr   []byte
	exitCode int
	runErr   error

	// capturedArgs is the most recent argv. Inspect after a Run to
	// assert secrets never appear there.
	capturedArgs []string
	// capturedName is the subprocess name (always "infisical" for
	// this backend).
	capturedName string
	// callCount counts invocations. The backend promises at most one
	// export per project+env+path per Provider; tests assert this.
	callCount int32
}

// Run implements the commander interface. Captures arguments and
// returns the preconfigured output.
func (f *fakeCommander) Run(_ context.Context, name string, args []string) ([]byte, []byte, int, error) {
	atomic.AddInt32(&f.callCount, 1)
	f.capturedName = name
	// Copy args to a fresh slice so later mutations do not racily
	// corrupt what the test assertion sees.
	copied := make([]string, len(args))
	copy(copied, args)
	f.capturedArgs = copied
	return f.stdout, f.stderr, f.exitCode, f.runErr
}

// jsonBody returns an Infisical-export-shaped JSON object body.
func jsonBody(entries map[string]string) []byte {
	var b strings.Builder
	b.WriteString("{")
	first := true
	for k, v := range entries {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, "%q:%q", k, v)
	}
	b.WriteString("}")
	return []byte(b.String())
}

// openWithCommander is a test helper that constructs an Infisical
// Provider wired to the supplied fakeCommander. Keeps tests terse.
func openWithCommander(t *testing.T, cfg vault.ProviderConfig, cmd *fakeCommander) vault.Provider {
	t.Helper()
	if cfg == nil {
		cfg = vault.ProviderConfig{}
	}
	if _, has := cfg["project"]; !has {
		cfg["project"] = "proj-1"
	}
	cfg["_commander"] = commander(cmd)
	p, err := (Factory{}).Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return p
}

// TestFactoryKind locks in the registry key ("infisical"). The
// Registry indexes on exactly this string.
func TestFactoryKind(t *testing.T) {
	if got := NewFactory().Kind(); got != "infisical" {
		t.Fatalf("Factory.Kind() = %q, want %q", got, "infisical")
	}
}

// TestInfisicalFactoryRegisteredInDefaultRegistry asserts that the
// package's init() registers a Factory with vault.DefaultRegistry.
// The symmetric test in the fake backend's suite asserts the fake
// is NOT registered; this one asserts the Infisical backend IS.
func TestInfisicalFactoryRegisteredInDefaultRegistry(t *testing.T) {
	// Attempting to register a second time must fail with an error
	// naming Kind — that confirms a Factory is already registered.
	err := vault.DefaultRegistry.Register(&Factory{})
	if err == nil {
		// Not registered — rollback so we don't leak.
		_ = vault.DefaultRegistry.Unregister("infisical")
		t.Fatalf("Infisical Factory is NOT registered in DefaultRegistry (no duplicate-register error)")
	}
	if !strings.Contains(err.Error(), "infisical") {
		t.Fatalf("duplicate-register error did not mention kind: %v", err)
	}
}

// TestFactoryOpenRejectsMissingProject covers the only required
// config field.
func TestFactoryOpenRejectsMissingProject(t *testing.T) {
	_, err := (Factory{}).Open(context.Background(), vault.ProviderConfig{})
	if err == nil {
		t.Fatalf("Open with no project should have failed")
	}
	if !strings.Contains(err.Error(), "project") {
		t.Fatalf("error should mention 'project', got: %v", err)
	}
}

// TestFactoryOpenRejectsMalformedConfig covers defensive parsing.
func TestFactoryOpenRejectsMalformedConfig(t *testing.T) {
	cases := []struct {
		name   string
		config vault.ProviderConfig
	}{
		{"project wrong type", vault.ProviderConfig{"project": 42}},
		{"project empty", vault.ProviderConfig{"project": ""}},
		{"env wrong type", vault.ProviderConfig{"project": "p", "env": 1.5}},
		{"path wrong type", vault.ProviderConfig{"project": "p", "path": true}},
		{"name wrong type", vault.ProviderConfig{"project": "p", "name": 42}},
		{"commander wrong type", vault.ProviderConfig{"project": "p", "_commander": "not a commander"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := (Factory{}).Open(context.Background(), c.config)
			if err == nil {
				t.Fatalf("Open with %s should have failed", c.name)
			}
		})
	}
}

// TestOpenIsLazy asserts AC: Factory.Open does NOT run `infisical`.
// The commander's callCount must remain zero until the first
// Resolve.
func TestOpenIsLazy(t *testing.T) {
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{"K": "v-long-enough"})}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()
	if cmd.callCount != 0 {
		t.Fatalf("Open ran the subprocess (callCount=%d); Open must be lazy", cmd.callCount)
	}
}

// TestResolveFetchesAndCaches exercises the end-to-end happy path:
// first Resolve triggers one subprocess; subsequent Resolves hit the
// cache (callCount stays at 1).
func TestResolveFetchesAndCaches(t *testing.T) {
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{
		"api-token":  "alpha-long-enough",
		"aux-secret": "bravo-long-enough",
	})}
	p := openWithCommander(t, vault.ProviderConfig{
		"project": "proj-1",
		"name":    "team",
	}, cmd)
	defer p.Close()

	if p.Name() != "team" {
		t.Fatalf("Name() = %q, want %q", p.Name(), "team")
	}
	if p.Kind() != "infisical" {
		t.Fatalf("Kind() = %q, want infisical", p.Kind())
	}

	val, token, err := p.Resolve(context.Background(), vault.Ref{Key: "api-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := string(reveal.UnsafeReveal(val)); got != "alpha-long-enough" {
		t.Fatalf("value = %q, want alpha-long-enough", got)
	}
	if token.Token == "" {
		t.Fatalf("token empty")
	}
	if !strings.Contains(token.Provenance, "proj-1") {
		t.Fatalf("Provenance = %q, want URL containing proj-1", token.Provenance)
	}
	if !strings.HasPrefix(token.Provenance, "https://app.infisical.com/projects/") {
		t.Fatalf("Provenance = %q, want Infisical audit URL", token.Provenance)
	}

	// Second Resolve must hit the cache — no additional subprocess.
	_, _, err = p.Resolve(context.Background(), vault.Ref{Key: "aux-secret"})
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if cmd.callCount != 1 {
		t.Fatalf("expected 1 subprocess call (cache hit on 2nd), got %d", cmd.callCount)
	}
}

// TestResolveReturnsKeyNotFound covers the missing-key case.
func TestResolveReturnsKeyNotFound(t *testing.T) {
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{"present": "x-marks-long-enough"})}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "missing"})
	if err == nil {
		t.Fatalf("Resolve(missing) returned no error")
	}
	if !errors.Is(err, vault.ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got: %v", err)
	}
}

// TestResolveBatch asserts AC: ResolveBatch returns one result per
// input ref and runs the subprocess exactly once.
func TestResolveBatch(t *testing.T) {
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{
		"a": "alpha-long-enough",
		"b": "bravo-long-enough",
	})}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	br, ok := p.(vault.BatchResolver)
	if !ok {
		t.Fatalf("Infisical provider does not satisfy BatchResolver")
	}

	refs := []vault.Ref{{Key: "a"}, {Key: "missing"}, {Key: "b"}}
	results, err := br.ResolveBatch(context.Background(), refs)
	if err != nil {
		t.Fatalf("ResolveBatch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results length = %d, want 3", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("results[0].Err = %v, want nil", results[0].Err)
	}
	if got := string(reveal.UnsafeReveal(results[0].Value)); got != "alpha-long-enough" {
		t.Fatalf("results[0].Value = %q", got)
	}
	if !errors.Is(results[1].Err, vault.ErrKeyNotFound) {
		t.Fatalf("results[1].Err = %v, want ErrKeyNotFound", results[1].Err)
	}
	if results[2].Err != nil {
		t.Fatalf("results[2].Err = %v, want nil", results[2].Err)
	}
	if cmd.callCount != 1 {
		t.Fatalf("expected 1 export call for the whole batch, got %d", cmd.callCount)
	}
}

// TestArgvHygiene asserts R21: no secrets on argv. We inspect the
// argv the commander receives: projectId, env, path, --format json
// are allowed; nothing else should appear.
func TestArgvHygiene(t *testing.T) {
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{"k": "v-long-enough"})}
	p := openWithCommander(t, vault.ProviderConfig{
		"project": "my-project-id",
		"env":     "production",
		"path":    "/team",
	}, cmd)
	defer p.Close()
	_, _, _ = p.Resolve(context.Background(), vault.Ref{Key: "k"})

	if cmd.capturedName != "infisical" {
		t.Fatalf("commander name = %q, want infisical", cmd.capturedName)
	}
	wantArgs := []string{
		"export",
		"--projectId", "my-project-id",
		"--env", "production",
		"--path", "/team",
		"--format", "json",
	}
	if len(cmd.capturedArgs) != len(wantArgs) {
		t.Fatalf("argv length = %d, want %d; got %v", len(cmd.capturedArgs), len(wantArgs), cmd.capturedArgs)
	}
	for i := range wantArgs {
		if cmd.capturedArgs[i] != wantArgs[i] {
			t.Fatalf("argv[%d] = %q, want %q; full argv = %v", i, cmd.capturedArgs[i], wantArgs[i], cmd.capturedArgs)
		}
	}
}

// TestAuthFailureMapsToUnreachable covers the non-zero-exit + auth-
// marker path across the tightened marker set. Each sub-case feeds
// a different marker through stderr; all must map to
// ErrProviderUnreachable.
func TestAuthFailureMapsToUnreachable(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
	}{
		{"401", "Error: 401 Unauthorized"},
		{"unauthorized", "Error: unauthorized access"},
		{"not logged in", "Error: not logged in, run `infisical login`"},
		{"login expired", "Error: login expired, please re-authenticate"},
		{"authentication failed", "Error: authentication failed for project"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := &fakeCommander{
				exitCode: 1,
				stderr:   []byte(c.stderr),
			}
			p := openWithCommander(t, nil, cmd)
			defer p.Close()

			_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"})
			if err == nil {
				t.Fatalf("Resolve should have failed")
			}
			if !errors.Is(err, vault.ErrProviderUnreachable) {
				t.Fatalf("expected ErrProviderUnreachable, got: %v", err)
			}
		})
	}
}

// TestGenericFailureDoesNotMapToUnreachable covers the non-zero-exit
// + no-auth-marker path: generic errors must not be misclassified
// as auth failures.
func TestGenericFailureDoesNotMapToUnreachable(t *testing.T) {
	cmd := &fakeCommander{
		exitCode: 1,
		stderr:   []byte("Error: project not found: proj-1"),
	}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"})
	if err == nil {
		t.Fatalf("Resolve should have failed")
	}
	if errors.Is(err, vault.ErrProviderUnreachable) {
		t.Fatalf("generic error should NOT map to ErrProviderUnreachable: %v", err)
	}
	if errors.Is(err, vault.ErrKeyNotFound) {
		t.Fatalf("generic export failure should NOT map to ErrKeyNotFound: %v", err)
	}
}

// TestStartFailureMapsToUnreachable covers the case where the CLI
// binary cannot be started (not installed / not on PATH).
func TestStartFailureMapsToUnreachable(t *testing.T) {
	cmd := &fakeCommander{
		exitCode: -1,
		runErr:   errors.New("exec: \"infisical\": executable file not found in $PATH"),
	}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"})
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Fatalf("expected ErrProviderUnreachable for start failure, got: %v", err)
	}
}

// TestR22StderrScrubPreventsLeak is the headline security-regression
// test for R22 (redact-logs invariant).
//
// Regression scenario: a prior Resolve call succeeded and returned a
// secret.Value. A later Resolve fails with an auth error whose
// stderr body happens to include (or echo) that same secret as a
// fragment. Without scrubbing, the secret bytes would surface in the
// returned error's message.
//
// Guard: vault.ScrubStderr is invoked on stderr before any error
// interpolation. This test registers the secret on the context's
// Redactor (as production code does during apply), induces a non-
// zero exit with the secret embedded in stderr, and asserts the
// fragment is absent from the returned error's Error() string.
//
// If this test ever fails, R22 is broken. Investigate before
// merging.
func TestR22StderrScrubPreventsLeak(t *testing.T) {
	// A plausibly-real-looking token. Never actually valid — the
	// "NOT_A_REAL_TOKEN" infix prevents secret-scanner false
	// positives in CI.
	const leakedSecret = "ghp_NOT_A_REAL_TOKEN_abcdef0123456789"

	// Stage 1: pre-register the secret on a context Redactor, as the
	// resolver does after a successful fetch.
	redactor := secret.NewRedactor()
	redactor.RegisterValue(secret.New([]byte(leakedSecret), secret.Origin{}))
	ctx := secret.WithRedactor(context.Background(), redactor)

	// Stage 2: induce an auth failure whose stderr embeds the
	// previously-resolved secret. This mirrors the realistic case of
	// the Infisical CLI echoing back part of a supplied token in its
	// error message.
	cmd := &fakeCommander{
		exitCode: 1,
		stderr: []byte("Error: authentication failed with token " +
			leakedSecret + "; please run `infisical login`"),
	}
	// Open uses context.Background; use explicit ctx for Resolve so
	// the redactor is picked up by ScrubStderr.
	p, err := (Factory{}).Open(ctx, vault.ProviderConfig{
		"project":    "proj-1",
		"_commander": commander(cmd),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	_, _, resolveErr := p.Resolve(ctx, vault.Ref{Key: "k"})
	if resolveErr == nil {
		t.Fatalf("Resolve should have failed")
	}
	// Must still classify as ErrProviderUnreachable (the scrubbing
	// step must not break downstream error-kind branching).
	if !errors.Is(resolveErr, vault.ErrProviderUnreachable) {
		t.Fatalf("expected ErrProviderUnreachable, got: %v", resolveErr)
	}

	// The headline assertion: the secret MUST NOT appear in the
	// returned error's surface string.
	msg := resolveErr.Error()
	if strings.Contains(msg, leakedSecret) {
		t.Fatalf("R22 REGRESSION: secret fragment leaked into error: %q", msg)
	}
	// The scrubber should have substituted "***" for the fragment.
	if !strings.Contains(msg, "***") {
		t.Fatalf("expected redacted placeholder in error, got: %q", msg)
	}

	// Belt-and-braces: wrap the error once more (as a caller
	// typically would) and re-assert. The underlying *secret.Error
	// preserves its Redactor across wraps.
	wrapped := fmt.Errorf("apply: %w", resolveErr)
	if strings.Contains(wrapped.Error(), leakedSecret) {
		t.Fatalf("R22 REGRESSION across wrap: secret leaked into wrapped error: %q", wrapped.Error())
	}
}

// TestR22ScrubStderrWithKnownValues covers the second ScrubStderr
// layer: known fragments supplied at call time. Even without a
// context-attached Redactor, if the caller passes the secret as a
// known Value, it must be scrubbed from stderr.
//
// This exercises vault.ScrubStderr's second pass — the runtime
// belt-and-braces that catches fragments the context redactor did
// not know about.
func TestR22ScrubStderrWithKnownValues(t *testing.T) {
	const token = "ghp_NOT_A_REAL_TOKEN_beef_deadbeef"
	stderr := []byte("boom: " + token + " is invalid")
	scrubbed := vault.ScrubStderr(context.Background(), stderr,
		secret.New([]byte(token), secret.Origin{}))
	if strings.Contains(scrubbed, token) {
		t.Fatalf("ScrubStderr did not redact known fragment: %q", scrubbed)
	}
}

// TestCloseClearsCache asserts that after Close, Resolve returns
// ErrProviderUnreachable and Close is idempotent.
func TestCloseClearsCache(t *testing.T) {
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{"k": "v-long-enough"})}
	p := openWithCommander(t, nil, cmd)
	if _, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"}); err != nil {
		t.Fatalf("pre-close Resolve: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close must be no-op: %v", err)
	}
	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"})
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Fatalf("after Close, Resolve = %v, want ErrProviderUnreachable", err)
	}
}

// TestEnvAndPathDefaults asserts the default env ("dev") and path
// ("/") when the config omits them.
func TestEnvAndPathDefaults(t *testing.T) {
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{"k": "v-long-enough"})}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()
	_, _, _ = p.Resolve(context.Background(), vault.Ref{Key: "k"})

	// Inspect argv for the defaults.
	args := strings.Join(cmd.capturedArgs, " ")
	if !strings.Contains(args, "--env dev") {
		t.Fatalf("default env not applied; argv = %s", args)
	}
	if !strings.Contains(args, "--path /") {
		t.Fatalf("default path not applied; argv = %s", args)
	}
}

// TestArrayShapeParses covers the alternate Infisical JSON output
// shape: [{"key":"K","value":"V"},...]. Some CLI versions use this
// instead of the flat object.
func TestArrayShapeParses(t *testing.T) {
	cmd := &fakeCommander{
		stdout: []byte(`[{"key":"a","value":"alpha-long-enough"},{"key":"b","value":"bravo-long-enough"}]`),
	}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	val, _, err := p.Resolve(context.Background(), vault.Ref{Key: "a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := string(reveal.UnsafeReveal(val)); got != "alpha-long-enough" {
		t.Fatalf("value = %q", got)
	}
}

// TestEmptyExportParses covers `{}` — a project with no secrets at
// the configured path. Resolve returns ErrKeyNotFound for any ref.
func TestEmptyExportParses(t *testing.T) {
	cmd := &fakeCommander{stdout: []byte("{}")}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "anything"})
	if !errors.Is(err, vault.ErrKeyNotFound) {
		t.Fatalf("empty project: want ErrKeyNotFound, got %v", err)
	}
}

// TestMalformedJSONIsGenericError covers the case where stdout is
// not valid JSON — we want a crisp error that does NOT map to
// ErrKeyNotFound or ErrProviderUnreachable (it's a CLI contract
// break, not a missing key or auth problem).
func TestMalformedJSONIsGenericError(t *testing.T) {
	cmd := &fakeCommander{stdout: []byte("garbage not json")}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"})
	if err == nil {
		t.Fatalf("Resolve should have failed")
	}
	if errors.Is(err, vault.ErrKeyNotFound) || errors.Is(err, vault.ErrProviderUnreachable) {
		t.Fatalf("malformed JSON should be a generic error, got: %v", err)
	}
}

// TestResolveReturnsSecretValue asserts the returned Value redacts
// through fmt — integration check that plaintext does not leak via
// the standard formatter paths.
func TestResolveReturnsSecretValue(t *testing.T) {
	const plaintext = "alpha-long-enough-to-redact"
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{"k": plaintext})}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	val, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
		got := fmt.Sprintf(format, val)
		if strings.Contains(got, plaintext) {
			t.Fatalf("format %s leaked plaintext: %q", format, got)
		}
	}
}

// TestTokenChangesOnRotation asserts that the synthesised version
// token changes when a secret value rotation changes the plaintext
// byte-length. This is the primary rotation-detection signal for
// the Issue 7 story. Note: under the key-metadata-only v1
// derivation, same-length rotations do NOT flip the token — that
// case is pinned down separately by
// TestVersionTokenDoesNotDeriveFromPlaintext. See the v1.1 TODO in
// buildVersionToken for the planned upgrade to native per-secret
// version IDs.
func TestTokenChangesOnRotation(t *testing.T) {
	cmd1 := &fakeCommander{stdout: jsonBody(map[string]string{"k": "alpha-long-enough"})}
	p1 := openWithCommander(t, nil, cmd1)
	defer p1.Close()
	_, token1, err := p1.Resolve(context.Background(), vault.Ref{Key: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	cmd2 := &fakeCommander{stdout: jsonBody(map[string]string{"k": "bravo-long-enough-and-then-some"})}
	p2 := openWithCommander(t, nil, cmd2)
	defer p2.Close()
	_, token2, err := p2.Resolve(context.Background(), vault.Ref{Key: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if token1.Token == token2.Token {
		t.Fatalf("version token did not change on length-affecting rotation: %q", token1.Token)
	}
}

// TestLooksLikeAuthFailure exercises the marker-matching helper
// directly, locking in the substring set. Keeping this as an
// internal test lets us iterate on markers without affecting the
// package's public surface.
//
// The marker set was tightened in a v1 pass: broad tokens like
// "auth" and "token" were removed because they misclassified
// transient network errors (e.g., "token refresh pending") as auth
// failures. The cases below exercise the current tighter list.
func TestLooksLikeAuthFailure(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"Error: 401 Unauthorized", true},
		{"Error: 403 Forbidden", true},
		{"Error: unauthorized access", true},
		{"Error: unauthorised access", true},
		{"Error: authentication failed", true},
		{"Error: not logged in", true},
		{"Error: login expired, please re-authenticate", true},
		{"Error: invalid credentials", true},
		{"Error: session expired", true},
		{"Error: project not found", false},
		{"Error: internal server error", false},
		// Tightening assertions: these used to match under the old
		// "auth"/"token" markers but must NOT match now.
		{"Error: token refresh pending", false},
		{"Error: auth scheme mismatch", false},
		{"please run infisical login", false},
		{"invalid token", false},
	}
	for _, c := range cases {
		if got := looksLikeAuthFailure(c.in); got != c.want {
			t.Fatalf("looksLikeAuthFailure(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestTransientErrorDoesNotMapToUnreachable guards the tightening
// of looksLikeAuthFailure: a transient network error whose stderr
// mentions "token refresh pending" must NOT be classified as an
// auth failure. Under --allow-missing-secrets (Issue 10) that
// classification would silently downgrade the result to empty,
// masking a retriable fault.
func TestTransientErrorDoesNotMapToUnreachable(t *testing.T) {
	cmd := &fakeCommander{
		exitCode: 1,
		stderr:   []byte("Error: token refresh pending, please retry"),
	}
	p := openWithCommander(t, nil, cmd)
	defer p.Close()

	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "k"})
	if err == nil {
		t.Fatalf("Resolve should have failed")
	}
	if errors.Is(err, vault.ErrProviderUnreachable) {
		t.Fatalf("transient error should NOT map to ErrProviderUnreachable: %v", err)
	}
}

// TestVersionTokenDoesNotDeriveFromPlaintext is the headline
// security regression test for Design Decision 4: VersionToken.Token
// MUST NOT incorporate decrypted plaintext bytes (that entropy would
// flow into state.json via SourceEntry.VersionToken and into the
// user-visible Provenance URL).
//
// The v1 Infisical backend hashes only (sorted key name + plaintext
// byte length). This test builds two export payloads with the same
// keys and same value lengths but completely different value bytes.
// If the token derivation ever re-introduces a dependence on
// plaintext content, the tokens will diverge and this test will
// fail — flagging the regression.
//
// This is the inverse of TestVersionTokenChangesOnLengthChange: the
// two together pin down the exact shape of the token (key + length
// only).
func TestVersionTokenDoesNotDeriveFromPlaintext(t *testing.T) {
	payloadA := jsonBody(map[string]string{"KEY1": "aaaaaa", "KEY2": "bbbbbb"})
	payloadB := jsonBody(map[string]string{"KEY1": "cccccc", "KEY2": "dddddd"})

	cmdA := &fakeCommander{stdout: payloadA}
	pA := openWithCommander(t, nil, cmdA)
	defer pA.Close()
	_, tokenA, err := pA.Resolve(context.Background(), vault.Ref{Key: "KEY1"})
	if err != nil {
		t.Fatalf("Resolve A: %v", err)
	}

	cmdB := &fakeCommander{stdout: payloadB}
	pB := openWithCommander(t, nil, cmdB)
	defer pB.Close()
	_, tokenB, err := pB.Resolve(context.Background(), vault.Ref{Key: "KEY1"})
	if err != nil {
		t.Fatalf("Resolve B: %v", err)
	}

	if tokenA.Token != tokenB.Token {
		t.Fatalf("tokens differ across equal-shape payloads — plaintext likely contributing to digest:\n  A=%q\n  B=%q",
			tokenA.Token, tokenB.Token)
	}
	if tokenA.Token == "" {
		t.Fatalf("token is empty; derivation produced no digest")
	}
}

// TestVersionTokenChangesOnLengthChange is the complementary
// assertion to TestVersionTokenDoesNotDeriveFromPlaintext: while
// content changes alone do not flip the token (v1 trade-off), a
// change in plaintext byte-length MUST flip it. This is the primary
// rotation signal for the key-metadata-only derivation.
func TestVersionTokenChangesOnLengthChange(t *testing.T) {
	cmdA := &fakeCommander{stdout: jsonBody(map[string]string{"KEY1": "aaaaaa"})}
	pA := openWithCommander(t, nil, cmdA)
	defer pA.Close()
	_, tokenA, err := pA.Resolve(context.Background(), vault.Ref{Key: "KEY1"})
	if err != nil {
		t.Fatalf("Resolve A: %v", err)
	}

	cmdB := &fakeCommander{stdout: jsonBody(map[string]string{"KEY1": "aaaaaaa"})}
	pB := openWithCommander(t, nil, cmdB)
	defer pB.Close()
	_, tokenB, err := pB.Resolve(context.Background(), vault.Ref{Key: "KEY1"})
	if err != nil {
		t.Fatalf("Resolve B: %v", err)
	}

	if tokenA.Token == tokenB.Token {
		t.Fatalf("token did not change when value length changed: %q", tokenA.Token)
	}
}

// TestRegistryBuildsInfisicalProvider exercises the end-to-end
// registry path: a ProviderSpec with Kind="infisical" resolves
// against vault.DefaultRegistry (where init() registered our
// Factory). This is the niwa-apply integration shape.
//
// We use a fresh Registry so the test is deterministic even if
// other test files probe DefaultRegistry concurrently.
func TestRegistryBuildsInfisicalProvider(t *testing.T) {
	reg := vault.NewRegistry()
	if err := reg.Register(&Factory{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cmd := &fakeCommander{stdout: jsonBody(map[string]string{"k": "v-long-enough"})}
	bundle, err := reg.Build(context.Background(), []vault.ProviderSpec{
		{
			Name: "team",
			Kind: "infisical",
			Config: vault.ProviderConfig{
				"project":    "proj-1",
				"name":       "team",
				"_commander": commander(cmd),
			},
			Source: "test",
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer bundle.CloseAll()

	p, err := bundle.Get("team")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	val, _, err := p.Resolve(context.Background(), vault.Ref{ProviderName: "team", Key: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := string(reveal.UnsafeReveal(val)); got != "v-long-enough" {
		t.Fatalf("value = %q", got)
	}
}
