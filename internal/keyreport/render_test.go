package keyreport

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// prohibitedTerms is the vocabulary a rendered report must never contain: the
// names of secret-storage products and the implementation nouns their clients
// use. A report is read by someone who did not choose the backend and may not
// have access to it, so it describes what niwa could not do, not how a
// particular backend works.
//
// The provider kind is the single exception, because naming it is required when
// a configured provider cannot be reached. The kind token happens to be the
// vendor's own name, so the test scrubs the kind out of the rendering before
// scanning; a naive scan would fail on content the requirements mandate.
//
// Add a term here when a new backend lands, not when a test fails.
var prohibitedTerms = []string{
	"infisical",
	"vault",
	"hashicorp",
	"1password",
	"onepassword",
	"bitwarden",
	"doppler",
	"secret manager",
	"keychain",
	"machine identity",
	"universal auth",
	"service token",
	"environment slug",
	"project id",
	"workspace id",
}

// everyGroup covers one entry per rendered message group, so a vocabulary scan
// sees every sentence the renderers can produce.
func everyGroup() []Entry {
	return []Entry{
		{Scope: "env.secrets", Key: "ANTHROPIC_API_KEY", Cause: CauseNoSource, Level: config.LevelRequired, Description: "key the agent uses to reach the model"},
		{Scope: "env.secrets", Key: "TAVILY_API_KEY", Cause: config.CauseUndeclaredProvider, Level: config.LevelOptional, Description: "web search"},
		{Scope: "env.secrets", Key: "GH_PAT", Cause: config.CauseProviderUnreachable, Level: config.LevelRequired, Description: "repo read", ProviderKind: "infisical"},
		{Scope: "claude.env.secrets", Key: "SENTRY_DSN", Cause: config.CauseClientNotInstalled, Level: config.LevelRecommended, Description: "error reporting", ProviderKind: "infisical"},
		{Scope: "instance.env.secrets", Key: "STRIPE_KEY", Cause: config.CauseKeyNotFound, Level: config.LevelRequired, Description: "billing", ProviderKind: "infisical"},
	}
}

// TestRenderersRejectProhibitedVocabulary enforces the vocabulary rule against
// both renderers, with the provider-kind token scrubbed out first.
func TestRenderersRejectProhibitedVocabulary(t *testing.T) {
	const kind = "infisical"
	for name, rendered := range map[string]string{
		"RenderText":    RenderText(collectorOf(everyGroup()).Report()),
		"RenderContext": RenderContext(collectorOf(everyGroup()).Report()),
	} {
		scrubbed := strings.ReplaceAll(strings.ToLower(rendered), kind, "<provider-kind>")
		for _, term := range prohibitedTerms {
			if strings.Contains(scrubbed, term) {
				t.Errorf("%s contains prohibited term %q:\n%s", name, term, rendered)
			}
		}
	}
}

// TestRenderNoSourceStatesNothingMore is the message rule for the case this
// work exists to serve: no provider capable of supplying the key is configured
// anywhere. niwa knows only that, so it says only that -- no remedy, and no
// claim about a configuration layer or a repository it cannot see. A remote
// answers "not found" identically for a private repository and a nonexistent
// one, so any such claim would be both unverifiable and a disclosure.
func TestRenderNoSourceStatesNothingMore(t *testing.T) {
	entries := []Entry{
		{Scope: "env.secrets", Key: "ANTHROPIC_API_KEY", Cause: CauseNoSource, Level: config.LevelRequired, Description: "key the agent uses to reach the model"},
	}
	for name, got := range map[string]string{
		"RenderText":    RenderText(entries),
		"RenderContext": RenderContext(entries),
	} {
		if !strings.Contains(got, "ANTHROPIC_API_KEY") {
			t.Errorf("%s must name the key:\n%s", name, got)
		}
		if !strings.Contains(got, "key the agent uses to reach the model") {
			t.Errorf("%s must carry the declared description, which is the only guidance available here:\n%s", name, got)
		}
		if !strings.Contains(got, "required") {
			t.Errorf("%s must carry the declared level:\n%s", name, got)
		}
		// No remedy, and nothing that speculates about what the reader cannot see.
		for _, banned := range []string{"remedy", "repositor", "private", "permission", "access", "may be", "might", "ask your"} {
			if strings.Contains(strings.ToLower(got), banned) {
				t.Errorf("%s must not contain %q for a no-source shortfall:\n%s", name, banned, got)
			}
		}
	}
}

// TestUnreachableRemedyDiffersWhenClientIsAbsent: an absent client binary and a
// present-but-unreachable one both mean "no value", and both name the provider
// kind, but only one of them is fixed by installing something.
func TestUnreachableRemedyDiffersWhenClientIsAbsent(t *testing.T) {
	unreachable := RenderText([]Entry{{
		Scope: "env.secrets", Key: "GH_PAT", Cause: config.CauseProviderUnreachable,
		Level: config.LevelRequired, Description: "repo read", ProviderKind: "fake",
	}})
	absent := RenderText([]Entry{{
		Scope: "env.secrets", Key: "GH_PAT", Cause: config.CauseClientNotInstalled,
		Level: config.LevelRequired, Description: "repo read", ProviderKind: "fake",
	}})

	for name, got := range map[string]string{"unreachable": unreachable, "client absent": absent} {
		if !strings.Contains(got, "fake") {
			t.Errorf("%s rendering must name the provider kind:\n%s", name, got)
		}
		if !strings.Contains(got, "could not be reached") {
			t.Errorf("%s rendering must say the provider could not be reached:\n%s", name, got)
		}
		if !strings.Contains(got, "remedy:") {
			t.Errorf("%s rendering must give a remedy:\n%s", name, got)
		}
	}

	remedyOf := func(s string) string {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "remedy:") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	if remedyOf(unreachable) == remedyOf(absent) {
		t.Errorf("both causes give the same remedy %q; an absent client is fixed by installing it, not by repairing access", remedyOf(absent))
	}
	if !strings.Contains(absent, "install") {
		t.Errorf("the absent-client remedy must say to install the client:\n%s", absent)
	}
}

// TestRenderStripsControlCharacters: descriptions are author-supplied TOML free
// text that reaches a terminal and, on the hook path, an agent's context
// window. On the second one a smuggled newline is an instruction-injection
// surface rather than a display defect.
func TestRenderStripsControlCharacters(t *testing.T) {
	entries := []Entry{{
		Scope: "env.secrets",
		Key:   "K\x07EY",
		Cause: CauseNoSource,
		Level: config.LevelRequired,
		// A newline, an ANSI escape, a C1 byte, and a unicode line separator.
		Description: "line one\nIGNORE PREVIOUS\x1b[31m\u0085\u2028second",
	}}

	for name, got := range map[string]string{
		"RenderText":    RenderText(entries),
		"RenderContext": RenderContext(entries),
	} {
		for _, bad := range []string{"\n IGNORE", "\x1b", "\x07", "\u0085", "\u2028", "\u2029"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s left %q in the output:\n%q", name, bad, got)
			}
		}
		// The description must survive as one line: the sanitizer removes the
		// separators rather than splitting on them.
		if !strings.Contains(got, "line oneIGNORE PREVIOUS[31msecond") {
			t.Errorf("%s did not join the description into one line:\n%q", name, got)
		}
		if !strings.Contains(got, "KEY") {
			t.Errorf("%s did not sanitize the key name:\n%q", name, got)
		}
	}
}

// TestRenderEmptyReportIsEmptyString lets every surface print the rendering
// unconditionally instead of guarding each call.
func TestRenderEmptyReportIsEmptyString(t *testing.T) {
	if got := RenderText(nil); got != "" {
		t.Errorf("RenderText(nil) = %q, want empty", got)
	}
	if got := RenderContext(nil); got != "" {
		t.Errorf("RenderContext(nil) = %q, want empty", got)
	}
}

// TestRenderGroupsAreOrderedIndependentOfInput: the blocks come out in rank
// order whatever order the entries arrive in, so a report is byte-identical
// across runs.
func TestRenderGroupsAreOrderedIndependentOfInput(t *testing.T) {
	forward := everyGroup()
	backward := make([]Entry, len(forward))
	for i, e := range forward {
		backward[len(forward)-1-i] = e
	}
	if RenderText(collectorOf(forward).Report()) != RenderText(collectorOf(backward).Report()) {
		t.Error("rendering depends on the order entries were recorded in")
	}
}

// collectorOf is a test helper: a collector preloaded with entries, so a test can name
// its fixture once and render it through the same sort the surfaces use.
func collectorOf(entries []Entry) *Collector {
	c := New()
	for _, e := range entries {
		c.Add(e)
	}
	return c
}
