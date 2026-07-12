package onboard

import (
	"errors"
	"strings"
)

// Punycode (RFC 3492) parameters, as used by IDNA for ASCII-Compatible
// Encoding of a domain label.
const (
	punycodeBase        = 36
	punycodeTMin        = 1
	punycodeTMax        = 26
	punycodeSkew        = 38
	punycodeDamp        = 700
	punycodeInitialBias = 72
	punycodeInitialN    = 128
	punycodeDelimiter   = '-'
)

var errPunycodeOverflow = errors.New("onboard: punycode encoding overflow")

// punycodeEncode implements the Punycode encoding algorithm (RFC 3492)
// used to render a Unicode domain label in ASCII form for display, so
// a homoglyph (a Cyrillic "o" standing in for a Latin "o") shows up as
// the unambiguous "xn--..." form instead of passing as legitimate.
//
// This is a from-scratch implementation rather than a dependency on
// golang.org/x/net/idna: the design explicitly rules out new
// third-party dependencies for this feature (stdlib plus the
// already-vetted golang.org/x/term only), and Punycode's core
// Bootstring algorithm needs no Unicode tables beyond rune comparison,
// so it's a reasonable from-scratch surface to own.
func punycodeEncode(input []rune) (string, error) {
	var output strings.Builder

	n := punycodeInitialN
	delta := 0
	bias := punycodeInitialBias

	basicCount := 0
	for _, r := range input {
		if r < 0x80 {
			output.WriteRune(r)
			basicCount++
		}
	}
	handled := basicCount
	if basicCount > 0 {
		output.WriteRune(punycodeDelimiter)
	}

	total := len(input)
	for handled < total {
		m := -1
		for _, r := range input {
			if int(r) >= n && (m == -1 || int(r) < m) {
				m = int(r)
			}
		}
		if m == -1 {
			return "", errPunycodeOverflow
		}

		delta += (m - n) * (handled + 1)
		n = m

		for _, r := range input {
			switch {
			case int(r) < n:
				delta++
			case int(r) == n:
				q := delta
				for k := punycodeBase; ; k += punycodeBase {
					t := k - bias
					switch {
					case t < punycodeTMin:
						t = punycodeTMin
					case t > punycodeTMax:
						t = punycodeTMax
					}
					if q < t {
						break
					}
					output.WriteByte(punycodeDigit(t + (q-t)%(punycodeBase-t)))
					q = (q - t) / (punycodeBase - t)
				}
				output.WriteByte(punycodeDigit(q))
				bias = punycodeAdapt(delta, handled+1, handled == basicCount)
				delta = 0
				handled++
			}
		}
		delta++
		n++
	}

	return output.String(), nil
}

// punycodeDigit maps a Bootstring digit value (0-35) to its ASCII
// character: a-z for 0-25, 0-9 for 26-35.
func punycodeDigit(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

// punycodeAdapt is the Bootstring bias adaptation function, verbatim
// per RFC 3492 section 6.1.
func punycodeAdapt(delta, numPoints int, firstTime bool) int {
	if firstTime {
		delta /= punycodeDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints

	k := 0
	threshold := ((punycodeBase - punycodeTMin) * punycodeTMax) / 2
	for delta > threshold {
		delta /= punycodeBase - punycodeTMin
		k += punycodeBase
	}
	return k + ((punycodeBase-punycodeTMin+1)*delta)/(delta+punycodeSkew)
}
