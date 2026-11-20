// Package firstread holds a dispatched worker to a bounded first read of its
// brief.
//
// A brief that wants to be read in stages can only say so in its own text,
// and a worker picks its first command before it has read any of that text.
// So a rule like "read lines 1-3 first" arrives in the same call that breaks
// it, typically a checksum chained with a whole-file read. `niwa dispatch
// --brief <file> --first-read <N>` moves the rule out of the file: the
// dispatch records the brief and N in the instance it creates (Declaration),
// and a PreToolUse hook carried in the launch's settings document asks Decide
// about every Bash, Read and Grep call. Until the worker has read lines 1-N
// on their own, any other call that names the brief is refused, and the
// refusal names the command that is allowed. The first bounded read is
// recorded (MarkDone) and from then on the guard allows everything.
//
// Like the worktree guard, this steers an agent; it is not a security
// boundary. It reads simple commands only, and a brief reached through a
// variable, a glob or a nested shell isn't recognized as the brief.
package firstread

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tsukumogami/niwa/internal/worktreeguard"
)

// DeclarationFile and DoneFile live in the instance's .niwa directory.
const (
	DeclarationFile = "first-read.json"
	DoneFile        = "first-read.done"
	stateDir        = ".niwa"
	schemaVersion   = 1
)

// Declaration is what a dispatch records about its brief's first read.
type Declaration struct {
	Schema int `json:"schema"`
	// Brief is the brief's absolute, symlink-resolved path.
	Brief string `json:"brief"`
	// Lines is N: the worker reads lines 1-N before anything else.
	Lines int `json:"lines"`
}

// Write records d in instanceDir/.niwa.
func Write(instanceDir string, d Declaration) error {
	d.Schema = schemaVersion
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(instanceDir, stateDir, DeclarationFile), append(data, '\n'), 0o600)
}

// Load reads instanceDir's declaration. ok is false when the instance has
// none, or has one this niwa can't read; the guard then allows every call.
func Load(instanceDir string) (Declaration, bool) {
	data, err := os.ReadFile(filepath.Join(instanceDir, stateDir, DeclarationFile))
	if err != nil {
		return Declaration{}, false
	}
	var d Declaration
	if err := json.Unmarshal(data, &d); err != nil || d.Schema != schemaVersion || d.Brief == "" || d.Lines < 1 {
		return Declaration{}, false
	}
	return d, true
}

// Done reports whether the bounded first read has run in instanceDir.
func Done(instanceDir string) bool {
	_, err := os.Stat(filepath.Join(instanceDir, stateDir, DoneFile))
	return err == nil
}

// MarkDone records that the bounded first read ran.
func MarkDone(instanceDir string) error {
	f, err := os.OpenFile(filepath.Join(instanceDir, stateDir, DoneFile), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// CountLines counts content's lines, a final line without a newline included.
func CountLines(content []byte) int {
	n := strings.Count(string(content), "\n")
	if len(content) > 0 && content[len(content)-1] != '\n' {
		n++
	}
	return n
}

// Payload is the part of a PreToolUse payload the guard reads.
type Payload struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Cwd       string          `json:"cwd"`
}

// Decision is the guard's answer for one call. Satisfies is set when the
// call is the bounded first read, so the caller records it as done.
type Decision struct {
	Allow     bool
	Satisfies bool
	Reason    string
}

// PathResolver resolves symlinks in an absolute, cleaned path.
type PathResolver func(string) (string, error)

var allow = Decision{Allow: true}

// Decide judges one call made before the first read is done.
func Decide(p Payload, d Declaration, resolve PathResolver) Decision {
	switch p.ToolName {
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(p.ToolInput, &in); err != nil {
			return allow
		}
		return decideBash(in.Command, p.Cwd, d, resolve)
	case "Read":
		var in struct {
			FilePath string `json:"file_path"`
			Offset   *int   `json:"offset"`
			Limit    *int   `json:"limit"`
		}
		if err := json.Unmarshal(p.ToolInput, &in); err != nil || !isBrief(in.FilePath, p.Cwd, d, resolve) {
			return allow
		}
		start := 1
		if in.Offset != nil && *in.Offset > 1 {
			start = *in.Offset
		}
		if in.Limit != nil && *in.Limit > 0 && start+*in.Limit-1 <= d.Lines {
			return Decision{Allow: true, Satisfies: true}
		}
		return Decision{Reason: Refusal(d)}
	case "Grep":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(p.ToolInput, &in); err != nil || !isBrief(in.Path, p.Cwd, d, resolve) {
			return allow
		}
		return Decision{Reason: Refusal(d)}
	}
	return allow
}

// decideBash allows a command that doesn't name the brief, the bounded read
// on its own, and a checksum of the brief on its own. Any other command that
// names it is refused, a chain holding the bounded read included: a chained
// read is the case the guard exists for.
func decideBash(command, cwd string, d Declaration, resolve PathResolver) Decision {
	if !strings.Contains(command, filepath.Base(d.Brief)) {
		return allow
	}
	cmds, err := worktreeguard.SplitSimpleCommands(command)
	if err != nil || len(cmds) != 1 || !singleCommand(command) {
		return Decision{Reason: Refusal(d)}
	}
	words := cmds[0]
	last := words[len(words)-1]
	if !isBrief(last, cwd, d, resolve) {
		return Decision{Reason: Refusal(d)}
	}
	args := words[:len(words)-1]
	switch {
	case boundedSed(args, d.Lines), boundedHead(args, d.Lines):
		return Decision{Allow: true, Satisfies: true}
	case checksum(args):
		return allow
	}
	return Decision{Reason: Refusal(d)}
}

// singleCommand reports whether the line carries no separator, pipe,
// redirection or substitution outside quotes. SplitSimpleCommands already
// splits on separators; this also catches what it keeps inside a word.
func singleCommand(line string) bool {
	var quote rune
	for _, c := range line {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case strings.ContainsRune(";&|<>`$\n", c):
			return false
		}
	}
	return true
}

var sedRange = regexp.MustCompile(`^([0-9]+),([0-9]+)p$`)

// boundedSed matches `sed -n A,Bp` with 1 <= A <= B <= n.
func boundedSed(args []string, n int) bool {
	if len(args) != 3 || filepath.Base(args[0]) != "sed" || args[1] != "-n" {
		return false
	}
	m := sedRange.FindStringSubmatch(args[2])
	if m == nil {
		return false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a >= 1 && a <= b && b <= n
}

// boundedHead matches `head -n K`, `head -nK` and `head -K` with 1 <= K <= n.
func boundedHead(args []string, n int) bool {
	if len(args) < 2 || filepath.Base(args[0]) != "head" {
		return false
	}
	var count string
	switch {
	case len(args) == 3 && args[1] == "-n":
		count = args[2]
	case len(args) == 2 && strings.HasPrefix(args[1], "-n"):
		count = strings.TrimPrefix(args[1], "-n")
	case len(args) == 2 && strings.HasPrefix(args[1], "-"):
		count = strings.TrimPrefix(args[1], "-")
	default:
		return false
	}
	k, err := strconv.Atoi(count)
	return err == nil && k >= 1 && k <= n
}

// checksum matches `sha256sum` and `shasum -a 256`, which print a digest and
// none of the brief.
func checksum(args []string) bool {
	switch {
	case len(args) == 1 && filepath.Base(args[0]) == "sha256sum":
		return true
	case len(args) == 3 && filepath.Base(args[0]) == "shasum" && args[1] == "-a" && args[2] == "256":
		return true
	}
	return false
}

// isBrief reports whether p, joined with cwd when relative, is the brief.
func isBrief(p, cwd string, d Declaration, resolve PathResolver) bool {
	if p == "" {
		return false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	if p == d.Brief {
		return true
	}
	resolved, err := resolve(p)
	return err == nil && resolved == d.Brief
}

// Refusal is the text a refused call shows the worker: what the brief
// declares and the one command that satisfies it.
func Refusal(d Declaration) string {
	q := shellQuote(d.Brief)
	return fmt.Sprintf("niwa: this dispatch's brief declares a first read of lines 1-%d, and this call reads it some other way. "+
		"Run `sed -n 1,%dp %s` as a call of its own, with nothing chained to it; once it has run, the brief can be read freely. "+
		"A `sha256sum %s` on its own is allowed before it.", d.Lines, d.Lines, q, q)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
