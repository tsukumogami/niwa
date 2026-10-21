package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(authorityCmd)
	authorityCmd.AddCommand(authorityShowCmd)
	// The decide subcommand, which answers who settles one decision point for
	// the calling session, registers here beside show once it exists. It
	// reads the same configuration through loadAuthorityConfig, then the
	// caller's dispatch record ahead of it.
}

var authorityCmd = &cobra.Command{
	Use:   "authority",
	Short: "Inspect how much dispatched sessions may decide",
	Long: `Inspect the authority level: how much a session launched by "niwa dispatch"
may decide on its own.

The levels, from widest to narrowest, are autonomous (the session settles a
decision point itself), supervised (the session that dispatched it decides) and
strict (a person decides). A dispatch runs at the narrowest of the workspace's
[workspace].authority_level (autonomous when unset), this machine's
[global].authority_level, and "niwa dispatch --authority <level>". A flag wider
than the other two is refused.`,
}

var authorityShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the configured authority levels and what a dispatch would get",
	Long: `Show the workspace's authority level, this machine's ceiling, and the level a
dispatch with no --authority flag would run at, with the setting it comes from.

Run it inside a workspace. It exits 1 outside one, or when either setting holds
a value other than autonomous, supervised or strict.`,
	Args: cobra.NoArgs,
	RunE: runAuthorityShow,
}

func runAuthorityShow(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("classifying working directory: %w", err)
	}
	// An orphan instance classifies with no workspace root, and there is no
	// workspace level to show for it, so it is refused the same as outside.
	if class.WorkspaceRoot == "" {
		return fmt.Errorf("not inside a niwa workspace; run niwa authority show from within a workspace")
	}

	in, err := loadAuthorityConfig(class.WorkspaceRoot)
	if err != nil {
		return err
	}
	writeAuthorityShow(cmd.OutOrStdout(), in)
	return nil
}

// loadAuthorityConfig reads the workspace and machine authority levels a
// dispatch from workspaceRoot would resolve against. Either file failing to
// load, or holding a value outside the three level names, is an error rather
// than "not set": reporting a broken setting as absent would show a wider
// level than any dispatch will actually get. A missing personal config is not
// an error; it loads as empty, the same as it does for dispatch.
func loadAuthorityConfig(workspaceRoot string) (authority.Inputs, error) {
	wsPath := filepath.Join(workspaceRoot, workspace.StateDir, workspace.WorkspaceConfigFile)
	res, err := config.Load(wsPath)
	if err != nil {
		return authority.Inputs{}, fmt.Errorf("loading %s: %w", wsPath, err)
	}
	wsLevel, err := res.Config.AuthorityLevel()
	if err != nil {
		return authority.Inputs{}, fmt.Errorf("%w (in %s)", err, wsPath)
	}

	cfgPath := globalConfigPathForError()
	gc, err := config.LoadGlobalConfig()
	if err != nil {
		return authority.Inputs{}, fmt.Errorf("loading %s: %w", cfgPath, err)
	}
	machineLevel, err := gc.AuthorityLevel()
	if err != nil {
		return authority.Inputs{}, fmt.Errorf("%w (in %s)", err, cfgPath)
	}

	return authority.Inputs{Workspace: wsLevel, Machine: machineLevel}, nil
}

// writeAuthorityShow prints the three lines `niwa authority show` promises.
// The last is the Ceiling, which is exactly what a dispatch with no flag
// resolves to.
func writeAuthorityShow(w io.Writer, in authority.Inputs) {
	c := authority.Ceiling(in)
	fmt.Fprintf(w, "workspace: %s\n", levelOrNotSet(in.Workspace))
	fmt.Fprintf(w, "machine: %s\n", levelOrNotSet(in.Machine))
	fmt.Fprintf(w, "default dispatch: %s from %s\n", c.Level, c.Source)
}

func levelOrNotSet(l *authority.Level) string {
	if l == nil {
		return "not set"
	}
	return l.String()
}
