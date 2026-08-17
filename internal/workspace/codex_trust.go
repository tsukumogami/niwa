package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// The trust bootstrap (DESIGN-dual-agent-workspace Decision 4A).
//
// Codex reads trust from the config layers it merges before a project layer
// exists, so the payload niwa plants inside an instance cannot vouch for
// itself. One entry per cloned repository in the developer's own Codex config
// is what makes a session there able to write files at all: without it the
// session runs in a read-only sandbox and the interactive TUI blocks on a
// trust prompt, both silently as far as the apply is concerned.
//
// This is the one file niwa edits that it does not own, so the write
// discipline is part of the contract rather than an implementation detail:
//
//   - Additive and path-scoped. Only whole [projects."<path>"] blocks whose
//     paths resolve inside a niwa instance are ever appended. No pre-existing
//     key is removed, reordered, or rewritten -- the previous bytes are copied
//     verbatim and the new blocks go after them -- and no global key is
//     written at all.
//   - Atomic replacement. The edited document goes to a temp file beside the
//     config, is fsynced, and is renamed over the original, so an interrupted
//     apply leaves the previous file whole rather than a truncated one.
//   - Never rewrite what did not parse. A pre-existing config niwa cannot
//     parse is left byte-untouched and reported; replacing it with something
//     parseable would discard content that is not niwa's to discard.
//   - Serialized. Instances share one developer config, so the whole
//     read-modify-write runs under an advisory lock -- taken in niwa's own
//     state directory, keyed by the config path, so serialization adds no
//     artifact to the developer's config directory -- with the file re-read
//     under the lock.
//
// Credentials are out of reach by construction: nothing here opens auth.json
// or any other login file, so an unreadable one cannot fail an apply (R13).

const (
	// codexTrustLevel is the value niwa writes. The sandbox gate was measured
	// to key on the entry's presence rather than its value, so this is the
	// honest statement of intent rather than a load-bearing string.
	codexTrustLevel = "trusted"

	// codexProjectsTable is the top-level table trust entries live under.
	codexProjectsTable = "projects"

	// codexTrustLevelKey is the per-project key that carries the verdict.
	codexTrustLevelKey = "trust_level"
)

// codexConfigHome resolves the developer's Codex home directory: $CODEX_HOME
// when set (Codex's own override), else ~/.codex. A package variable so tests
// point it at a fixture tree instead of the developer's real home, matching
// the seam pattern used for the plugin roots.
var codexConfigHome = func() (string, error) {
	if h := strings.TrimSpace(os.Getenv("CODEX_HOME")); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

// niwaLockRoot resolves the directory niwa keeps its own lock files in. The
// lock for a Codex config lives here rather than beside the config itself:
// serialization is niwa's business and must not leave an artifact in the
// developer's config directory (Decision 4A). Package variable for the same
// test-seam reason as codexConfigHome.
var niwaLockRoot = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".niwa", "locks"), nil
}

// CodexConfigPath returns the path of the developer's Codex config file.
func CodexConfigPath() (string, error) {
	home, err := codexConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config.toml"), nil
}

// CodexTrustRequest describes one reconciliation of niwa's trust entries
// against the developer's Codex config.
type CodexTrustRequest struct {
	// ConfigPath is the config to edit. Empty resolves the developer's own
	// via CodexConfigPath.
	ConfigPath string

	// RepoRoots are the repository roots to trust, as the apply holds them.
	// They are canonicalized here, not by the caller. One entry per cloned
	// repository and no more: Codex resolves trust through the main
	// repository root, so a repository's entry already covers every worktree
	// of that repository and per-worktree entries would be redundant.
	RepoRoots []string

	// Recorded is the set of trust keys niwa wrote on earlier applies, read
	// from instance state. It is what keeps the record honest across applies:
	// an entry that is already in the file is niwa's only if this list says
	// so, since Codex writes identically-shaped entries when the developer
	// answers its own trust prompt.
	Recorded []string
}

// CodexTrustResult reports what one reconciliation produced.
type CodexTrustResult struct {
	// Recorded is the record to persist in instance state: every trust key
	// niwa has written, this apply or an earlier one. It is the sole
	// authority for what niwa may later remove, so it carries forward keys
	// whose repositories are no longer in the workspace rather than
	// forgetting entries it left in the developer's file.
	Recorded []string

	// Added are the keys this call appended.
	Added []string

	// Warnings are per-key anomalies that did not stop the write.
	Warnings []string
}

// CanonicalTrustKey resolves root to the path Codex will compute for a session
// started inside it: absolute, with every path component's symlinks resolved.
//
// The resolution is the substance, not hygiene. Codex resolves the working
// directory and the git root before looking trust up, so an entry keyed by an
// unresolved path through a symlinked parent -- a linked home directory, an
// automounted volume, a symlinked workspace root -- is silently miskeyed, and
// a miskeyed entry fails in the worst available shape: a read-only session
// with no error anywhere.
func CanonicalTrustKey(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("canonicalizing %s: %w", abs, err)
	}
	return resolved, nil
}

// EnsureCodexTrust upserts one trust entry per requested repository root in
// the developer's Codex config, adding nothing else and altering nothing that
// was already there.
//
// The returned record is meaningful even when the error is non-nil: callers
// persist it and surface the error afterwards, so a refusal to touch an
// unparseable config does not also lose the record of what earlier applies
// wrote.
func EnsureCodexTrust(req CodexTrustRequest) (CodexTrustResult, error) {
	result := CodexTrustResult{Recorded: normalizeTrustKeys(req.Recorded)}

	configPath := req.ConfigPath
	if configPath == "" {
		resolved, err := CodexConfigPath()
		if err != nil {
			return result, fmt.Errorf("resolving the Codex config path: %w", err)
		}
		configPath = resolved
	}

	wanted := make([]string, 0, len(req.RepoRoots))
	for _, root := range req.RepoRoots {
		key, err := CanonicalTrustKey(root)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"no Codex trust entry written for %s: %v; a Codex session there runs read-only until the next apply resolves the path",
				root, err))
			continue
		}
		wanted = append(wanted, key)
	}
	wanted = normalizeTrustKeys(wanted)
	if len(wanted) == 0 {
		return result, nil
	}

	release, err := lockCodexTrust(configPath)
	if err != nil {
		return result, err
	}
	defer release()

	// Re-read under the lock. The read above the lock would be a read of
	// whatever another apply was midway through replacing.
	data, mode, err := readCodexConfig(configPath)
	if err != nil {
		return result, err
	}

	existing, err := parseCodexProjects(data, configPath)
	if err != nil {
		return result, err
	}

	var add []string
	for _, key := range wanted {
		entry, present := existing[key]
		if !present {
			add = append(add, key)
			continue
		}
		if !entry.hasTrustLevel {
			// A [projects."<path>"] table the developer (or some other tool)
			// already owns, carrying settings but no verdict. A second table
			// header at the same key would make the document invalid TOML,
			// and editing inside someone else's table is not the additive
			// write this is bounded to -- so niwa leaves it alone and says so.
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%s already carries a [projects.%s] entry with no %s; niwa left it untouched, so a Codex session in that repository runs read-only until the entry is trusted",
				configPath, quoteTOMLKey(key), codexTrustLevelKey))
			continue
		}
		// Already trusted -- by niwa on an earlier apply, or by the developer
		// answering Codex's own prompt. Either way the entry stands as it is:
		// this is what makes three successive applies leave one entry, and
		// what keeps a developer's own verdict from being overwritten.
	}

	if len(add) == 0 {
		return result, nil
	}

	if err := writeCodexConfig(configPath, appendTrustEntries(data, add), mode); err != nil {
		return result, err
	}

	result.Added = add
	result.Recorded = normalizeTrustKeys(append(result.Recorded, add...))
	return result, nil
}

// codexProjectEntry is what the parse needs to know about one pre-existing
// per-project table: that it exists, and whether it already carries a verdict.
type codexProjectEntry struct {
	hasTrustLevel bool
}

// readCodexConfig reads the config, treating an absent file as empty content.
// The mode of an existing file is carried out so the atomic replacement can
// preserve it; a config niwa creates is private (0600), since it lands in the
// developer's home.
func readCodexConfig(path string) ([]byte, os.FileMode, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0o600, nil
		}
		return nil, 0, fmt.Errorf("reading the Codex config at %s: %w", path, err)
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	return data, mode, nil
}

// parseCodexProjects returns the per-project entries the config already
// carries.
//
// A parse failure is returned as an error naming the file and nothing is
// written: niwa does not get to replace a document it could not read with one
// it can. The error reaches the caller as a deferred failure, so the rest of
// materialization still completes and the command exits non-zero -- the
// alternative, a warning, would leave an instance that looks prepared while
// every repository in it silently runs read-only.
func parseCodexProjects(data []byte, path string) (map[string]codexProjectEntry, error) {
	entries := map[string]codexProjectEntry{}
	if len(bytes.TrimSpace(data)) == 0 {
		return entries, nil
	}

	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return nil, fmt.Errorf(
			"the Codex config at %s is not valid TOML (%v); niwa left it untouched and wrote no trust entries, so Codex sessions in this instance run read-only until it parses",
			path, err)
	}

	raw, ok := doc[codexProjectsTable]
	if !ok {
		return entries, nil
	}
	projects, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"the Codex config at %s has a %s value that is not a table; niwa left it untouched and wrote no trust entries",
			path, codexProjectsTable)
	}

	for key, value := range projects {
		entry := codexProjectEntry{}
		if table, isTable := value.(map[string]any); isTable {
			_, entry.hasTrustLevel = table[codexTrustLevelKey]
		}
		entries[key] = entry
	}
	return entries, nil
}

// appendTrustEntries returns data with one [projects."<key>"] block appended
// per key. Every pre-existing byte is copied through unchanged: a table header
// resets the document's context whatever precedes it, so appending at the end
// is always correct and never needs the rest of the file re-rendered.
func appendTrustEntries(data []byte, keys []string) []byte {
	var buf bytes.Buffer
	buf.Write(data)
	if buf.Len() > 0 && data[len(data)-1] != '\n' {
		buf.WriteByte('\n')
	}
	for _, key := range keys {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "[%s.%s]\n%s = %q\n", codexProjectsTable, quoteTOMLKey(key), codexTrustLevelKey, codexTrustLevel)
	}
	return buf.Bytes()
}

// writeCodexConfig replaces the config atomically: a temp file beside it (an
// atomic rename needs the same filesystem), fsynced so the bytes are on disk
// before the rename publishes them, then renamed over the target. The target
// is never opened for writing, so an interruption at any point leaves the
// previous file whole.
//
// The temp file is the one transient artifact the "nothing else is written
// there" claim carves out; it exists only for the instant of the write.
func writeCodexConfig(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating the Codex config directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".niwa-config.toml.tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temp file beside the Codex config at %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the staged Codex config for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing the staged Codex config for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing the staged Codex config for %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("setting the mode on the staged Codex config for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing the Codex config at %s: %w", path, err)
	}
	cleanup = false
	return nil
}

// lockCodexTrust takes the advisory lock that serializes niwa's writers
// against one Codex config. The lock file lives in niwa's own state directory
// and is named for a hash of the config path, so two instances applying
// concurrently against the same config contend on the same file while two
// different configs never contend at all.
//
// It serializes niwa's writers only. Writes by Codex itself are outside
// niwa's control -- the same exposure Codex's own concurrent sessions carry.
func lockCodexTrust(configPath string) (func(), error) {
	root, err := niwaLockRoot()
	if err != nil {
		return nil, fmt.Errorf("resolving the niwa lock directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("creating the niwa lock directory %s: %w", root, err)
	}
	sum := sha256.Sum256([]byte(configPath))
	lockPath := filepath.Join(root, "codex-trust-"+hex.EncodeToString(sum[:])[:16]+".lock")
	return acquireCodexTrustLock(lockPath)
}

// quoteTOMLKey renders s as a TOML basic string, suitable as a quoted key in a
// table header.
func quoteTOMLKey(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// normalizeTrustKeys sorts and de-duplicates a key list, dropping empties, so
// the persisted record and the appended blocks are both stable across applies.
func normalizeTrustKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
