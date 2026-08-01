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
func truncateForDisplay(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	const ellipsis = "..."
	if width <= len(ellipsis) {
		return string([]rune(s)[:width])
	}
	return string([]rune(s)[:width-len(ellipsis)]) + ellipsis
}
