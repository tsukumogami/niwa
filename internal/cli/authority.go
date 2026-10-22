package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(authorityCmd)
	authorityCmd.AddCommand(authorityPolicyCmd)
	authorityPolicyCmd.Flags().BoolVar(&authorityPolicyJSON, "json", false,
		"print the policy as one "+authority.PolicySchemaV1+" JSON document")
	// A usage error in this group exits 2, apart from a config or record
	// error's 1, so a caller can tell a typo from a refusal. Cobra looks the
	// flag-error function up through the parents, so this covers every
	// subcommand.
	authorityCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitCodeError{code: 2, err: err}
	})
}

var authorityPolicyJSON bool

// exitCodeError carries the exit code a command wants; Execute prints the
// error and exits with it.
type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string { return e.err.Error() }
func (e *exitCodeError) Unwrap() error { return e.err }

// usageArgs wraps a cobra argument validator so its failure exits 2.
func usageArgs(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return &exitCodeError{code: 2, err: err}
		}
		return nil
	}
}

var authorityCmd = &cobra.Command{
	Use:   "authority",
	Short: "Inspect authority levels and named grants",
	Long: `Inspect the authority levels and named grants dispatched sessions run under.

A workspace declares its authority level, grant ceiling and roles in the
[authority] table of .niwa/workspace.toml; an overlay and the machine config
can only narrow them.`,
}

var authorityPolicyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Show the authority policy this workspace declares",
	Long: `Show the authority policy this workspace declares: for the workspace config,
the overlay and the machine config, each declared level and ceiling (or "not
set"); the effective level ceiling and grant ceiling with the layer each came
from; each role with its authorities, marking those outside the effective
ceiling; and what a dispatch with no authority flags would resolve to.

Warnings (an authority name this niwa doesn't know, dropped; an overlay or
machine ceiling naming an authority the workspace ceiling lacks; an overlay
roles table, which has no effect) go to stderr.

Exit codes: 0 printed, 1 a config error or outside a workspace, 2 usage.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runAuthorityPolicy,
}

// loadAuthorityPolicy finds the workspace containing dir and builds its
// authority policy from the base config, the overlay provisioning would use
// and the machine config. Every failure is an exit-1 error.
func loadAuthorityPolicy(dir string) (authority.Policy, []string, error) {
	fail := func(err error) (authority.Policy, []string, error) {
		return authority.Policy{}, nil, &exitCodeError{code: 1, err: err}
	}
	configPath, configDir, err := config.Discover(dir)
	if err != nil {
		return fail(fmt.Errorf("not inside a niwa workspace: %w", err))
	}
	result, err := config.Load(configPath)
	if err != nil {
		return fail(fmt.Errorf("loading %s: %w", configPath, err))
	}
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fail(fmt.Errorf("loading global config: %w", err))
	}
	workspaceRoot := filepath.Dir(configDir)
	sourceURL, err := registeredConfigSourceURL(workspaceRoot, result.Config, globalCfg)
	if err != nil {
		return fail(err)
	}
	overlay, err := workspace.ResolveAuthorityOverlay(workspaceRoot, sourceURL)
	if err != nil {
		return fail(err)
	}
	p, warnings, err := config.AuthorityPolicy(result.Config, overlay, globalCfg)
	if err != nil {
		return p, warnings, &exitCodeError{code: 1, err: err}
	}
	return p, warnings, nil
}

func runAuthorityPolicy(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return &exitCodeError{code: 1, err: fmt.Errorf("getting working directory: %w", err)}
	}
	p, warnings, err := loadAuthorityPolicy(cwd)
	for _, w := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	if err != nil {
		return err
	}
	report := authority.BuildPolicyReport(p, warnings)
	if authorityPolicyJSON {
		b, err := report.MarshalJSON()
		if err != nil {
			return &exitCodeError{code: 1, err: err}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", b)
		return nil
	}
	fmt.Fprint(cmd.OutOrStdout(), report.Text())
	return nil
}
