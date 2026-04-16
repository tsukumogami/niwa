package infisical

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// commander abstracts subprocess execution so tests can inject a
// deterministic stub without forking a real `infisical` binary.
//
// Run executes the named command with the given args and returns the
// combined stdout bytes, stderr bytes, the process exit code, and an
// error describing any failure to start/run the process (as distinct
// from a non-zero exit: exit code is the authoritative signal for
// that).
//
// Production callers use defaultCommander, which shells out via
// os/exec with Env = nil (inherit the parent environment).
type commander interface {
	Run(ctx context.Context, name string, args []string) (stdout, stderr []byte, exitCode int, err error)
}

// defaultCommander is the production commander. It wraps os/exec
// with the niwa-specific subprocess hygiene invariants:
//
//   - cmd.Env = nil (inherit the parent's environment unchanged).
//     niwa never filters or extends; the Infisical CLI reads its own
//     auth from INFISICAL_TOKEN / ~/.infisical config.
//   - Stdout and stderr are fully captured into buffers — neither is
//     streamed to the parent process's stdio. This upholds R22: no
//     raw CLI stderr ever reaches niwa's own stderr unfiltered.
//
// Constants (command name, argv flag names) live on the type so
// tests that want to probe argv hygiene can do so via the commander
// indirection.
type defaultCommander struct{}

// Run executes `infisical <args...>` and returns its captured output.
//
// On successful start, the returned error is nil regardless of exit
// code; callers inspect exitCode to branch on success vs failure.
// If the process cannot be started at all (binary missing, permission
// denied), Run returns a non-nil err and an exitCode of -1.
func (defaultCommander) Run(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// R28: never extend Env with secrets. Default behavior of
	// exec.Cmd is Env = nil which inherits the parent's environment
	// — exactly what we want so the Infisical CLI sees
	// INFISICAL_TOKEN (or equivalent) unchanged.
	cmd.Env = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		// exec.ExitError holds the exit code. Any other error type
		// (e.g., exec.ErrNotFound wrapped in *fs.PathError) means
		// the process never started.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.Bytes(), stderr.Bytes(), exitErr.ExitCode(), nil
		}
		return stdout.Bytes(), stderr.Bytes(), -1, err
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

// runInfisicalExport invokes `infisical export --projectId <proj>
// --env <env> --path <path> --format json` via the supplied
// commander, parses stdout as a JSON map of key → plaintext string,
// and returns the map together with a VersionToken derived from the
// payload.
//
// Error handling:
//
//   - A start failure (binary missing) maps to
//     vault.ErrProviderUnreachable.
//   - A non-zero exit with recognisable auth markers in (scrubbed)
//     stderr maps to vault.ErrProviderUnreachable.
//   - A non-zero exit without auth markers is treated as a generic
//     provider error (wrapped via secret.Errorf, stderr scrubbed).
//   - Malformed JSON stdout is a generic provider error.
//
// All returned errors are wrapped via secret.Errorf so that later
// re-wraps by the resolver continue to scrub any late-registered
// fragments.
//
// VersionToken derivation: the Infisical CLI's export command does
// not (as of v0.x) include per-secret version IDs in its JSON
// payload. We synthesise a single project-level token as a SHA-256
// digest over the sorted key names and their plaintext byte-lengths
// — never over plaintext byte content. This keeps decrypted secret
// entropy out of state.json and the Provenance URL while still
// flipping the token whenever keys are added, removed, renamed, or
// change length. Same-length rotations are the acknowledged v1
// coarse-grain trade-off (see buildVersionToken). The Provenance
// field points at the Infisical audit-log URL for the project.
//
// TODO(v1.1): when `infisical secrets get --format json` per-key
// version IDs become reliably available across the CLI versions we
// support, replace the synthesised token with the upstream per-
// secret version UUID (matching the design contract
// "Token = Infisical secret-version UUID"). The current length-only
// approximation is acceptable for v1 because our primary use of
// VersionToken is rotation detection — most rotations change value
// length — not single-key provenance.
func runInfisicalExport(ctx context.Context, c commander, project, env, path string) (map[string]string, vault.VersionToken, error) {
	if c == nil {
		c = defaultCommander{}
	}
	args := []string{
		"export",
		"--projectId", project,
		"--env", env,
		"--path", path,
		"--format", "json",
	}
	stdout, stderrBytes, exitCode, err := c.Run(ctx, "infisical", args)
	if err != nil {
		// Process failed to start (e.g., CLI not installed). We do
		// not scrub err.Error() — an os/exec start-error string is
		// a filesystem/syscall message that never carries secret
		// material.
		return nil, vault.VersionToken{}, secret.Errorf(
			"infisical: running export: %w: %w",
			vault.ErrProviderUnreachable, err,
		)
	}
	if exitCode != 0 {
		scrubbed := vault.ScrubStderr(ctx, stderrBytes)
		if looksLikeAuthFailure(scrubbed) {
			return nil, vault.VersionToken{}, secret.Errorf(
				"infisical: export exited %d (auth failure): %s: %w",
				exitCode, strings.TrimSpace(scrubbed), vault.ErrProviderUnreachable,
			)
		}
		return nil, vault.VersionToken{}, secret.Errorf(
			"infisical: export exited %d: %s",
			exitCode, strings.TrimSpace(scrubbed),
		)
	}

	values, parseErr := parseExportJSON(stdout)
	if parseErr != nil {
		scrubbed := vault.ScrubStderr(ctx, stderrBytes)
		return nil, vault.VersionToken{}, secret.Errorf(
			"infisical: parsing export output (stderr=%q): %w",
			strings.TrimSpace(scrubbed), parseErr,
		)
	}

	return values, buildVersionToken(project, values), nil
}

// parseExportJSON accepts either of the two shapes the Infisical CLI
// has emitted across releases:
//
//  1. A flat object: {"KEY": "value", ...}
//  2. An array of objects: [{"key":"KEY", "value":"value"}, ...]
//
// Unknown fields in shape (2) are ignored. The function returns a
// map keyed by the secret name.
//
// A well-formed empty payload (`{}` or `[]`) returns an empty map
// and no error — the project may legitimately have no secrets at
// this path.
func parseExportJSON(raw []byte) (map[string]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]string{}, nil
	}

	// Shape 1: flat object.
	if trimmed[0] == '{' {
		var obj map[string]string
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			// Fall back to map[string]any: the JSON may contain
			// values that are not bare strings (nested objects,
			// numbers); we reject those with a crisp error rather
			// than silently succeed.
			var loose map[string]any
			if looseErr := json.Unmarshal(trimmed, &loose); looseErr != nil {
				return nil, fmt.Errorf("json: %w", err)
			}
			out := make(map[string]string, len(loose))
			for k, v := range loose {
				s, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("json: value for key %q is not a string (got %T)", k, v)
				}
				out[k] = s
			}
			return out, nil
		}
		if obj == nil {
			obj = map[string]string{}
		}
		return obj, nil
	}

	// Shape 2: array of {key, value} objects.
	if trimmed[0] == '[' {
		var entries []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, fmt.Errorf("json: %w", err)
		}
		out := make(map[string]string, len(entries))
		for _, e := range entries {
			if e.Key == "" {
				continue
			}
			out[e.Key] = e.Value
		}
		return out, nil
	}

	return nil, fmt.Errorf("json: unexpected top-level token (want %q or %q)", '{', '[')
}

// looksLikeAuthFailure scans a scrubbed stderr string for common
// markers of an auth / login failure. The match is case-insensitive
// and substring-based.
//
// The marker set is deliberately specific: broad tokens like "auth"
// or "token" were removed in a v1 tightening because they
// misclassified transient network errors (e.g., "token refresh
// pending") as auth failures, which under --allow-missing-secrets
// silently downgrades the result to empty. The current list focuses
// on phrases that unambiguously signal a credential / session
// problem. Expand with care when new Infisical CLI versions ship
// additional error phrasing.
//
// The match runs AFTER scrubbing, so a stderr that happened to
// contain a secret fragment matching one of these markers has
// already been redacted to "***" before we scan — no leakage risk.
func looksLikeAuthFailure(scrubbedStderr string) bool {
	if scrubbedStderr == "" {
		return false
	}
	lower := strings.ToLower(scrubbedStderr)
	markers := []string{
		"401",          // HTTP unauthorized
		"403",          // HTTP forbidden
		"unauthorized", // case-insensitive; "unauthorised" is the other spelling
		"unauthorised",
		"forbidden",
		"not logged in",
		"login expired",
		"invalid credentials",
		"authentication failed",
		"session expired",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// buildVersionToken returns a rotation-sensitive token derived ONLY
// from provider-side metadata — never from decrypted plaintext
// bytes. For v1 Infisical (no per-key version IDs in the export
// output), we use a project-level digest: SHA-256 of the sorted key
// names concatenated with the sorted plaintext byte-lengths. This
// flips whenever keys are added, removed, renamed, or lengths
// change. It misses rotations where the new plaintext has the same
// length — that's a known v1 coarse-grain trade-off, documented in
// the TODO below. It does NOT touch plaintext byte content, so no
// secret entropy reaches the token (which in turn feeds state.json
// via SourceEntry.VersionToken and the user-visible Provenance URL).
//
// Provenance is the Infisical audit-log URL for the project; when
// the payload is empty the Provenance still points at the project's
// audit page so users have a landing target for the "why is this
// stale" investigation.
//
// TODO(v1.1): switch to `infisical secrets get --format json` per-
// key calls to obtain Infisical's native per-secret version UUIDs.
// That is the true rotation signal and matches the design's
// "Token = Infisical secret-version UUID" contract. See PLAN Issue 5
// key_decisions.
func buildVersionToken(project string, values map[string]string) vault.VersionToken {
	h := sha256.New()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Null-byte separators prevent ambiguity between e.g.,
		// ("a", len=23) and ("ab", len=3). We hash the key name
		// (non-secret metadata) and the LENGTH of the value only —
		// never the value bytes themselves.
		h.Write([]byte(k))
		h.Write([]byte{0})
		lenBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBuf, uint64(len(values[k])))
		h.Write(lenBuf)
		h.Write([]byte{0})
	}
	token := hex.EncodeToString(h.Sum(nil))
	provenance := fmt.Sprintf(
		"https://app.infisical.com/projects/%s/audit-logs?version=%s",
		project, token,
	)
	if len(values) == 0 {
		provenance = fmt.Sprintf(
			"https://app.infisical.com/projects/%s/audit-logs",
			project,
		)
	}
	return vault.VersionToken{Token: token, Provenance: provenance}
}
