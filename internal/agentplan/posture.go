package agentplan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This file is the posture half of the payload configuration: how much a
// session asks before it acts, and how much of the machine it may touch when it
// does, declared once in agent-neutral terms (config.SessionPostureConfig) and
// generated into whichever keys the agent reads.
//
// For Codex those keys are approval_policy and sandbox_mode. Both were measured
// on codex-cli 0.147.0 to take effect from a trusted project layer and to revert
// to their defaults the moment the trust entry is removed, which puts them on
// the same side of the trust line as MCP servers and the environment policy --
// hence the same document, the same validate-then-decode-back pass, and the same
// Requires: DirectoryTrust edge on the declaration.
//
// Three properties govern everything below, and each is mechanized rather than
// left to care:
//
//  1. Opt-in and absent by default. A workspace that declares no posture gets
//     neither key written and no posture reported. checkCodexPosture asserts the
//     absence directly rather than inferring it from the presence branch passing.
//  2. Reported when written. postureReport turns what was written into one line
//     the apply prints, so a developer can see what a workspace changed about
//     their session instead of discovering it when a prompt does not appear.
//  3. Approvals and sandbox stay separate decisions. Each key is written from
//     its own declared field and from nothing else. This is the one that costs
//     something to keep: Codex's most complete approval suppression --
//     approval_policy = "never" with sandbox_mode = "danger-full-access" --
//     collapses approvals and both the filesystem and network sandboxes into a
//     single setting, which Claude Code's bypassPermissions does not do. A
//     generator that derived a sandbox setting from an approval declaration
//     would switch sandboxing off as an unstated side effect of a declaration
//     that never mentioned it, so nothing here derives one from the other.

// codexApprovalKey and codexSandboxKey are the two top-level keys Codex reads
// its posture from. They are top-level rather than nested: the project layer
// carries them at the document root, beside [mcp_servers] and
// [shell_environment_policy] rather than inside either.
const (
	codexApprovalKey = "approval_policy"
	codexSandboxKey  = "sandbox_mode"
)

// SessionPosture is one workspace's declared posture, in niwa's neutral
// vocabulary. An empty field is an undeclared one, which is the whole of what
// "opt-in" means here: there is no zero value that stands for a posture, so a
// producer cannot write a key the workspace never asked for.
type SessionPosture struct {
	// Approvals is when the session pauses for the developer.
	Approvals string

	// Sandbox is how much of the machine the session may touch.
	Sandbox string
}

// IsZero reports whether the workspace declared no posture at all.
func (p SessionPosture) IsZero() bool {
	return p.Approvals == "" && p.Sandbox == ""
}

// codexApprovalPolicies maps niwa's approval vocabulary onto Codex's
// approval_policy values.
//
// The map is the vocabulary's definition as well as its translation: a value
// absent from it is not a posture niwa accepts, which is what keeps a typo from
// reaching a document that has to survive Codex's whole-config type check. The
// neutral names describe when the session asks rather than restating Codex's
// spelling, so a second agent's mapping can be written against the meaning.
var codexApprovalPolicies = map[string]string{
	"on-untrusted": "untrusted",
	"on-failure":   "on-failure",
	"on-request":   "on-request",
	"never":        "never",
}

// codexSandboxModes maps niwa's sandbox vocabulary onto Codex's sandbox_mode
// values.
//
// "full-access" spells out what it is rather than borrowing Codex's danger-
// prefixed name, and it maps to that name rather than to anything softer: a
// workspace declaring it gets exactly the setting it asked for, filesystem and
// network sandboxing both off. What it does not get is that setting arriving
// from an approval declaration -- see the file comment.
var codexSandboxModes = map[string]string{
	"read-only":       "read-only",
	"workspace-write": "workspace-write",
	"full-access":     "danger-full-access",
}

// validateSessionPosture checks a declared posture against the accepted
// vocabulary before any of it is rendered.
//
// It is a refusal rather than a repair, and it runs before the document exists
// for the same reason the MCP and environment checks do: one key Codex cannot
// type-check fails its whole config load, taking every valid sibling in the
// file with it. A posture value niwa guessed at would be a guess about how much
// a developer's session is allowed to do.
func validateSessionPosture(p SessionPosture) error {
	if p.Approvals != "" {
		if _, ok := codexApprovalPolicies[p.Approvals]; !ok {
			return fmt.Errorf(
				"session.posture declares approvals = %q, which is not a posture niwa accepts; the accepted values are %s",
				p.Approvals, quotedSorted(codexApprovalPolicies))
		}
	}
	if p.Sandbox != "" {
		if _, ok := codexSandboxModes[p.Sandbox]; !ok {
			return fmt.Errorf(
				"session.posture declares sandbox = %q, which is not a posture niwa accepts; the accepted values are %s",
				p.Sandbox, quotedSorted(codexSandboxModes))
		}
	}
	return nil
}

// codexPostureKeys is the posture half of the generated document: the keys to
// write, and nothing for a field the workspace left empty.
//
// The two branches read two different fields and write two different keys, with
// no path from one to the other. That structure is the third safety property
// rather than an implementation detail -- there is nowhere for an approval
// declaration to reach a sandbox key even by accident.
func codexPostureKeys(p SessionPosture) (map[string]any, error) {
	out := map[string]any{}
	if p.Approvals != "" {
		mapped, ok := codexApprovalPolicies[p.Approvals]
		if !ok {
			return nil, fmt.Errorf("agentplan: no Codex approval policy for declared posture %q", p.Approvals)
		}
		out[codexApprovalKey] = mapped
	}
	if p.Sandbox != "" {
		mapped, ok := codexSandboxModes[p.Sandbox]
		if !ok {
			return nil, fmt.Errorf("agentplan: no Codex sandbox mode for declared posture %q", p.Sandbox)
		}
		out[codexSandboxKey] = mapped
	}
	return out, nil
}

// checkCodexPosture checks the decoded generated document's posture keys
// against what was declared.
//
// The absent arms carry the weight here. A key niwa wrote without being asked
// would be niwa deciding how much a developer's session may do, so each key's
// absence is asserted directly whenever its own field was empty -- including the
// case this whole issue exists to guarantee, an approvals-only declaration
// leaving sandbox_mode unwritten. Asserting it over the decoded bytes rather
// than over the inputs is deliberate: what a session loads is the bytes.
func checkCodexPosture(doc map[string]any, p SessionPosture) error {
	want, err := codexPostureKeys(p)
	if err != nil {
		return err
	}
	for _, key := range []string{codexApprovalKey, codexSandboxKey} {
		raw, present := doc[key]
		expected, declared := want[key]
		if !declared {
			if present {
				return fmt.Errorf(
					"agentplan: the generated payload document carries %s although the workspace declared no posture for it; niwa writes each posture key only from its own declaration",
					key)
			}
			continue
		}
		if !present {
			return fmt.Errorf("agentplan: the generated payload document carries no %s although the workspace declared one", key)
		}
		got, isString := raw.(string)
		if !isString {
			return fmt.Errorf("agentplan: the generated %s is not a string", key)
		}
		if got != expected {
			return fmt.Errorf("agentplan: the generated %s did not decode back to the declared posture", key)
		}
	}
	return nil
}

// PostureReport is the line an apply prints for the posture this agent's
// generated configuration carries. An empty string means there is nothing to
// report -- which is the whole of what an undeclared posture produces, and is
// what makes "apply reports no posture write" a fact about this function rather
// than an absence somebody has to notice.
//
// The line names both keys either way: the one niwa wrote and its value, and the
// one it did not write and whose default therefore stands. Naming the second is
// the point as much as the first, because the risk this reporting exists against
// is a developer assuming that relaxing one relaxed the other.
func (p Producer) PostureReport(posture SessionPosture) string {
	delivers, err := p.delivers(ApprovalPosture)
	if err != nil || !delivers {
		return ""
	}
	layout, ok := p.payloadLayout()
	if !ok || !layout.carriesPosture || posture.IsZero() {
		return ""
	}
	resolved, err := agent.ParseAgent(string(p.ag))
	if err != nil {
		return ""
	}
	keys, err := codexPostureKeys(posture)
	if err != nil {
		return ""
	}
	return postureReport(keys, string(resolved))
}

// postureReport builds the sentence from the keys that were written.
func postureReport(keys map[string]any, agentName string) string {
	var written []string
	var untouched []string
	for _, key := range []string{codexApprovalKey, codexSandboxKey} {
		if value, present := keys[key]; present {
			written = append(written, fmt.Sprintf("%s = %q", key, value))
			continue
		}
		untouched = append(untouched, key)
	}
	if len(written) == 0 {
		return ""
	}

	report := fmt.Sprintf(
		"this workspace declares a session posture, so every generated %s project configuration sets %s",
		agentName, strings.Join(written, " and "))
	if len(untouched) > 0 {
		report += fmt.Sprintf(
			"; niwa writes no %s, so the default a %s session already ran under stands unchanged",
			strings.Join(untouched, " and "), agentName)
	}
	return report
}

// quotedSorted renders a vocabulary map's accepted values for an error message,
// in a stable order so the same mistake reads the same way every run.
func quotedSorted(vocabulary map[string]string) string {
	values := make([]string, 0, len(vocabulary))
	for value := range vocabulary {
		values = append(values, fmt.Sprintf("%q", value))
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}
