package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// hintShellInit prints a hint about shell integration if the wrapper is not loaded.
// Call this from cd-eligible commands (create, go).
func hintShellInit(cmd *cobra.Command) {
	if os.Getenv("_NIWA_SHELL_INIT") != "" {
		return
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "hint: shell integration not detected. For auto-cd and completions, run:")
	fmt.Fprintln(cmd.ErrOrStderr(), "  niwa shell-init install")
}
