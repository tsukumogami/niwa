package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
)

// This file is the workspace half of the MCP declaration: it turns the
// configured [mcp.servers.*] table into resolved values a producer can render,
// reads the developer's own configuration for the names a generated one would
// collide with, and installs the plan the producer returns.
//
// Nothing here knows which agent it is preparing for. Which file gets written,
// in which format, in which tree, and which developer-side configuration the
// collision check reads are all answers the producer gives -- this file
// resolves values, looks where it is told to look, and executes.

// vaultRefPrefix is the prefix a vault reference carries. It is spelled here
// rather than imported for the same reason internal/config spells it: the
// packages that need to recognize one must not have to depend on the resolver
// to do it.
const vaultRefPrefix = "vault://"

// mcpServerFromConfig resolves one declared server into the value form the
// producer takes.
//
// Values are read through maybeSecretString, so a vault-backed one arrives as
// the literal it resolved to. A value the resolver could not supply is dropped
// rather than written empty, with a report: an MCP server started with an empty
// credential fails in a way that looks like the server's fault, and a key that
// is absent at least fails as itself.
func mcpServerFromConfig(name string, srv config.MCPServerConfig) (agentplan.MCPServer, []string) {
	out := agentplan.MCPServer{
		Name:      name,
		Transport: agentplan.MCPTransport(srv.Transport),
		Command:   srv.Command,
		Args:      srv.Args,
		URL:       srv.URL,
		Agents:    srv.Agents,
	}

	var warnings []string
	resolveMap := func(field string, in map[string]config.MaybeSecret) map[string]string {
		if len(in) == 0 {
			return nil
		}
		keys := make([]string, 0, len(in))
		for key := range in {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		out := make(map[string]string, len(in))
		for _, key := range keys {
			value := in[key]
			if value.IsUnresolved() {
				warnings = append(warnings, fmt.Sprintf(
					"mcp.servers.%s.%s.%s has no value (%s), so it was left out of the generated MCP configuration; the server will start without it",
					name, field, key, value.Unresolved.Cause))
				continue
			}
			// A reference that reached this far as a literal was never
			// resolved -- the standalone worktree path, for one, merges the
			// configuration without running the resolver. Writing it would put
			// a vault URI where a credential belongs, in a file no agent
			// expands anything in.
			if !value.IsSecret() && strings.HasPrefix(value.Plain, vaultRefPrefix) {
				warnings = append(warnings, fmt.Sprintf(
					"mcp.servers.%s.%s.%s is still a %s reference here, so it was left out of the generated MCP configuration rather than written unresolved",
					name, field, key, vaultRefPrefix))
				continue
			}
			out[key] = maybeSecretString(value)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}

	out.Env = resolveMap("env", srv.Env)
	out.Headers = resolveMap("headers", srv.Headers)
	return out, warnings
}

// MCPServersFromConfig resolves every declared server, in name order, and
// reports what could not be resolved.
func MCPServersFromConfig(cfg *config.WorkspaceConfig) ([]agentplan.MCPServer, []string) {
	if cfg == nil {
		return nil, nil
	}
	names := cfg.MCP.MCPServerNames()
	if len(names) == 0 {
		return nil, nil
	}

	servers := make([]agentplan.MCPServer, 0, len(names))
	var warnings []string
	for _, name := range names {
		server, warns := mcpServerFromConfig(name, cfg.MCP.Servers[name])
		servers = append(servers, server)
		warnings = append(warnings, warns...)
	}
	return servers, warnings
}

// ReadDeclaredMCPNames reads the server names the developer's own configuration
// for one agent already defines, following the producer's spec.
//
// It is read-only and it never fails an apply. An absent file means no
// collision is possible. A file that cannot be read, cannot be parsed, or
// carries the table in an unexpected shape degrades to a reported skip of the
// collision check -- which is safe on the merits, because a configuration the
// agent itself cannot load runs no session that could see a merged server.
func ReadDeclaredMCPNames(home string, spec agentplan.MCPCollisionSpec) ([]string, string) {
	if spec.IsZero() {
		return nil, ""
	}
	path, err := declaredMCPConfigPath(home, spec)
	if err != nil {
		return nil, ""
	}

	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return nil, ""
	case err != nil:
		return nil, fmt.Sprintf(
			"the configuration at %s could not be read (%v), so niwa could not check the MCP server names it defines against the ones this workspace declares; a session there merges the two definitions field by field if they share a name",
			path, err)
	}

	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return nil, fmt.Sprintf(
			"the configuration at %s is not valid TOML (%v), so niwa could not check the MCP server names it defines against the ones this workspace declares; the agent cannot load it either, so no session sees a merged definition until it parses",
			path, err)
	}

	raw, present := doc[spec.Table]
	if !present {
		return nil, ""
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Sprintf(
			"the configuration at %s has a %s value that is not a table, so niwa could not check the MCP server names it defines against the ones this workspace declares",
			path, spec.Table)
	}

	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, ""
}

// declaredMCPConfigPath resolves the developer-side configuration the spec
// names: the agent's own directory override when it is set, otherwise the
// declared directory under the developer's home.
func declaredMCPConfigPath(home string, spec agentplan.MCPCollisionSpec) (string, error) {
	if spec.ConfigDirEnv != "" {
		if dir := os.Getenv(spec.ConfigDirEnv); dir != "" {
			return filepath.Join(dir, spec.ConfigFile), nil
		}
	}
	if home == "" {
		return "", fmt.Errorf("no home directory to resolve the configuration under")
	}
	return filepath.Join(append(append([]string{home}, spec.ConfigDir...), spec.ConfigFile)...), nil
}

// MCPInstall is what one generated MCP configuration produced.
type MCPInstall struct {
	// Written are the paths the plan wrote.
	Written []string

	// Excludes are the git-exclude patterns the write implies, relative to the
	// tree it landed in.
	Excludes []string

	// Exempt are the paths the plan refused to write because a repository's
	// own file occupies them, which cleanup must leave alone.
	Exempt []string

	// Warnings are what the user needs to hear about the generation.
	Warnings []string
}

// InstallMCPConfig generates one tree's MCP configuration for one agent.
//
// The producer decides everything specific: whether this agent takes a
// configuration at this scope, where it goes, what format it is in, and whether
// the declaration can be expressed in it at all. A declaration this agent
// cannot express -- a transport it would silently serve as a different one, a
// value that never resolved, a name the developer's own configuration already
// defines -- comes back as an error here, and the caller fails the apply with
// it. That is deliberate: the alternative to a loud failure is a file that
// takes the developer's whole session down when the agent tries to load it.
func InstallMCPConfig(scope agentplan.MCPScope, dir string, servers []agentplan.MCPServer, existing []string, producer agentplan.Producer) (*MCPInstall, error) {
	probe, err := probeContextTree(producer.MCPProbeSpec(scope, dir))
	if err != nil {
		return nil, err
	}

	plan, err := producer.MCPPlan(agentplan.MCPInputs{
		Scope:    scope,
		Dir:      dir,
		Servers:  servers,
		Existing: existing,
		Probe:    probe,
	})
	if err != nil {
		return nil, err
	}
	if err := checkPlanContainment(plan, dir); err != nil {
		return nil, err
	}

	written, excludes, err := applyPlan(plan)
	if err != nil {
		return nil, err
	}
	return &MCPInstall{
		Written:  written,
		Excludes: excludes,
		Exempt:   plan.Exempt,
		Warnings: plan.Warnings,
	}, nil
}

// mcpVerbatimDestinations collects the destinations of every verbatim
// file-distribution table an apply carries, which is what the compatibility
// report is recognized from. The keys are sources; the values are where the
// file lands, and only the destination can name an agent's own configuration.
func mcpVerbatimDestinations(tables ...map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, table := range tables {
		for _, dest := range table {
			if dest == "" || seen[dest] {
				continue
			}
			seen[dest] = true
			out = append(out, dest)
		}
	}
	sort.Strings(out)
	return out
}
