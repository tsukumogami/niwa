package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/onboard"
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

func runOnboard(cmd *cobra.Command, args []string) error {
	if onboardTeam && onboardIndividual {
		return fmt.Errorf("--team and --individual are mutually exclusive")
	}
	if onboardSameLogin && onboardSplitLogin {
		return fmt.Errorf("--same-login and --split-login are mutually exclusive")
	}
	if onboardTeam && (onboardSameLogin || onboardSplitLogin) {
		return fmt.Errorf("--same-login/--split-login only apply to an individual setup; combining either with --team is a usage conflict")
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

	result, runErr := onboard.Run(onboard.Options{
		SetupOverride:    setupOverride,
		TopologyOverride: topologyOverride,
		AcceptAPIURL:     onboardAcceptAPIURL,
		Interactive:      interactive,
		Confirm:          confirm,
	})

	if onboardJSON {
		emitOnboardJSON(cmd, result, runErr)
	}

	return runErr
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
