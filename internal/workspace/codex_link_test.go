package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/gitexclude"
)

// codexLinkFixture lays down an instance whose payload is already written (the
// shape InstallCodexPayload produces: a marked config plus one skills link into
// a plugin tree) and one cloned repository under it. It returns the instance
// root and the repository directory.
func codexLinkFixture(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	instanceRoot := filepath.Join(root, "ws")
	payloadDir := filepath.Join(instanceRoot, CodexPayloadDirName)
	if err := os.MkdirAll(filepath.Join(payloadDir, codexPayloadSkillsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payloadDir, codexPayloadConfigName),
		[]byte(renderCodexPayloadConfig(131072)), 0o644); err != nil {
		t.Fatal(err)
	}

	// A plugin tree outside the instance, linked into the payload's skills
	// directory exactly as the payload writer links one.
	pluginRoot := filepath.Join(root, "plugins", "demo")
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills", "greet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "skills", "greet", "SKILL.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pluginRoot, filepath.Join(payloadDir, codexPayloadSkillsDirName, "demo")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	repoDir := filepath.Join(instanceRoot, "tools", "app")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return instanceRoot, repoDir
}

func TestInstallRepoCodexLink_DeliversPayloadAndIsIdempotent(t *testing.T) {
	instanceRoot, repoDir := codexLinkFixture(t)
	linkPath := filepath.Join(repoDir, CodexPayloadDirName)

	result, err := InstallRepoCodexLink(instanceRoot, repoDir)
	if err != nil {
		t.Fatalf("InstallRepoCodexLink: %v", err)
	}
	if result.Path != linkPath || result.Copied || result.Foreign {
		t.Fatalf("result = %+v, want a symlink at %s", result, linkPath)
	}

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected a symlink at %s, got mode %v", linkPath, info.Mode())
	}

	// The payload is reachable through the link: config and skills alike, which
	// is the whole point of planting it beside the repository's .git.
	data, err := os.ReadFile(filepath.Join(linkPath, codexPayloadConfigName))
	if err != nil {
		t.Fatalf("reading config through the link: %v", err)
	}
	if !strings.Contains(string(data), "project_doc_max_bytes") {
		t.Errorf("payload config not reachable through the link, got:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(linkPath, codexPayloadSkillsDirName, "demo", "skills", "greet", "SKILL.md")); err != nil {
		t.Errorf("skills not reachable through the link: %v", err)
	}

	// Three applies leave one link, unchanged.
	for i := 0; i < 2; i++ {
		again, err := InstallRepoCodexLink(instanceRoot, repoDir)
		if err != nil {
			t.Fatalf("re-apply %d: %v", i, err)
		}
		if again.Path != linkPath || again.Copied || again.Foreign {
			t.Errorf("re-apply %d: result = %+v", i, again)
		}
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != filepath.Join(instanceRoot, CodexPayloadDirName) {
		t.Errorf("link target = %q, want the instance payload", target)
	}
}

func TestInstallRepoCodexLink_RepairsDeletedAndStaleLinks(t *testing.T) {
	instanceRoot, repoDir := codexLinkFixture(t)
	linkPath := filepath.Join(repoDir, CodexPayloadDirName)

	if _, err := InstallRepoCodexLink(instanceRoot, repoDir); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// A developer deletes the link.
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallRepoCodexLink(instanceRoot, repoDir); err != nil {
		t.Fatalf("apply after delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(linkPath, codexPayloadConfigName)); err != nil {
		t.Errorf("deleted link was not restored: %v", err)
	}

	// A link left pointing at a payload that is no longer this instance's --
	// the instance moved, or an older delivery survived a rename.
	stale := filepath.Join(t.TempDir(), "old-instance", CodexPayloadDirName)
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stale, linkPath); err != nil {
		t.Fatal(err)
	}

	result, err := InstallRepoCodexLink(instanceRoot, repoDir)
	if err != nil {
		t.Fatalf("apply after retarget: %v", err)
	}
	if result.Foreign {
		t.Fatalf("a stale niwa link must be repaired, not refused: %+v", result)
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(instanceRoot, CodexPayloadDirName) {
		t.Errorf("link target = %q, want the current instance payload", target)
	}
}

func TestInstallRepoCodexLink_LeavesForeignEntriesAlone(t *testing.T) {
	t.Run("committed directory", func(t *testing.T) {
		instanceRoot, repoDir := codexLinkFixture(t)
		own := filepath.Join(repoDir, CodexPayloadDirName)
		if err := os.MkdirAll(own, 0o755); err != nil {
			t.Fatal(err)
		}
		// A repository's own .codex config -- a real Codex convention, with no
		// niwa generation marker.
		if err := os.WriteFile(filepath.Join(own, codexPayloadConfigName), []byte("model = \"o3\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result, err := InstallRepoCodexLink(instanceRoot, repoDir)
		if err != nil {
			t.Fatalf("InstallRepoCodexLink: %v", err)
		}
		if !result.Foreign || result.Path != "" {
			t.Fatalf("result = %+v, want a refusal with nothing written", result)
		}
		data, err := os.ReadFile(filepath.Join(own, codexPayloadConfigName))
		if err != nil || string(data) != "model = \"o3\"\n" {
			t.Errorf("the repository's own config was modified: %q (%v)", data, err)
		}
	})

	t.Run("regular file", func(t *testing.T) {
		instanceRoot, repoDir := codexLinkFixture(t)
		own := filepath.Join(repoDir, CodexPayloadDirName)
		if err := os.WriteFile(own, []byte("not niwa's\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result, err := InstallRepoCodexLink(instanceRoot, repoDir)
		if err != nil {
			t.Fatalf("InstallRepoCodexLink: %v", err)
		}
		if !result.Foreign {
			t.Fatalf("result = %+v, want a refusal", result)
		}
		data, err := os.ReadFile(own)
		if err != nil || string(data) != "not niwa's\n" {
			t.Errorf("the repository's own file was modified: %q (%v)", data, err)
		}
	})
}

func TestInstallRepoCodexLink_CopyFallback(t *testing.T) {
	instanceRoot, repoDir := codexLinkFixture(t)
	forceCodexCopyFallback(t)
	copyPath := filepath.Join(repoDir, CodexPayloadDirName)

	result, err := InstallRepoCodexLink(instanceRoot, repoDir)
	if err != nil {
		t.Fatalf("InstallRepoCodexLink: %v", err)
	}
	if !result.Copied || result.Path != copyPath {
		t.Fatalf("result = %+v, want a copy at %s", result, copyPath)
	}

	info, err := os.Lstat(copyPath)
	if err != nil {
		t.Fatalf("lstat copy: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("expected a real directory, got mode %v", info.Mode())
	}

	// The skills link is followed, not reproduced: the fallback exists where
	// links are unavailable, so a copy full of links would deliver nothing.
	skill := filepath.Join(copyPath, codexPayloadSkillsDirName, "demo", "skills", "greet", "SKILL.md")
	skillInfo, err := os.Lstat(skill)
	if err != nil {
		t.Fatalf("skill missing from the copy: %v", err)
	}
	if !skillInfo.Mode().IsRegular() {
		t.Errorf("expected a real file in the copy, got mode %v", skillInfo.Mode())
	}

	// A second apply recognizes its own copy by the payload's generation marker
	// and refreshes it, so content a prior apply left behind does not survive.
	stray := filepath.Join(copyPath, codexPayloadSkillsDirName, "gone")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	again, err := InstallRepoCodexLink(instanceRoot, repoDir)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if again.Foreign || !again.Copied {
		t.Fatalf("second apply = %+v, want niwa's own copy refreshed", again)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("stale entry survived the refresh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(copyPath, codexPayloadConfigName)); err != nil {
		t.Errorf("payload config missing after refresh: %v", err)
	}
}

func TestInstallRepoCodexLink_CopyFallbackTerminatesOnLinkCycle(t *testing.T) {
	instanceRoot, repoDir := codexLinkFixture(t)
	payloadDir := filepath.Join(instanceRoot, CodexPayloadDirName)
	if err := os.Symlink(payloadDir, filepath.Join(payloadDir, codexPayloadSkillsDirName, "loop")); err != nil {
		t.Fatal(err)
	}
	forceCodexCopyFallback(t)

	if _, err := InstallRepoCodexLink(instanceRoot, repoDir); err != nil {
		t.Fatalf("InstallRepoCodexLink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, CodexPayloadDirName, codexPayloadConfigName)); err != nil {
		t.Errorf("payload config missing after a cycle-bounded copy: %v", err)
	}
}

// TestCodexDelivery_LeavesGitStatusClean is the decisive check for R11: with
// everything niwa writes for Codex present in a real repository, git reports
// nothing. It asserts on git's own output rather than on the exclude file's
// text, because a trailing-slash pattern would write a plausible-looking line
// and still leave "?? .codex" in every repository forever.
func TestCodexDelivery_LeavesGitStatusClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	for _, mode := range []string{"symlink", "copy"} {
		t.Run(mode, func(t *testing.T) {
			instanceRoot, repoDir := codexLinkFixture(t)
			if mode == "copy" {
				forceCodexCopyFallback(t)
			}
			runGitWT(t, repoDir, "init")

			if _, err := InstallRepoCodexLink(instanceRoot, repoDir); err != nil {
				t.Fatalf("InstallRepoCodexLink: %v", err)
			}
			// The composed override issue 4 writes, at the name it writes.
			if err := os.WriteFile(filepath.Join(repoDir, CodexOverrideFileName),
				[]byte(CodexGenerationMarker+"\n\ncontext\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			// Three applies, as the pipeline would run them.
			for i := 0; i < 3; i++ {
				if _, err := InstallRepoCodexLink(instanceRoot, repoDir); err != nil {
					t.Fatalf("re-delivery %d: %v", i, err)
				}
				if err := gitexclude.EnsureRepoExclude(repoDir); err != nil {
					t.Fatalf("EnsureRepoExclude %d: %v", i, err)
				}
			}

			if out := gitStatusPorcelainWT(t, repoDir); out != "" {
				t.Fatalf("expected a clean working tree, got:\n%s", out)
			}

			// One managed block, however many applies ran.
			excludeFile, err := os.ReadFile(filepath.Join(repoDir, ".git", "info", "exclude"))
			if err != nil {
				t.Fatalf("reading the exclude file: %v", err)
			}
			if n := strings.Count(string(excludeFile), "# >>> niwa managed >>>"); n != 1 {
				t.Errorf("expected exactly one managed block after three applies, found %d:\n%s", n, excludeFile)
			}

			// The coverage is scoped, not blanket: a file niwa did not write
			// still shows, so the assertion above means something.
			if err := os.WriteFile(filepath.Join(repoDir, "leak.txt"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if out := gitStatusPorcelainWT(t, repoDir); !strings.Contains(out, "leak.txt") {
				t.Errorf("expected an uncovered file to show in status, got:\n%s", out)
			}
		})
	}
}

// TestCreate_DeliversCodexPayloadIntoEveryRepo covers the wiring: a plain
// create, with the workspace defaulting to Claude, still plants the link --
// preparation is for both agents whatever default_agent says.
func TestCreate_DeliversCodexPayloadIntoEveryRepo(t *testing.T) {
	instanceRoot := createDualAgentInstance(t, "claude")
	linkPath := filepath.Join(instanceRoot, "tools", "app", CodexPayloadDirName)

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("no Codex delivery in the cloned repo: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected a symlink at %s, got mode %v", linkPath, info.Mode())
	}

	data, err := os.ReadFile(filepath.Join(linkPath, codexPayloadConfigName))
	if err != nil {
		t.Fatalf("payload not reachable from the repo: %v", err)
	}
	if !strings.Contains(string(data), "project_doc_max_bytes") {
		t.Errorf("payload config reached through the link has no budget:\n%s", data)
	}
}

// forceCodexCopyFallback makes the delivery take the copy branch for one test,
// so the fallback is exercised on a platform where symlinks work.
func forceCodexCopyFallback(t *testing.T) {
	t.Helper()
	prior := codexLinkPrefersCopy
	codexLinkPrefersCopy = func() bool { return true }
	t.Cleanup(func() { codexLinkPrefersCopy = prior })
}
