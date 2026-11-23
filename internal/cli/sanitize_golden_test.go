package cli

import (
	"strings"
	"testing"
)

// sanitizeGoldenCases pins what sanitizeRemovalGateText and
// sanitizeOnReapText print for each input. Every expected string is written
// out literally, so a change to either sanitizer's output, however small,
// fails here.
var sanitizeGoldenCases = []struct {
	name   string
	in     string
	max    int
	gate   string
	onReap string
}{
	{
		name:   "plain ASCII",
		in:     "plain text",
		max:    200,
		gate:   "plain text",
		onReap: "plain text",
	},
	{
		name:   "empty",
		in:     "",
		max:    200,
		gate:   "",
		onReap: "",
	},
	{
		name:   "escape sequences leave their printable remainder",
		in:     "a\x1b[31mred\x1b[0m",
		max:    200,
		gate:   "a [31mred [0m",
		onReap: "a [31mred [0m",
	},
	{
		name:   "OSC sequence",
		in:     "x\x1b]0;title\x07y",
		max:    200,
		gate:   "x ]0;title y",
		onReap: "x ]0;title y",
	},
	{
		name:   "C0 controls",
		in:     "\x07bell\x00nul\r\n",
		max:    200,
		gate:   "bell nul",
		onReap: "bell nul",
	},
	{
		name:   "DEL",
		in:     "a\x7fb",
		max:    200,
		gate:   "a b",
		onReap: "a b",
	},
	{
		name:   "C1 controls",
		in:     "\u0085x\u009b",
		max:    200,
		gate:   "x",
		onReap: "x",
	},
	{
		name:   "C1 controls between words",
		in:     "one\u0080two\u009fthree",
		max:    200,
		gate:   "one two three",
		onReap: "one two three",
	},
	{
		name:   "every Bidi_Control rune",
		in:     "a؜b‎c‏d‪e‫f‬g‭h‮i⁦j⁧k⁨l⁩m",
		max:    200,
		gate:   "a b c d e f g h i j k l m",
		onReap: "a؜b‎c‏d‪e‫f‬g‭h‮i⁦j⁧k⁨l⁩m",
	},
	{
		name:   "Bidi_Control runes in a run collapse to one space in the gate",
		in:     "left‮⁦ right",
		max:    200,
		gate:   "left right",
		onReap: "left‮⁦ right",
	},
	{
		name:   "Bidi_Control runes at the ends are trimmed by the gate",
		in:     "‏word‎",
		max:    200,
		gate:   "word",
		onReap: "‏word‎",
	},
	{
		// The gate maps runes before the shared loop runs, and that map
		// turns each invalid byte into U+FFFD, so the gate prints the
		// replacement character where the on-reap sanitizer prints a space.
		name:   "invalid UTF-8",
		in:     "a\xffb\xc3",
		max:    200,
		gate:   "a�b�",
		onReap: "a b",
	},
	{
		name:   "a run of invalid bytes",
		in:     "a\xff\xfe\xfdb",
		max:    200,
		gate:   "a���b",
		onReap: "a b",
	},
	{
		name:   "invalid bytes and a cut",
		in:     "\xff\xffabcdefgh",
		max:    10,
		gate:   "��a...",
		onReap: "abcdefgh",
	},
	{
		name:   "runs of spaces, tabs and newlines collapse",
		in:     "a  \t\t b\n\n\nc\r\n\td",
		max:    200,
		gate:   "a b c d",
		onReap: "a b c d",
	},
	{
		name:   "no-break space and line separator count as space",
		in:     "a b c",
		max:    200,
		gate:   "a b c",
		onReap: "a b c",
	},
	{
		name:   "leading and trailing space trimmed",
		in:     "  \t padded \n ",
		max:    200,
		gate:   "padded",
		onReap: "padded",
	},
	{
		name:   "only space",
		in:     " \t\n\x00 ",
		max:    200,
		gate:   "",
		onReap: "",
	},
	{
		name:   "over-long ASCII cut with an ellipsis",
		in:     "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		max:    10,
		gate:   "xxxxxxx...",
		onReap: "xxxxxxx...",
	},
	{
		name:   "one byte over the maximum",
		in:     "abcdefghijk",
		max:    10,
		gate:   "abcdefg...",
		onReap: "abcdefg...",
	},
	{
		name:   "exactly the maximum is not cut",
		in:     "abcdefghij",
		max:    10,
		gate:   "abcdefghij",
		onReap: "abcdefghij",
	},
	{
		name:   "exactly the maximum after collapsing is not cut",
		in:     "  abcd \t\n efgh  ",
		max:    9,
		gate:   "abcd efgh",
		onReap: "abcd efgh",
	},
	{
		name:   "two-byte runes cut at a rune boundary",
		in:     strings.Repeat("é", 50),
		max:    41,
		gate:   "ééééééééééééééééééé...",
		onReap: "ééééééééééééééééééé...",
	},
	{
		name:   "two-byte runes cut backs up to a rune start",
		in:     strings.Repeat("é", 50),
		max:    42,
		gate:   "ééééééééééééééééééé...",
		onReap: "ééééééééééééééééééé...",
	},
	{
		name:   "three-byte runes cut backs up to a rune start",
		in:     strings.Repeat("日", 10),
		max:    10,
		gate:   "日日...",
		onReap: "日日...",
	},
	{
		name:   "controls removed before the cut",
		in:     "\x1b[1myyyyyyyyyyyyyyyyyyyy",
		max:    12,
		gate:   "[1myyyyyy...",
		onReap: "[1myyyyyy...",
	},
	{
		name:   "the gate's Bidi spaces count toward the cut",
		in:     "ab‮cdefghij",
		max:    10,
		gate:   "ab cdef...",
		onReap: "ab‮cd...",
	},
}

func TestSanitizeRemovalGateTextGolden(t *testing.T) {
	for _, tc := range sanitizeGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeRemovalGateText(tc.in, tc.max); got != tc.gate {
				t.Fatalf("sanitizeRemovalGateText(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.gate)
			}
		})
	}
}

func TestSanitizeOnReapTextGolden(t *testing.T) {
	for _, tc := range sanitizeGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeOnReapText(tc.in, tc.max); got != tc.onReap {
				t.Fatalf("sanitizeOnReapText(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.onReap)
			}
		})
	}
}
