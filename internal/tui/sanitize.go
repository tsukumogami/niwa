package tui

import "regexp"

// ansiPattern matches ANSI/VT100 escape sequences that should be
// stripped from strings before display.
var ansiPattern = regexp.MustCompile(
	`\x1b\[[\x30-\x3F]*[\x20-\x2F]*[A-Za-z]` +
		`|\x1b\][^\x07]*?(?:\x07|\x1b\\)` +
		`|\x1b`,
)

// SanitizeDisplayString strips all ANSI/VT100 escape sequences from s.
// Use this before rendering any externally-sourced string to the
// terminal — prevents a malicious payload from repositioning the
// cursor or overwriting the picker frame.
func SanitizeDisplayString(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}
