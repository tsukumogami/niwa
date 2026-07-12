// The individual-phase pipeline (DESIGN-niwa-onboard.md Solution
// Architecture / Implementation Approach Phase 5): read the existing
// team identity's client_id, mint a fresh client secret, verify the
// minted pair with a real two-hop authentication proof, pause once for
// a login switch in the split-login topology, refuse a self-
// referential target before any write, assemble the credential body by
// construction, and store it -- with best-effort R20 revocation on
// supersession and on any post-verify failure to complete the write.
//
// RunIndividualSetup assumes Detect/ConfirmSetup/ConfirmTopology
// (detect.go) have already run and the operator has already confirmed
// an individual setup, and that a credential-sync destination
// (IndividualSetupParams.SyncSpec) is already resolved -- deciding
// *where* that destination lives (prompting for it, defaulting it, or
// reusing an existing declaration) is out of this file's scope; this
// file owns the mint/verify/guard/store/record pipeline only.
package onboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// credentialSyncKeyPrefix mirrors workspace's unexported "p-" key
// prefix (internal/workspace/credentialpool.go) -- that one genuinely
// can't be imported, since exporting it would widen that package's
// surface for a one-line value. The path prefix itself is the already-
// exported workspace.CredentialSyncPathPrefix, reused directly below
// rather than duplicated.
const credentialSyncKeyPrefix = "p-"

// defaultDestinationEnv mirrors infisical's own unexported defaultEnv
// ("dev"), used when the credential-sync provider's spec does not
// declare its own "env". Duplicated rather than exported from that
// package for a one-line literal.
const defaultDestinationEnv = "dev"

// IndividualSetupParams collects every resolved input the individual
// runner needs. See the package-level doc comment above for the
// caller-responsibility boundary.
type IndividualSetupParams struct {
	// APIURL is the resolved, already-gated api_url for the org that
	// hosts the workspace vault (CheckAPIURL has already run against
	// it -- this pipeline's first call is the first bearer-carrying
	// call after that gate, same as Detect's).
	APIURL string
	// Bearer is the operator's own session bearer for that org.
	Bearer secret.Value
	// IdentityID is the team machine identity's REST resource id,
	// config-sourced (never typed by the operator).
	IdentityID string

	// Kind is the vault backend kind (e.g. "infisical"), config-
	// sourced; selects the /niwa/provider-auth/<kind> path segment.
	Kind string
	// Project is the workspace vault provider's project id, used
	// verbatim (no case-folding, mixed case preserved) as the
	// credential key's uuid segment (R10/AC-16) and as
	// ReadEnvironmentSecrets' target project.
	Project string
	// Environment is the target environment slug for the R9 read-hop.
	Environment string
	// SecretPath is the target secret path for the R9 read-hop;
	// defaults to "/" when empty.
	SecretPath string
	// CredentialAPIURL is the api_url to embed in the stored
	// credential body -- only when it differs from the provider
	// default. Empty means "omit the field" (R10).
	CredentialAPIURL string

	// SyncSpec is the credential-sync provider spec: the personal
	// vault destination this pipeline stores into
	// (SyncSpec.Config["project"], SyncSpec.Config["env"]), and the
	// comparison target for the self-referential guard (R13/AC-22).
	SyncSpec vault.ProviderSpec

	// Topology names which login relationship applies. Must be
	// TopologySameLogin or TopologySplitLogin.
	Topology Topology
	// Pause is invoked exactly once, only when Topology is
	// TopologySplitLogin, between mint-time verification and the
	// store (R4). Must be non-nil in that case.
	Pause func(prompt string) error

	// RecordDir overrides where the R20 record lives; empty resolves
	// to recordDir() (production default: alongside config.toml).
	// Tests set this to a temp directory.
	RecordDir string
}

// IndividualSetupResult is RunIndividualSetup's non-error outcome:
// non-secret identifiers only, suitable for direct display or a
// --json envelope (AC-27 -- no secret value on this or any other
// output surface).
type IndividualSetupResult struct {
	StoredPath string
	StoredKey  string
	SecretID   string
	// Warnings holds non-fatal messages (best-effort revocation
	// failures, an unrecoverable prior id) the caller should surface
	// to the operator without changing the exit outcome (R20: revoke
	// failures never change an exit code).
	Warnings []string
}

// ErrSelfReferential is returned (wrapped in an *ExitCodeError) when
// the target (kind, project) would bootstrap the credential-sync
// provider's own pair (R13/AC-22).
var ErrSelfReferential = errors.New("onboard: refusing to write -- the credential-sync provider's own (kind, project) would be bootstrapped from the pool")

// RunIndividualSetup drives the individual-phase pipeline (design
// Phase 5): read -> mint -> R9 verify -> [split-login pause] ->
// self-referential guard -> assemble -> store, then R20 bookkeeping
// (supersession revoke on success; revoke-just-minted on guard/store
// failure). Every terminal failure is an *ExitCodeError so callers can
// propagate the exit code without re-deriving it.
func RunIndividualSetup(ctx context.Context, p IndividualSetupParams) (IndividualSetupResult, error) {
	return runIndividualSetup(ctx, p, execSecretsSetRunner{})
}

func runIndividualSetup(ctx context.Context, p IndividualSetupParams, runner secretsSetRunner) (IndividualSetupResult, error) {
	result := IndividualSetupResult{}

	if p.Topology != TopologySameLogin && p.Topology != TopologySplitLogin {
		return result, fmt.Errorf("onboard: RunIndividualSetup requires TopologySameLogin or TopologySplitLogin, got %v", p.Topology)
	}

	dir := p.RecordDir
	if dir == "" {
		d, err := recordDir()
		if err != nil {
			return result, fmt.Errorf("onboard: %w", err)
		}
		dir = d
	}

	priorRecord, priorFound, priorErr := readMintRecord(dir, p.Kind, p.Project)
	if priorErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("R20: could not read prior mint record, treating as unrecoverable: %v", priorErr))
	}

	// R8 step 1: read the existing identity's client_id. Never creates
	// an identity (AC-13) -- MintClientSecret below targets the same
	// identityID and fails closed server-side if Universal Auth isn't
	// attached to it.
	clientID, err := infisical.ReadIdentity(ctx, p.APIURL, p.Bearer, p.IdentityID)
	if err != nil {
		return result, &ExitCodeError{Code: ExitAuthFailure, Msg: fmt.Sprintf("reading identity: %v", err)}
	}

	// R8 step 2: mint a fresh secret.
	clientSecretValue, secretID, err := infisical.MintClientSecret(ctx, p.APIURL, p.Bearer, p.IdentityID)
	if err != nil {
		return result, &ExitCodeError{Code: ExitAuthFailure, Msg: fmt.Sprintf("minting client secret: %v", err)}
	}
	result.SecretID = secretID

	// R9: two-hop verification before any store. A login-exchange or
	// read-hop failure here is reported as ExitAuthFailure with NO
	// revocation attempt: R20 (AC-33/34/35b) scopes best-effort
	// revocation to (a) a re-run superseding a prior recorded secret
	// and (b) a mint-then-VERIFY-SUCCESS followed by a store failure.
	// A verify failure is neither, so this deliberately follows the
	// PRD's letter rather than extending revocation to a case it
	// doesn't name.
	secretPath := p.SecretPath
	if secretPath == "" {
		secretPath = "/"
	}
	if err := verifyMintedPair(ctx, p.APIURL, clientID, clientSecretValue, p.Project, p.Environment, secretPath); err != nil {
		return result, &ExitCodeError{Code: ExitAuthFailure, Msg: fmt.Sprintf("verifying minted credential: %v", err)}
	}

	// R4: exactly one login pause in split-login, between mint-time
	// verification and the store; zero in same-login (AC-6, AC-7).
	if p.Topology == TopologySplitLogin {
		if p.Pause == nil {
			return result, fmt.Errorf("onboard: IndividualSetupParams.Pause must be non-nil for split-login topology")
		}
		if err := p.Pause("Log in to the personal-overlay vault's organization now, then press Enter to continue."); err != nil {
			return result, fmt.Errorf("onboard: split-login pause: %w", err)
		}
	}

	// R13/AC-22: self-referential guard, run right before the store --
	// the last point before any write, matching the design's sequence
	// diagram. A violation here still followed a real mint+verify, so
	// the just-minted secret is live; best-effort revoke it before
	// refusing, so a refused run never leaves an orphaned live secret
	// behind (extending AC-34's revoke-on-failure-after-verify
	// discipline to this refusal path, for the same reason it exists
	// on the store-failure path).
	if isSelfReferential(p.Kind, p.Project, p.SyncSpec) {
		if revokeErr := infisical.RevokeClientSecret(ctx, p.APIURL, p.Bearer, p.IdentityID, secretID); revokeErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("best-effort revocation of just-minted secret %s failed after self-referential refusal: %v", secretID, revokeErr))
		}
		return result, &ExitCodeError{
			Code: ExitStorageWrite,
			Msg:  fmt.Sprintf("%s (kind=%s, project=%s)", ErrSelfReferential, p.Kind, p.Project),
		}
	}

	// R10: assemble the credential body. Every interpolated field is
	// TOML-encoded (EncodeTOMLString, config_authoring.go) so a
	// hostile client_id/api_url carrying '"', a newline, or ']' cannot
	// break the body or inject a key/table.
	body := renderCredentialBody(clientID, string(reveal.UnsafeReveal(clientSecretValue)), p.CredentialAPIURL)
	bodyValue := secret.New([]byte(body), secret.Origin{ProviderName: p.Kind, Key: p.Project})
	if r := secret.RedactorFrom(ctx); r != nil {
		r.RegisterValue(bodyValue)
	}

	storePath := workspace.CredentialSyncPathPrefix + p.Kind
	storeKey := credentialSyncKeyPrefix + p.Project

	destProject, _ := p.SyncSpec.Config["project"].(string)
	destEnv, _ := p.SyncSpec.Config["env"].(string)
	if destEnv == "" {
		destEnv = defaultDestinationEnv
	}

	if err := storeCredential(ctx, runner, destProject, destEnv, storePath, storeKey, bodyValue); err != nil {
		if revokeErr := infisical.RevokeClientSecret(ctx, p.APIURL, p.Bearer, p.IdentityID, secretID); revokeErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("best-effort revocation of just-minted secret %s failed after store failure: %v", secretID, revokeErr))
		}
		return result, &ExitCodeError{Code: ExitStorageWrite, Msg: fmt.Sprintf("storing credential: %v", err)}
	}

	result.StoredPath = storePath
	result.StoredKey = storeKey

	// R20 bookkeeping: only reached after a successful store.
	switch {
	case priorFound && priorRecord.SecretID != "" && priorRecord.SecretID != secretID:
		// Supersession (AC-33): best-effort revoke the prior id.
		if revokeErr := infisical.RevokeClientSecret(ctx, p.APIURL, p.Bearer, p.IdentityID, priorRecord.SecretID); revokeErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("could not revoke superseded secret %s: %v", priorRecord.SecretID, revokeErr))
		}
	case !priorFound:
		// AC-35b: no prior id is recoverable. Nothing to revoke; if a
		// secret from an earlier run genuinely still exists, it
		// remains live until its own TTL lapses.
		result.Warnings = append(result.Warnings, "no prior recorded secret id for this (kind, project); if one exists it will remain live until its own TTL lapses")
	}

	if err := writeMintRecord(dir, p.Kind, p.Project, mintRecord{SecretID: secretID}); err != nil {
		// The credential is already stored and verified; a failure to
		// persist R20 bookkeeping must not turn a successful setup
		// into a failure -- surfaced as a warning only.
		result.Warnings = append(result.Warnings, fmt.Sprintf("R20: could not persist mint record: %v", err))
	}

	return result, nil
}

// isSelfReferential reports whether (kind, project) matches syncSpec's
// own (kind, project) -- the R13/AC-22 chicken-and-egg condition.
// Mirrors workspace.vaultProviderConfigMatchesKindProject's logic
// (internal/workspace/credentialsync.go); duplicated rather than
// exported across the package boundary for a five-line predicate, and
// gated on kind exactly as that function is: only backends that
// identify providers by a "project" config key participate. Future
// backends must extend both switches in lockstep.
func isSelfReferential(kind, project string, syncSpec vault.ProviderSpec) bool {
	if syncSpec.Kind == "" || kind != syncSpec.Kind {
		return false
	}
	switch kind {
	case infisical.Kind:
		syncProject, _ := syncSpec.Config["project"].(string)
		return syncProject != "" && syncProject == project
	default:
		// Unknown kinds: refuse to assume a matching key shape, same
		// rationale as the workspace-package original.
		return false
	}
}

// renderCredentialBody renders the TOML document stored at
// /niwa/provider-auth/<kind>, matching the credential-sync contract's
// wire shape exactly (version, client_id, client_secret, and api_url
// only when non-default). Every interpolated value passes through
// EncodeTOMLString (config_authoring.go) so a hostile client_id/
// api_url carrying '"', a newline, or ']' cannot break the body or
// inject a key/table (R10; Security Considerations' encode-or-
// validate normative rule).
func renderCredentialBody(clientID, clientSecret, apiURL string) string {
	var b strings.Builder
	b.WriteString("version = " + EncodeTOMLString("1") + "\n")
	b.WriteString("client_id = " + EncodeTOMLString(clientID) + "\n")
	b.WriteString("client_secret = " + EncodeTOMLString(clientSecret) + "\n")
	if apiURL != "" {
		b.WriteString("api_url = " + EncodeTOMLString(apiURL) + "\n")
	}
	return b.String()
}

// verifyMintedPair performs R9's two-hop proof: a real universal-auth
// login exchange with the minted pair (reusing infisical.Authenticate
// -- the same tested hygiene infisical/auth.go's authenticateHTTP
// already carries, rather than a second reimplementation), followed by
// a target-environment read carrying the resulting access token in a
// header (infisical.ReadEnvironmentSecrets, never `infisical export
// --token`, which would put the token on argv).
func verifyMintedPair(ctx context.Context, apiURL, clientID string, clientSecret secret.Value, project, environment, path string) error {
	entry := map[string]any{
		"client_id":     clientID,
		"client_secret": string(reveal.UnsafeReveal(clientSecret)),
		"api_url":       apiURL,
	}
	accessTokenStr, err := infisical.Authenticate(ctx, entry)
	if err != nil {
		return fmt.Errorf("login exchange: %w", err)
	}
	if r := secret.RedactorFrom(ctx); r != nil {
		r.Register([]byte(accessTokenStr))
	}
	accessToken := secret.New([]byte(accessTokenStr), secret.Origin{ProviderName: infisical.Kind, Key: "access_token"})

	if err := infisical.ReadEnvironmentSecrets(ctx, apiURL, accessToken, project, environment, path); err != nil {
		return fmt.Errorf("target-environment read: %w", err)
	}
	return nil
}

// secretsSetRunner abstracts the `infisical secrets set` subprocess
// delegation so tests can inject a stub without forking a real
// infisical binary. Deliberately separate from internal/vault/
// infisical's own commander interface (subprocess.go): that interface
// has no stdin parameter, and widening it would ripple across every
// existing fake commander in that package's tests for a delegation
// only this file needs.
type secretsSetRunner interface {
	Run(ctx context.Context, args []string, stdin []byte) (stdout, stderr []byte, exitCode int, err error)
}

// execSecretsSetRunner is the production secretsSetRunner: it shells
// out to `infisical` via os/exec, mirroring the hygiene invariants
// internal/vault/infisical's defaultCommander establishes -- cmd.Env =
// nil (inherit the parent environment; the CLI reads its own session
// from ~/.infisical, established by the R22 `infisical login`
// precondition), full stdout/stderr capture (never streamed raw), and
// here, additionally, the secret body fed over stdin rather than argv.
type execSecretsSetRunner struct{}

func (execSecretsSetRunner) Run(ctx context.Context, args []string, stdin []byte) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, "infisical", args...)
	cmd.Env = nil
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.Bytes(), stderr.Bytes(), exitErr.ExitCode(), nil
		}
		return stdout.Bytes(), stderr.Bytes(), -1, err
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

// storeCredential invokes `infisical secrets set` to store body at
// path/key in destProject/destEnv, feeding the secret over stdin --
// never argv (R17/AC-28). The argv token names the key together with
// an "@/dev/stdin" file reference (the CLI's own documented
// `secretName=@/path/to/file` value-from-file syntax): the value bytes
// never appear on argv, only this fixed sentinel path does, and the
// real bytes travel over the subprocess's stdin pipe. destProject,
// destEnv, path, and key are config-sourced identifiers, never
// response bodies -- consistent with the existing `infisical export`
// delegation's argv hygiene (internal/vault/infisical/subprocess.go),
// which passes --projectId/--env/--path the same way.
//
// Verified (not merely assumed) against the locally installed CLI
// (version 0.43.101, matching NOTE-onboard-rest-verification.md's
// pinned version): `secrets set KEY=@/dev/stdin ...` with a value piped
// to stdin proceeds past argument parsing into the network call (it
// fails later on an unrelated 404 for a fake project id), whereas the
// same invocation against a deliberately nonexistent file path fails
// immediately with "open <path>: no such file or directory" / "Unable
// to read file <path>" -- a distinct, earlier error. That difference is
// the CLI actually opening and reading /dev/stdin as the secret's
// value, not silently treating the literal string as the value (which
// would have produced the SAME behavior in both cases: proceeding to
// the network call with a garbage value instead of failing to read a
// nonexistent path).
func storeCredential(ctx context.Context, runner secretsSetRunner, destProject, destEnv, path, key string, body secret.Value) error {
	if destProject == "" {
		return secret.Errorf("onboard: storeCredential requires a non-empty destination project (credential-sync provider spec missing \"project\")")
	}
	args := []string{
		"secrets", "set", key + "=@/dev/stdin",
		"--path", path,
		"--env", destEnv,
		"--projectId", destProject,
	}
	stdout, stderr, exitCode, err := runner.Run(ctx, args, reveal.UnsafeReveal(body))
	if err != nil {
		return secret.Errorf("onboard: infisical secrets set: starting subprocess: %w", err)
	}
	scrubbedErr := vault.ScrubStderr(ctx, stderr, body)
	if exitCode != 0 {
		return secret.Errorf("onboard: infisical secrets set failed (exit %d): %s", exitCode, scrubbedErr)
	}
	// stdout is scrubbed too, even though the CLI has no documented
	// reason to echo a stored value -- belt-and-suspenders matching
	// the design's explicit extension of ScrubStderr's treatment to
	// this store path.
	_ = vault.ScrubStderr(ctx, stdout, body)
	return nil
}
