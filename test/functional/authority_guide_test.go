package functional

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The authority guide is a contract: gate authors copy its reference checks
// as they are. This file reads the guide itself, never a copy, so a change to
// the guide is what the tests below and the gate-contract scenarios run.

// authorityGuidePath is the guide, relative to this package's directory,
// which is where go test runs a package's tests.
const authorityGuidePath = "../../docs/guides/authority-grants.md"

// The functions the guide's reference block must define.
var authorityGuideFunctions = []string{"niwa_lookup", "niwa_holds", "niwa_grant_decides", "niwa_settles"}

// authorityGuideExit matches an exit command anywhere in the reference block.
// Pasted into a gate hook, `exit` ends the hook before it reports the verdict
// it reached, so the block may only return.
var authorityGuideExit = regexp.MustCompile(`\bexit\b`)

// authorityGuideChecks returns the guide's one fenced bash block that defines
// niwa_grant_decides: the reference checks a gate sources.
func authorityGuideChecks() (string, error) {
	data, err := os.ReadFile(authorityGuidePath)
	if err != nil {
		return "", fmt.Errorf("reading the authority guide: %w", err)
	}
	var blocks []string
	var cur []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case !in && strings.TrimSpace(line) == "```bash":
			in, cur = true, nil
		case in && strings.TrimSpace(line) == "```":
			in = false
			block := strings.Join(cur, "\n") + "\n"
			if strings.Contains(block, "niwa_grant_decides() {") {
				blocks = append(blocks, block)
			}
		case in:
			cur = append(cur, line)
		}
	}
	if len(blocks) != 1 {
		return "", fmt.Errorf("the authority guide has %d bash blocks defining niwa_grant_decides, want exactly 1", len(blocks))
	}
	return blocks[0], nil
}

// checkAuthorityGuideChecks is what every reader of the block relies on: the
// four functions are there and none of them can exit.
func checkAuthorityGuideChecks(block string) error {
	for _, fn := range authorityGuideFunctions {
		if !strings.Contains(block, fn+"() {") {
			return fmt.Errorf("the guide's reference block does not define %s", fn)
		}
	}
	for i, line := range strings.Split(block, "\n") {
		code := line
		if j := strings.Index(code, "#"); j >= 0 {
			code = code[:j]
		}
		if authorityGuideExit.MatchString(code) {
			return fmt.Errorf("the guide's reference block uses exit on line %d: %q; its functions must return, never exit", i+1, strings.TrimSpace(line))
		}
	}
	return nil
}

func TestAuthorityGuideReferenceChecksOnlyReturn(t *testing.T) {
	block, err := authorityGuideChecks()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkAuthorityGuideChecks(block); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityGuideExitCheckCatchesAnExit(t *testing.T) {
	block, err := authorityGuideChecks()
	if err != nil {
		t.Fatal(err)
	}
	altered := strings.Replace(block, "|| return 1", "|| exit 1", 1)
	if altered == block {
		t.Fatal("the reference block has no `|| return 1` to alter")
	}
	if err := checkAuthorityGuideChecks(altered); err == nil {
		t.Fatal("a reference block that exits passed the check")
	}
}

// The R8a example the guide carries, verbatim.
const authorityGuideR8a = "```toml\n" + `[authority]
level = "autonomous"
ceiling = ["merge-pr", "close-issue", "cut-release"]

[authority.roles.coordinator]
authorities = ["merge-pr", "close-issue", "cut-release"]
` + "```\n"

func TestAuthorityGuideNamesTheContract(t *testing.T) {
	data, err := os.ReadFile(authorityGuidePath)
	if err != nil {
		t.Fatal(err)
	}
	guide := string(data)
	for _, want := range []string{
		"[authority]", "authority_level", "authority_ceiling",
		"--level", "--grant", "--role", "--parent-from",
		"merge-pr", "close-issue", "cut-release",
		"niwa.authority.record/v1", "dispatch record: none", "decider=session",
		"recorded, not enforced", "grant_ceiling_source",
		authorityGuideR8a,
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("the authority guide lacks %q", want)
		}
	}
	// The guide references only durable paths.
	if strings.Contains(guide, "wip/") {
		t.Error("the authority guide references a wip/ path")
	}
}

func TestClaudeMDListsTheAuthorityGuide(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- `docs/guides/authority-grants.md` — ") {
		t.Error("CLAUDE.md's Contributor Guides has no entry for docs/guides/authority-grants.md")
	}
}
