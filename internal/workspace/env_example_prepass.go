package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/guardrail"
)

// runEnvExamplePrePass reads .env.example from ctx.RepoDir and populates
// ctx.EnvExampleVars and ctx.EnvExampleSources on success.
//
// Returns non-nil error only when probable-secret keys are found that are not
// declared in the workspace config. Per-file parse errors and skip conditions
// (absent file, symlink, binary content, size limit) emit warnings to stderr
// and return nil — they are not failures.
//
// The function never includes value text in any warning or error string (R22).
func (e *EnvMaterializer) runEnvExamplePrePass(ctx *MaterializeContext) error {
	if !config.EffectiveReadEnvExample(ctx.Config, ctx.RepoName) {
		return nil
	}

	path := filepath.Join(ctx.RepoDir, ".env.example")

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		fmt.Fprintf(e.stderr(), "warning: .env.example in %s: %v\n", ctx.RepoName, err)
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(e.stderr(), "warning: .env.example in %s is a symlink; skipping\n", ctx.RepoName)
		return nil
	}

	vars, warnings, err := parseDotEnvExample(path)
	for _, w := range warnings {
		fmt.Fprintf(e.stderr(), "warning: %s\n", w)
	}
	if err != nil {
		fmt.Fprintf(e.stderr(), "warning: .env.example in %s: %v\n", ctx.RepoName, err)
		return nil
	}
	if len(vars) == 0 {
		return nil
	}

	excluded := buildSecretsExclusionSet(ctx)
	declared := make(map[string]bool, len(ctx.Effective.Env.Vars.Values))
	for k := range ctx.Effective.Env.Vars.Values {
		declared[k] = true
	}

	// Hoist remote visibility check outside the key loop; ctx.RepoDir is fixed.
	publicRemotes, haveGit := guardrail.EnumerateGitHubRemotes(ctx.RepoDir)
	gitWarnOnce := false // warn at most once if a probable-secret key needs the guardrail

	result := make(map[string]string)
	var errs []error

	for _, key := range sortedKeysStr(vars) {
		value := vars[key]

		if excluded[key] {
			// Excluded keys are silently skipped — no diagnostic.
			continue
		}

		if declared[key] {
			result[key] = value
			continue
		}

		isSafe, reason := classifyEnvValue(value)
		if isSafe {
			fmt.Fprintf(e.stderr(), "warning: .env.example in %s: undeclared key %s has a safe value; including\n", ctx.RepoName, key)
			result[key] = value
		} else if !haveGit {
			if !gitWarnOnce {
				fmt.Fprintf(e.stderr(), "warning: .env.example in %s: could not check remote visibility; public-remote guardrail skipped\n", ctx.RepoName)
				gitWarnOnce = true
			}
			errs = append(errs, fmt.Errorf("key %s: %s", key, reason))
		} else if len(publicRemotes) > 0 {
			// Public GitHub remote found.
			if ctx.AllowPlaintextSecrets {
				fmt.Fprintf(e.stderr(), "warning: .env.example in %s: key %s is a probable secret but --allow-plaintext-secrets is set; including\n", ctx.RepoName, key)
				result[key] = value
			} else {
				errs = append(errs, fmt.Errorf("key %s: %s (repo has a public GitHub remote; use --allow-plaintext-secrets to bypass or add to workspace [env.secrets])", key, reason))
			}
		} else {
			// Private or non-GitHub remote: basic probable-secret error.
			errs = append(errs, fmt.Errorf("key %s: %s", key, reason))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf(".env.example in %s contains probable secrets not declared in workspace config:\n%w", ctx.RepoName, errors.Join(errs...))
	}

	if len(result) == 0 {
		return nil
	}

	ctx.EnvExampleVars = result
	ctx.EnvExampleSources = []SourceEntry{{Kind: SourceKindEnvExample, SourceID: ".env.example"}}
	return nil
}

// buildSecretsExclusionSet collects keys declared as secrets across effective
// env.secrets and claude.env.secrets (Values, Required, Recommended, Optional)
// and per-repo env.secrets, so they are excluded from the .env.example pre-pass.
func buildSecretsExclusionSet(ctx *MaterializeContext) map[string]bool {
	set := make(map[string]bool)
	addTable := func(t config.EnvVarsTable) {
		for k := range t.Values {
			set[k] = true
		}
		for k := range t.Required {
			set[k] = true
		}
		for k := range t.Recommended {
			set[k] = true
		}
		for k := range t.Optional {
			set[k] = true
		}
	}
	addTable(ctx.Effective.Env.Secrets)
	addTable(ctx.Effective.Claude.Env.Secrets)
	if ctx.Config != nil {
		if repo, ok := ctx.Config.Repos[ctx.RepoName]; ok {
			addTable(repo.Env.Secrets)
		}
	}
	return set
}

// sortedKeysStr returns the keys of a string map in lexical order.
func sortedKeysStr(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
