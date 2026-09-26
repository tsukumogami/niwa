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
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// The directory-trust writer.
//
// Codex reads trust from the config layers it merges before a project layer
// exists, so nothing niwa plants inside an instance can vouch for itself. One
// entry per cloned repository in the developer's own Codex config is what makes
// a session there able to write files at all: without it the session runs in a
// read-only sandbox and the interactive TUI blocks on a trust prompt, both
// silently as far as the apply is concerned.
//
// This is the one file niwa edits that it does not own, so the write discipline
// is part of the contract rather than an implementation detail:
//
//   - Additive and path-scoped. Only whole [projects."<path>"] blocks are ever
//     appended. No pre-existing key is removed, reordered, or rewritten -- the
//     previous bytes are copied verbatim and the new blocks go after them --
//     and no global key is written at all. The one retraction below is bounded
//     to niwa's own entries, so additivity over everyone else's keys still
//     holds: niwa retracts its own signature, never anyone else's.
//   - Record-bounded retraction. What may be removed is decided by the record
//     of keys niwa wrote, never by the entry's shape: Codex writes an
//     identically-shaped [projects."<path>"] entry when the developer answers
//     its own trust prompt, so a shape test would delete the developer's own
//     answer.
//   - Atomic replacement. The edited document goes to a temp file beside the
//     config, is fsynced, and is renamed over the original, so an interrupted
//     apply leaves the previous file whole rather than a truncated one.
//   - Never rewrite what did not parse, and never fail the apply over it. A
//     config niwa cannot read or cannot parse is left byte-untouched and
//     reported as a warning: replacing it would discard content that is not
//     niwa's to discard, and failing over it would make a developer's own
//     broken file break every create and apply (R17).
//   - Serialized. Instances share one developer config, so the whole
//     read-modify-write runs under an advisory lock -- taken in niwa's own
//     state directory, keyed by the config path, so serialization adds no
//     artifact to the developer's config directory -- with the file re-read
//     under the lock.
//
// Credentials are out of reach by construction: nothing here opens auth.json or
// any other login file, so an unreadable one cannot fail an apply.

const (
	// codexTrustLevel is the value niwa writes. The sandbox gate was measured
	// to key on the entry's presence rather than its value, so this is the
	// honest statement of intent rather than a load-bearing string.
	codexTrustLevel = "trusted"

	// codexProjectsTable is the top-level table trust entries live under.
	codexProjectsTable = "projects"

	// codexTrustLevelKey is the per-project key that carries the verdict.
	codexTrustLevelKey = "trust_level"

	// codexHomeEnv is Codex's own override for where its configuration lives.
	// niwa honors it for the same reason it writes the entry at all: the file
	// this names is the one a session will read.
	codexHomeEnv = "CODEX_HOME"
)

// codexTrustProcedure is the directory-trust delivery as the contract reaches
// it: the pipeline looks the procedure up by (capability, agent) and calls it,
// so the writer below has no call site that names an agent.
type codexTrustProcedure struct{}

// Name is the delivery name the contract binds this procedure under.
func (codexTrustProcedure) Name() string { return string(agentplan.DeliveryCodexTrust) }

// Deliver reconciles the trust entries for the repositories this apply
// materialized and carries the record back for the caller to persist.
//
// The record comes out on the failure path too: it is the sole authority for
// what niwa may later remove from the developer's config, so a run that could
// not write must still carry forward what earlier runs did. Forgetting a key
// would strand the entry it names with nothing left able to retract it.
//
// Five of the input's fields go unread here, and that is the shape working as
// intended rather than an oversight: the instance root, the producer, the
// plugin opt-out, the reporter, and the disclosure record all belong to the
// niwa-plugin deliveries. This one writes outside every instance, declares no
// plan, reports through its warnings, and has no notice to repeat.
func (codexTrustProcedure) Deliver(in procedureInput) (procedureResult, error) {
	res, err := EnsureCodexTrust(CodexTrustRequest{
		DeveloperHome: in.DeveloperHome,
		RepoRoots:     in.RepoRoots,
		Recorded:      in.Recorded,
	})
	return procedureResult{Recorded: res.Recorded, Warnings: res.Warnings}, err
}

// CodexTrustRequest describes one reconciliation of niwa's trust entries
// against the developer's Codex config.
type CodexTrustRequest struct {
	// DeveloperHome is the developer's own home directory, which is where the
	// Codex config and niwa's lock directory are resolved under. It arrives as
	// data rather than being read from the environment here, so a caller that
	// has not been wired to supply one cannot reach a real home by accident --
	// which is what keeps the unit suites, which build Appliers by the dozen,
	// off the developer's files.
	DeveloperHome string

	// RepoRoots are the repository roots to trust, as the apply holds them.
	// They are canonicalized here, not by the caller. One entry per cloned
	// repository and no more: Codex resolves trust through the main repository
	// root, so a repository's entry already covers every worktree of that
	// repository and per-worktree entries would be redundant.
	RepoRoots []string

	// Recorded is the set of trust keys niwa wrote on earlier applies, read
	// from instance state. It is what keeps the record honest across applies:
	// an entry that is already in the file is niwa's only if this list says
	// so, since Codex writes identically-shaped entries when the developer
	// answers its own trust prompt.
	Recorded []string

	// Conflicted are the repository roots niwa refuses to vouch for. Each is
	// withheld (no entry is written for it) and, when the record names its
	// key, retracted: with niwa's own content absent from the repository, an
	// entry would vouch for whatever occupies its place.
	//
	// Nothing populates this today. The detection that will -- the payload
	// conflict pass -- arrives with the payload it detects, and the withhold
	// and retract halves travel with the writer because the record's
	// retraction rule is the safety property the writer exists to keep: a
	// writer that can only ever add has no way back out of an entry it should
	// not have written.
	Conflicted []string
}

// CodexTrustResult reports what one reconciliation produced.
type CodexTrustResult struct {
	// Recorded is the record to persist in instance state: every trust key
	// niwa has written, this apply or an earlier one. It is the sole authority
	// for what niwa may later remove, so it carries forward keys whose
	// repositories are no longer in the workspace rather than forgetting
	// entries it left in the developer's file.
	Recorded []string

	// Added are the keys this call appended.
	Added []string

	// Removed are the keys this call retracted: recorded keys whose repository
	// is conflicted and whose block was found in the file. A recorded key the
	// file no longer carries is not listed here -- nothing was removed -- but
	// it still leaves the record, since niwa no longer owns anything at it.
	Removed []string

	// Warnings are the per-key lines the apply reports: anomalies that did not
	// stop the write, and every retraction, since a trust entry disappearing
	// from the developer's file is not something niwa does quietly.
	Warnings []string
}

// CodexConfigPath returns the path of the developer's Codex config file under
// home: $CODEX_HOME when set (Codex's own override), else <home>/.codex.
func CodexConfigPath(home string) (string, error) {
	if h := strings.TrimSpace(os.Getenv(codexHomeEnv)); h != "" {
		return filepath.Join(h, "config.toml"), nil
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("no home directory to resolve the Codex config under")
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

// CanonicalTrustKey resolves root to the path Codex will compute for a session
// started inside it: absolute, with every path component's symlinks resolved.
//
// The resolution is the substance, not hygiene. Codex resolves the working
// directory and the git root before looking trust up, so an entry keyed by an
// unresolved path through a symlinked parent -- a linked home directory, an
// automounted volume, a symlinked workspace root -- is silently miskeyed, and a
// miskeyed entry fails in the worst available shape: a read-only session with
// no error anywhere.
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

// EnsureCodexTrust upserts one trust entry per requested repository root in the
// developer's Codex config, retracts the entries the record names for
// repositories that have become conflicted, and alters nothing else that was
// already there.
//
// The returned record is meaningful even when the error is non-nil: callers
// persist it and surface the error afterwards, so a failed write does not also
// lose the record of what earlier applies wrote.
func EnsureCodexTrust(req CodexTrustRequest) (CodexTrustResult, error) {
	result := CodexTrustResult{Recorded: normalizeTrustKeys(req.Recorded)}

	configPath, err := CodexConfigPath(req.DeveloperHome)
	if err != nil {
		return result, fmt.Errorf("resolving the Codex config path: %w", err)
	}

	withheld := map[string]bool{}
	for _, root := range req.Conflicted {
		key, err := CanonicalTrustKey(root)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"could not resolve %s while withholding its Codex trust entry: %v; any entry niwa recorded for it stays until the next apply resolves the path",
				root, err))
			continue
		}
		withheld[key] = true
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
		if withheld[key] {
			// Which repositories niwa refuses to vouch for is decided in one
			// place: the caller hands over every repository it materialized
			// and the withheld set subtracts from it.
			continue
		}
		wanted = append(wanted, key)
	}
	wanted = normalizeTrustKeys(wanted)

	// Removal is bounded by the record and nothing else: only a key niwa is
	// recorded as having written may be retracted, so the developer's own
	// answer to Codex's trust prompt -- identical in shape, absent from the
	// record -- is never touched.
	var retract []string
	for _, key := range result.Recorded {
		if withheld[key] {
			retract = append(retract, key)
		}
	}

	if len(wanted) == 0 && len(retract) == 0 {
		return result, nil
	}

	release, err := lockCodexTrust(req.DeveloperHome, configPath)
	if err != nil {
		return result, err
	}
	defer release()

	// Re-read under the lock. The read above the lock would be a read of
	// whatever another apply was midway through replacing.
	data, mode, readErr := readCodexConfig(configPath)
	if readErr != nil {
		// An unreadable config fails neither create nor apply (R17). Nothing
		// is written and the record is unchanged, so the next apply over a
		// readable file writes exactly what this one would have.
		result.Warnings = append(result.Warnings, readErr.Error())
		return result, nil
	}

	existing, parseErr := parseCodexProjects(data, configPath)
	if parseErr != nil {
		result.Warnings = append(result.Warnings, parseErr.Error())
		return result, nil
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
			// header at the same key would make the document invalid TOML, and
			// editing inside someone else's table is not the additive write
			// this is bounded to -- so niwa leaves it alone and says so.
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%s already carries a [projects.%s] entry with no %s; niwa left it untouched, so a Codex session in that repository runs read-only until the entry is trusted",
				configPath, quoteTOMLKey(key), codexTrustLevelKey))
			continue
		}
		// Already trusted -- by niwa on an earlier apply, or by the developer
		// answering Codex's own prompt. Either way the entry stands as it is:
		// this is what makes three successive applies leave one entry, and what
		// keeps a developer's own verdict from being overwritten.
	}

	// Retract the recorded entries the file still carries. A recorded key the
	// file no longer has is a no-op here -- the safe direction of record/file
	// disagreement -- and so is an unrecorded key the file does carry, which
	// never reaches this list at all.
	edited := data
	var removed, stranded []string
	for _, key := range retract {
		if _, present := existing[key]; !present {
			continue
		}
		next, ok := removeTrustEntry(edited, key)
		if !ok {
			// The file carries the key in a shape niwa cannot retract by block
			// -- an inline table under [projects], say. Leaving the record in
			// place is what keeps the next apply trying rather than abandoning
			// an entry with nothing left able to withdraw it.
			stranded = append(stranded, key)
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%s carries a [projects.%s] entry niwa recorded writing but could not retract in place; it was left untouched, so it still vouches for a repository niwa no longer prepares for Codex",
				configPath, quoteTOMLKey(key)))
			continue
		}
		edited = next
		removed = append(removed, key)
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"niwa withdrew its Codex trust entry for %s: that repository no longer carries the content niwa wrote, so niwa no longer vouches for what a session there would load; Codex asks at session start instead",
			key))
	}

	if len(add) == 0 && len(removed) == 0 {
		// Nothing changed in the file, but a recorded key that the file no
		// longer carries still leaves the record: niwa owns nothing at it.
		result.Recorded = clearTrustKeys(result.Recorded, retract, stranded)
		return result, nil
	}

	if err := writeCodexConfig(configPath, appendTrustEntries(edited, add), mode); err != nil {
		return result, err
	}

	result.Added = add
	result.Removed = removed

	// The record moves with the file. Clearing the retracted keys is what makes
	// withholding and removal compose: a record left behind would license the
	// next apply to delete whatever sits at that key -- by then possibly the
	// developer's own answer to Codex's prompt. What niwa promises is that it
	// never re-adds its entry while the conflict stands, not that no entry
	// exists afterwards.
	result.Recorded = clearTrustKeys(result.Recorded, retract, stranded)
	result.Recorded = normalizeTrustKeys(append(result.Recorded, add...))
	return result, nil
}

// clearTrustKeys removes every key in clear from keys, except those in keep.
func clearTrustKeys(keys, clear, keep []string) []string {
	if len(clear) == 0 {
		return keys
	}
	drop := make(map[string]bool, len(clear))
	for _, key := range clear {
		drop[key] = true
	}
	for _, key := range keep {
		drop[key] = false
	}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if drop[key] {
			continue
		}
		out = append(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
		return nil, 0, fmt.Errorf(
			"the Codex config at %s could not be read (%v); niwa left it untouched and wrote no trust entries, so Codex sessions in this instance run read-only until it can be read",
			path, err)
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
// A parse failure is returned as an error the caller reports as a warning and
// nothing is written: niwa does not get to replace a document it could not read
// with one it can, and a developer's own malformed config is not a reason to
// fail their create or apply (R17). The instance is still prepared; what the
// warning says is that Codex sessions in it run read-only until the file
// parses.
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

// removeTrustEntry returns data with the [projects."<key>"] block removed, and
// reports whether it found one. Everything outside that block is copied through
// byte for byte, the same discipline the append side keeps: the retraction is
// of niwa's own entry, not a re-rendering of the developer's document.
//
// The block is the header line and every line up to the next table header, so a
// sub-table of the key -- [projects."<key>".something], which niwa never writes
// -- survives as content niwa did not put there. Only a key the record names
// and the parse found reaches this function, so a false match would have to be
// a table header niwa itself wrote appearing inside a multi-line string
// somewhere else in the file.
func removeTrustEntry(data []byte, key string) ([]byte, bool) {
	var out bytes.Buffer
	found := false
	dropping := false

	for _, line := range splitLinesKeepingEnds(data) {
		if path, isHeader := tomlTableHeaderPath(line); isHeader {
			dropping = len(path) == 2 && path[0] == codexProjectsTable && path[1] == key
			if dropping {
				found = true
			}
		}
		if !dropping {
			out.WriteString(line)
		}
	}
	if !found {
		return data, false
	}
	return out.Bytes(), true
}

// tomlHeaderProbeKey is the key appended to a candidate header line to find out
// which table the header opens. It is never written to any file.
const tomlHeaderProbeKey = "niwa_header_probe"

// tomlTableHeaderPath reports whether line opens a TOML table, and if so the
// dotted path of the table it opens.
//
// The path comes from the TOML decoder rather than from string surgery, so
// quoting, escapes, literal-string keys, and a trailing comment are all handled
// by the same rules that will read the file back. An array-of-tables header is
// a header (it ends the previous table, which is what the caller needs to know)
// with no path niwa can match, since niwa never writes that shape.
func tomlTableHeaderPath(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var doc map[string]any
	if _, err := toml.Decode(trimmed+"\n"+tomlHeaderProbeKey+" = true\n", &doc); err != nil {
		return nil, false
	}
	path, ok := probedTablePath(doc, nil)
	if !ok {
		// A well-formed header whose table niwa cannot address by a plain
		// dotted path: an array-of-tables. It still ends the preceding table.
		return nil, true
	}
	return path, true
}

// probedTablePath walks doc for the probe key and returns the path of the table
// holding it.
func probedTablePath(doc map[string]any, prefix []string) ([]string, bool) {
	if _, ok := doc[tomlHeaderProbeKey]; ok {
		return prefix, true
	}
	for name, value := range doc {
		table, isTable := value.(map[string]any)
		if !isTable {
			continue
		}
		child := append(append([]string{}, prefix...), name)
		if path, ok := probedTablePath(table, child); ok {
			return path, true
		}
	}
	return nil, false
}

// splitLinesKeepingEnds splits data into lines with their terminators attached,
// so a rebuild that drops some lines leaves the rest byte-identical -- including
// a final line with no newline and CRLF endings.
func splitLinesKeepingEnds(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, string(data[start:i+1]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

// writeCodexConfig replaces the config atomically: a temp file beside it (an
// atomic rename needs the same filesystem), fsynced so the bytes are on disk
// before the rename publishes them, then renamed over the target. The target is
// never opened for writing, so an interruption at any point leaves the previous
// file whole.
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

// lockCodexTrust takes the advisory lock that serializes niwa's writers against
// one Codex config. The lock file lives in niwa's own state directory under
// home and is named for a hash of the config path, so two instances applying
// concurrently against the same config contend on the same file while two
// different configs never contend at all.
//
// It serializes niwa's writers only. Writes by Codex itself are outside niwa's
// control -- the same exposure Codex's own concurrent sessions carry.
func lockCodexTrust(home, configPath string) (func(), error) {
	if strings.TrimSpace(home) == "" {
		return nil, fmt.Errorf("no home directory to resolve the niwa lock directory under")
	}
	root := filepath.Join(home, ".niwa", "locks")
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
