package agentplan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This file is the session-environment half of the payload configuration: the
// variables a session's commands run with, declared once in agent-neutral terms
// (config.SessionEnvConfig) and generated into whichever shape the agent reads.
//
// For Codex that shape is the shell environment policy, whose measured pipeline
// on codex-cli 0.147.0 runs inherit -> exclude -> set -> include_only. niwa
// writes only set: it is additive over whatever the session inherited and
// overrides on collision, its values are literal strings with no interpolation
// of any kind, and it is the only stage that adds rather than removes. The
// three stages around it decide what a developer's own environment does or does
// not survive into their session, which is theirs to decide and not something
// an apply should quietly change underneath them.
//
// That restraint has one consequence worth stating where the code is rather
// than only in the guide. ignore_default_excludes defaults to true on the
// measured version, so Codex's own *KEY* / *TOKEN* exclude patterns are not
// applied and a session's commands inherit those variables from the parent
// environment. niwa does not write that key. Setting it would drop variables
// the developer never asked niwa to touch, and it protects nothing niwa
// delivers -- values written to set survive the exclude stage regardless,
// because set runs after it.

// codexEnvTable is the top-level table Codex reads its environment policy from.
const codexEnvTable = "shell_environment_policy"

// codexEnvSetKey is the one key inside that table niwa writes: the additive,
// literal-valued map applied after the inherit and exclude stages.
const codexEnvSetKey = "set"

// validateSessionEnv checks every declared variable against what an environment
// policy can carry, before any of it is rendered.
//
// Both checks are refusals rather than repairs. A name the shell cannot export
// is not something to sanitize into a different name the workspace did not
// declare, and a value still carrying interpolation syntax is an authoring bug:
// niwa writes resolved values only, and the agent this document is written for
// expands nothing, so the literal characters would reach the command.
func validateSessionEnv(env map[string]string) error {
	for _, key := range sortedStringKeys(env) {
		if !isEnvVarName(key) {
			return fmt.Errorf(
				"session.env declares %q, which is not a usable environment variable name; a name is a letter or underscore followed by letters, digits, or underscores",
				key)
		}
		if strings.Contains(env[key], "${") {
			return fmt.Errorf(
				"session.env value for %q carries \"${\" after resolution; niwa writes resolved values only, and the agents this reaches expand nothing, so those characters would arrive at the command verbatim",
				key)
		}
	}
	return nil
}

// isEnvVarName reports whether name is one a shell can export: a letter or
// underscore followed by letters, digits, or underscores. It is deliberately
// the conservative POSIX shape rather than everything a TOML key can spell --
// the document has to survive Codex's whole-config type check, and a name no
// shell can set would be a variable that silently never arrives.
func isEnvVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// checkCodexEnvPolicy checks the decoded generated document's environment
// policy against what was declared.
//
// The absent-when-nothing-was-declared arm matters as much as the present one.
// A policy table niwa wrote without being asked would be niwa deciding what a
// developer's session inherits, so its absence is asserted rather than assumed;
// and the exact-keys check is what mechanizes "niwa writes set and nothing
// else", ignore_default_excludes included.
func checkCodexEnvPolicy(doc map[string]any, env map[string]string) error {
	raw, present := doc[codexEnvTable]
	if len(env) == 0 {
		if present {
			return fmt.Errorf(
				"agentplan: the generated payload document carries a [%s] table although the workspace declared no session environment",
				codexEnvTable)
		}
		return nil
	}
	if !present {
		return fmt.Errorf("agentplan: the generated payload document carries no [%s] table", codexEnvTable)
	}

	table, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("agentplan: the generated payload document's %s is not a table", codexEnvTable)
	}
	for key := range table {
		if key != codexEnvSetKey {
			return fmt.Errorf(
				"agentplan: the generated payload document's %s carries %q; niwa writes %q and nothing else, so the rest of a developer's environment policy stays theirs",
				codexEnvTable, key, codexEnvSetKey)
		}
	}

	rawSet, present := table[codexEnvSetKey]
	if !present {
		return fmt.Errorf("agentplan: the generated payload document's %s carries no %q", codexEnvTable, codexEnvSetKey)
	}
	set, ok := rawSet.(map[string]any)
	if !ok {
		return fmt.Errorf("agentplan: the generated %s.%s is not a table", codexEnvTable, codexEnvSetKey)
	}
	if len(set) != len(env) {
		return fmt.Errorf(
			"agentplan: the generated %s.%s carries %d variable(s), want %d",
			codexEnvTable, codexEnvSetKey, len(set), len(env))
	}
	for _, key := range sortedStringKeys(env) {
		value, present := set[key]
		if !present {
			return fmt.Errorf("agentplan: the generated %s.%s is missing %q", codexEnvTable, codexEnvSetKey, key)
		}
		got, isString := value.(string)
		if !isString {
			return fmt.Errorf("agentplan: the generated %s.%s.%s is not a string", codexEnvTable, codexEnvSetKey, key)
		}
		if got != env[key] {
			return fmt.Errorf("agentplan: the generated %s.%s.%s did not decode back to the declared value", codexEnvTable, codexEnvSetKey, key)
		}
	}
	return nil
}

// InstanceExcludePatterns returns the git-ignore patterns an instance root
// needs for the secret-bearing configuration this agent's plans generate, in a
// stable order.
//
// The instance root is not itself a git repository, but workspaces are
// routinely prepared inside an outer tracked tree, and the .gitignore there
// covers only the ".local" infix niwa's materializers enforce. A generated
// payload carries no such infix -- it has to be at the name its agent reads --
// so without these patterns a secret-bearing file could be staged by the outer
// repository. Context documents are not here: they carry orientation prose, not
// credentials, and covering them would be niwa deciding what an unrelated
// repository tracks.
//
// The patterns are the directory or file name rather than a path, so one line
// covers the name wherever below the instance root it appears.
func (p Producer) InstanceExcludePatterns() []string {
	layout, ok := p.payloadLayout()
	if !ok {
		return nil
	}
	pattern := layout.excludePattern()
	if pattern == "" {
		return nil
	}
	return []string{pattern}
}

// InstanceExcludePatterns returns the union of every enumerated agent's
// patterns, in matrix order, deduplicated.
//
// It exists so internal/workspace can cover the instance root without ranging
// over agents to ask each one: the caller there writes one file for every
// agent's names at once, and which names those are is this package's answer.
func InstanceExcludePatterns() []string {
	var out []string
	for _, ag := range agent.All() {
		for _, pattern := range For(ag).InstanceExcludePatterns() {
			if !slices.Contains(out, pattern) {
				out = append(out, pattern)
			}
		}
	}
	return out
}
