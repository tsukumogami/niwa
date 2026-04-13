package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// niwaResponseFileEnv is the internal protocol environment variable used by the
// shell wrapper to communicate a temp file path to the CLI for landing path
// delivery. See docs/designs/current/DESIGN-shell-navigation-protocol.md.
const niwaResponseFileEnv = "NIWA_RESPONSE_FILE"

// niwaResponseFile caches the NIWA_RESPONSE_FILE value captured before the
// root command's PersistentPreRunE unsets the environment variable (to prevent
// subprocess inheritance). writeLandingPath reads from this cache so that it
// still honors the protocol after the unset.
var niwaResponseFile string

// captureNiwaResponseFile reads NIWA_RESPONSE_FILE into the package-level cache
// and unsets the variable so subprocesses (git, gh, hooks) don't inherit it.
// It is called from the root command's PersistentPreRunE.
func captureNiwaResponseFile() error {
	niwaResponseFile = os.Getenv(niwaResponseFileEnv)
	return os.Unsetenv(niwaResponseFileEnv)
}

// writeLandingPath writes the landing directory path to the location negotiated
// with the shell wrapper.
//
// When NIWA_RESPONSE_FILE was set at process start, the path (followed by a
// newline) is written to that file and nothing is written to stdout. The file
// path must live under $TMPDIR or /tmp; any other location is rejected to
// prevent arbitrary file overwrites via env var injection.
//
// When NIWA_RESPONSE_FILE was absent, the path is written to stdout (one line),
// preserving backward compatibility with scripts that call niwa directly via
// command substitution (e.g., `dir=$(niwa go workspace)`).
func writeLandingPath(cmd *cobra.Command, path string) error {
	if f := niwaResponseFile; f != "" {
		if err := validateResponseFilePath(f); err != nil {
			return err
		}
		return os.WriteFile(f, []byte(path+"\n"), 0o600)
	}
	fmt.Fprintln(cmd.OutOrStdout(), path)
	return nil
}

// validateResponseFilePath ensures NIWA_RESPONSE_FILE points inside the temp
// directory ($TMPDIR or /tmp). Any other location is rejected.
func validateResponseFilePath(f string) error {
	tmpDir := strings.TrimRight(os.Getenv("TMPDIR"), "/")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	if strings.HasPrefix(f, tmpDir+"/") || strings.HasPrefix(f, "/tmp/") {
		return nil
	}
	return fmt.Errorf("%s %q is outside temp directory", niwaResponseFileEnv, f)
}
