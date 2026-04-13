package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
//
// Must be called after the root command's PersistentPreRunE; reads from the
// cache populated by captureNiwaResponseFile.
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
//
// The path is cleaned with filepath.Clean before the prefix check so traversal
// sequences like "/tmp/../home/user/.bashrc" are collapsed and rejected. See
// the "NIWA_RESPONSE_FILE injection" section of
// docs/designs/current/DESIGN-shell-navigation-protocol.md for the threat
// model: an attacker who can set this env var must not be able to use it to
// overwrite files outside the temp directory.
func validateResponseFilePath(f string) error {
	if !filepath.IsAbs(f) {
		return fmt.Errorf("%s %q is not an absolute path", niwaResponseFileEnv, f)
	}
	cleaned := filepath.Clean(f)
	tmpDir := strings.TrimRight(os.Getenv("TMPDIR"), "/")
	if tmpDir != "" {
		tmpDir = filepath.Clean(tmpDir)
		if cleaned == tmpDir || strings.HasPrefix(cleaned, tmpDir+string(filepath.Separator)) {
			return nil
		}
	}
	const fallback = "/tmp"
	if cleaned == fallback || strings.HasPrefix(cleaned, fallback+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("%s %q is outside temp directory", niwaResponseFileEnv, f)
}

// validateLandingPath ensures the path is safe for the landing-path protocol.
// Both the stdout branch and the NIWA_RESPONSE_FILE branch require an absolute
// path with no embedded newlines (a newline would break the one-line-per-path
// contract the shell wrapper reads).
func validateLandingPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("internal error: landing path is not absolute: %s", path)
	}
	if strings.Contains(path, "\n") {
		return fmt.Errorf("internal error: landing path contains newline: %s", path)
	}
	return nil
}
