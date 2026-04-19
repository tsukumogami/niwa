package workspace

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// maxEnvExampleSize is the file-size limit for .env.example files.
// Files larger than this are treated as a whole-file failure (R22).
const maxEnvExampleSize = 512 * 1024 // 512 KB

// envKeyRe validates environment variable key names. Only ASCII letters,
// digits, and underscores are permitted. Any other character is treated as
// a per-line error and the line is skipped.
var envKeyRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// parseDotEnvExample reads a .env.example file using Node-style syntax.
//
// Precondition: the caller has already confirmed via os.Lstat that the path
// exists and is a regular file (not a symlink). Do not call this function with
// a path that has not been stat-checked; the size guard reads the file in full
// so there is no partial-read shortcut for the binary-content check.
//
// Returns:
//   - vars: parsed key-value map (nil on whole-file failure)
//   - warnings: per-line diagnostic strings in "file:line:problem" format;
//     no value text ever appears in warning strings
//   - err: non-nil only for whole-file failures (permission denied, binary
//     content, >512 KB); per-line parse errors accumulate in warnings and do
//     not set err
//
// Supported syntax:
//   - Blank lines and lines whose first non-space character is '#' are skipped
//   - "export KEY=VALUE" prefix: "export" is stripped, rest parsed normally
//   - Unquoted values: everything after '=' up to end of line
//   - Single-quoted values: treated as literals; no escape processing
//   - Double-quoted values: support \n, \t, \", \\; any other backslash
//     sequence is a per-line warning and the line is skipped
//   - CRLF line endings are normalised to LF before parsing
//   - Duplicate keys: last occurrence wins
func parseDotEnvExample(path string) (map[string]string, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if int64(len(data)) > maxEnvExampleSize {
		return nil, nil, fmt.Errorf("%s: file exceeds 512 KB limit", path)
	}

	// Reject binary content: if the file contains a NUL byte it is not a
	// text file and should not be parsed.
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, fmt.Errorf("%s: file contains binary content", path)
	}

	// Normalise CRLF to LF.
	text := strings.ReplaceAll(string(data), "\r\n", "\n")

	vars := make(map[string]string)
	var warnings []string

	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		lineNum := i + 1
		line := strings.TrimSpace(raw)

		// Skip blank lines and comment lines.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Strip leading "export " prefix (case-sensitive).
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}

		// Split on the first '='.
		key, rawVal, ok := strings.Cut(line, "=")
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s:%d:missing '=' separator", path, lineNum))
			continue
		}

		// Validate key name.
		if !envKeyRe.MatchString(key) {
			warnings = append(warnings, fmt.Sprintf("%s:%d:invalid key name (only [A-Za-z0-9_] allowed)", path, lineNum))
			continue
		}

		// Parse the value.
		value, warn := parseEnvExampleValue(rawVal)
		if warn != "" {
			warnings = append(warnings, fmt.Sprintf("%s:%d:%s", path, lineNum, warn))
			continue
		}

		vars[key] = value
	}

	return vars, warnings, nil
}

// parseEnvExampleValue parses the raw value portion (everything after '=') of
// a .env.example line according to Node-style quoting rules.
//
// Returns the parsed value and an empty string on success. Returns an empty
// string and a non-empty warning description (no value text) when the value
// contains an unrecognised escape sequence in a double-quoted string; the
// caller should skip the line in that case.
func parseEnvExampleValue(raw string) (value string, warnMsg string) {
	if len(raw) == 0 {
		return "", ""
	}

	switch raw[0] {
	case '\'':
		// Single-quoted literal: no escape processing.
		// Find the closing single quote.
		end := strings.IndexByte(raw[1:], '\'')
		if end < 0 {
			// Unclosed single quote: treat the rest as the literal value.
			return raw[1:], ""
		}
		return raw[1 : 1+end], ""

	case '"':
		// Double-quoted: process escape sequences.
		return parseDoubleQuoted(raw[1:])

	default:
		// Unquoted: strip inline comment (# preceded by whitespace) and
		// trim trailing whitespace.
		return parseUnquoted(raw), ""
	}
}

// parseDoubleQuoted processes the content inside a double-quoted value
// (the leading '"' has already been consumed). Supports \n, \t, \", \\.
// Any other backslash sequence returns a non-empty warning and an empty value.
func parseDoubleQuoted(inner string) (value string, warnMsg string) {
	// Find closing quote, respecting escapes.
	end := -1
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' {
			i++ // skip escaped character
			continue
		}
		if inner[i] == '"' {
			end = i
			break
		}
	}

	content := inner
	if end >= 0 {
		content = inner[:end]
	}

	var sb strings.Builder
	for i := 0; i < len(content); i++ {
		ch := content[i]
		if ch != '\\' {
			sb.WriteByte(ch)
			continue
		}
		// Escape sequence.
		i++
		if i >= len(content) {
			// Trailing backslash: unrecognised.
			return "", "unrecognised escape sequence in double-quoted value"
		}
		switch content[i] {
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case '"':
			sb.WriteByte('"')
		case '\\':
			sb.WriteByte('\\')
		default:
			return "", "unrecognised escape sequence in double-quoted value"
		}
	}

	return sb.String(), ""
}

// parseUnquoted returns the unquoted value, stripping trailing whitespace and
// any inline comment (a '#' character preceded by at least one space).
func parseUnquoted(raw string) string {
	// Strip inline comment: first occurrence of " #" (space then hash).
	if idx := strings.Index(raw, " #"); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.TrimRight(raw, " \t")
}
