// Team setup runner: the team-phase step loop (Decision 1 / Phase 4).
// Every landing decision is a live world-state probe, never a
// remembered cursor -- folder creation is automated and re-probed by
// re-invoking the same idempotent CLI delegation; identity/UA-attach
// and the environment read grant are guided dashboard steps from the
// start, each landing-check-guarded by a read-only REST probe run
// against the operator's own session bearer. A failed landing check
// re-surfaces its instruction and does not advance (AC-9b); a
// plan-gated folder-create degrades to guided instructions for that
// one step and resumes automatically (AC-11). The R21 sweep re-derives
// all three probes from scratch, so a resumed run that finds
// everything already landed skips straight to it.
package onboard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
)

// TeamOptions collects the config- and session-sourced inputs the
// team runner needs. Every value that would otherwise be a hardcoded,
// org-specific constant (identity name, auth method, environment
// slug, project id) is config-sourced by the caller, per R14.
type TeamOptions struct {
	// APIURL is the resolved Infisical API base URL (post api_url
	// gate).
	APIURL string
	// Bearer is the operator's own authenticated session bearer.
	// Every REST call this runner makes carries it in the
	// Authorization header -- never a niwa-custodied admin token
	// (AC-12).
	Bearer secret.Value

	// ProjectID is the Infisical project id the team vault lives in.
	ProjectID string
	// IdentityID is the org-level identity id the wizard checks and
	// guides the operator to create/attach Universal Auth on.
	IdentityID string
	// IdentityName is the identity's configured display name, printed
	// in the guided identity-creation instruction.
	IdentityName string
	// AuthMethod is the configured auth method name (e.g. "Universal
	// Auth"), printed in the guided identity-creation instruction.
	AuthMethod string
	// EnvironmentSlug is the target environment the guided grant step
	// names, and the environment the folder-create delegation targets.
	EnvironmentSlug string
	// SecretPath is the folder/secret path to create. Empty defaults
	// to "/" (CreateSecretsFolder's own default).
	SecretPath string

	// In and Out back the guided steps' Pause prompts.
	In  io.Reader
	Out io.Writer

	// CreateFolder is the folder-create CLI-delegation seam. nil (the
	// production default) wires up infisical.CreateSecretsFolder
	// against the real CLI on PATH; team_test.go substitutes a fake
	// directly here instead of touching a package-level global, so
	// tests carry no shared mutable state and remain safe to run in
	// parallel.
	CreateFolder FolderCreator
}

// FolderCreator is the folder-create CLI-delegation seam's shape.
// infisical.CreateSecretsFolder's own commander parameter is of an
// unexported type, so this package cannot inject a fake commander
// directly into it; FolderCreator lets TeamOptions carry a swappable
// function value instead, at the same three-string call shape.
type FolderCreator func(ctx context.Context, projectID, env, path string) error

// defaultCreateFolder is the production FolderCreator: it shells out
// to the real infisical CLI (or, under test, the writeFakeInfisical
// stub on PATH) via infisical.CreateSecretsFolder's own default
// commander.
func defaultCreateFolder(ctx context.Context, projectID, env, path string) error {
	return infisical.CreateSecretsFolder(ctx, nil, projectID, env, path)
}

// TeamResult is RunTeam's terminal, successful outcome.
type TeamResult struct {
	// ClientID is the identity's Universal Auth client_id, confirmed
	// present by the R21 sweep.
	ClientID string
}

// RunTeam runs the team-phase step loop: folder creation, then the
// guided identity/UA-attach step, then the guided environment-grant
// step, then the R21 re-run verification sweep. Each step is a
// landing-check-guarded unit (Decision 1): every ensure* helper below
// probes world state first and only guides the operator when the
// probe shows the step hasn't landed yet, so a resumed invocation that
// finds everything already done walks straight through with no
// prompts at all.
func RunTeam(ctx context.Context, opts TeamOptions) (TeamResult, error) {
	if opts.CreateFolder == nil {
		opts.CreateFolder = defaultCreateFolder
	}

	// One bufio.Reader for every guided Pause across this whole
	// invocation, not one per Pause call: bufio.Reader fills its
	// internal buffer from opts.In in one Read, which for any real
	// pipe, file, or strings.Reader typically returns more than one
	// buffered line at once. A fresh bufio.Reader per Pause call (as
	// the public Pause function itself constructs) would silently
	// discard that over-read, unconsumed input the moment it goes out
	// of scope -- breaking AC-9b for any non-interactive input source
	// that returns multiple lines per Read (which is the common case
	// for piped/scripted stdin, not just an unusual edge case). A real
	// interactive TTY, which tends to return one line per Read, masks
	// this bug -- which is why it matters to reuse br explicitly here
	// rather than calling the public Pause helper directly.
	br := bufio.NewReader(opts.In)

	if err := ensureFolder(ctx, opts, br); err != nil {
		return TeamResult{}, err
	}
	// ensureIdentityAndUA's clientID return is deliberately discarded
	// here: verifyR21 below re-fetches it fresh rather than threading
	// this value through, because Decision 1 requires every landing
	// decision to be a live world-state probe, never a remembered
	// value -- reusing it here would be exactly the "remembered
	// cursor" the design exists to avoid, and would silently break the
	// resumed-run guarantee (a run that starts already-landed must hit
	// the same verifyR21 code path a stepped-through run does). Do not
	// "optimize" this into a single fetch.
	if _, err := ensureIdentityAndUA(ctx, opts, br); err != nil {
		return TeamResult{}, err
	}
	if err := ensureGrant(ctx, opts, br); err != nil {
		return TeamResult{}, err
	}
	return verifyR21(ctx, opts)
}

// pause prints instruction (sanitized, matching the public Pause
// function's own behavior) and reads one line from the shared br,
// discarding its content -- used instead of calling Pause directly so
// every guided step within one RunTeam invocation reads from the same
// bufio.Reader (see RunTeam's comment on br for why that matters).
func pause(instruction string, br *bufio.Reader, out io.Writer) error {
	_, err := readLine(Sanitize(instruction), br, out)
	return err
}

// ensureFolder automates folder/secret-path creation via the
// infisical CLI delegation (AC-8). CreateSecretsFolder is idempotent
// from this caller's point of view, so the same call serves as both
// the one-time automated action and its own re-probe: a plan-gated
// response degrades to a guided dashboard instruction for this one
// step (AC-11, never a raw provider error), and re-invoking the
// delegation after the operator's Pause both re-attempts the creation
// and re-checks whether it now exists.
func ensureFolder(ctx context.Context, opts TeamOptions, br *bufio.Reader) error {
	for {
		err := opts.CreateFolder(ctx, opts.ProjectID, opts.EnvironmentSlug, opts.SecretPath)
		if err == nil {
			return nil
		}
		if !errors.Is(err, infisical.ErrPlanGated) {
			return fmt.Errorf("onboard: creating secrets folder: %w", err)
		}

		instruction := fmt.Sprintf(
			"Your Infisical plan does not allow automated folder creation.\n"+
				"In the Infisical dashboard, create the secret path %s in the %s environment of project %s.\n"+
				"Press Enter once done.",
			Sanitize(secretPathOrDefault(opts.SecretPath)),
			Sanitize(opts.EnvironmentSlug),
			Sanitize(opts.ProjectID),
		)
		if err := pause(instruction, br, opts.Out); err != nil {
			return fmt.Errorf("onboard: waiting for guided folder creation: %w", err)
		}
		// Loop back: re-attempt the same idempotent delegation as the
		// re-probe (AC-11's "resumes the remaining steps
		// automatically" -- this step in particular resumes itself).
	}
}

// ensureIdentityAndUA guides the operator through creating the
// machine identity and attaching Universal Auth to it, landing-check-
// guarded by ReadIdentity: a 200 (client_id present) means both steps
// landed, per Decision 4's Assumption A (the read-identity call IS the
// attach check). A 404 means neither has landed yet -- the guided
// instruction covers both in one prompt, matching the design's single
// "identity create, UA attach" step. A failed landing check
// re-surfaces the instruction and does not advance (AC-9b).
func ensureIdentityAndUA(ctx context.Context, opts TeamOptions, br *bufio.Reader) (string, error) {
	for {
		clientID, err := infisical.ReadIdentity(ctx, opts.APIURL, opts.Bearer, opts.IdentityID)
		if err == nil {
			return clientID, nil
		}
		if errors.Is(err, infisical.ErrUnauthorized) {
			return "", &ExitCodeError{
				Code: ExitAuthFailure,
				Msg:  fmt.Sprintf("onboard: reading identity during team setup: %v", err),
			}
		}
		if !errors.Is(err, infisical.ErrIdentityNotFound) {
			return "", fmt.Errorf("onboard: reading identity: %w", err)
		}

		instruction := fmt.Sprintf(
			"In the Infisical dashboard, create a machine identity named %s and attach the %s auth method.\n"+
				"Press Enter once done.",
			Sanitize(opts.IdentityName), Sanitize(opts.AuthMethod),
		)
		if err := pause(instruction, br, opts.Out); err != nil {
			return "", fmt.Errorf("onboard: waiting for guided identity creation: %w", err)
		}
	}
}

// ensureGrant guides the operator through granting the identity read
// access to the target environment, landing-check-guarded by
// ReadProjectMembership -- the project-level membership read
// confirmed in NOTE-onboard-rest-verification.md as the environment-
// grant landing check's REST surface. A failed landing check
// re-surfaces the instruction and does not advance (AC-9b).
//
// granted here confirms project-level role assignment only -- per
// ReadProjectMembership's own doc comment, it cannot verify that the
// assigned role's permission conditions actually scope to
// opts.EnvironmentSlug specifically (that finer-grained detail isn't
// readable from this endpoint). This landing check, and R21 below,
// both inherit that same project-level-only blind spot; the design's
// documented fallback for it is trusting the operator's claim, which
// is exactly what the guided instruction below does.
func ensureGrant(ctx context.Context, opts TeamOptions, br *bufio.Reader) error {
	for {
		granted, err := infisical.ReadProjectMembership(ctx, opts.APIURL, opts.Bearer, opts.ProjectID, opts.IdentityID)
		if err != nil {
			if errors.Is(err, infisical.ErrUnauthorized) {
				return &ExitCodeError{
					Code: ExitAuthFailure,
					Msg:  fmt.Sprintf("onboard: reading project membership during team setup: %v", err),
				}
			}
			return fmt.Errorf("onboard: reading project membership: %w", err)
		}
		if granted {
			return nil
		}

		instruction := fmt.Sprintf(
			"In the Infisical dashboard, grant %s read access to the %s environment of project %s.\n"+
				"Press Enter once done.",
			Sanitize(opts.IdentityName), Sanitize(opts.EnvironmentSlug), Sanitize(opts.ProjectID),
		)
		if err := pause(instruction, br, opts.Out); err != nil {
			return fmt.Errorf("onboard: waiting for guided environment grant: %w", err)
		}
	}
}

// verifyR21 re-derives every team-setup probe from scratch: does the
// identity now expose a client_id, is the grant present, does the
// folder exist. This is what makes a resumed run correct without any
// persisted step state (Decision 1) -- an invocation that finds
// everything already landed reaches this function directly, with none
// of the ensure* guided loops above ever prompting. On failure, names
// the first missing artifact and is reported distinctly from R11
// (AC-35): the message is always prefixed "R21", never reusing R11's
// wording. R11 itself (the individual-phase wizard-end check) is not
// implemented in this package yet -- it lands in a later plan issue
// (PLAN-niwa-onboard.md Issue 8) -- so there is nothing to diff
// against directly today; "distinct" means this prefix convention,
// not a byte-for-byte comparison against existing R11 output.
func verifyR21(ctx context.Context, opts TeamOptions) (TeamResult, error) {
	clientID, err := infisical.ReadIdentity(ctx, opts.APIURL, opts.Bearer, opts.IdentityID)
	if err != nil {
		return TeamResult{}, &ExitCodeError{
			Code: ExitVerification,
			Msg:  fmt.Sprintf("R21 team verification failed: identity does not yet expose a client_id: %v", err),
		}
	}

	granted, err := infisical.ReadProjectMembership(ctx, opts.APIURL, opts.Bearer, opts.ProjectID, opts.IdentityID)
	if err != nil {
		return TeamResult{}, &ExitCodeError{
			Code: ExitVerification,
			Msg:  fmt.Sprintf("R21 team verification failed: reading environment grant: %v", err),
		}
	}
	if !granted {
		return TeamResult{}, &ExitCodeError{
			Code: ExitVerification,
			Msg:  "R21 team verification failed: environment read grant is not present",
		}
	}

	if opts.CreateFolder == nil {
		opts.CreateFolder = defaultCreateFolder
	}
	if err := opts.CreateFolder(ctx, opts.ProjectID, opts.EnvironmentSlug, opts.SecretPath); err != nil {
		return TeamResult{}, &ExitCodeError{
			Code: ExitVerification,
			Msg:  fmt.Sprintf("R21 team verification failed: secret-path folder does not exist: %v", err),
		}
	}

	return TeamResult{ClientID: clientID}, nil
}

// secretPathOrDefault mirrors CreateSecretsFolder's own empty-path
// default so the guided instruction names the exact path that
// delegation actually targeted.
func secretPathOrDefault(path string) string {
	if path == "" {
		return "/"
	}
	return path
}
