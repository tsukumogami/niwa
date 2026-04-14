package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(shellInitCmd)
	shellInitCmd.AddCommand(shellInitBashCmd)
	shellInitCmd.AddCommand(shellInitZshCmd)
	shellInitCmd.AddCommand(shellInitAutoCmd)
	shellInitCmd.AddCommand(shellInitInstallCmd)
	shellInitCmd.AddCommand(shellInitUninstallCmd)
	shellInitCmd.AddCommand(shellInitStatusCmd)
}

var shellInitCmd = &cobra.Command{
	Use:   "shell-init",
	Short: "Generate shell integration (wrapper function and completions)",
	Long: `Generate shell wrapper function and completions for niwa.

The wrapper intercepts cd-eligible commands (create, go) so that niwa can
change the shell's working directory after creating or switching to a
workspace instance.

Add this to your shell profile:

  eval "$(niwa shell-init auto)"`,
}

const shellWrapperTemplate = `export _NIWA_SHELL_INIT=1

niwa() {
    case "$1" in
        create|go)
            local __niwa_tmp __niwa_dir __niwa_rc
            __niwa_tmp=$(mktemp) || { command niwa "$@"; return $?; }
            NIWA_RESPONSE_FILE="$__niwa_tmp" command niwa "$@"
            __niwa_rc=$?
            __niwa_dir=$(cat "$__niwa_tmp" 2>/dev/null)
            rm -f "$__niwa_tmp"
            if [ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ]; then
                builtin cd "$__niwa_dir" || return
            fi
            return $__niwa_rc
            ;;
        *)
            command niwa "$@"
            ;;
    esac
}
`

var shellInitBashCmd = &cobra.Command{
	Use:   "bash",
	Short: "Generate bash shell integration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprint(cmd.OutOrStdout(), shellWrapperTemplate)

		var buf bytes.Buffer
		if err := rootCmd.GenBashCompletionV2(&buf, true); err != nil {
			return fmt.Errorf("generating bash completions: %w", err)
		}
		fmt.Fprint(cmd.OutOrStdout(), buf.String())
		return nil
	},
}

var shellInitZshCmd = &cobra.Command{
	Use:   "zsh",
	Short: "Generate zsh shell integration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprint(cmd.OutOrStdout(), shellWrapperTemplate)

		var buf bytes.Buffer
		if err := rootCmd.GenZshCompletion(&buf); err != nil {
			return fmt.Errorf("generating zsh completions: %w", err)
		}
		fmt.Fprint(cmd.OutOrStdout(), buf.String())
		return nil
	},
}

var shellInitAutoCmd = &cobra.Command{
	Use:   "auto",
	Short: "Detect shell and generate appropriate integration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		shell := detectShell()
		switch shell {
		case "bash":
			return shellInitBashCmd.RunE(cmd, args)
		case "zsh":
			return shellInitZshCmd.RunE(cmd, args)
		default:
			// Unknown shell: empty output, exit 0.
			return nil
		}
	},
}

func detectShell() string {
	if os.Getenv("ZSH_VERSION") != "" {
		return "zsh"
	}
	if os.Getenv("BASH_VERSION") != "" {
		return "bash"
	}
	return ""
}

// EnvFileWithDelegation returns the full env file content with shell wrapper delegation.
func EnvFileWithDelegation() string {
	return `# niwa shell configuration
export PATH="$HOME/.niwa/bin:$PATH"
if command -v niwa >/dev/null 2>&1; then
  eval "$(niwa shell-init auto 2>/dev/null)"
fi
`
}

// EnvFilePathOnly returns the env file content with PATH setup only (no delegation).
func EnvFilePathOnly() string {
	return `# niwa shell configuration
export PATH="$HOME/.niwa/bin:$PATH"
`
}

func shellRCFiles() []string {
	home := os.Getenv("HOME")
	var files []string
	bashrc := filepath.Join(home, ".bashrc")
	if _, err := os.Stat(bashrc); err == nil {
		files = append(files, bashrc)
	}
	zshenv := filepath.Join(home, ".zshenv")
	if _, err := os.Stat(zshenv); err == nil {
		files = append(files, zshenv)
	}
	return files
}

func addSourceLine(rcFile, sourceLine string) (bool, error) {
	data, err := os.ReadFile(rcFile)
	if err != nil {
		return false, err
	}
	if strings.Contains(string(data), sourceLine) {
		return false, nil
	}
	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n%s\n", sourceLine)
	return err == nil, err
}

var shellInitInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install shell integration (env file and rc file source line)",
	Args:  cobra.NoArgs,
	RunE:  runShellInitInstall,
}

func runShellInitInstall(cmd *cobra.Command, args []string) error {
	niwaHome := filepath.Join(os.Getenv("HOME"), ".niwa")
	envFile := filepath.Join(niwaHome, "env")

	if err := os.MkdirAll(niwaHome, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	envContent := EnvFileWithDelegation()
	if err := os.WriteFile(envFile, []byte(envContent), 0o644); err != nil {
		return fmt.Errorf("writing env file: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s\n", envFile)

	sourceLine := `. "$HOME/.niwa/env"`
	rcFiles := shellRCFiles()
	for _, rc := range rcFiles {
		added, err := addSourceLine(rc, sourceLine)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not update %s: %v\n", rc, err)
			continue
		}
		if added {
			fmt.Fprintf(cmd.ErrOrStderr(), "Added source line to %s\n", rc)
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "Source line already present in %s\n", rc)
		}
	}

	fmt.Fprintln(cmd.ErrOrStderr(), "\nShell integration installed. Open a new terminal to activate.")
	return nil
}

var shellInitUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Disable shell wrapper delegation (keeps PATH setup)",
	Args:  cobra.NoArgs,
	RunE:  runShellInitUninstall,
}

func runShellInitUninstall(cmd *cobra.Command, args []string) error {
	envFile := filepath.Join(os.Getenv("HOME"), ".niwa", "env")

	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		fmt.Fprintln(cmd.ErrOrStderr(), "Shell integration is not installed (no env file found).")
		return nil
	}

	envContent := EnvFilePathOnly()
	if err := os.WriteFile(envFile, []byte(envContent), 0o644); err != nil {
		return fmt.Errorf("writing env file: %w", err)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s (PATH-only, delegation removed)\n", envFile)
	fmt.Fprintln(cmd.ErrOrStderr(), "Shell integration disabled. Open a new terminal for the change to take effect.")
	return nil
}

var shellInitStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show shell integration status",
	Args:  cobra.NoArgs,
	RunE:  runShellInitStatus,
}

func runShellInitStatus(cmd *cobra.Command, args []string) error {
	w := cmd.ErrOrStderr()

	if os.Getenv("_NIWA_SHELL_INIT") != "" {
		fmt.Fprintln(w, "Shell wrapper: loaded in current shell")
	} else {
		fmt.Fprintln(w, "Shell wrapper: not loaded in current shell")
	}

	envFile := filepath.Join(os.Getenv("HOME"), ".niwa", "env")
	data, err := os.ReadFile(envFile)
	if err != nil {
		fmt.Fprintln(w, "Env file: not found")
	} else if strings.Contains(string(data), "niwa shell-init auto") {
		fmt.Fprintln(w, "Env file: delegation block present (will load on next shell)")
	} else {
		fmt.Fprintln(w, "Env file: PATH-only (no delegation)")
	}

	return nil
}
