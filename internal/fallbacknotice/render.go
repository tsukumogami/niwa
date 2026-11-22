package fallbacknotice

import (
	"fmt"
	"strings"
	"time"
)

// The wording below is the operator-facing contract. The served warning and
// the nothing-to-fall-back-on line are fixed by the design; "may be stale"
// and "Run `infisical login` to refresh them" are the phrases outside tooling
// is told it can search for, so neither line may be reworded. The golden
// tests in render_test.go pin every line here byte for byte, the three store
// warnings included. Each format takes the rendered fields in order.
//
// The provider kind is filled in but the login command is literal: the
// design fixes `infisical login`, which is right only for kind "infisical"
// (the only real backend today). A second backend needs its own wording
// rather than a substituted command.
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
	contextLead = "While provisioning, niwa's store of last-resolved secret values reported the following. Any command these messages name is for the operator to run, not for you:"

	// contextLogin follows every notice that asks for a login in the agent-context
	// rendering. An interactive login from the agent's shell would prompt
	// or hang, so the agent is told to hand it to the operator.
	contextLogin = "Ask the operator to run `infisical login`; do not run it yourself, because it is interactive and would hang this session."

	// maxFieldRunes caps each identity field and directory in a rendering.
	maxFieldRunes = 200

	// notePrefix starts a terminal line that reports something niwa did
	// rather than something that went wrong.
	notePrefix = "note: "

	// The plugin store's repair records. Each format takes the release
	// ("github.com/<owner>/<repo>@<commit>") and the instances that link it,
	// then the reason where there is one. None names a command to run: the
	// next provisioning command restores a missing release by itself.
	releaseRestoredFormat    = "restored plugin release %s for instances: %s."
	releaseRefusedFormat     = "refused to restore plugin release %s, linked from instances: %s: %s; nothing was fetched and the links were left as they are."
	releaseUndeclaredFormat  = "plugin release %s, linked from instances: %s, is from a marketplace source this workspace no longer declares; it was not restored and the links were left as they are."
	releaseNotRestoredFormat = "could not restore plugin release %s, linked from instances: %s: %s; nothing was marked held for it."
	releaseTamperedFormat    = "plugin release %s was changed after it was placed (tampered with); fetched it again for instances: %s."

	// sweepSkippedFormat: why the sweep of unused releases deleted nothing,
	// naming the path it couldn't read.
	sweepSkippedFormat = "the plugin store's sweep of unused releases was skipped: %s; nothing was deleted."

	// sweepStoppedFormat: why the sweep stopped after it had already removed
	// some unused releases, with their count and its noun.
	sweepStoppedFormat = "the plugin store's sweep of unused releases stopped: %s; %d unused %s had already been removed."

	// storeFallbackFormat: the store's plugins directory, its condition and
	// the marketplaces that fetched their own copy. It names no command: niwa
	// never changes a mode it didn't just create, so a store narrowed by hand
	// is the operator's to put right.
	storeFallbackFormat = "the host plugin store at %s can't be used (%s); this instance fetched its own copy of marketplaces: %s."

	// releaseContextLead opens the plugin store's part of the agent-context
	// rendering.
	releaseContextLead = "While provisioning, niwa's host plugin store reported the following about releases that instances of this workspace link to:"

	// maxListedInstances caps the instances one release line names.
	maxListedInstances = 10
)

// notice is one rendered message: its sentence, and whether it concerns a
// provider login (and so earns the agent-context login instruction).
type notice struct {
	text       string
	needsLogin bool
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
			text:       fmt.Sprintf(servedFormat, kind, project, env, path, domain, reasonClause(n.Reason), formatAge(now.Sub(n.OldestResolvedAt))),
			needsLogin: true,
		})
	}
	for _, id := range c.Misses() {
		kind, project, env, path, domain := fields(id)
		out = append(out, notice{text: fmt.Sprintf(missFormat, kind, project, env, path, domain), needsLogin: true})
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

// RenderText renders the notices for a terminal, one "warning: " line each,
// the plugin store's records after them ("note: " for a restored release).
// It returns the empty string when nothing was recorded, including on a nil
// collector, so a caller can print it unconditionally.
func (c *Collector) RenderText() string {
	var sb strings.Builder
	for _, n := range c.notices() {
		sb.WriteString(textPrefix)
		sb.WriteString(n.text)
		sb.WriteString("\n")
	}
	for _, n := range c.releaseNotices() {
		if n.restored {
			sb.WriteString(notePrefix)
		} else {
			sb.WriteString(textPrefix)
		}
		sb.WriteString(n.text)
		sb.WriteString("\n")
	}
	return sb.String()
}

// RenderContext renders the notices for an agent's context window, which is
// where the SessionStart hook delivers them. Each notice carries the same
// sentence as its terminal line, and each one about a provider login tells
// the agent to leave the login to the operator. The plugin store's records
// follow under a lead of their own. It returns the empty string when nothing
// was recorded.
func (c *Collector) RenderContext() string {
	var sb strings.Builder
	if ns := c.notices(); len(ns) > 0 {
		sb.WriteString(contextLead)
		sb.WriteString("\n\n")
		for _, n := range ns {
			sb.WriteString("  - ")
			sb.WriteString(n.text)
			if n.needsLogin {
				sb.WriteString(" ")
				sb.WriteString(contextLogin)
			}
			sb.WriteString("\n")
		}
	}
	if rs := c.releaseNotices(); len(rs) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(releaseContextLead)
		sb.WriteString("\n\n")
		for _, n := range rs {
			sb.WriteString("  - ")
			sb.WriteString(n.text)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// releaseNotice is one rendered plugin release record.
type releaseNotice struct {
	text     string
	restored bool
}

// releaseNotices renders the plugin store's fallback notes first, one per
// store and condition, then its repair records in the order they were
// recorded, then the skipped sweep when there was one. They follow the
// secret-store notices and have a lead of their own in the agent context, so
// nothing about a plugin release reads as a secret-store message.
func (c *Collector) releaseNotices() []releaseNotice {
	var out []releaseNotice
	for _, n := range c.StoreFallbacks() {
		out = append(out, releaseNotice{text: fmt.Sprintf(storeFallbackFormat, clean(n.Store), clean(n.Condition), instanceList(n.Marketplaces))})
	}
	for _, n := range c.ReleaseNotes() {
		name, served := clean(n.Release), instanceList(n.Instances)
		switch n.Outcome {
		case ReleaseRestored:
			out = append(out, releaseNotice{text: fmt.Sprintf(releaseRestoredFormat, name, served), restored: true})
		case ReleaseRefused:
			out = append(out, releaseNotice{text: fmt.Sprintf(releaseRefusedFormat, name, served, clean(n.Reason))})
		case ReleaseUndeclared:
			out = append(out, releaseNotice{text: fmt.Sprintf(releaseUndeclaredFormat, name, served)})
		case ReleaseNotRestored:
			out = append(out, releaseNotice{text: fmt.Sprintf(releaseNotRestoredFormat, name, served, clean(n.Reason))})
		case ReleaseTampered:
			out = append(out, releaseNotice{text: fmt.Sprintf(releaseTamperedFormat, name, served)})
		}
	}
	if reason, ok := c.SweepSkipped(); ok {
		if removed := c.SweepRemovedBeforeStop(); removed > 0 {
			noun := "releases"
			if removed == 1 {
				noun = "release"
			}
			out = append(out, releaseNotice{text: fmt.Sprintf(sweepStoppedFormat, clean(reason), removed, noun)})
		} else {
			out = append(out, releaseNotice{text: fmt.Sprintf(sweepSkippedFormat, clean(reason))})
		}
	}
	return out
}

// instanceList joins the cleaned instance paths, at most maxListedInstances
// of them, and says how many more there are.
func instanceList(instances []string) string {
	if len(instances) == 0 {
		return "none"
	}
	shown := instances
	if len(shown) > maxListedInstances {
		shown = shown[:maxListedInstances]
	}
	parts := make([]string, len(shown))
	for i, p := range shown {
		parts[i] = clean(p)
	}
	list := strings.Join(parts, ", ")
	if extra := len(instances) - len(shown); extra > 0 {
		list += fmt.Sprintf(" and %d more", extra)
	}
	return list
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
// context; the character classes removed match keyreport's sanitize, which
// this package can't import (it stays stdlib-only), so change both together.
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
