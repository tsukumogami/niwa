package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// The never-wider scenarios dispatch a child from inside a dispatched
// instance, or from the workspace root as a dispatched session, and check the
// child is never granted what its dispatcher doesn't hold. The NIWA_BIN
// scenarios read the settings documents `niwa apply` wrote.

func registerAuthorityNeverWiderSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the newest instance is the dispatch instance$`, theNewestInstanceIsTheDispatchInstance)
	// The refusal step with the message in double quotes, for a message that
	// holds an apostrophe.
	ctx.Step(`^the dispatch was refused at authority resolution with "([^"]*)"$`, theDispatchWasRefusedAtAuthorityResolution)
	ctx.Step(`^every Claude settings document niwa wrote carries NIWA_BIN naming the niwa that wrote it$`, everySettingsDocumentCarriesNiwaBin)
	ctx.Step(`^a hook with no niwa on PATH runs "\$NIWA_BIN" authority check for session "([^"]*)" against the dispatch instance$`, aHookRunsNiwaBinCheck)
}

// theNewestInstanceIsTheDispatchInstance points the dispatch-instance steps at
// the instance created last, whatever made it (`niwa create` among others).
func theNewestInstanceIsTheDispatchInstance(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return ctx, err
	}
	var newest string
	var newestAt time.Time
	for _, e := range entries {
		dir := filepath.Join(s.workspaceRoot, e.Name())
		info, err := os.Stat(filepath.Join(dir, ".niwa", "instance.json"))
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestAt) {
			newest, newestAt = dir, info.ModTime()
		}
	}
	if newest == "" {
		return ctx, fmt.Errorf("no instance in %s", s.workspaceRoot)
	}
	s.lastDispatchInstancePath = newest
	return ctx, nil
}

// settingsDocuments lists every Claude settings document under the workspace
// root.
func settingsDocuments(s *testState) ([]string, error) {
	var docs []string
	err := filepath.Walk(s.workspaceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		base := filepath.Base(path)
		if filepath.Base(filepath.Dir(path)) == ".claude" && (base == "settings.json" || base == "settings.local.json") {
			docs = append(docs, path)
		}
		return nil
	})
	return docs, err
}

func niwaBinOf(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return doc.Env["NIWA_BIN"], nil
}

func everySettingsDocumentCarriesNiwaBin(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	docs, err := settingsDocuments(s)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return fmt.Errorf("niwa wrote no settings documents under %s", s.workspaceRoot)
	}
	want := resolvedPath(s.binPath)
	for _, doc := range docs {
		bin, err := niwaBinOf(doc)
		if err != nil {
			return err
		}
		if bin == "" {
			return fmt.Errorf("%s has no NIWA_BIN in its env", doc)
		}
		if resolvedPath(bin) != want {
			return fmt.Errorf("%s names NIWA_BIN %s, want the niwa that wrote it, %s", doc, bin, want)
		}
		if info, err := os.Stat(bin); err != nil || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("%s names NIWA_BIN %s, which is not an executable", doc, bin)
		}
	}
	return nil
}

// aHookRunsNiwaBinCheck runs what a gate hook would, under a PATH holding
// no niwa: the check through "$NIWA_BIN" as the instance root's settings
// document sets it.
func aHookRunsNiwaBinCheck(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	bin, err := niwaBinOf(filepath.Join(s.lastDispatchInstancePath, ".claude", "settings.json"))
	if err != nil {
		return err
	}
	if bin == "" {
		return fmt.Errorf("the instance root's settings document has no NIWA_BIN")
	}
	hook := `command -v niwa >/dev/null 2>&1 && { echo "niwa is on PATH" >&2; exit 3; }
"$NIWA_BIN" authority check --json --session "$1" -C "$2" --point merge-pr`
	cmd := exec.Command("/bin/sh", "-c", hook, "hook", sessionID, s.lastDispatchInstancePath)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + s.homeDir, "NIWA_BIN=" + bin}
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("the hook failed (%v):\n%s", err, out)
	}
	m, err := checkDoc(string(out))
	if err != nil {
		return err
	}
	if m["state"] != "bound" || !strings.Contains(string(out), `"merge-pr"`) {
		return fmt.Errorf("the hook's check read:\n%s", out)
	}
	return nil
}
