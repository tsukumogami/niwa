// Package claudetrust is the only code in niwa that edits Claude Code's user
// config. It marks a niwa instance root as a trusted project, so Claude Code
// honors the instance's project settings and approved MCP servers, and it
// removes exactly what a recorded grant says niwa added.
//
// Claude Code reads a project's .claude/settings.json only when its user config
// holds projects[<key>].hasTrustDialogAccepted = true for that exact key. The
// file also holds login-adjacent state and every project's history, and Claude
// Code rewrites it constantly, so the write discipline is part of the contract:
//
//   - One flag, one key. Only projects[<key>].hasTrustDialogAccepted is ever
//     set. Every other value in the file is written back as the exact bytes it
//     was read as. A rewrite re-indents the file, sorts each object's keys,
//     and keeps one value for a key repeated within one object (the last, as
//     a JSON parser reading the file would); no value is changed.
//     hasTrustDialogHooksAccepted is never written.
//   - Claude Code's key and file. The key is the instance root with every
//     symlink resolved, the key Claude Code computes for a session started
//     there. The file is $CLAUDE_CONFIG_DIR/.claude.json, else
//     $HOME/.claude.json; a symlinked file is written through, never replaced.
//     When the legacy <config home>/.config.json exists, Claude Code reads it
//     instead, so niwa writes nothing and warns.
//   - Claude Code's lock. The read-modify-write runs under the mkdir lock
//     Claude Code itself takes, <config>.lock, so neither side loses the
//     other's write.
//   - Never write what did not parse, and never fail a command over it. A file
//     that is unreadable, not JSON, or not the expected shape is left
//     byte-untouched and reported as a warning.
//   - Atomic replacement. New contents go to a 0600 temp file beside the
//     target, are fsynced, and are renamed over it with the original mode, so
//     a reader sees the old file or the new one. A file already in the desired
//     state is not rewritten at all.
//   - Record-bounded removal. Claude Code writes an identically-shaped entry
//     when a person answers its trust prompt, so ownership can't be read from
//     the entry. Removal is driven by a Grant, validated against the instance
//     it is withdrawn for, and nothing a Grant doesn't name is ever removed.
package claudetrust

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// configBasename is the name of Claude Code's user config file, both under
// $CLAUDE_CONFIG_DIR and under $HOME.
const configBasename = ".claude.json"

// legacyConfigBasename is the older config file name Claude Code still reads,
// in preference to .claude.json, when it exists in the config home.
const legacyConfigBasename = ".config.json"

// trustFlag is the one entry key niwa writes or removes.
const trustFlag = "hasTrustDialogAccepted"

// Mode says what a grant added, and so what destroying the instance removes.
type Mode string

const (
	// ModeCreated means niwa created the whole projects entry. Destroying the
	// instance removes the entry.
	ModeCreated Mode = "created"
	// ModeFlagOnly means the entry already existed and niwa only set the flag.
	// Destroying the instance removes only the flag.
	ModeFlagOnly Mode = "flag-only"
)

// Source says which niwa feature recorded a grant. An apply with the option
// off retracts only a workspace grant, so it never pulls trust out from under
// a running review.
type Source string

const (
	// SourceWorkspace is a grant written by the create/apply pipeline.
	SourceWorkspace Source = "workspace"
	// SourceReview is a grant written for a review instance.
	SourceReview Source = "review"
)

// Grant is the ownership record of one trust write, persisted in instance
// state. Removal uses these recorded values, never values recomputed at
// removal time.
type Grant struct {
	// ConfigFile is the unresolved config path niwa locked and wrote through,
	// e.g. $HOME/.claude.json even when that path is a symlink. The lock name
	// is derived from it, so it must be the name Claude Code locks.
	ConfigFile string `json:"config_file"`
	// Key is the symlink-resolved instance root used as the projects key.
	Key    string `json:"key"`
	Mode   Mode   `json:"mode"`
	Source Source `json:"source"`
}

// Env is the environment the config file is resolved from. Callers pass it in
// explicitly so tests never reach the developer's own file.
type Env struct {
	// Home is the developer's home directory. Empty disables every write.
	Home string
	// ClaudeConfigDir is the value of CLAUDE_CONFIG_DIR at the call. Empty
	// means unset; a relative value is refused.
	ClaudeConfigDir string
}

// EnvFor returns the Env for home with ClaudeConfigDir read from the process's
// CLAUDE_CONFIG_DIR, the value a Claude Code session started from this
// environment would use.
func EnvFor(home string) Env {
	return Env{Home: home, ClaudeConfigDir: os.Getenv("CLAUDE_CONFIG_DIR")}
}

// Outcome is the result of Ensure.
type Outcome struct {
	// Grant is the record to persist: nil when nothing is recorded (the entry
	// was already trusted by someone else, or the write was skipped with no
	// usable prior record), otherwise the new grant or the prior one.
	Grant *Grant
	// Trusted reports that, after the call, the entry at the instance's key
	// in the config file env names holds hasTrustDialogAccepted = true:
	// written by this call, already recorded as niwa's, or already trusted by
	// someone else. It is false on every skip, an empty Home included.
	Trusted bool
	// Changed reports that the current config file was rewritten by this
	// call. A retraction from a previously recorded file doesn't count.
	Changed bool
	// Created reports that the rewrite added the whole entry, not only the
	// flag on an entry that was already there. Undo reads it to take back
	// only what this call wrote. Grant.Mode can't say this, since a re-set
	// flag keeps the mode of the record it re-establishes.
	Created bool
	// Warning names the file and the reason when something was skipped or
	// left behind, and never means the command failed. It can sit beside a
	// write (a stale record dropped), and a skip that isn't a problem, an
	// empty Home, carries none. Read Trusted, not Warning, to learn whether
	// the entry is trusted.
	Warning string
}

// Undo takes back this call's write, for a caller that couldn't persist the
// grant: the whole entry when the call created it, otherwise only the flag it
// set. It does nothing unless Changed. The warning is Withdraw's, empty when
// the write was taken back.
func (o Outcome) Undo() (warning string) {
	if !o.Changed || o.Grant == nil {
		return ""
	}
	how := Retract
	if o.Created {
		how = Destroy
	}
	return Withdraw(o.Grant, o.Grant.Key, how)
}

// WithdrawMode selects what Withdraw removes. The zero value is invalid and
// refused, so an unset mode can never remove anything.
type WithdrawMode int

const (
	// Destroy removes what the grant added: the whole entry for a created
	// grant, only the flag for a flag-only grant. Used when the instance goes.
	Destroy WithdrawMode = iota + 1
	// Retract removes only the flag, whatever the grant's mode, since the
	// instance and Claude Code's data for it still exist. Used when an apply
	// turns trust off or moves it to another config file.
	Retract
)

// ConfigFile returns the unresolved path of the Claude config file for env:
// $CLAUDE_CONFIG_DIR/.claude.json when ClaudeConfigDir is non-empty, else
// $HOME/.claude.json. A relative ClaudeConfigDir is an error rather than a
// path resolved against niwa's working directory, which Claude Code would
// resolve differently.
func ConfigFile(env Env) (string, error) {
	dir, err := configDir(env)
	if err != nil {
		return "", err
	}
	if dir != "" {
		return filepath.Join(dir, configBasename), nil
	}
	if env.Home == "" {
		return "", fmt.Errorf("no home directory to find the Claude config file under")
	}
	return filepath.Join(filepath.Clean(env.Home), configBasename), nil
}

// ConfigHome returns the directory Claude Code keeps its own data in for env:
// $CLAUDE_CONFIG_DIR when ClaudeConfigDir is non-empty, else $HOME/.claude,
// cleaned. It applies ConfigFile's CLAUDE_CONFIG_DIR rule, so a caller that
// guards this directory and the writer agree on where it is. With
// CLAUDE_CONFIG_DIR unset the config file sits beside this directory, at
// $HOME/.claude.json, not inside it.
func ConfigHome(env Env) (string, error) {
	dir, err := configDir(env)
	if err != nil || dir != "" {
		return dir, err
	}
	if env.Home == "" {
		return "", fmt.Errorf("no home directory to find the Claude config home under")
	}
	return filepath.Join(filepath.Clean(env.Home), ".claude"), nil
}

// legacyConfigFile returns <config home>/.config.json for env, the legacy
// config file Claude Code reads instead of ConfigFile's when it exists.
func legacyConfigFile(env Env) (string, error) {
	home, err := ConfigHome(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, legacyConfigBasename), nil
}

// configDir returns env's CLAUDE_CONFIG_DIR cleaned, or "" when it is unset.
// A relative value is an error rather than a path resolved against niwa's
// working directory, which Claude Code would resolve differently.
func configDir(env Env) (string, error) {
	if env.ClaudeConfigDir == "" {
		return "", nil
	}
	if !filepath.IsAbs(env.ClaudeConfigDir) {
		return "", fmt.Errorf("CLAUDE_CONFIG_DIR %q is not an absolute path", env.ClaudeConfigDir)
	}
	return filepath.Clean(env.ClaudeConfigDir), nil
}

// Key returns the projects key for instanceRoot: its absolute path with every
// symlink resolved, the root itself and every parent, which is the key Claude
// Code computes for a session started there. The root must exist.
func Key(instanceRoot string) (string, error) {
	abs, err := filepath.Abs(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("resolving the instance root %s: %w", instanceRoot, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks in the instance root %s: %w", abs, err)
	}
	return filepath.Clean(resolved), nil
}

// Validate checks a grant before anything acts on it. Instance state is a file
// anyone controlling the instance can edit, so a crafted record must not turn
// removal into a write to an arbitrary file or the removal of another
// project's trust. It checks, in order: known mode and source; an absolute,
// clean config_file named .claude.json; a key equal to expectedKey; and that
// config_file resolves to a regular file. A config file that doesn't exist
// fails with an error wrapping fs.ErrNotExist, and only after every other
// check passed, so a caller can treat it as "nothing left to remove".
func (g *Grant) Validate(expectedKey string) error {
	if err := g.ValidateShape(); err != nil {
		return err
	}
	if g.Key == "" || g.Key != expectedKey {
		return fmt.Errorf("key %q is not this instance's key %q", g.Key, expectedKey)
	}
	info, err := os.Stat(g.ConfigFile)
	if err != nil {
		return fmt.Errorf("config file %s: %w", g.ConfigFile, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config file %s is not a regular file", g.ConfigFile)
	}
	return nil
}

// ValidateShape is the part of Validate that needs neither the instance nor
// the filesystem: a known mode and source, and an absolute, clean config_file
// named .claude.json. A record that fails it is malformed and is never acted
// on, only dropped.
func (g *Grant) ValidateShape() error {
	if g == nil {
		return fmt.Errorf("no grant")
	}
	switch g.Mode {
	case ModeCreated, ModeFlagOnly:
	default:
		return fmt.Errorf("unknown mode %q", g.Mode)
	}
	switch g.Source {
	case SourceWorkspace, SourceReview:
	default:
		return fmt.Errorf("unknown source %q", g.Source)
	}
	return validateConfigPath(g.ConfigFile)
}

// MalformedWarning is the warning for dropping g, a record that fails
// ValidateShape, from the instance whose key is key.
func MalformedWarning(g *Grant, key string) string {
	return fmt.Sprintf("ignoring a malformed Claude trust record for %s (config file %q, mode %q, source %q)",
		key, g.ConfigFile, g.Mode, g.Source)
}

// MovedWarning is the warning for dropping g, a record whose key is not key,
// the path of the instance it was found in: the instance moved, so niwa can't
// prove the old entry is this instance's and leaves it in place.
func MovedWarning(g *Grant, key string) string {
	return fmt.Sprintf(
		"the recorded Claude trust entry %q in %s is for a different path than this instance (%s); leaving it in place, remove it by hand if it is stale",
		g.Key, g.ConfigFile, key)
}

// validateConfigPath is the purely lexical part of ValidateShape.
func validateConfigPath(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("config file %q is not an absolute path", p)
	}
	if filepath.Clean(p) != p {
		return fmt.Errorf("config file %q is not a clean path", p)
	}
	if filepath.Base(p) != configBasename {
		return fmt.Errorf("config file %q is not named %s", p, configBasename)
	}
	return nil
}

// Ensure marks instanceRoot trusted in the config file env names and returns
// the record to persist. prior is the record already in instance state, if
// any; src is the feature asking.
//
// The existing entry decides what happens:
//
//	no entry                         create {hasTrustDialogAccepted: true}, record created
//	flag absent or false             set the flag, record flag-only
//	flag true, prior names this key  nothing, return prior
//	flag true, otherwise             nothing, record nothing
//
// When prior already names this key and file, the instance's first grant
// keeps its place: a flag someone reset is set again under prior's mode and
// source, and an entry someone removed is created again under prior's source.
// A later caller with another source never re-labels the record.
//
// A prior record for a different config file but the same key is retracted
// from its file first, unless both names reach the same physical file, in
// which case the record is carried over to the current name as it is. A prior record for a different key (the instance
// moved), or one with an unknown mode or source or a malformed path, is
// dropped with a warning and its entry left alone, since niwa can't prove that
// entry is this instance's; the outcome then carries no record unless this
// call writes a new one. Any skip (Home empty, a relative CLAUDE_CONFIG_DIR, a
// legacy <config home>/.config.json that Claude Code would read instead, a
// lock timeout, a file that can't be parsed) writes nothing, reports Trusted
// false and returns the prior record it still holds, so a valid record is
// never lost because one write couldn't happen.
func Ensure(env Env, instanceRoot string, prior *Grant, src Source) Outcome {
	if env.Home == "" {
		return Outcome{Grant: prior}
	}
	if src != SourceWorkspace && src != SourceReview {
		return Outcome{Grant: prior, Warning: fmt.Sprintf("Claude trust for %s not written: unknown source %q", instanceRoot, src)}
	}
	file, err := ConfigFile(env)
	if err != nil {
		return Outcome{Grant: prior, Warning: fmt.Sprintf("Claude trust for %s not written: %v", instanceRoot, err)}
	}
	// Claude Code reads the legacy file instead whenever it exists (any stat
	// that succeeds, as its own existence check), so a write to file would
	// trust nothing Claude Code looks at.
	if legacy, err := legacyConfigFile(env); err == nil {
		if _, err := os.Stat(legacy); err == nil {
			return Outcome{Grant: prior, Warning: fmt.Sprintf(
				"Claude trust for %s not written to %s: Claude Code reads %s instead while that file exists",
				instanceRoot, file, legacy)}
		}
	}
	key, err := Key(instanceRoot)
	if err != nil {
		return Outcome{Grant: prior, Warning: fmt.Sprintf("Claude trust not written to %s: %v", file, err)}
	}

	var warnings []string
	if prior != nil {
		switch {
		case prior.Key != key:
			warnings = append(warnings, MovedWarning(prior, key))
			prior = nil
		case prior.ValidateShape() != nil:
			warnings = append(warnings, MalformedWarning(prior, key))
			prior = nil
		case prior.ConfigFile != file && sameFile(prior.ConfigFile, file):
			// Two names for one file (CLAUDE_CONFIG_DIR is a symlink to
			// HOME, say): nothing moved, so the record keeps its mode and
			// is carried over to the name in use now.
			moved := *prior
			moved.ConfigFile = file
			prior = &moved
		case prior.ConfigFile != file:
			if w := Withdraw(prior, key, Retract); w != "" {
				// A skipped withdrawal keeps the record so the next apply
				// retries it; the new file waits until then.
				return Outcome{Grant: prior, Warning: joinWarnings(append(warnings, w))}
			}
			prior = nil
		}
	}

	out, err := ensureEntry(file, key, prior, src)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("Claude trust for %s not written; left %s unchanged: %v", key, file, err))
		return Outcome{Grant: prior, Warning: joinWarnings(warnings)}
	}
	out.Warning = joinWarnings(warnings)
	return out
}

// ensureEntry is Ensure's locked read-modify-write against one file. Its
// outcome carries no warning; on error nothing was written.
func ensureEntry(file, key string, prior *Grant, src Source) (Outcome, error) {
	// The lock lives beside the config file, so its directory must exist
	// before the lock can be taken. A missing CLAUDE_CONFIG_DIR is created
	// private, as Claude Code would.
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return Outcome{}, fmt.Errorf("creating the config directory: %w", err)
	}
	release, err := acquireLock(file+lockSuffix, true)
	if err != nil {
		return Outcome{}, err
	}
	defer release()

	doc, err := loadConfig(file)
	if err != nil {
		return Outcome{}, err
	}
	projects, entry, err := doc.entry(key)
	if err != nil {
		return Outcome{}, err
	}

	// recorded is prior when it already names this entry: the instance's
	// first grant, which keeps its source, and its mode while the entry
	// it describes is still there.
	var recorded *Grant
	if prior != nil && prior.Key == key && prior.ConfigFile == file {
		recorded = prior
	}

	grant := &Grant{ConfigFile: file, Key: key, Source: src}
	if recorded != nil {
		grant.Source = recorded.Source
	}
	created := entry == nil
	switch {
	case created:
		grant.Mode = ModeCreated
		entry = rawObject{}
	default:
		trusted, err := flagValue(entry)
		if err != nil {
			return Outcome{}, err
		}
		if trusted {
			return Outcome{Grant: recorded, Trusted: true}, nil
		}
		grant.Mode = ModeFlagOnly
		if recorded != nil {
			grant.Mode = recorded.Mode
		}
	}

	entry[trustFlag] = rawTrue
	if err := doc.setEntry(projects, key, entry); err != nil {
		return Outcome{}, err
	}
	if err := doc.write(); err != nil {
		return Outcome{}, err
	}
	return Outcome{Grant: grant, Trusted: true, Changed: true, Created: created}, nil
}

// Withdraw removes what g names from g's recorded config file: the whole
// entry for Destroy on a created grant, otherwise only the flag. expectedKey
// is the canonical key of the instance being destroyed or applied, computed
// while its directory still exists; a grant naming any other key is refused.
//
// It returns a warning, naming the file and key, when it refused or skipped;
// nothing is written then. Withdrawing an entry or flag that is already gone,
// or from a config file that no longer exists, is a silent no-op. Withdraw
// writes the recorded file even when Ensure would now refuse to write there
// (a legacy .config.json appeared), since what it removes is niwa's own.
func Withdraw(g *Grant, expectedKey string, how WithdrawMode) (warning string) {
	if g == nil {
		return ""
	}
	if how != Destroy && how != Retract {
		return fmt.Sprintf("Claude trust entry %q in %s left in place: unknown withdrawal mode %d", g.Key, g.ConfigFile, how)
	}
	if err := g.Validate(expectedKey); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ""
		}
		return fmt.Sprintf("Claude trust entry %q in %s left in place: refusing the recorded grant: %v", g.Key, g.ConfigFile, err)
	}
	if err := withdrawEntry(g, how); err != nil {
		return fmt.Sprintf("Claude trust entry %q in %s left in place: %v", g.Key, g.ConfigFile, err)
	}
	return ""
}

// withdrawEntry is Withdraw's locked read-modify-write. It always waits for
// the lock, even after an earlier wait on the same path timed out, so one busy
// moment during a teardown sweep can't leave every later instance's entry
// behind.
func withdrawEntry(g *Grant, how WithdrawMode) error {
	release, err := acquireLock(g.ConfigFile+lockSuffix, false)
	if err != nil {
		return err
	}
	defer release()

	doc, err := loadConfig(g.ConfigFile)
	if err != nil {
		return err
	}
	if !doc.exists {
		return nil
	}
	projects, entry, err := doc.entry(g.Key)
	if err != nil {
		return err
	}
	if entry == nil {
		return nil
	}

	if how == Destroy && g.Mode == ModeCreated {
		delete(projects, g.Key)
	} else {
		if _, ok := entry[trustFlag]; !ok {
			return nil
		}
		delete(entry, trustFlag)
		raw, err := marshalRaw(entry)
		if err != nil {
			return err
		}
		projects[g.Key] = raw
	}
	if err := doc.setProjects(projects); err != nil {
		return err
	}
	return doc.write()
}

// sameFile reports whether a and b both exist and name the same physical
// file, following symlinks. A path that can't be stat'ed is never the same.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// joinWarnings combines the warnings one call produced into one line.
func joinWarnings(ws []string) string {
	return strings.Join(ws, "; ")
}
