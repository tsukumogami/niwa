package agentplan

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This file renders the user guide's account of what one agent's sessions do
// not get. It is a filter over the declaration table -- the unavailable rows for
// one agent, grouped by reason kind, each rendering its own Reason -- and
// nothing else. There is no judgment here and no second list of gaps to keep in
// step with the first: if a row flips, the rendered section changes, and the
// drift test in gaplist_test.go fails until the committed guide catches up.
//
// That is the whole point of generating it. A hand-maintained gap list is
// accurate on the day it is written and quietly wrong afterwards, and a doc that
// overstates what an agent gets is worse than no doc at all. Here the code is
// the source and the guide is a rendering of it, so a disagreement between them
// is a test failure rather than a surprise in someone's session.
//
// The reason kinds render differently on purpose. A no-such-concept row is not a
// gap -- the capability names surface the other agent has and this one does not,
// so there is nothing that failed to arrive -- and listing it beside a real gap
// would inflate the list with items no amount of work will ever remove. The
// other two kinds are the gap list proper: one the agent's own mechanics close
// off, one niwa still owes.

// GapEntry is one declared-unavailable capability, prepared for a reader rather
// than for a lookup. Subject says what the developer does not get in their own
// terms; Reason is the declaration's reason verbatim, so the guide cannot soften
// what the table says.
type GapEntry struct {
	// Capability is the row this entry came from.
	Capability Capability

	// Kind is the declaration's reason kind, which decides the group the entry
	// renders into and the form the line takes.
	Kind ReasonKind

	// Subject is the reader-facing noun phrase for the capability, without a
	// trailing period so a renderer can punctuate it either way.
	Subject string

	// Reason is the declaration's Reason, unedited.
	Reason string
}

// gapSubjects is the reader-facing name of each capability. The declaration
// table's own capability names are stable identifiers ("root-session-
// orientation"), which is what a machine should key on and not what a developer
// deciding whether to run an agent here should have to read.
//
// This is a second table over the closed set, so it is kept honest by a test
// rather than by care: TestEveryCapabilityHasAGuideSubject fails when a
// capability is added without one, and UnavailableFor returns an error rather
// than rendering a row it has no words for.
var gapSubjects = map[Capability]string{
	WorkspaceOrientation:    "Workspace and group orientation reaching a session opened inside a repository",
	RootSessionOrientation:  "Orientation for a session you start at the workspace or instance root",
	RepoOrientationDoc:      "The repository's own orientation document",
	WorktreeOrientationDoc:  "A worktree's own orientation document",
	PluginSkills:            "Workspace-declared plugin skills, usable by name in the session",
	MarketplaceRegistration: "Registering a marketplace or plugin with the agent's own plugin system",
	SubagentTypes:           "Named subagent types the session can dispatch work to",
	MCPServers:              "The workspace's MCP servers, reachable from the session",
	SessionEnvironment:      "Workspace-declared environment variables present in the session",
	DotenvFiles:             "Dotenv files written to the paths the workspace declares",
	FileDistribution:        "Arbitrary file distribution from a source path to a destination path",
	ApprovalPosture:         "The session's approval and sandbox posture",
	Hooks:                   "Hooks, the lifecycle commands an agent runs on its own events",
	WorkSummaryHooks:        "A written summary of the work a session did",
	PRBodyHook:              "Filling in a pull request's body from the session",
	WorktreeHookDelegation:  "Worktree-hook delegation and the deny fallback behind it",
	// The title says "niwa did not launch" because the distinction became load-
	// bearing once background dispatch reached a second agent. This row is the
	// session-start hook: a session that starts on its own gets an instance
	// provisioned for it. `niwa dispatch` provisions its own instance
	// explicitly, before it launches anything, and does so for every agent it
	// can launch -- so a title reading "for each dispatched session" would now
	// tell a developer the opposite of what happens.
	EphemeralSessions:     "An instance provisioned automatically for a session niwa did not launch",
	RootProjectSkills:     "Instance-root skills such as `/dispatch`",
	NiwaPlugin:            "niwa's own plugin, which carries the migrate-config skill",
	RemoteControl:         "Remote control of a session at startup",
	DispatchKeepAlive:     "Keeping a dispatched background session warm",
	DispatchLaunch:        "Launching a background worker with `niwa dispatch`",
	DirectoryTrust:        "The per-directory trust entry that lets an agent read a project-layer configuration niwa wrote",
	GitExcludeBookkeeping: "Git-exclude coverage for the files niwa writes into a repository",
}

// gapGroup is one rendered group: a reason kind, the heading it gets, and the
// sentence that tells the reader what the group means. The heading and intro
// carry a %s for the agent's display name.
type gapGroup struct {
	kind    ReasonKind
	heading string
	intro   string
}

// gapGroups fixes both the order the groups appear in and which of them is the
// gap list proper. The two kinds a developer can act on come first -- the ones
// the agent closes off, then the ones niwa owes -- and the no-such-concept notes
// come last, where they read as a footnote rather than as a tally of losses.
var gapGroups = []gapGroup{
	{
		kind:    ReasonAgentCannotReceive,
		heading: "What %s can't receive",
		intro:   "%s's own mechanics put these out of reach. niwa could write something and the session would never read it, so these move only if the agent changes.",
	},
	{
		kind:    ReasonNotBuilt,
		heading: "What niwa hasn't built yet",
		intro:   "A route exists on %s's side and niwa hasn't wired it up. This is niwa's own debt, and it's the one group that can shrink without the agent changing.",
	},
	{
		kind:    ReasonNoSuchConcept,
		heading: "What doesn't apply to %s",
		intro:   "Nothing is missing here. Each one names something that exists only in the other agent's harness, so there's no delivery to make and none of them failed to arrive.",
	},
}

// gapWrapWidth is the column the generated prose wraps at. The guide's
// hand-written paragraphs wrap around here, and a generated section of unbroken
// 300-column lines in the middle of them is a section nobody reviews as a diff.
const gapWrapWidth = 78

// UnavailableFor returns every capability declared unavailable for ag, in matrix
// order, with the words a guide needs to render it.
//
// It is fail-closed the way Lookup is: an agent outside the accepted set is an
// error, and so is a capability the subject table has no phrase for. A guide
// that silently skipped a row it could not name would be exactly the quietly
// incomplete gap list this generator exists to prevent.
func UnavailableFor(ag agent.Agent) ([]GapEntry, error) {
	resolved, err := agent.ParseAgent(string(ag))
	if err != nil {
		return nil, fmt.Errorf("agentplan: %w", err)
	}
	var out []GapEntry
	for _, c := range All() {
		d, err := Lookup(c, resolved)
		if err != nil {
			return nil, err
		}
		if d.State != StateUnavailable {
			continue
		}
		subject, ok := gapSubjects[c]
		if !ok || subject == "" {
			return nil, fmt.Errorf("agentplan: capability %s has no guide subject", c)
		}
		out = append(out, GapEntry{
			Capability: c,
			Kind:       d.Kind,
			Subject:    subject,
			Reason:     d.Reason,
		})
	}
	return out, nil
}

// RenderGapSection renders the guide's generated section for ag as Markdown,
// starting at heading level three and ending with a newline. An empty group is
// omitted entirely rather than rendered with a "none" line: a heading over
// nothing invites a reader to wonder what belongs under it.
func RenderGapSection(ag agent.Agent) (string, error) {
	entries, err := UnavailableFor(ag)
	if err != nil {
		return "", err
	}
	resolved, err := agent.ParseAgent(string(ag))
	if err != nil {
		return "", fmt.Errorf("agentplan: %w", err)
	}
	name := agentDisplayName(resolved)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", wrap(gapSectionPreamble, "", ""))

	for _, g := range gapGroups {
		group := entriesOfKind(entries, g.kind)
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "\n### %s\n\n", fillAgentName(g.heading, name))
		fmt.Fprintf(&sb, "%s\n\n", wrap(fillAgentName(g.intro, name), "", ""))
		for _, e := range group {
			fmt.Fprintf(&sb, "%s\n", wrapBullet(renderGapLine(e, name)))
		}
	}
	return sb.String(), nil
}

// gapSectionPreamble opens the section by saying where its words came from. It
// is generated along with the rest so that a reader who edits the section by
// hand is editing the sentence that tells them not to.
const gapSectionPreamble = "This list is generated from niwa's capability declarations in " +
	"`internal/agentplan/declaration.go`. If it and the code ever disagree, the code is " +
	"right and this section is the bug — a test fails until they match again."

// entriesOfKind filters entries to one reason kind, preserving matrix order.
func entriesOfKind(entries []GapEntry, kind ReasonKind) []GapEntry {
	var out []GapEntry
	for _, e := range entries {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// renderGapLine renders one entry as a list item. A no-such-concept row is
// phrased as a note that the capability doesn't apply; the other kinds are
// phrased as a loss followed by its reason, which is what a developer weighing
// the agent actually needs from the line.
func renderGapLine(e GapEntry, name string) string {
	if e.Kind == ReasonNoSuchConcept {
		return fmt.Sprintf("- **%s** doesn't apply to %s. %s", e.Subject, name, e.Reason)
	}
	return fmt.Sprintf("- **%s.** %s", e.Subject, e.Reason)
}

// wrapBullet wraps an already-rendered "- ..." list item, hanging its
// continuation lines under the text rather than under the marker.
func wrapBullet(line string) string {
	return wrap(strings.TrimPrefix(line, "- "), "- ", "  ")
}

// wrap reflows text to gapWrapWidth, prefixing the first line with firstPrefix
// and every later one with contPrefix. A word longer than the width gets its own
// overlong line rather than being cut: a URL or a long identifier broken in half
// is worse than a line that runs past the margin.
func wrap(text, firstPrefix, contPrefix string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	var out strings.Builder
	line := firstPrefix + words[0]
	for _, w := range words[1:] {
		// Width is counted in runes, not bytes: an em dash is three bytes and
		// one column, and counting bytes would wrap those lines early.
		if utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > gapWrapWidth {
			out.WriteString(line + "\n")
			line = contPrefix + w
			continue
		}
		line += " " + w
	}
	out.WriteString(line)
	return out.String()
}

// fillAgentName substitutes the agent's display name into every %s of s.
func fillAgentName(s, name string) string {
	args := make([]any, strings.Count(s, "%s"))
	for i := range args {
		args[i] = name
	}
	return fmt.Sprintf(s, args...)
}

// agentDisplayName is the name a guide calls the agent by, as against the
// lowercase identifier the configuration and the declaration table use.
func agentDisplayName(a agent.Agent) string {
	if a == agent.AgentCodex {
		return "Codex"
	}
	return "Claude Code"
}
