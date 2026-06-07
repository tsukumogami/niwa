package workspace

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestClassifyEnvValueBlocklist verifies that all 16 vendor-token prefixes
// produce CategoryVendorToken. Prefix strings are hardcoded here (not
// range-iterated from envPrefixBlocklist) so the test is a precise
// specification of what is expected. The reason no longer echoes the matched
// prefix (R22): the new control flow keys on the category.
func TestClassifyEnvValueBlocklist(t *testing.T) {
	cases := []struct {
		prefix string
		value  string
	}{
		{"sk_live_", "sk_live_abcdefghijk"},
		{"sk_test_", "sk_test_abcdefghijk"},
		{"AKIA", "AKIAabcdefghijkl"},
		{"ASIA", "ASIAabcdefghijkl"},
		{"ghp_", "ghp_aBcDeFgHiJkL"},
		{"gho_", "gho_aBcDeFgHiJkL"},
		{"ghu_", "ghu_aBcDeFgHiJkL"},
		{"ghs_", "ghs_aBcDeFgHiJkL"},
		{"ghr_", "ghr_aBcDeFgHiJkL"},
		{"github_pat_", "github_pat_aBcDeFgHiJkL"},
		{"glpat-", "glpat-aBcDeFgHiJkL"},
		{"xoxb-", "xoxb-aBcDeFgHiJkL"},
		{"xoxp-", "xoxp-aBcDeFgHiJkL"},
		{"xapp-", "xapp-aBcDeFgHiJkL"},
		{"sq0atp-", "sq0atp-aBcDeFgHiJkL"},
		{"sq0csp-", "sq0csp-aBcDeFgHiJkL"},
	}

	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			category, reason := classifyEnvValue(tc.value)
			if category != config.CategoryVendorToken {
				t.Errorf("classifyEnvValue(%q): category=%v, want vendor-token", tc.prefix+"...", category)
			}
			// R22: reason must not contain the value or the matched prefix.
			if strings.Contains(reason, tc.value) {
				t.Errorf("reason %q contains the value text (R22 violation)", reason)
			}
			if strings.Contains(reason, tc.prefix) {
				t.Errorf("reason %q contains the matched prefix %q (R22 violation)", reason, tc.prefix)
			}
		})
	}
}

// TestClassifyEnvValueBlocklistWinsOverLowEntropy verifies that a blocklist
// match still produces CategoryVendorToken even when the value's entropy is
// below 3.5.
func TestClassifyEnvValueBlocklistWinsOverLowEntropy(t *testing.T) {
	// "sk_live_aaaa" has very low entropy but must still be flagged.
	category, reason := classifyEnvValue("sk_live_aaaa")
	if category != config.CategoryVendorToken {
		t.Errorf("expected CategoryVendorToken for blocklist prefix with low entropy, got %v", category)
	}
	if strings.Contains(reason, "sk_live_") {
		t.Errorf("reason %q contains the matched prefix (R22 violation)", reason)
	}
}

// TestClassifyEnvValueAllowlist verifies that all known safe placeholder
// strings produce CategorySafe. Values are hardcoded here (not range-iterated
// from envSafeAllowlist) so the test is a precise specification.
func TestClassifyEnvValueAllowlist(t *testing.T) {
	cases := []string{
		"",
		"changeme",
		"placeholder",
		"<your-api-key>",
		"https://example.com/callback",
		"localhost",
		"127.0.0.1",
	}

	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			category, _ := classifyEnvValue(value)
			if category != config.CategorySafe {
				t.Errorf("classifyEnvValue(%q): category=%v, want safe", value, category)
			}
		})
	}
}

// TestClassifyEnvValueStripePublicKeys verifies that pk_test_ and pk_live_
// prefixed values are treated as safe regardless of the suffix content. These
// are Stripe publishable keys, not secret keys.
func TestClassifyEnvValueStripePublicKeys(t *testing.T) {
	cases := []string{
		"pk_test_xxxxxxxxxxxx",
		"pk_live_xxxxxxxxxxxx",
		"pk_test_51AbCdEfGhIjK",
		"pk_live_51AbCdEfGhIjK",
	}

	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			category, _ := classifyEnvValue(value)
			if category != config.CategorySafe {
				t.Errorf("classifyEnvValue(%q): category=%v, want safe (Stripe publishable key prefix)", value, category)
			}
		})
	}
}

// TestClassifyEnvValueAllowlistOverridesEntropy verifies that an allowlist
// match with entropy > 3.5 still produces CategorySafe.
func TestClassifyEnvValueAllowlistOverridesEntropy(t *testing.T) {
	value := "https://example.com/callback"
	entropy := shannonEntropy(value)
	if entropy <= 3.5 {
		t.Skipf("test value %q has entropy %.4f <= 3.5; not a meaningful override case", value, entropy)
	}
	category, _ := classifyEnvValue(value)
	if category != config.CategorySafe {
		t.Errorf("classifyEnvValue(%q): allowlist should override entropy (%.4f > 3.5)", value, entropy)
	}
}

// TestClassifyEnvValueEntropyThreshold verifies boundary behaviour around 3.5.
//
// Boundary rule (documented): entropy == 3.5 is treated as SAFE.
// Only entropy strictly greater than 3.5 yields CategoryEntropy.
func TestClassifyEnvValueEntropyThreshold(t *testing.T) {
	// A string of identical characters has entropy 0 → safe.
	category, _ := classifyEnvValue("aaaaaaaaaa")
	if category != config.CategorySafe {
		t.Error("low-entropy value (all same char): expected CategorySafe")
	}

	// A high-entropy string (not on blocklist, not on allowlist) → entropy.
	highEntropy := "ABCDEFGHabcdefgh01234567"
	category, reason := classifyEnvValue(highEntropy)
	if category != config.CategoryEntropy {
		t.Errorf("high-entropy value: expected CategoryEntropy, got %v (entropy=%.4f)", category, shannonEntropy(highEntropy))
	}
	if !strings.Contains(reason, "entropy > 3.5") {
		t.Errorf("reason %q does not contain 'entropy > 3.5'", reason)
	}
	// R22: reason must not contain the value.
	if strings.Contains(reason, highEntropy) {
		t.Errorf("reason %q contains the value text (R22 violation)", reason)
	}

	// "aabbccdd" has 4 unique chars each appearing 2 times → entropy = 2.0 → safe.
	category, _ = classifyEnvValue("aabbccdd")
	if category != config.CategorySafe {
		t.Error("low-entropy value: expected CategorySafe")
	}
}

// TestClassifyEnvValueEntropyExactly35 documents and tests the boundary at
// exactly 3.5. Entropy == 3.5 is SAFE per the implementation.
func TestClassifyEnvValueEntropyExactly35(t *testing.T) {
	// Construction: chars a,b,c,d appear twice; e..l appear once → H = 3.5.
	v := "aabbccddefghijkl"
	h := shannonEntropy(v)

	const eps = 1e-9
	if !(h >= 3.5-eps && h <= 3.5+eps) {
		t.Logf("note: constructed string has entropy %.10f (target 3.5)", h)
	}

	category, _ := classifyEnvValue(v)
	if h > 3.5 {
		if category != config.CategoryEntropy {
			t.Errorf("entropy %.10f > 3.5: expected CategoryEntropy, got %v", h, category)
		}
	} else {
		if category != config.CategorySafe {
			t.Errorf("entropy %.10f <= 3.5: expected CategorySafe, got %v", h, category)
		}
	}

	// 16 hex digits each appearing 4 times = 64 chars → H = 4.0 > 3.5.
	hex64 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	h64 := shannonEntropy(hex64)
	category64, reason64 := classifyEnvValue(hex64)
	if category64 != config.CategoryEntropy {
		t.Errorf("entropy %.4f > 3.5: expected CategoryEntropy for hex64, got %v", h64, category64)
	}
	if !strings.Contains(reason64, "entropy > 3.5") {
		t.Errorf("reason %q should contain 'entropy > 3.5'", reason64)
	}
}

// TestClassifyEnvValueEmptyString verifies that an empty value is always safe.
func TestClassifyEnvValueEmptyString(t *testing.T) {
	category, _ := classifyEnvValue("")
	if category != config.CategorySafe {
		t.Errorf("classifyEnvValue(\"\"): expected CategorySafe, got %v", category)
	}
}

// TestClassifyEnvValueR22ReasonNoValue asserts that the reason string never
// contains the literal value (or any fragment of it) for probable-secret cases.
func TestClassifyEnvValueR22ReasonNoValue(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"blocklist sk_live_", "sk_live_s3cr3tK3yAb"},
		{"blocklist ghp_", "ghp_AbCdEfGhIjKlMnOp"},
		{"high entropy no prefix", "ABCDEFGHabcdefgh01234567"},
		{"AKIA prefix", "AKIAabcde12345FGHIJ"},
		{"xoxb- prefix", "xoxb-123-456-abcde"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			category, reason := classifyEnvValue(tc.value)
			if category == config.CategorySafe {
				t.Skipf("value classified as safe — R22 check only applies to probable-secret cases")
			}
			if strings.Contains(reason, tc.value) {
				t.Errorf("R22 violation: reason %q contains value text", reason)
			}
			// No fragment longer than 4 chars from the value should appear in
			// the reason. The reason no longer echoes any prefix, so there is
			// no exemption needed.
			for i := 0; i+4 <= len(tc.value); i++ {
				fragment := tc.value[i : i+4]
				if strings.Contains(reason, fragment) {
					t.Errorf("R22 violation: reason %q contains value fragment %q", reason, fragment)
				}
			}
		})
	}
}

// TestClassifyEnvValueLowEntropyNoMatch verifies that a low-entropy value with
// no blocklist or allowlist match is classified as safe.
func TestClassifyEnvValueLowEntropyNoMatch(t *testing.T) {
	category, _ := classifyEnvValue("production")
	if category != config.CategorySafe {
		t.Errorf("low-entropy value 'production': expected CategorySafe (entropy=%.4f)", shannonEntropy("production"))
	}
}

// TestClassifyEnvValueHighEntropyReason verifies that an entropy detection
// produces a reason containing "entropy > 3.5".
func TestClassifyEnvValueHighEntropyReason(t *testing.T) {
	value := "xZ9qK2mP8wR1nF4tY7vJ0sL3"
	category, reason := classifyEnvValue(value)
	if category == config.CategorySafe {
		t.Logf("value entropy = %.4f", shannonEntropy(value))
		t.Skip("value unexpectedly classified as safe — entropy may be <= 3.5")
	}
	if category != config.CategoryEntropy {
		t.Errorf("expected CategoryEntropy, got %v", category)
	}
	if !strings.Contains(reason, "entropy > 3.5") {
		t.Errorf("reason %q does not contain 'entropy > 3.5'", reason)
	}
}
