package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// setupVerdictHarness builds a workspace whose repos are pre-cloned (a .git
// marker makes the cloner a no-op) and returns everything Create needs. Each
// entry in scripts maps a repo name to the body of a single setup script
// planted at scripts/setup/01-setup.sh; a repo absent from the map gets no
// setup directory at all.
type setupVerdictHarness struct {
	applier       *Applier
	cfg           *config.WorkspaceConfig
	niwaDir       string
	workspaceRoot string
	instanceName  string
	out           *syncBuffer
}

func newSetupVerdictHarness(t *testing.T, repos []string, scripts map[string]string) *setupVerdictHarness {
	t.Helper()

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configTOML := `
[workspace]
name = "myws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	ghRepos := make([]github.Repo, 0, len(repos))
	for _, name := range repos {
		ghRepos = append(ghRepos, github.Repo{
			Name:       name,
			Visibility: "public",
			SSHURL:     "git@github.com:testorg/" + name + ".git",
		})
	}

	applier := NewApplier(&mockGitHubClient{repos: map[string][]github.Repo{"testorg": ghRepos}})
	applier.Cloner = &Cloner{}

	var out syncBuffer
	applier.Reporter = NewReporterWithTTY(&out, false)

	const instanceName = "myws"
	for _, name := range repos {
		repoDir := filepath.Join(tmpDir, instanceName, "all", name)
		if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		body, ok := scripts[name]
		if !ok {
			continue
		}
		writeSetupScript(t, repoDir, "01-setup.sh", body)
	}

	return &setupVerdictHarness{
		applier:       applier,
		cfg:           loaded.Config,
		niwaDir:       niwaDir,
		workspaceRoot: tmpDir,
		instanceName:  instanceName,
		out:           &out,
	}
}

// TestCreate_SetupFailureIsCountedBelowSummary covers the placement that is
// the substance of the fix: the counted verdict line sits between the summary
// and the deferred warning block, so a setup failure is attached to the
// verdict rather than trailing below it as noise.
func TestCreate_SetupFailureIsCountedBelowSummary(t *testing.T) {
	h := newSetupVerdictHarness(t,
		[]string{"alpha", "beta", "gamma"},
		map[string]string{
			"alpha": "#!/bin/sh\necho alpha is fine\n",
			"beta":  "#!/bin/sh\necho beta is broken >&2\nexit 1\n",
			"gamma": "#!/bin/sh\necho gamma is broken >&2\nexit 1\n",
		})

	instanceRoot, err := h.applier.Create(context.Background(), h.cfg, h.niwaDir, h.workspaceRoot, h.instanceName)
	if err != nil {
		t.Fatalf("Create returned an error for a setup-script failure: %v", err)
	}

	perm := permanentOutput(h.out.String())

	summary := strings.Index(perm, "created myws (3 repos)")
	counted := strings.Index(perm, "setup incomplete for 2 repos: beta, gamma")
	warned := strings.Index(perm, "warning: setup script scripts/setup/01-setup.sh failed for beta")

	if summary < 0 {
		t.Fatalf("summary line missing from:\n%s", perm)
	}
	if counted < 0 {
		t.Fatalf("counted verdict line missing from:\n%s", perm)
	}
	if warned < 0 {
		t.Fatalf("deferred warning missing from:\n%s", perm)
	}
	if !(summary < counted && counted < warned) {
		t.Errorf("wrong order (summary=%d counted=%d warning=%d):\n%s", summary, counted, warned, perm)
	}

	// The failing scripts' own explanations are in the stream too.
	for _, want := range []string{"[beta/01-setup.sh] beta is broken", "[gamma/01-setup.sh] gamma is broken"} {
		if !strings.Contains(perm, want) {
			t.Errorf("missing %q in:\n%s", want, perm)
		}
	}

	// The instance must survive. A setup failure that reached the pipeline's
	// error path would run os.RemoveAll over this directory.
	if _, err := os.Stat(instanceRoot); err != nil {
		t.Fatalf("instance directory did not survive the setup failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(instanceRoot, ".niwa", "instance.json")); err != nil {
		t.Fatalf("instance state did not survive the setup failure: %v", err)
	}
	if _, err := LoadState(instanceRoot); err != nil {
		t.Fatalf("instance state is unreadable after the setup failure: %v", err)
	}
}

// TestCreate_SingleSetupFailureUsesSingularVerdict pins the singular branch,
// which matches the singular/plural branch on the adjacent summary line.
func TestCreate_SingleSetupFailureUsesSingularVerdict(t *testing.T) {
	h := newSetupVerdictHarness(t,
		[]string{"alpha", "beta"},
		map[string]string{
			"alpha": "#!/bin/sh\ntrue\n",
			"beta":  "#!/bin/sh\nexit 1\n",
		})

	if _, err := h.applier.Create(context.Background(), h.cfg, h.niwaDir, h.workspaceRoot, h.instanceName); err != nil {
		t.Fatalf("Create returned an error for a setup-script failure: %v", err)
	}

	perm := permanentOutput(h.out.String())
	if !strings.Contains(perm, "setup incomplete for 1 repo: beta") {
		t.Errorf("expected the singular verdict line in:\n%s", perm)
	}
	if strings.Contains(perm, "1 repos") {
		t.Errorf("singular/plural branch is wrong:\n%s", perm)
	}
}

// TestCreate_NoSetupFailureIsSilent verifies nothing is printed when every
// repo's setup completed — including the repos that have no setup directory
// at all, which must not be counted as failures.
func TestCreate_NoSetupFailureIsSilent(t *testing.T) {
	h := newSetupVerdictHarness(t,
		[]string{"alpha", "beta"},
		map[string]string{"alpha": "#!/bin/sh\ntrue\n"})

	if _, err := h.applier.Create(context.Background(), h.cfg, h.niwaDir, h.workspaceRoot, h.instanceName); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	perm := permanentOutput(h.out.String())
	if strings.Contains(perm, "setup incomplete") {
		t.Errorf("verdict line printed with no failures:\n%s", perm)
	}
}

// TestCreate_MultiErrorRepoIsCountedOnce guards the counting rule: a
// non-executable script is warned about and skipped rather than stopping the
// repo, so one repo can produce several errors and must still be named once.
func TestCreate_MultiErrorRepoIsCountedOnce(t *testing.T) {
	h := newSetupVerdictHarness(t, []string{"beta"}, nil)

	repoDir := filepath.Join(h.workspaceRoot, h.instanceName, "all", "beta")
	setupDir := filepath.Join(repoDir, "scripts", "setup")
	if err := os.MkdirAll(setupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Not executable: warned and skipped, the repo keeps going.
	if err := os.WriteFile(filepath.Join(setupDir, "01-noexec.sh"), []byte("#!/bin/sh\ntrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Executable and failing: a second error for the same repo.
	if err := os.WriteFile(filepath.Join(setupDir, "02-fails.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := h.applier.Create(context.Background(), h.cfg, h.niwaDir, h.workspaceRoot, h.instanceName); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	perm := permanentOutput(h.out.String())
	if !strings.Contains(perm, "setup incomplete for 1 repo: beta") {
		t.Errorf("a repo with two script errors should be counted once:\n%s", perm)
	}
	if strings.Contains(perm, "beta, beta") {
		t.Errorf("repo named twice in the verdict line:\n%s", perm)
	}
}
