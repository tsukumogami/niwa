package workspace

import (
	"math"
	"slices"
	"strings"
)

// envPrefixBlocklist is the set of vendor-token prefixes that always indicate
// a probable secret. A blocklist match wins over entropy and allowlist checks.
var envPrefixBlocklist = []string{
	"sk_live_",
	"sk_test_",
	"AKIA",
	"ASIA",
	"ghp_",
	"gho_",
	"ghu_",
	"ghs_",
	"ghr_",
	"github_pat_",
	"glpat-",
	"xoxb-",
	"xoxp-",
	"xapp-",
	"sq0atp-",
	"sq0csp-",
}

// envSafeAllowlist is the set of well-known placeholder strings that are safe
// regardless of their entropy score. An allowlist match overrides the entropy
// check but does NOT override a blocklist match.
var envSafeAllowlist = []string{
	"",
	"changeme",
	"placeholder",
	"<your-api-key>",
	"https://example.com/callback",
	"localhost",
	"127.0.0.1",
}

// envSafePrefixes are publishable-key prefixes whose values are safe by
// definition (Stripe pk_test_/pk_live_). These differ from blocklist prefixes
// in that they indicate non-secret keys intended for client-side use.
var envSafePrefixes = []string{
	"pk_test_",
	"pk_live_",
}

// classifyEnvValue reports whether value is safe to materialize as an implicit
// var. isSafe=false means the value is a probable secret; reason names the
// detection rule (e.g. "known prefix sk_live_", "entropy > 3.5").
//
// Priority order:
//  1. Blocklist match → isSafe=false, even when entropy is low.
//  2. Allowlist match → isSafe=true, overrides entropy check.
//  3. Entropy strictly > 3.5 → isSafe=false.
//  4. Otherwise → isSafe=true.
//
// reason MUST NOT include the value, any fragment of the value, or the raw
// entropy score — only the rule name and threshold (R22: diagnostics never
// contain secret bytes).
//
// Boundary: entropy == 3.5 is treated as safe (strictly greater than 3.5 is
// required to classify as a probable secret).
func classifyEnvValue(value string) (isSafe bool, reason string) {
	// Step 1: blocklist check — wins regardless of entropy or allowlist.
	for _, prefix := range envPrefixBlocklist {
		if strings.HasPrefix(value, prefix) {
			return false, "known prefix " + prefix
		}
	}

	// Step 2: allowlist check — safe patterns override entropy.
	if slices.Contains(envSafeAllowlist, value) {
		return true, "allowlist match"
	}
	for _, prefix := range envSafePrefixes {
		if strings.HasPrefix(value, prefix) {
			return true, "allowlist match"
		}
	}

	// Step 3: entropy check.
	if shannonEntropy(value) > 3.5 {
		return false, "entropy > 3.5"
	}

	return true, ""
}

// shannonEntropy computes the Shannon entropy of s in bits per character.
// Returns 0 for the empty string.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}

	// Count frequency of each byte value.
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}

	n := float64(len(s))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}
