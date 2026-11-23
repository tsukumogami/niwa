package removal

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Redacted is what ScrubEvidence prints in place of a secret it finds.
const Redacted = "[redacted]"

// StripBidi turns every Unicode bidirectional control into a space.
// unicode.IsControl covers the C0 and C1 ranges but not these, and an
// override would let text print in an order other than the one it was
// written in. Like strings.Map, it turns each invalid UTF-8 byte into
// U+FFFD.
func StripBidi(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Bidi_Control, r) {
			return ' '
		}
		return r
	}, s)
}

// StripControls makes s safe for one terminal line: invalid UTF-8 and
// control characters become spaces, runs of spaces collapse into one, and
// leading and trailing space is trimmed. A control becomes a space rather
// than being deleted, so removing one never joins the text on either side.
func StripControls(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToValidUTF8(s, " ") {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// CutText cuts s to at most max bytes, backing up to the start of a rune,
// with "..." marking a cut. max must be at least 3 when s is longer than
// max.
func CutText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max - len("...")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// ScrubEvidence makes a unit-sourced string safe to print: a branch name, a
// path, git's error text, a recorded reason. Anything a unit holds can carry
// a credential or a terminal escape, so every such string niwa prints passes
// through here first.
//
// The steps run in this order:
//
//  1. Whole escape sequences are removed: CSI (ESC [ ... final byte), OSC
//     (ESC ] ... ended by BEL or ESC \) and the two-byte ESC forms. What a
//     terminal would hide is gone, not left behind as inert text.
//  2. The control removal the removal gate's sanitizer uses (StripBidi, then
//     StripControls): every remaining control and bidirectional control
//     becomes a space and runs of space collapse. This runs before the
//     secret patterns so they see valid UTF-8 with its white space already
//     collapsed.
//  3. Secrets are replaced: URL userinfo is dropped, and known token shapes,
//     Authorization and Bearer values, the value of any pair whose key names
//     a secret, and long runs of mixed-case base64 become [redacted].
//  4. The result is cut to max characters (runes), with "..." marking a cut.
//
// The cut comes last on purpose: cutting first could leave the front half
// of a token that straddles the cut, too short for any pattern to know it.
func ScrubEvidence(s string, max int) string {
	return cutRunes(scrubSecrets(StripControls(StripBidi(stripEscapes(s)))), max)
}

// cutRunes cuts s to at most max runes, with "..." marking a cut.
func cutRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	keep := max - len("...")
	switch {
	case max <= 0:
		return ""
	case keep < 0:
		return "..."[:max]
	}
	n := 0
	for i := range s {
		if n == keep {
			return s[:i] + "..."
		}
		n++
	}
	return s + "..."
}

// stripEscapes removes every ESC-introduced sequence from s: a CSI sequence
// through its final byte, an OSC sequence through the BEL or ESC \ that ends
// it (or the end of s when nothing does), and an ESC followed by its
// intermediate bytes and one final byte. A lone ESC is removed too.
func stripEscapes(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch c := s[i]; {
		case c == '[':
			// CSI: parameter and intermediate bytes, then a final byte.
			i++
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
				i++
			}
			if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
				i++
			}
		case c == ']':
			// OSC: anything up to BEL or ESC \.
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		case c >= 0x20 && c <= 0x2f:
			// Intermediate bytes, then a final byte.
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
				i++
			}
			if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
				i++
			}
		case c >= 0x30 && c <= 0x7e:
			i++
		}
	}
	return b.String()
}

// secretKey is a key that names a secret: any word holding token, secret,
// password, passwd, key, auth or credential.
const secretKey = `[A-Za-z0-9_.-]*(?:token|secret|password|passwd|key|auth|credential)[A-Za-z0-9_.-]*`

var (
	// urlUserinfo is a URL's userinfo, up to the last @ before the host.
	urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/?#\s]*@`)

	// tokenShapes are credentials recognised by their own shape.
	tokenShapes = []*regexp.Regexp{
		regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
		regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),
		regexp.MustCompile(`xox[abposr]-[A-Za-z0-9-]{10,}`),
		regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`),
		regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`),
	}

	// skToken is an sk- key. It counts only where sk- doesn't end a longer
	// word, so task- and disk- survive.
	skToken = regexp.MustCompile(`(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{16,}`)

	// authorizationValue is the credential after an Authorization header
	// name, with its scheme kept when it names one.
	authorizationValue = regexp.MustCompile(`(?i)(authorization\s*:\s*)((?:basic|bearer|token|digest|negotiate)\s+)?[^\s,;"']+`)

	// bearerValue is the word after Bearer.
	bearerValue = regexp.MustCompile(`(?i)\b(bearer\s+)[^\s,;"']+`)

	// secretQuoted is a pair whose key names a secret and whose value is
	// quoted: key="value", key = 'value', "key": "value", "key":"value".
	// The whole quoted value goes, white space and all, and a quote that
	// is never closed takes the rest of the text with it. With a quoted
	// value a colon needs no white space after it.
	secretQuoted = regexp.MustCompile(`(?i)(` + secretKey + `)(["']?\s*[=:]\s*)("[^"]*(?:"|$)|'[^']*(?:'|$))`)

	// secretPair is a key=value or key: value pair whose key names a
	// secret, with or without white space around the = or before the
	// colon. A colon counts only when white space follows it, so the
	// scheme of a URL in a configuration key isn't read as a pair.
	secretPair = regexp.MustCompile(`(?i)(` + secretKey + `)(\s*=\s*|\s*:\s+)([^\s&;,"']+)`)

	// base64Run is a run of base64 characters long enough to be a key. The
	// alphabet leaves out / and . so an ordinary path breaks into short
	// runs.
	base64Run = regexp.MustCompile(`[A-Za-z0-9+=_-]{32,}`)
)

// scrubSecrets replaces every credential it recognises in s.
func scrubSecrets(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "${1}")
	for _, re := range tokenShapes {
		s = re.ReplaceAllString(s, Redacted)
	}
	s = skToken.ReplaceAllString(s, "${1}"+Redacted)
	s = authorizationValue.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = bearerValue.ReplaceAllString(s, "${1}"+Redacted)
	s = secretQuoted.ReplaceAllStringFunc(s, func(m string) string {
		sub := secretQuoted.FindStringSubmatch(m)
		value := sub[3]
		quote := value[:1]
		if len(value) > 1 && strings.HasSuffix(value, quote) {
			return sub[1] + sub[2] + quote + Redacted + quote
		}
		return sub[1] + sub[2] + quote + Redacted
	})
	s = secretPair.ReplaceAllStringFunc(s, func(m string) string {
		sub := secretPair.FindStringSubmatch(m)
		if strings.Contains(sub[2], ":") && strings.HasSuffix(strings.ToLower(sub[1]), "authorization") {
			// An Authorization header, already scrubbed above with its
			// scheme kept. Written with = it is a plain pair, and goes.
			return m
		}
		return sub[1] + sub[2] + Redacted
	})
	return base64Run.ReplaceAllStringFunc(s, func(m string) string {
		if mixedBase64(m) {
			return Redacted
		}
		return m
	})
}

// mixedBase64 reports whether s holds an upper-case letter, a lower-case
// letter and a digit.
func mixedBase64(s string) bool {
	var upper, lower, digit bool
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= '0' && c <= '9':
			digit = true
		}
	}
	return upper && lower && digit
}
