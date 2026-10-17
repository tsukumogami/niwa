package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

// renderLaunchSettings renders the inline settings document a dispatch passes
// to the launched agent's settings flag, and reports whether there is one.
//
// A launch has exactly one settings slot: a repeated settings flag is
// last-wins and silent, so a second document would quietly drop the first.
// Every contributor therefore adds its key to one map that step (9c) of
// runDispatch builds, and the map is rendered here once. Each contributor
// decides for itself whether its key goes in; rendering says nothing about
// which of them did, so a caller that needs to know whether its own key was
// sent must track that from its own decision, not from this result.
//
// The encoding is encoding/json's, which sorts map keys, so two contributors
// always produce the same bytes in the same order. With remote control's key
// alone the output is byte-identical to remoteControlSettingsJSON.
//
// Contributors must pass constant keys and values only -- never workspace,
// repository, or prompt input. The document outranks the instance's own
// settings files, so anything a caller could influence here would let that
// caller rewrite the worker's configuration.
//
// Two values are exceptions, and they are named here so they stay the only
// ones. The first is the lineage contributor's "env" key, a resourceAttributesEnv that can
// marshal to nothing but {"OTEL_RESOURCE_ATTRIBUTES": "<value>"}. Its value is
// composed in dispatch_lineage.go from niwa's own entries, each checked
// against an alphabet that has no ',' or '=', and from attributes the
// developer's own settings and environment already set, each checked against
// the attribute parser's own rule. It is one string under one fixed key, so
// nothing in it can add a settings key.
//
// The second exception is the MCP approval contributor's
// enabledMcpjsonServers key, an mcpApprovalList that can marshal to nothing
// but a JSON array of strings. Its names come from the ApprovedMCPServers
// niwa recorded in the state of the instance this dispatch just provisioned,
// which the pipeline computed from its own approval decision over
// [mcp.servers]; they are never read from a .mcp.json or a settings file.
// Each name must match mcpServerNamePattern and at most
// maxLaunchMCPServers are sent, so a hand-edited state file can't push a
// key-like or instruction-like string into the document.
//
// An empty or nil map returns ("", false), and the caller then appends no
// settings flag at all. A value encoding/json can't encode panics, since only
// a contributor breaking the constants rule can cause one.
func renderLaunchSettings(settings map[string]any) (string, bool) {
	if len(settings) == 0 {
		return "", false
	}
	doc, err := json.Marshal(settings)
	if err != nil {
		// The string, boolean, and number constants contributors pass always
		// encode, so this is a contributor's programming error. Returning no
		// document would leave that contributor's record (rcInjected, for one)
		// saying its key was sent, so it panics instead.
		panic(fmt.Sprintf("renderLaunchSettings: encoding launch settings: %v", err))
	}
	return string(doc), true
}

// mcpServerNamePattern is the alphabet an MCP server name must use to enter
// the launch settings document. Claude Code's server names have no legitimate
// use for anything outside it.
var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// maxLaunchMCPServers caps how many approved names one launch sends.
const maxLaunchMCPServers = 64

// filterMCPServerNames splits names into the ones the launch approval may
// carry and the ones it leaves out, both in their original order. A name is
// left out when it doesn't match mcpServerNamePattern, or when
// maxLaunchMCPServers names are already kept.
func filterMCPServerNames(names []string) (kept, dropped []string) {
	for _, name := range names {
		if !mcpServerNamePattern.MatchString(name) || len(kept) >= maxLaunchMCPServers {
			dropped = append(dropped, name)
			continue
		}
		kept = append(kept, name)
	}
	return kept, dropped
}

// mcpApprovalList is the launch settings document's enabledMcpjsonServers
// value. Its one field is unexported and it marshals to a JSON array of
// strings and nothing else, so no name can add a key or change the
// document's shape.
type mcpApprovalList struct{ names []string }

// MarshalJSON renders the names as a JSON array of strings, [] when empty.
func (l mcpApprovalList) MarshalJSON() ([]byte, error) {
	names := l.names
	if names == nil {
		names = []string{}
	}
	return json.Marshal(names)
}

// maxWarnedNameBytes bounds how much of a dropped name a warning prints.
const maxWarnedNameBytes = 80

// quoteDroppedName renders a name for a warning line: Go-quoted, so control
// characters and escape sequences reach the terminal escaped, and at most
// maxWarnedNameBytes long, with "..." marking a cut.
func quoteDroppedName(name string) string {
	q := strconv.Quote(name)
	if len(q) <= maxWarnedNameBytes {
		return q
	}
	// Every raw byte quotes to at least one byte, so no prefix longer than
	// the budget can fit.
	n := len(name)
	if n > maxWarnedNameBytes {
		n = maxWarnedNameBytes
	}
	for ; n > 0; n-- {
		q = strconv.Quote(name[:n]) + "..."
		if len(q) <= maxWarnedNameBytes {
			return q
		}
	}
	return `""...`
}
