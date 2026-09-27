package infisical

import (
	"context"
	"errors"
	"strings"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// ErrPlanGated is returned by CreateSecretsFolder when the CLI rejects
// the delegation with a plan-tier restriction marker in its (scrubbed)
// stderr. This is the signal the team-phase step loop branches on
// (R6/R7/AC-11): a plan-gated step degrades to guided dashboard
// instructions for that specific step, never a raw provider error; any
// other CLI failure is not plan-gated and surfaces as a genuine error.
var ErrPlanGated = errors.New("infisical: action rejected by plan restriction")

// planGateMarkers are case-insensitive substrings that identify a
// plan-tier restriction in the CLI's stderr. The set is deliberately
// narrow -- phrases that unambiguously name a plan/billing restriction
// -- mirroring looksLikeAuthFailure's own narrow-marker discipline in
// subprocess.go, so a transient or unrelated failure is never
// misclassified as gated (which would silently degrade a real error
// into a guided-wait loop that can never succeed).
var planGateMarkers = []string{
	"plan does not allow",
	"upgrade your plan",
	"not available on your plan",
	"not included in your plan",
	"requires a paid plan",
	"plan restriction",
}

// looksLikePlanGate reports whether scrubbed stderr names a plan-tier
// restriction. Match runs after scrubbing (same discipline as
// looksLikeAuthFailure), so no secret fragment can influence or leak
// through this classification.
func looksLikePlanGate(scrubbed string) bool {
	if scrubbed == "" {
		return false
	}
	lower := strings.ToLower(scrubbed)
	for _, m := range planGateMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// CreateSecretsFolder delegates folder/secret-path creation to
// `infisical secrets folders create` on the operator's own CLI
// session -- never a management REST call, preserving the team
// phase's custody boundary (R5, AC-10). It is idempotent from the
// caller's point of view: invoking it against a path that already
// exists is expected to succeed (a no-op on the provider side), so the
// same call serves both as the one-time automated creation step and,
// re-invoked, as the world-state landing-check probe Decision 1
// requires ("does the folder path exist?") -- there is no separate
// list/get-folder REST surface to probe instead.
//
// c is the commander to run the subprocess through; pass nil to use
// the real infisical CLI on PATH (production default), matching the
// nil-defaults-to-defaultCommander convention DetectSessionStatus
// already establishes.
func CreateSecretsFolder(ctx context.Context, c commander, projectID, env, path string) error {
	if c == nil {
		c = defaultCommander{}
	}
	if projectID == "" || env == "" {
		return secret.Errorf("infisical: CreateSecretsFolder requires non-empty projectID and env")
	}
	if path == "" {
		path = "/"
	}

	args := []string{
		"secrets", "folders", "create",
		"--projectId", projectID,
		"--env", env,
		"--path", path,
	}
	_, stderrBytes, exitCode, err := c.Run(ctx, "infisical", args)
	if err != nil {
		return secret.Errorf(
			"infisical: running secrets folders create: %w: %w",
			vault.ErrProviderUnreachable, err,
		)
	}
	if exitCode != 0 {
		scrubbed := vault.ScrubStderr(ctx, stderrBytes)
		if looksLikePlanGate(scrubbed) {
			return secret.Errorf(
				"infisical: secrets folders create exited %d: %s: %w",
				exitCode, strings.TrimSpace(scrubbed), ErrPlanGated,
			)
		}
		return secret.Errorf(
			"infisical: secrets folders create exited %d: %s",
			exitCode, strings.TrimSpace(scrubbed),
		)
	}
	return nil
}
