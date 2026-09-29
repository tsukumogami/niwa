package fallbacknotice

import (
	"fmt"
	"strings"
	"time"
)

// The wording below is the operator-facing contract. The served warning and
// the nothing-to-fall-back-on line are fixed by the design, and a release
// check greps the binary for "may be stale" and "Run `infisical login` to
// refresh them", so neither may be reworded. The three store warnings are
// pinned by golden tests. Each format takes the rendered fields in order.
const (
	// servedFormat: kind, project, env, path, domain, reason clause, age.
	servedFormat = "using stored values that may be stale for %s project %s (env %s, path %s, %s): the provider %s; the oldest value is %s old. Run `infisical login` to refresh them."

	// missFormat: kind, project, env, path, domain.
	missFormat = "%s project %s (env %s, path %s, %s) could not be used and no previously resolved value exists to fall back on. Run `infisical login`."

	// lockTimeoutFormat: kind, project, env, path, domain.
	lockTimeoutFormat = "another niwa process held the secret store lock for %s project %s (env %s, path %s, %s) too long; values resolved for it in this run were not stored."

	// workTreeFormat: the store directory.
	workTreeFormat = "the secret store directory %s is inside a git work tree; niwa stored no values in it this run."

	// unwritableFormat: the store directory.
	unwritableFormat = "the secret store directory %s could not be read or written; values resolved in this run were not stored."

	// unlocatedText is the unwritable warning when the store directory itself
	// could not be located.
	unlocatedText = "the secret store directory could not be located (HOME is unset or not absolute); values resolved in this run were not stored."

	// textPrefix starts every terminal line.
	textPrefix = "warning: "

	// contextLead opens the agent-context rendering.
	contextLead = "While provisioning, niwa's store of last-resolved secret values reported the following:"

	// contextLogin follows every provider notice in the agent-context
	// rendering. An interactive login from the agent's shell would prompt
	// or hang, so the agent is told to hand it to the operator.
	contextLogin = "Ask the operator to run `infisical login`; do not run it yourself, because it is interactive and would hang this session."

	// maxFieldRunes caps each identity field and directory in a rendering.
	maxFieldRunes = 200
)

// notice is one rendered message: its sentence, and whether it concerns a
// provider login (and so earns the agent-context login instruction).
type notice struct {
	text     string
	provider bool
}

// notices renders every record in output order: served warnings, then
// nothing-to-fall-back-on lines, then lock timeouts, the work-tree warning
// and the unwritable warning. Identities within a kind are sorted by the
// accessors, so equal records always produce equal output.
func (c *Collector) notices() []notice {
	if c == nil {
		return nil
	}
	var out []notice
	now := c.now()
	for _, n := range c.ServedNotes() {
		kind, project, env, path, domain := fields(n.Identity)
		out = append(out, notice{
			text:     fmt.Sprintf(servedFormat, kind, project, env, path, domain, reasonClause(n.Reason), formatAge(now.Sub(n.OldestResolvedAt))),
			provider: true,
		})
	}
	for _, id := range c.Misses() {
		kind, project, env, path, domain := fields(id)
		out = append(out, notice{text: fmt.Sprintf(missFormat, kind, project, env, path, domain), provider: true})
	}
	for _, id := range c.LockTimeouts() {
		kind, project, env, path, domain := fields(id)
		out = append(out, notice{text: fmt.Sprintf(lockTimeoutFormat, kind, project, env, path, domain)})
	}
	if dir, ok := c.WorkTreeDir(); ok {
		out = append(out, notice{text: fmt.Sprintf(workTreeFormat, clean(dir))})
	}
	if dir, ok := c.UnwritableDir(); ok {
		if dir == "" {
			out = append(out, notice{text: unlocatedText})
		} else {
			out = append(out, notice{text: fmt.Sprintf(unwritableFormat, clean(dir))})
		}
	}
	return out
}

// RenderText renders the notices for a terminal, one "warning: " line each.
// It returns the empty string when nothing was recorded, including on a nil
// collector, so a caller can print it unconditionally.
func (c *Collector) RenderText() string {
	var sb strings.Builder
	for _, n := range c.notices() {
		sb.WriteString(textPrefix)
		sb.WriteString(n.text)
		sb.WriteString("\n")
	}
	return sb.String()
}

// RenderContext renders the notices for an agent's context window, which is
// where the SessionStart hook delivers them. Each notice carries the same
// sentence as its terminal line, and each one about a provider login tells
// the agent to leave the login to the operator. It returns the empty string
// when nothing was recorded.
func (c *Collector) RenderContext() string {
	ns := c.notices()
	if len(ns) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(contextLead)
	sb.WriteString("\n\n")
	for _, n := range ns {
		sb.WriteString("  - ")
		sb.WriteString(n.text)
		if n.provider {
			sb.WriteString(" ")
			sb.WriteString(contextLogin)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// reasonClause completes "the provider ..." for a reason.
func reasonClause(r Reason) string {
	switch r {
	case ReasonLoggedOut:
		return "is logged out or expired"
	case ReasonTimedOut:
		return "timed out"
	default:
		return "is unreachable"
	}
}

// formatAge renders an age the way the served warning states it: "less than
// a minute" below 60 seconds, then whole minutes below 60 minutes, whole
// hours below 48 hours, and whole days from there, rounded down, with the
// singular unit for 1. A negative age (a clock that moved backwards) reads
// as less than a minute.
func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return plural(int64(d/time.Minute), "minute")
	case d < 48*time.Hour:
		return plural(int64(d/time.Hour), "hour")
	default:
		return plural(int64(d/(24*time.Hour)), "day")
	}
}

func plural(n int64, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// fields returns an identity's fields cleaned for display. An empty kind
// reads as "vault" so the sentence still parses.
func fields(id Identity) (kind, project, env, path, domain string) {
	kind = clean(id.Kind)
	if kind == "" {
		kind = "vault"
	}
	return kind, clean(id.ProjectID), clean(id.Environment), clean(id.FolderPath), clean(id.APIDomain)
}

// clean strips control and line-separator characters from a field and caps
// it at maxFieldRunes. Identity fields come from workspace configuration,
// which niwa did not write, and reach both a terminal and an agent's
// context; the character classes removed match the key report's.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f:
			return -1
		case r >= 0x80 && r <= 0x9f:
			return -1
		case r == 0x2028, r == 0x2029:
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxFieldRunes {
		s = string(r[:maxFieldRunes])
	}
	return s
}
