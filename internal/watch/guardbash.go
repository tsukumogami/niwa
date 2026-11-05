package watch

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

// guardBashInput is the subset of the Claude Code PreToolUse hook payload the
// session-command deny reads: the Bash tool's command.
type guardBashInput struct {
	ToolInput struct {
		Command *string `json:"command"`
	} `json:"tool_input"`
}

// sessionCommandVerbs maps each program the session-command deny watches for to the
// arguments that make it a session command. An argument matches when it equals a
// key, or starts with one that ends in "=" (claude --resume=<id>).
var sessionCommandVerbs = map[string][]string{
	"niwa":   {"retask", "resume", "dispatch"},
	"claude": {"--resume", "--resume=", "-r", "attach", "stop", "rm"},
}

// GuardBashDecision is the session-command deny for a review session's Bash tool. It
// reads a PreToolUse hook payload from stdin and returns the exit code the hook
// contract uses: 0 to let the command run, 2 to block it, with
// sessionCommandDenyMessage on stderr. It blocks a command that runs niwa retask,
// niwa resume, niwa dispatch, claude --resume (or -r), claude attach, claude stop or
// claude rm. Unreadable or unparseable input, or a payload with no command, blocks
// too. It always reads stdin to the end (see readHookInput).
//
// It is a string match, and a script, a variable or quoting tricks get around it.
// It catches the plain cases and is accident prevention when watch_sandbox = off;
// it is not a boundary. In sandbox mode the OS sandbox's denial of all network, unix
// sockets included, is what keeps review Bash off the Claude Code daemon those
// commands need.
func GuardBashDecision(stdin io.Reader, stderr io.Writer) int {
	data, err := readHookInput(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "niwa watch guard-bash: reading hook input: %v\n", err)
		return 2
	}
	var in guardBashInput
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintf(stderr, "niwa watch guard-bash: parsing hook input: %v\n", err)
		return 2
	}
	if in.ToolInput.Command == nil {
		fmt.Fprintln(stderr, "niwa watch guard-bash: no command in tool input; denying")
		return 2
	}
	if namesSessionCommand(*in.ToolInput.Command) {
		fmt.Fprintln(stderr, sessionCommandDenyMessage)
		return 2
	}
	return 0
}

// namesSessionCommand reports whether a shell command line runs one of the session
// commands in sessionCommandVerbs. It splits the line into simple commands at the
// shell's separators and grouping characters (; & | newline ( ) ` { } < >), drops
// quote and backslash characters, and splits each piece into words on whitespace. A
// piece names a session command when a word whose last path element is a watched
// program ("claude", "/usr/local/bin/claude", "~/.local/bin/niwa") is followed later
// in the same piece by one of that program's verbs. Matching the program anywhere in
// the piece, not only first, covers environment prefixes (FOO=1 claude stop) and
// wrappers (env, nohup, sudo, bash -c "...").
func namesSessionCommand(line string) bool {
	pieces := strings.FieldsFunc(line, func(r rune) bool {
		switch r {
		case ';', '&', '|', '\n', '\r', '(', ')', '`', '{', '}', '<', '>':
			return true
		}
		return false
	})
	for _, piece := range pieces {
		piece = strings.NewReplacer(`"`, "", `'`, "", `\`, "").Replace(piece)
		words := strings.Fields(piece)
		for i, w := range words {
			verbs, ok := sessionCommandVerbs[path.Base(w)]
			if !ok {
				continue
			}
			for _, arg := range words[i+1:] {
				if isSessionVerb(arg, verbs) {
					return true
				}
			}
		}
	}
	return false
}

// isSessionVerb reports whether arg is one of verbs, or starts with a verb that
// ends in "=".
func isSessionVerb(arg string, verbs []string) bool {
	for _, v := range verbs {
		if arg == v || (strings.HasSuffix(v, "=") && strings.HasPrefix(arg, v)) {
			return true
		}
	}
	return false
}
