package keyreport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// Group ranks. A rank is the unit a message is written for: two causes that
// deserve the same sentence share one, and the order here is the order the
// blocks appear in a rendered report.
const (
	// rankNoSource covers both shapes of "nothing could have supplied this":
	// a key declared with no value anywhere, and a reference naming a provider
	// the merged configuration does not declare. Neither establishes anything
	// about a backend, so both get the same sentence and no remedy.
	rankNoSource = iota

	// rankUnreachable is a configured provider that could not be contacted.
	rankUnreachable

	// rankClientAbsent narrows rankUnreachable to a missing client binary,
	// which earns a different remedy: install it, rather than repair access.
	rankClientAbsent

	// rankNotHeld is a provider that answered and does not hold the key.
	rankNotHeld

	// rankOther catches a cause added later that no renderer knows about, so a
	// new cause degrades to a plain listing instead of vanishing from the
	// report.
	rankOther
)

// group is what a block of the report is keyed by: the message to write, and
// the provider kind that message names. Two kinds produce two blocks.
type group struct {
	rank int
	kind string
}

func groupOf(e Entry) group {
	switch e.Cause {
	case CauseNoSource, config.CauseUndeclaredProvider:
		// Neither shape names a provider, so the kind is dropped from the key:
		// they must not split into two blocks on an empty-vs-set kind.
		return group{rank: rankNoSource}
	case config.CauseProviderUnreachable:
		return group{rank: rankUnreachable, kind: e.ProviderKind}
	case config.CauseClientNotInstalled:
		return group{rank: rankClientAbsent, kind: e.ProviderKind}
	case config.CauseKeyNotFound:
		return group{rank: rankNotHeld, kind: e.ProviderKind}
	default:
		return group{rank: rankOther}
	}
}

// headline is the sentence introducing a block's keys.
//
// The rankNoSource wording is load-bearing and deliberately spare. niwa knows
// only that nothing in the configuration it read can supply the key. It must
// not name a repository it did not successfully read, and it must not suggest
// that some layer it cannot see would have supplied the value: a remote answers
// "not found" identically for a repository that is private and for one that
// does not exist, so any such claim would be both unverifiable and a
// disclosure about the reader's access.
func headline(g group) string {
	switch g.rank {
	case rankNoSource:
		return "no configured source can supply these keys:"
	case rankUnreachable:
		return fmt.Sprintf("the %s provider could not be reached:", providerLabel(g.kind))
	case rankClientAbsent:
		return fmt.Sprintf("the %s provider could not be reached, because its client is not installed on this host:", providerLabel(g.kind))
	case rankNotHeld:
		return fmt.Sprintf("the %s provider was reached and does not hold these keys:", providerLabel(g.kind))
	default:
		return "these keys have no value:"
	}
}

// remedy is the action line printed under a block, or the empty string when
// there is none to give.
//
// rankNoSource has no remedy on purpose. niwa has nothing safe to suggest
// there — it cannot tell the reader to configure a provider it has no evidence
// exists, or to seek access it cannot confirm is available. That silence is why
// each key carries its declared description instead: the description is the
// only thing in the report that tells the reader what the value is for and
// therefore where to get it.
func remedy(g group) string {
	switch g.rank {
	case rankUnreachable:
		return fmt.Sprintf("remedy: check that this host can reach the %s provider and that its credentials are current, then run this command again.", providerLabel(g.kind))
	case rankClientAbsent:
		return fmt.Sprintf("remedy: install the %s client, then run this command again.", providerLabel(g.kind))
	case rankNotHeld:
		return "remedy: add each key to the provider, supply it from your personal overlay, or drop it from the requirement sub-table."
	default:
		return ""
	}
}

// providerLabel substitutes a neutral word when the kind is unknown, so a
// sentence that names the kind still reads as a sentence without one.
func providerLabel(kind string) string {
	if kind == "" {
		return "configured"
	}
	return sanitize(kind)
}

// block is one headline plus the keys it covers.
type block struct {
	g       group
	entries []Entry
}

// blocks buckets entries into their message groups. Input order is preserved
// inside a bucket, and buckets come out in rank order, so a sorted input
// produces a fully determined output.
func blocks(entries []Entry) []block {
	index := make(map[group]int)
	var out []block
	for _, e := range entries {
		g := groupOf(e)
		i, ok := index[g]
		if !ok {
			i = len(out)
			index[g] = i
			out = append(out, block{g: g})
		}
		out[i].entries = append(out[i].entries, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].g.rank != out[j].g.rank {
			return out[i].g.rank < out[j].g.rank
		}
		return out[i].g.kind < out[j].g.kind
	})
	return out
}

// entryLine renders one key: its name, its declared level when it has one, the
// table it was declared in, and its description.
func entryLine(e Entry) string {
	var sb strings.Builder
	sb.WriteString(sanitize(e.Key))
	if e.Level != config.LevelUnspecified {
		fmt.Fprintf(&sb, " [%s]", sanitize(string(e.Level)))
	}
	fmt.Fprintf(&sb, " (%s)", sanitize(e.Scope))
	if d := sanitize(e.Description); d != "" {
		sb.WriteString(": ")
		sb.WriteString(d)
	}
	return sb.String()
}

// RenderText renders the report for a terminal. It returns the empty string
// for an empty report so a caller can print it unconditionally.
func RenderText(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	if len(entries) == 1 {
		sb.WriteString("niwa: 1 declared key has no value.\n")
	} else {
		fmt.Fprintf(&sb, "niwa: %d declared keys have no value.\n", len(entries))
	}
	for _, b := range blocks(entries) {
		fmt.Fprintf(&sb, "  %s\n", headline(b.g))
		for _, e := range b.entries {
			fmt.Fprintf(&sb, "    - %s\n", entryLine(e))
		}
		if r := remedy(b.g); r != "" {
			fmt.Fprintf(&sb, "    %s\n", r)
		}
	}
	return sb.String()
}

// RenderContext renders the report for an agent's context window, which is
// where the SessionStart hook delivers it. It differs from RenderText in
// framing rather than content: it says what state the instance is in and tells
// the reader not to invent the missing values.
//
// The sanitization the shared line renderer applies matters more here than on a
// terminal. Descriptions are author-supplied free text arriving in an agent's
// instructions, so control characters are an injection surface and not merely a
// display defect.
func RenderContext(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	if len(entries) == 1 {
		sb.WriteString("This instance was provisioned, but 1 declared environment key could not be supplied.\n")
	} else {
		fmt.Fprintf(&sb, "This instance was provisioned, but %d declared environment keys could not be supplied.\n", len(entries))
	}
	for _, b := range blocks(entries) {
		fmt.Fprintf(&sb, "\n%s\n", capitalize(headline(b.g)))
		for _, e := range b.entries {
			fmt.Fprintf(&sb, "  - %s\n", entryLine(e))
		}
		if r := remedy(b.g); r != "" {
			fmt.Fprintf(&sb, "  %s\n", capitalize(r))
		}
	}
	sb.WriteString("\nTreat those variables as unset. Do not guess or fabricate values for them; if a task needs one, say so and stop.\n")
	return sb.String()
}

// capitalize upper-cases the first byte of a sentence written for a headline
// position. The message table is written lower-case for the indented terminal
// layout; the prose rendering starts sentences.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// sanitize strips control characters from author-supplied text.
//
// Descriptions and key names come out of a TOML file niwa did not write, and
// they reach two places: a terminal, where a control character can rewrite
// lines already printed, and an agent's context window on the hook path, where
// it can separate text that must stay one line. Removed: C0 (including tab,
// newline and carriage return, since every rendered field is one line), DEL,
// C1, and the unicode line and paragraph separators, which terminate a line for
// several consumers without being C0.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
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
}
