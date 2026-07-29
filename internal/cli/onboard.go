package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/onboard"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(onboardCmd)
	onboardCmd.Flags().BoolVar(&onboardTeam, "team", false, "force team setup regardless of detected state (mutually exclusive with --individual)")
	onboardCmd.Flags().BoolVar(&onboardIndividual, "individual", false, "force individual setup regardless of detected state (mutually exclusive with --team)")
	onboardCmd.Flags().BoolVar(&onboardSameLogin, "same-login", false, "force same-login topology for an individual setup (mutually exclusive with --split-login)")
	onboardCmd.Flags().BoolVar(&onboardSplitLogin, "split-login", false, "force split-login topology for an individual setup (mutually exclusive with --same-login)")
	onboardCmd.Flags().BoolVar(&onboardJSON, "json", false, "emit the terminal outcome as a single JSON object on stdout")
	onboardCmd.Flags().BoolVar(&onboardAcceptAPIURL, "accept-api-url", false, "pre-acknowledge a non-default (self-hosted) api_url without an interactive prompt")
}

var (
	onboardTeam         bool
	onboardIndividual   bool
	onboardSameLogin    bool
	onboardSplitLogin   bool
	onboardJSON         bool
	onboardAcceptAPIURL bool
)

var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Interactively set up team or individual Infisical vault access",
	Long: `niwa onboard walks through vault access setup: team setup (declaring a
vault provider for the workspace) or individual setup (minting and
storing your own Universal Auth credential). The wizard detects which
setup applies and confirms with you before making any change.

--team/--individual and --same-login/--split-login override that
detection; combining either mutually-exclusive pair is a usage error.
In a non-interactive run (stdin is not a terminal), the overrides the
wizard needs must be supplied up front or the command fails fast rather
than guessing.`,
	Args: cobra.NoArgs,
	RunE: runOnboard,
}

// onboardEnvelope is the --json terminal envelope (Decision 2): status
// is tied 1:1 to exit_code, setup names which path was attempted (empty
// when unknown), and detail is a non-secret human-readable message.
// Setup-specific non-secret identifiers are added by the team/individual
// runners as they land; this envelope carries only the fields the
// command shell itself knows about.
type onboardEnvelope struct {
	Status   string `json:"status"`
	Setup    string `json:"setup,omitempty"`
	ExitCode int    `json:"exit_code"`
	Detail   string `json:"detail"`
}

// runOnboard is the cobra RunE entry point. It delegates to
// resolveAndRunOnboard for the actual work so that the --json envelope
// (below) can be emitted exactly once, from a single call site, for
// every terminal outcome -- including the flag-conflict usage errors,
// not just the wizard's own gate/stub outcomes.
func runOnboard(cmd *cobra.Command, args []string) error {
	result, runErr := resolveAndRunOnboard(cmd)

	if onboardJSON {
		emitOnboardJSON(cmd, result, runErr)
	}

	return runErr
}

func resolveAndRunOnboard(cmd *cobra.Command) (onboard.Result, error) {
	if onboardTeam && onboardIndividual {
		return onboard.Result{}, fmt.Errorf("--team and --individual are mutually exclusive")
	}
	if onboardSameLogin && onboardSplitLogin {
		return onboard.Result{}, fmt.Errorf("--same-login and --split-login are mutually exclusive")
	}
	if onboardTeam && (onboardSameLogin || onboardSplitLogin) {
		return onboard.Result{}, fmt.Errorf("--same-login/--split-login only apply to an individual setup; combining either with --team is a usage conflict")
	}

	setupOverride := onboard.PhaseUnknown
	switch {
	case onboardTeam:
		setupOverride = onboard.PhaseTeam
	case onboardIndividual:
		setupOverride = onboard.PhaseIndividual
	}

	topologyOverride := onboard.TopologyUnknown
	switch {
	case onboardSameLogin:
		topologyOverride = onboard.TopologySameLogin
	case onboardSplitLogin:
		topologyOverride = onboard.TopologySplitLogin
	}

	// Single TTY gate at entry (per Decision 3): interactive is decided
	// exactly once, here, and threaded through to the wizard rather than
	// re-checked per prompt.
	interactive := onboard.IsStdinTTY()
	var confirm onboard.ConfirmFunc
	if interactive {
		confirm = func(prompt string, defaultYes bool) (bool, error) {
			return onboard.Confirm(prompt, defaultYes, cmd.InOrStdin(), cmd.OutOrStdout())
		}
	}

	bundle, err := loadOnboardConfig()
	if err != nil {
		return onboard.Result{}, err
	}
	bearer, err := resolveOperatorBearer()
	if err != nil {
		return onboard.Result{}, err
	}
	apiURL := infisical.ResolveAPIURL(bundle.apiURLConfigVal)

	pause := func(prompt string) error {
		return onboard.Pause(prompt, cmd.InOrStdin(), cmd.OutOrStdout())
	}

	teamOpts := &onboard.TeamOptions{
		APIURL:          apiURL,
		Bearer:          bearer,
		ProjectID:       bundle.projectID,
		IdentityID:      bundle.identityID,
		IdentityName:    bundle.identityName,
		AuthMethod:      bundle.authMethod,
		EnvironmentSlug: bundle.environmentSlug,
		SecretPath:      bundle.secretPath,
		In:              cmd.InOrStdin(),
		Out:             cmd.OutOrStdout(),
	}
	individualOpts := &onboard.IndividualSetupParams{
		APIURL:      apiURL,
		Bearer:      bearer,
		IdentityID:  bundle.identityID,
		Kind:        bundle.kind,
		Project:     bundle.projectID,
		Environment: bundle.environmentSlug,
		SecretPath:  bundle.secretPath,
		SyncSpec:    bundle.syncSpec,
		Topology:    topologyOverride,
		Pause:       pause,
	}
	verifyOpts := &onboard.VerifyIndividualParams{
		GlobalOverride: bundle.globalOverride,
		TeamVault:      bundle.teamVault,
		Kind:           bundle.kind,
		Project:        bundle.projectID,
	}
	preconditionsOpts := &onboard.PreconditionsParams{
		Overlay: onboard.EnsurePersonalOverlayParams{
			OverlayDir: bundle.overlayDir,
			Repo:       bundle.registeredRepo,
			GitInvoker: workspace.StdGitInvoker(),
			Pause:      pause,
		},
		Pause: pause,
	}

	var detectOpts *onboard.DetectInputs
	if setupOverride == onboard.PhaseUnknown {
		detectOpts = &onboard.DetectInputs{
			APIURL:               apiURL,
			Bearer:               bearer,
			IdentityID:           bundle.identityID,
			TeamVaultEmpty:       bundle.teamVault == nil || bundle.teamVault.IsEmpty(),
			PersonalCredResolves: personalCredResolves(cmd.Context(), bundle),
		}
	}

	return onboard.Run(onboard.Options{
		SetupOverride:    setupOverride,
		TopologyOverride: topologyOverride,
		APIURLConfigVal:  bundle.apiURLConfigVal,
		AcceptAPIURL:     onboardAcceptAPIURL,
		Interactive:      interactive,
		Confirm:          confirm,
		Team:             teamOpts,
		Individual:       individualOpts,
		Verify:           verifyOpts,
		Preconditions:    preconditionsOpts,
		Detect:           detectOpts,
	})
}

// emitOnboardJSON writes the --json terminal envelope to stdout,
// independent of whatever cli.Execute() later prints to stderr for the
// same error -- scripts read the envelope from stdout, humans read the
// message from stderr.
func emitOnboardJSON(cmd *cobra.Command, result onboard.Result, err error) {
	env := onboardEnvelope{Status: onboard.StatusForCode(0), ExitCode: 0}
	if result.Setup != onboard.PhaseUnknown {
		env.Setup = result.Setup.String()
	}
	if err != nil {
		var ece *onboard.ExitCodeError
		if errors.As(err, &ece) {
			env.ExitCode = ece.Code
			env.Detail = ece.Msg
		} else {
			env.ExitCode = 1
			env.Detail = err.Error()
		}
		env.Status = onboard.StatusForCode(env.ExitCode)
	}
	_ = json.NewEncoder(cmd.OutOrStdout()).Encode(env)
}
