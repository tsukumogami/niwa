package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/agent"
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

// instanceRootMCPApprovals returns the names of the declared MCP servers the
// instance root's generated payload carries for an agent whose sessions read a
// standing approval out of the instance root's settings document. The settings
// document writes them as its approval list, so a server the workspace
// declares once is both shipped and approved.
//
// It asks every agent's producer, gated exactly as the payload step gates the
// instance root, and the producers decide: an agent whose approval does not
// live in that document, a closed gate, and a workspace declaring no servers
// all answer with nothing. The resolution warnings MCPServersFromConfig
// returns are dropped here because the payload step resolves the same
// declaration and reports them; only the names matter for an approval.
func instanceRootMCPApprovals(cfg *config.WorkspaceConfig) ([]string, error) {
	servers, _ := MCPServersFromConfig(cfg)
	if len(servers) == 0 {
		return nil, nil
	}
	var names []string
	for _, ag := range agent.All() {
		producer := agentplan.For(ag).Gated(AgentEnabled(cfg, "", string(ag)))
		approved, err := producer.MCPApprovals(agentplan.PayloadAtInstanceRoot, servers)
		if err != nil {
			return nil, err
		}
		names = append(names, approved...)
	}
	return names, nil
}

// repoMCPApprovals returns the approval list the Claude settings document of
// the repository repoName carries: the names instanceRootMCPApprovals gives,
// since a session started inside a repository finds the instance root's
// generated .mcp.json by walking up from its directory and asks to approve
// each server in it unless the repository's own project settings list it.
// Claude Code reads that list only in a trusted directory, and niwa trusts a
// repository directory only under [claude] trust_instances and with Claude's
// gate open for it, so otherwise (cfg is the merged config, overlay
// included) the list is empty and the document stays the one a workspace
// that never opted in gets.
//
// A review instance's repositories still get the list when the option is
// on, since the settings are written before niwa watch marks the instance;
// niwa never trusts those directories, so Claude Code never reads it there.
func repoMCPApprovals(cfg *config.WorkspaceConfig, repoName string) ([]string, error) {
	if !trustInstancesOn(cfg) || !claudeEnabled(cfg, repoName) {
		return nil, nil
	}
	return instanceRootMCPApprovals(cfg)
}

// recordedMCPApprovals returns the approval list an instance's state records:
// the instance root's, normalized exactly as the settings materializer writes
// it, so the recorded value and the settings document's enabledMcpjsonServers
// cannot disagree on a fresh instance.
//
// backed reports that every agent approving servers at the instance root had
// its instance-root payload written by this run (see payloadBacksApprovals).
// When one didn't, the names would describe servers niwa never wrote, and a
// launch that sent them would approve whatever the file in that place
// declares under those names, so nothing is recorded. A review instance
// (existing is its state; nil on create, which never makes one) records
// nothing either: niwa watch launches its reviews itself, and nothing should
// read an approval out of an instance staged for untrusted code.
func recordedMCPApprovals(cfg *config.WorkspaceConfig, backed bool, existing *InstanceState) ([]string, error) {
	if !backed || (existing != nil && existing.isReviewInstance()) {
		return nil, nil
	}
	names, err := instanceRootMCPApprovals(cfg)
	if err != nil {
		return nil, err
	}
	return approvedMCPServerList(names), nil
}

// payloadBacksApprovals reports whether install wrote its payload rather than
// leaving the place to a file niwa did not generate.
func payloadBacksApprovals(install *PayloadInstall) bool {
	return install != nil && len(install.Written) > 0 && len(install.Exempt) == 0
}

// SessionPostureFromConfig reads the workspace's declared approval and sandbox
// posture into the form a producer takes.
//
// There is no resolution step and deliberately no default: a key the workspace
// left unset arrives empty, and every producer treats an empty field as "write
// nothing" rather than as a value to supply. That is what makes the delivery
// opt-in end to end -- niwa has no posture of its own to fall back on, so it
// cannot write one nobody asked for.
func SessionPostureFromConfig(cfg *config.WorkspaceConfig) agentplan.SessionPosture {
	if cfg == nil {
		return agentplan.SessionPosture{}
	}
	return agentplan.SessionPosture{
		Approvals: cfg.Session.Posture.Approvals,
		Sandbox:   cfg.Session.Posture.Sandbox,
	}
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

// PayloadInstall is what one generated payload configuration produced.
type PayloadInstall struct {
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

// PayloadRequest is one tree's generated payload configuration: which tree,
// what the workspace declared, and what the developer's own configuration
// already defines.
//
// It is a struct rather than a parameter list because both declarations feeding
// the document are agent-neutral and resolved once per apply: every producer is
// offered the same request, and which half of it that agent reads out of this
// document is the producer's answer rather than the caller's.
type PayloadRequest struct {
	// Scope selects which tree this is.
	Scope agentplan.PayloadScope

	// Dir is the absolute root of that tree.
	Dir string

	// Servers are the workspace's declared MCP servers, resolved.
	Servers []agentplan.MCPServer

	// Env is the workspace's declared session environment, resolved.
	Env map[string]string

	// Posture is the workspace's declared approval and sandbox posture. Its
	// zero value means none was declared, and no producer turns that into a
	// written key.
	Posture agentplan.SessionPosture

	// Existing are the server names the developer's own configuration for this
	// agent already defines.
	Existing []string

	// ContextChainBytes is what the context install for this same tree and this
	// same agent reported as the worst chain a session there reads. The
	// producer sizes the budget it declares from it, so a caller that installs
	// context before the payload passes what that install measured and a caller
	// with no context install behind it passes zero.
	ContextChainBytes int
}

// InstallPayloadConfig generates one tree's payload configuration for one agent.
//
// The producer decides everything specific: whether this agent takes a
// configuration at this scope, where it goes, what format it is in, which of
// the declarations it carries, and whether they can be expressed in it at all.
// A declaration this agent cannot express -- a transport it would silently
// serve as a different one, a value that never resolved, an environment key no
// shell could export, a name the developer's own configuration already defines
// -- comes back as an error here, and the caller fails the apply with it. That
// is deliberate: the alternative to a loud failure is a file that takes the
// developer's whole session down when the agent tries to load it.
func InstallPayloadConfig(req PayloadRequest, producer agentplan.Producer) (*PayloadInstall, error) {
	probe, err := probeContextTree(producer.PayloadProbeSpec(req.Scope, req.Dir))
	if err != nil {
		return nil, err
	}

	plan, err := producer.PayloadPlan(agentplan.PayloadInputs{
		Scope:             req.Scope,
		Dir:               req.Dir,
		Servers:           req.Servers,
		Env:               req.Env,
		Posture:           req.Posture,
		Existing:          req.Existing,
		ContextChainBytes: req.ContextChainBytes,
		Probe:             probe,
	})
	if err != nil {
		return nil, err
	}
	dir := req.Dir
	if err := checkPlanContainment(plan, dir); err != nil {
		return nil, err
	}

	written, excludes, err := applyPlan(plan)
	if err != nil {
		return nil, err
	}
	return &PayloadInstall{
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
