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
}

// TeamResult is RunTeam's terminal, successful outcome.
type TeamResult struct {
	// ClientID is the identity's Universal Auth client_id, confirmed
	// present by the R21 sweep.
	ClientID string
}

// createFolder is the folder-create CLI-delegation seam. Production
// wiring shells out to the real infisical CLI (or, under test, the
// writeFakeInfisical stub on PATH) via infisical.CreateSecretsFolder's
// own default commander -- infisical's commander type is unexported,
// so this package has no other injection point. team_test.go
// substitutes this var directly, mirroring the IsStdinTTY seam in
// prompt.go.
var createFolder = func(ctx context.Context, projectID, env, path string) error {
	return infisical.CreateSecretsFolder(ctx, nil, projectID, env, path)
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
	if err := ensureFolder(ctx, opts); err != nil {
		return TeamResult{}, err
	}
	if _, err := ensureIdentityAndUA(ctx, opts); err != nil {
		return TeamResult{}, err
	}
	if err := ensureGrant(ctx, opts); err != nil {
		return TeamResult{}, err
	}
	return verifyR21(ctx, opts)
}

// ensureFolder automates folder/secret-path creation via the
// infisical CLI delegation (AC-8). CreateSecretsFolder is idempotent
// from this caller's point of view, so the same call serves as both
// the one-time automated action and its own re-probe: a plan-gated
// response degrades to a guided dashboard instruction for this one
// step (AC-11, never a raw provider error), and re-invoking the
// delegation after the operator's Pause both re-attempts the creation
// and re-checks whether it now exists.
func ensureFolder(ctx context.Context, opts TeamOptions) error {
	for {
		err := createFolder(ctx, opts.ProjectID, opts.EnvironmentSlug, opts.SecretPath)
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
		if err := Pause(instruction, opts.In, opts.Out); err != nil {
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
func ensureIdentityAndUA(ctx context.Context, opts TeamOptions) (string, error) {
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
		if err := Pause(instruction, opts.In, opts.Out); err != nil {
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
func ensureGrant(ctx context.Context, opts TeamOptions) error {
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
		if err := Pause(instruction, opts.In, opts.Out); err != nil {
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
// wording.
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

	if err := createFolder(ctx, opts.ProjectID, opts.EnvironmentSlug, opts.SecretPath); err != nil {
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
