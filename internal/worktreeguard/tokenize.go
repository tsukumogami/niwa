package worktreeguard

import (
	"errors"
	"path"
	"strings"
)

// errUnparseable means the command couldn't be split into words, for example
// because a quote is never closed. The guard lets such a command through.
var errUnparseable = errors.New("command could not be tokenized")

// splitSimpleCommands splits a shell command line into simple commands, each
// a list of words. It handles what an agent's Bash calls commonly contain:
// single and double quotes, backslash escapes, the separators ; & && | || and
// newlines, and heredoc bodies, which it skips. It does not interpret
// subshells, command substitution, variables, aliases, or nested shells: a
// string handed to `bash -c` is one word to it, which is the documented limit
// of the guard.
func splitSimpleCommands(line string) ([][]string, error) {
	var (
		commands     [][]string
		words        []string
		word         strings.Builder
		inWord       bool
		heredocDelim []string
	)
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			commands = append(commands, words)
		}
		words = nil
	}

	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\':
			if i+1 < len(runes) {
				i++
				if runes[i] != '\n' {
					word.WriteRune(runes[i])
					inWord = true
				}
			}
		case c == '\'':
			end := indexRune(runes, i+1, '\'')
			if end < 0 {
				return nil, errUnparseable
			}
			word.WriteString(string(runes[i+1 : end]))
			inWord = true
			i = end
		case c == '"':
			j := i + 1
			for ; j < len(runes) && runes[j] != '"'; j++ {
				if runes[j] == '\\' && j+1 < len(runes) {
					j++
				}
				word.WriteRune(runes[j])
			}
			if j >= len(runes) {
				return nil, errUnparseable
			}
			inWord = true
			i = j
		case c == '<' && i+1 < len(runes) && runes[i+1] == '<':
			// A heredoc: read its delimiter now and skip its body at the end
			// of the line. `<<<` is a here-string, not a heredoc.
			if i+2 < len(runes) && runes[i+2] == '<' {
				i += 2
				continue
			}
			endWord()
			j := i + 2
			if j < len(runes) && runes[j] == '-' {
				j++
			}
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			start := j
			for j < len(runes) && !isDelimiterEnd(runes[j]) {
				j++
			}
			delim := strings.Trim(string(runes[start:j]), `'"`)
			if delim == "" {
				return nil, errUnparseable
			}
			heredocDelim = append(heredocDelim, delim)
			i = j - 1
		case c == '\n':
			endCommand()
			for _, delim := range heredocDelim {
				end, ok := skipHeredoc(runes, i+1, delim)
				if !ok {
					return nil, errUnparseable
				}
				i = end
			}
			heredocDelim = nil
		case c == ';' || c == '&' || c == '|':
			endCommand()
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	if len(heredocDelim) > 0 {
		return nil, errUnparseable
	}
	endCommand()
	return commands, nil
}

func indexRune(runes []rune, from int, r rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == r {
			return i
		}
	}
	return -1
}

func isDelimiterEnd(r rune) bool {
	switch r {
	case ' ', '\t', '\n', ';', '&', '|', '<', '>':
		return true
	}
	return false
}

// skipHeredoc returns the index of the newline that ends the heredoc body
// starting at from, which closes on a line equal to delim (leading tabs
// allowed, for <<-).
func skipHeredoc(runes []rune, from int, delim string) (int, bool) {
	lineStart := from
	for i := from; i <= len(runes); i++ {
		if i == len(runes) || runes[i] == '\n' {
			if strings.TrimLeft(string(runes[lineStart:i]), "\t") == delim {
				if i == len(runes) {
					return i - 1, true
				}
				return i, true
			}
			lineStart = i + 1
		}
	}
	return 0, false
}

// isWorktreeAdd reports whether a simple command's words run
// `git [global options] worktree add`. Leading VAR=value assignments and an
// `env` word are skipped, and the command word matches by basename, so
// `/usr/bin/git` counts. -C and -c take an argument; any other option before
// `worktree` is skipped as a global option.
func isWorktreeAdd(words []string) bool {
	i := 0
	for i < len(words) && isAssignment(words[i]) {
		i++
	}
	if i < len(words) && path.Base(words[i]) == "env" {
		i++
		for i < len(words) && (isAssignment(words[i]) || strings.HasPrefix(words[i], "-")) {
			i++
		}
	}
	if i >= len(words) || path.Base(words[i]) != "git" {
		return false
	}
	i++
	for i < len(words) && strings.HasPrefix(words[i], "-") {
		if words[i] == "-C" || words[i] == "-c" {
			i++
		}
		i++
	}
	return i+1 < len(words) && words[i] == "worktree" && words[i+1] == "add"
}

// isAssignment reports whether a word is a VAR=value prefix assignment.
func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range w[:eq] {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// commandAddsWorktree reports whether any simple command in line runs
// `git worktree add`. An unparseable line reports false: the guard lets it
// through rather than blocking a command it can't read.
func commandAddsWorktree(line string) bool {
	commands, err := splitSimpleCommands(line)
	if err != nil {
		return false
	}
	for _, words := range commands {
		if isWorktreeAdd(words) {
			return true
		}
	}
	return false
}
