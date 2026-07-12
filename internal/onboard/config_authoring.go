// Config authoring: onboard writes configuration in three places that
// behave nothing alike -- the operator's personal-overlay repo (a real git
// clone, entirely the operator's account), the operator-local pointer file
// (not a git repo at all), and the team's workspace source repo (a shared,
// review-gated object the operator has merge access to but the wizard does
// not commit to). This file provides the one shared editing primitive
// (table-header-aware TOML insertion with a pre-write landing check) and
// the three per-site drivers built on top of it.
//
// BurntSushi/toml has no format-preserving encode path -- a struct
// round-trip is a full-file rewrite that loses comments, key ordering, and
// unknown keys. InsertOrReplaceTable operates on raw bytes instead: it
// finds a top-level table by its header line and replaces only that
// table's span (header through the next top-level header or EOF),
// leaving every other byte in the file untouched.
package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// EncodeTOMLString renders s as a TOML basic string (double-quoted),
// escaping every byte that would otherwise let the value break out of the
// quotes or inject structure: backslash, double-quote, and the C0 control
// bytes (newline, tab, carriage return, etc. via their short escapes; any
// other control byte via \u00XX). A value carrying a literal `"`, a
// newline, or a `]` comes out as inert string content -- the `]` needs no
// special escape in TOML, but wrapping the value in quotes is what removes
// its meaning as array/table syntax.
func EncodeTOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
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
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// isTopLevelHeaderLine reports whether line (already trimmed of leading/
// trailing whitespace) opens a new top-level TOML section: a standard
// table header (`[table]`) or an array-of-tables header (`[[table]]`).
// Either one ends the span of a preceding table.
func isTopLevelHeaderLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "[")
}

// InsertOrReplaceTable applies the surgical table-level edit described in
// DESIGN-niwa-onboard.md Decision 5 to content, targeting the top-level
// table named by tablePath (e.g. "global.vault.provider"). tableBody is the
// full rendered replacement, including its own header line and a trailing
// newline (see RenderVaultProviderTable).
//
// Three outcomes:
//   - The table is absent: tableBody is appended, preceded by a blank line
//     separator (omitted when content is empty), and every existing byte is
//     left untouched. changed=true.
//   - The table is present and its existing span is byte-identical to
//     tableBody: no-op. changed=false, result==content. This is the landing
//     check -- re-running the same write can never produce a duplicate
//     top-level table, which would otherwise be a hard TOML parse error.
//   - The table is present with a different span: only that span (header
//     line through the line before the next top-level header, or EOF) is
//     replaced. Comments, unknown keys, and unrelated tables anywhere else
//     in the file -- including inside the replaced table itself -- are not
//     preserved across the replace; that is the accepted cost of a
//     whole-table replace rather than a merge.
func InsertOrReplaceTable(content []byte, tablePath, tableBody string) (result []byte, changed bool) {
	header := "[" + tablePath + "]"
	lines := splitKeepEnds(content)

	headerLineIdx := -1
	for i, ln := range lines {
		if strings.TrimSpace(stripLineEnd(ln)) == header {
			headerLineIdx = i
			break
		}
	}

	if headerLineIdx < 0 {
		return appendTable(content, tableBody), true
	}

	// Find the end of this table's span: the start of the next top-level
	// header line, or EOF.
	spanEndIdx := len(lines)
	for i := headerLineIdx + 1; i < len(lines); i++ {
		if isTopLevelHeaderLine(strings.TrimSpace(stripLineEnd(lines[i]))) {
			spanEndIdx = i
			break
		}
	}

	spanStartOffset := lineOffset(lines, headerLineIdx)
	spanEndOffset := lineOffset(lines, spanEndIdx)
	existingSpan := string(content[spanStartOffset:spanEndOffset])

	if sameTableSpan(existingSpan, tableBody) {
		return content, false
	}

	var out []byte
	out = append(out, content[:spanStartOffset]...)
	out = append(out, []byte(normalizeTrailingNewline(tableBody))...)
	out = append(out, content[spanEndOffset:]...)
	return out, true
}

// sameTableSpan compares two rendered table spans for the landing check,
// tolerating only a difference in trailing-newline count (both sides are
// otherwise required to match exactly, including key order and internal
// comments).
func sameTableSpan(a, b string) bool {
	return strings.TrimRight(a, "\n") == strings.TrimRight(b, "\n")
}

// appendTable appends tableBody to content, preceded by a blank-line
// separator unless content is empty (a fresh file gets no leading blank
// line).
func appendTable(content []byte, tableBody string) []byte {
	trimmedBody := normalizeTrailingNewline(tableBody)
	if len(bytesTrimSpace(content)) == 0 {
		return []byte(trimmedBody)
	}
	var out []byte
	out = append(out, content...)
	if !endsInNewline(out) {
		out = append(out, '\n')
	}
	out = append(out, '\n')
	out = append(out, []byte(trimmedBody)...)
	return out
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func endsInNewline(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] == '\n'
}

func normalizeTrailingNewline(s string) string {
	s = strings.TrimRight(s, "\n")
	return s + "\n"
}

// splitKeepEnds splits content into lines, keeping each line's trailing
// "\n" (if any) attached so callers can reconstruct exact byte offsets.
func splitKeepEnds(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	s := string(content)
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func stripLineEnd(line string) string {
	return strings.TrimRight(line, "\n")
}

// lineOffset returns the byte offset of the start of lines[idx], or the
// total length when idx == len(lines) (EOF).
func lineOffset(lines []string, idx int) int {
	off := 0
	for i := 0; i < idx && i < len(lines); i++ {
		off += len(lines[i])
	}
	return off
}

// RenderVaultProviderTable renders a `[tablePath]` table declaring the
// wizard-owned vault-provider fields (kind, project, api_url), each passed
// through EncodeTOMLString so a hostile value can't inject structure. Used
// both for the personal-overlay write (site 1) and the team-config
// render-only snippet (site 3) -- the two sites share the same generation
// so the printed snippet is exactly what would have been written, not a
// hand-approximation.
func RenderVaultProviderTable(tablePath, kind, project, apiURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", tablePath)
	fmt.Fprintf(&b, "kind = %s\n", EncodeTOMLString(kind))
	fmt.Fprintf(&b, "project = %s\n", EncodeTOMLString(project))
	fmt.Fprintf(&b, "api_url = %s\n", EncodeTOMLString(apiURL))
	return b.String()
}

// ConfigSite identifies one of the three places onboard authors
// configuration.
type ConfigSite int

const (
	// SitePersonalOverlay is the operator's personal-overlay repo
	// (niwa.toml at its root, a real git clone).
	SitePersonalOverlay ConfigSite = iota
	// SiteLocalPointer is ~/.config/niwa/config.toml -- not a git repo.
	SiteLocalPointer
	// SiteTeamConfig is the team's workspace source repo -- shared and
	// review-gated; onboard never writes to it directly.
	SiteTeamConfig
)

// LandedOn reports which side of the upstream/operator-local boundary a
// config write landed on (AC-25).
type LandedOn int

const (
	// LandedUpstreamRepo means the write was committed (without pushing)
	// to a repo the operator owns.
	LandedUpstreamRepo LandedOn = iota
	// LandedOperatorLocal means the write went to machine-local state
	// with no git involved.
	LandedOperatorLocal
	// LandedRenderOnly means nothing was written anywhere; a snippet was
	// produced for the operator to carry into their own edit/PR flow.
	LandedRenderOnly
)

// WriteResult reports the outcome of one config-authoring write, including
// which side it landed on (AC-25).
type WriteResult struct {
	Site ConfigSite
	// Landed is unset (zero value) only when Changed is false and no
	// write of any kind was attempted or needed; callers should not infer
	// meaning from Landed on a no-op result beyond "nothing happened".
	Landed LandedOn
	// Location is the file path the config lives in (site 1 and 2) or
	// the destination path named for the operator to edit (site 3).
	Location string
	// Changed reports whether this call produced a new write (false for
	// an idempotent no-op landing-check hit).
	Changed bool
	// Message is a human-readable summary suitable for direct display,
	// already naming which side (upstream repo vs. operator-local) the
	// write landed on.
	Message string
	// Snippet holds the rendered TOML block for the render-only site
	// (SiteTeamConfig); empty for the other two sites.
	Snippet string
}

// sanitizeCommitEnv returns env with every GIT_AUTHOR_*/GIT_COMMITTER_*
// entry removed, so the wizard's commit never inherits an author identity
// from the parent process. Mirrors workspace.sanitizeCommitEnv (RunBootstrap's
// R18 invariant); duplicated here rather than exported across the package
// boundary for a four-line helper.
func sanitizeCommitEnv(env []string) []string {
	blocklist := []string{
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE",
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		key := kv
		if eq >= 0 {
			key = kv[:eq]
		}
		blocked := false
		for _, b := range blocklist {
			if key == b {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, kv)
		}
	}
	return out
}

// WritePersonalOverlayVaultProvider is the site-1 driver: it surgically
// inserts or replaces the `[global.vault.provider]` table in the personal
// overlay's niwa.toml (at overlayDir, per config.GlobalConfigDir --
// workspace.GlobalConfigOverrideFile names the exact filename), then
// commits the change locally with no custom author identity. It never
// pushes; the operator's own `git push` is what gets the commit upstream.
//
// overlayDir MUST already exist as a git working tree (R22's precondition
// step is responsible for cloning/scaffolding it before this runs). The
// niwa.toml file inside it may or may not exist yet -- an absent file is
// treated as empty content, matching "starts empty if the repo/file is
// being scaffolded."
//
// The landing check that InsertOrReplaceTable performs is a content-only
// comparison: it does not know whether the matching bytes on disk were
// ever actually committed (e.g. a prior run wrote the file but died before
// `git commit`, because overlayDir wasn't yet a git repo). To avoid
// silently reporting a false "already landed" on such a retry, `git add`
// and the "anything staged?" check always run -- regardless of whether
// InsertOrReplaceTable itself reports a content change -- and only the
// staged-diff outcome decides whether `git commit` runs. A truly
// unmodified, already-committed niwa.toml results in an empty `git add`
// and no staged diff, so no commit fires and Changed is reported false.
func WritePersonalOverlayVaultProvider(ctx context.Context, gitInvoker workspace.GitInvoker, overlayDir, kind, project, apiURL string) (WriteResult, error) {
	tomlPath := filepath.Join(overlayDir, workspace.GlobalConfigOverrideFile)

	existing, err := os.ReadFile(tomlPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return WriteResult{}, fmt.Errorf("reading %s: %w", tomlPath, err)
		}
		existing = nil
	}

	tableBody := RenderVaultProviderTable("global.vault.provider", kind, project, apiURL)
	newContent, contentChanged := InsertOrReplaceTable(existing, "global.vault.provider", tableBody)

	if contentChanged {
		if err := atomicWriteFile(tomlPath, newContent, 0o600); err != nil {
			return WriteResult{}, fmt.Errorf("writing %s: %w", tomlPath, err)
		}
	}

	addCmd := gitInvoker.CommandContext(ctx, "-C", overlayDir, "add", workspace.GlobalConfigOverrideFile)
	if out, err := addCmd.CombinedOutput(); err != nil {
		return WriteResult{}, fmt.Errorf("git add %s: %w\n%s", workspace.GlobalConfigOverrideFile, err, out)
	}

	// git diff --cached --quiet exits 0 when nothing is staged relative
	// to HEAD, non-zero when something is. This is what actually decides
	// whether a commit is needed -- not contentChanged -- so a file that
	// matches on disk but was never committed still gets committed.
	diffCmd := gitInvoker.CommandContext(ctx, "-C", overlayDir, "diff", "--cached", "--quiet", "--", workspace.GlobalConfigOverrideFile)
	diffErr := diffCmd.Run()
	nothingStaged := diffErr == nil

	if nothingStaged {
		return WriteResult{
			Site:     SitePersonalOverlay,
			Landed:   LandedUpstreamRepo,
			Location: tomlPath,
			Changed:  false,
			Message:  fmt.Sprintf("upstream repo: %s already declares this vault provider; no change", tomlPath),
		}, nil
	}

	commitCmd := gitInvoker.CommandContext(ctx, "-C", overlayDir, "commit", "-m", "onboard: update personal-overlay vault provider")
	commitCmd.Env = sanitizeCommitEnv(os.Environ())
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return WriteResult{}, fmt.Errorf("git commit: %w\n%s", err, out)
	}

	return WriteResult{
		Site:     SitePersonalOverlay,
		Landed:   LandedUpstreamRepo,
		Location: tomlPath,
		Changed:  true,
		Message:  fmt.Sprintf("upstream repo: committed to %s (not pushed -- run `git push` in %s)", tomlPath, overlayDir),
	}, nil
}

// atomicWriteFile writes data to path using the temp-in-dir-then-rename
// discipline: a new file is created in path's directory (so the rename is
// same-filesystem and atomic), written, closed, and renamed over path.
// There is no in-place truncate+rewrite, so a reader never observes a
// partially-written file at path.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".niwa-config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpPath, path, err)
	}
	success = true
	return nil
}

// WriteLocalPointer is the site-2 driver: it registers repo as the
// personal-overlay source in the operator-local
// ~/.config/niwa/config.toml, reusing config.LoadGlobalConfig /
// config.SaveGlobalConfigTo (the same writer `niwa config set global`
// uses) rather than a second serialization path. Not a git repo, so there
// is no commit/push posture -- the write either lands or it doesn't.
func WriteLocalPointer(repo string) (WriteResult, error) {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return WriteResult{}, fmt.Errorf("loading global config: %w", err)
	}

	path, err := config.GlobalConfigPath()
	if err != nil {
		return WriteResult{}, fmt.Errorf("resolving global config path: %w", err)
	}

	if cfg.GlobalConfig.Repo == repo {
		return WriteResult{
			Site:     SiteLocalPointer,
			Landed:   LandedOperatorLocal,
			Location: path,
			Changed:  false,
			Message:  fmt.Sprintf("operator-local: %s already points at %s; no change", path, repo),
		}, nil
	}

	cfg.GlobalConfig = config.GlobalConfigSource{Repo: repo}
	if err := config.SaveGlobalConfigTo(path, cfg); err != nil {
		return WriteResult{}, fmt.Errorf("saving global config: %w", err)
	}

	return WriteResult{
		Site:     SiteLocalPointer,
		Landed:   LandedOperatorLocal,
		Location: path,
		Changed:  true,
		Message:  fmt.Sprintf("operator-local: registered personal-overlay repo %s in %s (no git)", repo, path),
	}, nil
}

// RenderTeamConfigSnippet is the site-3 driver: the team's workspace
// source repo is shared and review-gated, so onboard makes no git write of
// any kind here. It computes the exact TOML snippet (the same generation
// site 1 writes) and names the destination file; the operator carries it
// into their own edit/PR/review flow.
func RenderTeamConfigSnippet(tablePath, kind, project, apiURL, destPath string) WriteResult {
	snippet := RenderVaultProviderTable(tablePath, kind, project, apiURL)
	return WriteResult{
		Site:     SiteTeamConfig,
		Landed:   LandedRenderOnly,
		Location: destPath,
		Changed:  false,
		Message:  fmt.Sprintf("not written: carry this block into %s yourself (requires your own review/merge access)", destPath),
		Snippet:  snippet,
	}
}
