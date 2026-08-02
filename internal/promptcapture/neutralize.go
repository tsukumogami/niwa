package promptcapture

import (
	"strings"
	"unicode/utf8"
)

// neutralize renders b so it cannot act on the terminal.
//
// The rule, not a summary of it, is the property to hold: no byte that can
// introduce an escape sequence survives. Tab passes through because it is
// ordinary layout. Every other C0 control and DEL becomes caret notation; C1
// controls and bytes that are not valid UTF-8 become a hex escape.
//
// Because no escape-introducing byte survives, the output cannot carry an
// operating-system command (including a clipboard write), a device-control or
// application-program string, a mode set, or a cursor movement. Stating the
// property as "the cursor never moves" would be strictly weaker and would admit
// an implementation that still leaked a clipboard write or a title change.
//
// The rule also protects the payload: a terminal only answers sequences it
// renders, so an embedded device query cannot provoke a reply that would arrive
// back on standard input and land inside the capture.
func neutralize(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == '\t':
			sb.WriteByte(c)
			i++
		case c < 0x20:
			sb.WriteByte('^')
			sb.WriteByte(c + '@')
			i++
		case c == 0x7f:
			sb.WriteString("^?")
			i++
		case c < 0x80:
			sb.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && size <= 1 {
				sb.WriteString(hexEscape(c))
				i++
				continue
			}
			// A decoded rune in the C1 range is still a control.
			if r >= 0x80 && r <= 0x9f {
				sb.WriteString(hexEscape(c))
				i += size
				continue
			}
			sb.Write(b[i : i+size])
			i += size
		}
	}
	return sb.String()
}

const hexDigits = "0123456789abcdef"

func hexEscape(c byte) string {
	return `\x` + string(hexDigits[c>>4]) + string(hexDigits[c&0x0f])
}

// truncateForDisplay bounds a single rendered line to width columns, counting
// runes rather than bytes so a multi-byte character is never split.
//
// It walks the string rather than converting it to a rune slice. The
// conversion is the obvious spelling and allocates four bytes per rune over the
// WHOLE input before discarding all but width of them -- 2.4 MB to render 100
// columns of a 614 KB line. Walking costs one pass bounded by width.
func truncateForDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	const ellipsis = "..."
	keep := width
	if width > len(ellipsis) {
		keep = width - len(ellipsis)
	}

	// Find the byte offset of rune `keep`, and learn whether more follow,
	// without scanning past what we need.
	cut, seen := len(s), 0
	for i := range s {
		if seen == keep {
			cut = i
		}
		seen++
		if seen > width {
			return s[:cut] + ellipsisIf(width > len(ellipsis))
		}
	}
	return s
}

func ellipsisIf(yes bool) string {
	if yes {
		return "..."
	}
	return ""
}

// truncateBytesForNeutralize bounds how many raw bytes are handed to
// neutralize when the result will be cut to width columns anyway. Four bytes
// per column covers the worst case: a byte that renders as a four-character
// hex escape. Cutting on a rune boundary keeps a multi-byte character whole so
// neutralize does not see it as invalid.
func truncateBytesForNeutralize(b []byte, width int) []byte {
	max := width * 4
	if len(b) <= max {
		return b
	}
	for max > 0 && !utf8.RuneStart(b[max]) {
		max--
	}
	return b[:max]
}
