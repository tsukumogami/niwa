package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// secretClassification is the auditable state of a single *.secrets
// table entry. Classification is derived from the parsed team config:
// vault:// refs stay in MaybeSecret.Plain until the resolver runs, so a
// plain-text-Plain with no vault:// prefix is a plaintext leak.
const (
	classVaultRef  = "vault-ref"
	classPlaintext = "plaintext"
	classEmpty     = "empty"
	classResolved  = "resolved"
)

// auditEntry describes one enumerated *.secrets key with its
// classification, table attribution, and shadow state.
type auditEntry struct {
	Key            string
	Classification string
	Table          string
	Shadowed       string // "no" or "yes (personal-overlay[, scope=<s>])"
}

// runAuditSecrets enumerates *.secrets tables in the team workspace
// config and prints a classification table. Exits non-zero (returned
// error) when any plaintext value is found AND a vault is configured.
//
// The command runs entirely offline: it reads the parsed workspace
// config and the last-applied state's Shadows slice, without invoking
// any provider.
func runAuditSecrets(cmd *cobra.Command, cwd string) error {
	configPath, _, err := config.Discover(cwd)
	if err != nil {
		return fmt.Errorf("finding workspace config: %w", err)
	}

	result, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg := result.Config

	// Pull shadows from the nearest instance state, if any. The
	// audit may be run from either the workspace root or an instance
	// directory; both paths are supported by walking up for a state
	// file.
	shadows := loadShadowsForAudit(cwd)

	entries := collectAuditEntries(cfg, shadows)

	printAuditTable(cmd.OutOrStdout(), entries)

	// Exit non-zero when plaintext values are found AND a vault is
	// configured. The rationale: without a vault, the user hasn't
	// opted into the vault workflow, so plaintext values are their
	// intentional baseline. With a vault configured, plaintext is a
	// leak the user has the machinery to fix.
	//
	// "Configured" here means the same thing it means in init.go's
	// emitVaultBootstrapPointer and status_check_vault.go's
	// runCheckVault: at least one provider is declared. A [vault]
	// block with only team_only is not enough — there is no provider
	// to route vault:// refs to, so the user has no machinery to fix
	// plaintext even if they want to.
	hasPlaintext := false
	for _, e := range entries {
		if e.Classification == classPlaintext {
			hasPlaintext = true
			break
		}
	}
	if hasPlaintext && cfg.Vault != nil && !cfg.Vault.IsEmpty() {
		return fmt.Errorf("plaintext values present in *.secrets tables while a vault is configured")
	}
	return nil
}

// loadShadowsForAudit returns the Shadows slice from the nearest
// instance state, or nil when no state is reachable. The audit command
// is informational and MUST NOT fail when there is no state to read;
// missing state simply yields SHADOWED="no" for every row.
func loadShadowsForAudit(cwd string) []workspace.Shadow {
	instanceRoot, err := workspace.DiscoverInstance(cwd)
	if err != nil {
		return nil
	}
	state, err := workspace.LoadState(instanceRoot)
	if err != nil {
		return nil
	}
	return state.Shadows
}

// collectAuditEntries walks every *.secrets location in cfg and
// produces a sorted slice of audit entries. Walks:
//
//   - [env.secrets]
//   - [claude.env.secrets]
//   - [repos.<name>.env.secrets]
//   - [repos.<name>.claude.env.secrets]
//   - [instance.env.secrets]
//   - [instance.claude.env.secrets]
//
// *.vars tables are intentionally skipped (non-sensitive by
// declaration; see PRD R33).
//
// The shadows slice comes from InstanceState.Shadows, populated by the
// last apply. The SHADOWED column reads from that slice rather than
// re-running DetectShadows here, so the audit works without a global
// config directory and without re-parsing overlay TOML.
func collectAuditEntries(cfg *config.WorkspaceConfig, shadows []workspace.Shadow) []auditEntry {
	if cfg == nil {
		return nil
	}

	// Build a quick lookup: (kind, key) -> yes/no+scope metadata.
	// Any shadow for an env-secret or claude-env-secret KEY flips
	// the SHADOWED column. For v1 all shadows are personal-overlay.
	shadowFor := func(key string) string {
		for _, s := range shadows {
			if s.Name != key {
				continue
			}
			if s.Kind == "env-secret" || s.Kind == "claude-env-secret" {
				// v1 has only the personal-overlay layer; future
				// layers would extend the suffix.
				return fmt.Sprintf("yes (%s)", s.Layer)
			}
		}
		return "no"
	}

	var entries []auditEntry
	add := func(table string, values map[string]config.MaybeSecret) {
		for _, key := range sortedSecretKeys(values) {
			ms := values[key]
			entries = append(entries, auditEntry{
				Key:            key,
				Classification: classifyMaybeSecret(ms),
				Table:          table,
				Shadowed:       shadowFor(key),
			})
		}
	}

	add("env.secrets", cfg.Env.Secrets.Values)
	add("claude.env.secrets", cfg.Claude.Env.Secrets.Values)

	// Per-repo overrides. Sort repo names for deterministic output.
	repoNames := make([]string, 0, len(cfg.Repos))
	for name := range cfg.Repos {
		repoNames = append(repoNames, name)
	}
	sort.Strings(repoNames)
	for _, name := range repoNames {
		ov := cfg.Repos[name]
		add(fmt.Sprintf("repos.%s.env.secrets", name), ov.Env.Secrets.Values)
		if ov.Claude != nil {
			add(fmt.Sprintf("repos.%s.claude.env.secrets", name), ov.Claude.Env.Secrets.Values)
		}
	}

	add("instance.env.secrets", cfg.Instance.Env.Secrets.Values)
	if cfg.Instance.Claude != nil {
		add("instance.claude.env.secrets", cfg.Instance.Claude.Env.Secrets.Values)
	}

	return entries
}

// classifyMaybeSecret returns one of classVaultRef, classPlaintext,
// classEmpty, or classResolved. The audit runs on the parsed team
// config (pre-resolve), so classResolved is only produced when the
// caller has already run the resolver against the same struct; in the
// normal CLI path cfg is pre-resolve and classResolved is unreachable.
func classifyMaybeSecret(ms config.MaybeSecret) string {
	if ms.IsSecret() {
		return classResolved
	}
	if strings.HasPrefix(ms.Plain, "vault://") {
		return classVaultRef
	}
	if ms.Plain == "" {
		return classEmpty
	}
	return classPlaintext
}

// sortedSecretKeys returns the keys of a MaybeSecret map in lexical
// order. Duplicates a helper in internal/workspace but the dependency
// direction (cli -> workspace) forbids borrowing it; keeping a local
// copy is cheaper than exporting.
func sortedSecretKeys(m map[string]config.MaybeSecret) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// printAuditTable writes a KEY/CLASSIFICATION/TABLE/SHADOWED table to
// out. The column widths are computed once from the rows so every row
// aligns even when one key is much longer than the rest.
func printAuditTable(out io.Writer, entries []auditEntry) {
	if len(entries) == 0 {
		fmt.Fprintln(out, "No *.secrets entries found.")
		return
	}

	keyWidth := len("KEY")
	classWidth := len("CLASSIFICATION")
	tableWidth := len("TABLE")
	for _, e := range entries {
		if w := len(e.Key); w > keyWidth {
			keyWidth = w
		}
		if w := len(e.Classification); w > classWidth {
			classWidth = w
		}
		if w := len(e.Table); w > tableWidth {
			tableWidth = w
		}
	}

	format := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%s\n", keyWidth, classWidth, tableWidth)
	fmt.Fprintf(out, format, "KEY", "CLASSIFICATION", "TABLE", "SHADOWED")
	for _, e := range entries {
		fmt.Fprintf(out, format, e.Key, e.Classification, e.Table, e.Shadowed)
	}
}
