package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// resetEvidenceMax bounds each line reset's uncommitted-changes check prints.
const resetEvidenceMax = 400

// checkResetUncommitted is reset's uncommitted-changes check. It returns the
// repositories with uncommitted changes, by the walk's label, and each thing
// it couldn't check, as a line naming it and why. Reset refuses while either
// list holds anything.
//
// Every cloned repository's name in the state file is resolved inside the
// instance with removal.ResolveRecorded before anything is read or run at
// it: a name that leaves the instance, or that crosses a link or another
// device on the way, is listed with the refusal and nothing at it is touched.
// A repository whose directory is gone is skipped.
//
// Then the removal walk and the configuration check run over the whole
// instance before any other git call (removal.Env.ScanRepositories). Every
// walk keep and every repository the configuration check keeps is listed as
// unchecked with the reason, and so is a cloned repository the walk found no
// repository at. Every other work tree is read with its own status through
// the scan's command builder, which checks the configuration again right
// before it and reads every untracked file. That status ignores submodules,
// whose own changes come back from their own walk entries, so each work tree
// is also checked for gitlink changes: a submodule checked out at another
// commit than the index records, a staged submodule commit, or a submodule
// work tree that is gone.
func checkResetUncommitted(ctx context.Context, workspaceRoot, instanceDir string) (dirty, unchecked []string, err error) {
	state, err := workspace.LoadState(instanceDir)
	if err != nil {
		return nil, nil, fmt.Errorf("loading instance state: %w", err)
	}

	var names []string
	for name, repo := range state.Repos {
		if repo.Cloned {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var expected []string
	for _, name := range names {
		if _, err := removal.ResolveRecorded(removal.Base{Instance: instanceDir}, name); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			unchecked = append(unchecked, err.Error())
			continue
		}
		expected = append(expected, strings.TrimSuffix(name, "/"))
	}

	env := newRemovalEnv(workspaceRoot, userHomeDir(), os.Getenv("PATH"))
	defer env.Close()
	repos := env.ScanRepositories(ctx, instanceDir)
	byRel := make(map[string]removal.ScanRepository, len(repos))
	for _, r := range repos {
		if _, ok := byRel[r.Rel]; !ok || r.Reason != "" {
			byRel[r.Rel] = r
		}
	}

	for _, name := range expected {
		if !walkFound(repos, name) {
			unchecked = append(unchecked, fmt.Sprintf("%q: no git repository was found there", name))
		}
	}

	isDirty := map[string]bool{}
	for _, r := range repos {
		if r.Reason != "" {
			unchecked = append(unchecked, labelled(r.Rel, r.Reason))
			continue
		}
		changed, why := resetRepoChanged(ctx, env, r, byRel)
		switch {
		case why != "":
			unchecked = append(unchecked, labelled(r.Rel, why))
		case changed:
			isDirty[r.Rel] = true
		}
	}
	for rel := range isDirty {
		dirty = append(dirty, rel)
	}
	sort.Strings(dirty)
	for i, s := range dirty {
		dirty[i] = removal.ScrubEvidence(s, resetEvidenceMax)
	}
	for i, s := range unchecked {
		unchecked[i] = removal.ScrubEvidence(s, resetEvidenceMax)
	}
	return dirty, unchecked, nil
}

// walkFound reports whether the walk came back with the repository at name,
// or with a keep inside it.
func walkFound(repos []removal.ScanRepository, name string) bool {
	for _, r := range repos {
		if r.Rel == name || (r.Reason != "" && strings.HasPrefix(r.Rel, name+"/")) {
			return true
		}
	}
	return false
}

// labelled is reason with rel in front, unless it starts with rel already.
func labelled(rel, reason string) string {
	if strings.HasPrefix(reason, rel+":") {
		return reason
	}
	return rel + ": " + reason
}

// resetRepoChanged reads one work tree the walk returned. changed is whether
// it holds uncommitted changes; why, when set, is why it couldn't be read.
// The status comes first, so the configuration check runs again before any
// of the gitlink reads, which are all reads of the index, the refs and the
// object store: no filter, hook or diff driver runs for any of them.
func resetRepoChanged(ctx context.Context, env *removal.Env, r removal.ScanRepository, byRel map[string]removal.ScanRepository) (changed bool, why string) {
	out, err := scanOutput(ctx, env, r.WorkTree, "status", "--porcelain=v2", "-z")
	if err != nil {
		return false, "status: " + err.Error()
	}
	if len(out) > 0 {
		return true, ""
	}

	head, born, err := scanHead(ctx, env, r.WorkTree)
	if err != nil {
		return false, "reading HEAD: " + err.Error()
	}
	out, err = scanOutput(ctx, env, r.WorkTree, "ls-files", "-s", "-z")
	if err != nil {
		return false, "listing the index: " + err.Error()
	}
	links, err := parseGitlinks(out)
	if err != nil {
		return false, "listing the index: " + err.Error()
	}
	if born {
		// The status listed no staged change, so anything this lists is a
		// staged gitlink: a submodule commit added, changed or removed.
		out, err = scanOutput(ctx, env, r.WorkTree, "diff-index", "--cached", "--raw", "-z",
			"--no-ext-diff", "--no-textconv", "--ignore-submodules=dirty", head, "--")
		if err != nil {
			return false, "reading staged submodule changes: " + err.Error()
		}
		if len(out) > 0 {
			return true, ""
		}
	} else if len(links) > 0 {
		return true, ""
	}

	for _, l := range links {
		if l.stage != "0" {
			return true, ""
		}
		changed, why := gitlinkChanged(ctx, env, r, l, byRel)
		if changed || why != "" {
			return changed, why
		}
	}
	return false, ""
}

// gitlinkChanged compares one gitlink the index records with the submodule
// checked out at its path, the way git status does with submodules not
// ignored: a submodule at another commit and a path that's gone are changes,
// and an empty directory is a submodule never initialized. git runs only in a
// submodule the walk returned and the configuration check let through.
func gitlinkChanged(ctx context.Context, env *removal.Env, super removal.ScanRepository, l gitlink, byRel map[string]removal.ScanRepository) (changed bool, why string) {
	rel := path.Join(super.Rel, l.path)
	if sub, ok := byRel[rel]; ok {
		if sub.Reason != "" {
			// Listed as unchecked on its own.
			return false, ""
		}
		head, born, err := scanHead(ctx, env, sub.WorkTree)
		if err != nil {
			return false, "reading the HEAD of submodule " + l.path + ": " + err.Error()
		}
		return !born || head != l.id, ""
	}
	p, err := removal.ResolveRecorded(removal.Base{Instance: super.WorkTree}, l.path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, ""
	}
	if err != nil {
		return false, "submodule " + err.Error()
	}
	info, err := os.Lstat(p)
	if err != nil {
		return false, "submodule " + l.path + ": " + err.Error()
	}
	if !info.IsDir() {
		return true, ""
	}
	if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
		return false, "submodule " + l.path + ": a repository the walk did not read"
	} else if !os.IsNotExist(err) {
		return false, "submodule " + l.path + ": " + err.Error()
	}
	return false, ""
}

// gitlink is one gitlink entry of an index: the submodule commit it records,
// its merge stage and its path.
type gitlink struct {
	id, stage, path string
}

// parseGitlinks reads git ls-files -s -z output, "<mode> <id> <stage>\t<path>"
// per NUL-terminated entry, and returns the gitlinks, mode 160000.
func parseGitlinks(out []byte) ([]gitlink, error) {
	var links []gitlink
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || p == "" {
			return nil, fmt.Errorf("unexpected ls-files entry %q", rec)
		}
		if f[0] == "160000" {
			links = append(links, gitlink{id: f[1], stage: f[2], path: p})
		}
	}
	return links, nil
}

// scanHead reads the commit HEAD names in the work tree at dir. born is false
// for a HEAD that names no commit yet.
func scanHead(ctx context.Context, env *removal.Env, dir string) (head string, born bool, err error) {
	cmd, err := env.ScanCommand(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", false, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 && stderr.Len() == 0 {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New(firstLine(stderr.String(), err))
	}
	return strings.TrimSpace(string(out)), true, nil
}

// scanOutput runs one git call the scan's command builder makes in the work
// tree at dir and returns its output. A call that fails returns the first
// line it printed on stderr.
func scanOutput(ctx context.Context, env *removal.Env, dir string, args ...string) ([]byte, error) {
	cmd, err := env.ScanCommand(ctx, dir, nil, args...)
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New(firstLine(stderr.String(), err))
	}
	return out, nil
}

// reportResetCheck prints the check's lists to w and returns the refusal, or
// nil when both are empty.
func reportResetCheck(w io.Writer, dirty, unchecked []string) error {
	if len(dirty) > 0 {
		fmt.Fprintf(w, "Repos with uncommitted changes:\n")
		for _, name := range dirty {
			fmt.Fprintf(w, "  %s\n", name)
		}
	}
	if len(unchecked) > 0 {
		fmt.Fprintf(w, "Repos that could not be checked for uncommitted changes:\n")
		for _, line := range unchecked {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
	switch {
	case len(dirty) > 0 && len(unchecked) > 0:
		return fmt.Errorf("instance has uncommitted changes in %d repo(s) and %d that could not be checked; use --force to override", len(dirty), len(unchecked))
	case len(dirty) > 0:
		return fmt.Errorf("instance has uncommitted changes in %d repo(s); use --force to override", len(dirty))
	case len(unchecked) > 0:
		return fmt.Errorf("instance has %d repo(s) or path(s) that could not be checked for uncommitted changes; use --force to override", len(unchecked))
	}
	return nil
}
