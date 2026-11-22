package functional

// Steps for features/mirror-remote-identity.feature: one sandbox remote
// reached under several spellings, a URL that sends git to the same bare
// repo whatever its scheme or user, and what the mirror records and logs
// when the config moves between them or to another remote. Like the steps
// beside them they read the store, the state file and the mirror log on disk
// rather than importing niwa's mirror package.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"
)

func registerMirrorIdentitySteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the sandbox git config sends "([^"]*)" to source repo "([^"]*)"$`, theSandboxGitConfigSends)
	ctx.Step(`^a source repo "([^"]*)" is a copy of source repo "([^"]*)"$`, aSourceRepoIsACopy)
	ctx.Step(`^the state file of "([^"]*)" records the identity "([^"]*)"$`, theStateFileRecordsTheIdentity)
	ctx.Step(`^the mirror of "([^"]*)" has the remote URL "([^"]*)"$`, theMirrorHasTheLiteralRemoteURL)
	ctx.Step(`^the restore line the mirror log gained for "([^"]*)" salvaged from the remembered store$`, theRestoreLineSalvagedFromTheRememberedStore)
	ctx.Step(`^the restore line the mirror log gained for "([^"]*)" salvaged from no store$`, theRestoreLineSalvagedNothing)
}

// theSandboxGitConfigSends adds an insteadOf rewrite of from to the source
// repo's file:// URL in the sandbox home's git config. Every git niwa runs
// that reads the user's global config, its clones and the mirror's network
// calls among them, then reaches that bare repo under from, with no network.
func theSandboxGitConfigSends(ctx context.Context, from, source string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	to, ok := s.repoURLs[source]
	if !ok {
		return ctx, fmt.Errorf("no source repo %q", source)
	}
	f, err := os.OpenFile(filepath.Join(s.homeDir, ".gitconfig"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return ctx, err
	}
	_, err = fmt.Fprintf(f, "[url %q]\n\tinsteadOf = %s\n", to, from)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return ctx, err
}

// aSourceRepoIsACopy makes another bare repo on the local git server holding
// everything source does: a different remote with the same history, so
// borrowers keep reading after the mirror moves to it.
func aSourceRepoIsACopy(ctx context.Context, name, source string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	from, ok := s.repoURLs[source]
	if !ok {
		return ctx, fmt.Errorf("no source repo %q", source)
	}
	path := filepath.Join(s.gitServer.root, name+".git")
	if out, err := fixtureGit(s.gitServer.root, "clone", "--quiet", "--bare", from, path); err != nil {
		return ctx, fmt.Errorf("copying %s to %s: %w\n%s", source, name, err, out)
	}
	s.repoURLs[name] = "file://" + path
	return ctx, nil
}

func theStateFileRecordsTheIdentity(ctx context.Context, repoID, want string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	path, err := mirrorStatePath(s, repoID)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var st struct {
		URL      string `json:"url"`
		Identity string `json:"identity"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("decoding the state file: %w\n%s", err, data)
	}
	if st.Identity != want {
		return fmt.Errorf("the state file's identity = %q (url %q), want %q", st.Identity, st.URL, want)
	}
	return nil
}

func theMirrorHasTheLiteralRemoteURL(ctx context.Context, repoID, want string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return err
	}
	out, err := fixtureGitBare(store, "config", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("reading remote.origin.url: %w\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		return fmt.Errorf("the mirror's remote.origin.url = %q, want %q", got, want)
	}
	return nil
}

// gainedRestoreLine is the one restore line for repoID among the lines the
// mirror log gained since the last count.
func gainedRestoreLine(ctx context.Context, repoID string) (map[string]any, *mirrorScenario, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return nil, nil, err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return nil, nil, err
	}
	var found []map[string]any
	for _, line := range lines {
		if line["event"] == "restore" && line["mirror"] == repoID {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		return nil, nil, fmt.Errorf("the mirror log gained %d restore lines for %q, want 1: %v", len(found), repoID, lines)
	}
	return found[0], m, nil
}

// theRestoreLineSalvagedFromTheRememberedStore checks that the restore's
// line names the store the link pointed at before it, and only that, as
// the one its new store was salvaged from.
func theRestoreLineSalvagedFromTheRememberedStore(ctx context.Context, repoID string) error {
	line, m, err := gainedRestoreLine(ctx, repoID)
	if err != nil {
		return err
	}
	if m.link == "" {
		return fmt.Errorf("no mirror link was remembered")
	}
	raw, ok := line["salvaged_from"].([]any)
	if !ok {
		return fmt.Errorf("the restore line %v has no salvaged_from list", line)
	}
	var from []string
	for _, v := range raw {
		from = append(from, fmt.Sprint(v))
	}
	if !slices.Equal(from, []string{m.link}) {
		return fmt.Errorf("the restore line salvaged from %v, want only the remembered store %q", from, m.link)
	}
	return nil
}

func theRestoreLineSalvagedNothing(ctx context.Context, repoID string) error {
	line, _, err := gainedRestoreLine(ctx, repoID)
	if err != nil {
		return err
	}
	if from, ok := line["salvaged_from"]; ok {
		return fmt.Errorf("the restore line salvaged from %v, want no salvage", from)
	}
	return nil
}
