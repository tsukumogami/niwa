package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tsukumogami/niwa/internal/config"
)

// runEnvExamplePrePass reads .env.example from ctx.RepoDir and populates
// ctx.EnvExampleVars and ctx.EnvExampleSources on success.
//
// For each undeclared, non-excluded key it classifies the value into a typed
// detection category and resolves a warn/fail action via
// EffectiveEnvExamplePolicy (operator per-variable -> inline annotation ->
// per-category global/workspace/repo -> default warn). A resolved fail
// accumulates an error that aborts apply; a resolved warn includes the key.
// --allow-plaintext-secrets downgrades every resolved fail to warn for the
// run, emitting a per-key audit diagnostic; an inline annotation lowering a
// configured fail emits a distinct downgrade diagnostic. There is no
// remote-visibility branch.
//
// Per-file parse errors and skip conditions (absent file, symlink, binary
// content, size limit) emit warnings to stderr and return nil — not failures.
//
// Every diagnostic names the key and category only; the function never
// includes value text, a value fragment, the matched vendor prefix, or the
// entropy score in any warning or error string (R10/R22).
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

	vars, annotations, warnings, err := parseDotEnvExample(path)
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

		category, _ := classifyEnvValue(value)
		if category == config.CategorySafe {
			// Preserve the safe-key behavior: include without a
			// probable-secret diagnostic.
			fmt.Fprintf(e.stderr(), "warning: .env.example in %s: undeclared key %s has a safe value; including\n", ctx.RepoName, key)
			result[key] = value
			continue
		}

		// Resolve the effective action for this undeclared probable-secret key.
		// The inline annotation (parsed from the file) participates in the
		// most-specific-wins cascade.
		var inline *config.Action
		if a, ok := annotations[key]; ok {
			inline = &a
		}
		action := config.EffectiveEnvExamplePolicy(ctx.GlobalEnvExamplePolicy, ctx.Config, ctx.RepoName, key, category, inline)

		// Detect an inline-driven downgrade: if the action resolved WITHOUT the
		// inline annotation is fail but WITH it is warn, a repo-supplied inline
		// annotation lowered an operator-configured floor. Emit a distinct,
		// greppable diagnostic so the downgrade is observable (R-supply-chain).
		if inline != nil {
			withoutInline := config.EffectiveEnvExamplePolicy(ctx.GlobalEnvExamplePolicy, ctx.Config, ctx.RepoName, key, category, nil)
			if withoutInline == config.ActionFail && action == config.ActionWarn {
				fmt.Fprintf(e.stderr(), "warning: .env.example in %s: inline annotation lowered a configured fail to warn for key %s (category %s)\n", ctx.RepoName, key, category)
			}
		}

		// --allow-plaintext-secrets downgrades every resolved fail to warn for
		// this run, emitting a per-key audit diagnostic so the broadened blast
		// radius is greppable (Decision 4).
		if ctx.AllowPlaintextSecrets && action == config.ActionFail {
			fmt.Fprintf(e.stderr(), "audit: .env.example in %s: --allow-plaintext-secrets downgraded fail to warn for key %s (category %s)\n", ctx.RepoName, key, category)
			action = config.ActionWarn
		}

		switch action {
		case config.ActionFail:
			errs = append(errs, fmt.Errorf("key %s (category %s)", key, category))
		default:
			fmt.Fprintf(e.stderr(), "warning: .env.example in %s: undeclared key %s is a probable secret (category %s); including\n", ctx.RepoName, key, category)
			result[key] = value
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
