package workspace

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/secret"
)

// reportOf runs the post-merge walk and returns "cause|scope|key|level" for
// each entry, which is enough to pin what the report says without pinning how
// it is rendered.
func reportOf(cfg *config.WorkspaceConfig) []string {
	c := keyreport.New()
	collectUnresolvedKeys(cfg, c)
	var out []string
	for _, e := range c.Report() {
		out = append(out, string(e.Cause)+"|"+e.Scope+"|"+e.Key+"|"+string(e.Level))
	}
	return out
}

// TestCollectUnresolvedKeysReportsDeclaredKeyWithNoValue is the case the whole
// feature exists for: a key declared under a requirement sub-table with no
// entry in the values map at all. Nothing referenced it, so the resolver's
// walker never visited it and there is no mark anywhere. A report assembled
// only from marks comes back empty here.
func TestCollectUnresolvedKeysReportsDeclaredKeyWithNoValue(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values:   map[string]config.MaybeSecret{},
				Required: map[string]string{"GITHUB_TOKEN": "GitHub PAT with repo:read scope"},
			},
		},
	}

	got := reportOf(cfg)
	want := []string{"no-source|env.secrets|GITHUB_TOKEN|required"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("report = %v, want %v", got, want)
	}

	c := keyreport.New()
	collectUnresolvedKeys(cfg, c)
	if desc := c.Report()[0].Description; desc != "GitHub PAT with repo:read scope" {
		t.Errorf("description = %q; it is the only guidance the no-source message can carry", desc)
	}
}

// TestCollectUnresolvedKeysCoversEveryDeclaredLevel: the report carries a
// declared-level column, so the walk covers optional as well as required and
// recommended. Nothing else in the codebase walks the optional sub-table, which
// is exactly why it is easy to leave out.
func TestCollectUnresolvedKeysCoversEveryDeclaredLevel(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values:      map[string]config.MaybeSecret{},
				Required:    map[string]string{"REQ": "required key"},
				Recommended: map[string]string{"REC": "recommended key"},
				Optional:    map[string]string{"OPT": "optional key"},
			},
		},
	}

	got := strings.Join(reportOf(cfg), "\n")
	for _, want := range []string{
		"no-source|env.secrets|REQ|required",
		"no-source|env.secrets|REC|recommended",
		"no-source|env.secrets|OPT|optional",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
}

// TestCollectUnresolvedKeysReadsMarks: the second source. A value that the
// resolver marked carries its own cause, level, description and provider kind,
// and the walk must prefer those over anything it could infer.
func TestCollectUnresolvedKeysReadsMarks(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"GH": {Unresolved: &config.Unresolved{
						Cause:        config.CauseProviderUnreachable,
						Level:        config.LevelRequired,
						Description:  "GitHub PAT",
						ProviderKind: "fake",
					}},
				},
				Required: map[string]string{"GH": "GitHub PAT"},
			},
		},
	}

	c := keyreport.New()
	collectUnresolvedKeys(cfg, c)
	got := c.Report()
	if len(got) != 1 {
		t.Fatalf("report = %v, want exactly one entry (the mark, not a second no-source record)", got)
	}
	if got[0].Cause != config.CauseProviderUnreachable {
		t.Errorf("cause = %q, want the mark's cause", got[0].Cause)
	}
	if got[0].ProviderKind != "fake" {
		t.Errorf("provider kind = %q, want the mark's kind; the unreachable message names it", got[0].ProviderKind)
	}
}

// TestCollectUnresolvedKeysIgnoresDeliberateEmpties pins the silent-downgrade
// contract at the report boundary. A value present but empty with no mark is
// either an author's empty literal or a ?required=false reference that opted
// out of resolution failure. Neither is a shortfall, and naming either in the
// report would break the silence opt-out exists to provide.
func TestCollectUnresolvedKeysIgnoresDeliberateEmpties(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"OPTED_OUT": {},
					"SUPPLIED":  {Secret: secret.New([]byte("v"), secret.Origin{Key: "SUPPLIED"})},
				},
				Required: map[string]string{"OPTED_OUT": "opted out", "SUPPLIED": "supplied"},
			},
		},
	}

	if got := reportOf(cfg); len(got) != 0 {
		t.Errorf("report = %v, want empty", got)
	}
}

// TestCollectUnresolvedKeysCoversSettings: settings keys can carry a mark too,
// and they have no requirement sub-tables, so they arrive with no declared
// level. Leaving them out would make the report claim completeness it does not
// have.
func TestCollectUnresolvedKeysCoversSettings(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Settings: config.SettingsConfig{
				"apiKeyHelper": {Unresolved: &config.Unresolved{Cause: config.CauseUndeclaredProvider}},
			},
		},
	}

	got := reportOf(cfg)
	want := "undeclared-provider|claude.settings|apiKeyHelper|"
	if len(got) != 1 || got[0] != want {
		t.Errorf("report = %v, want [%s]", got, want)
	}
}

// TestCollectUnresolvedKeysWalksEveryScope: repo and instance overrides declare
// keys too, and a scope the walk forgets is a key the user never hears about.
func TestCollectUnresolvedKeysWalksEveryScope(t *testing.T) {
	declared := func(key string) config.EnvVarsTable {
		return config.EnvVarsTable{
			Values:   map[string]config.MaybeSecret{},
			Required: map[string]string{key: "why " + key + " is needed"},
		}
	}
	cfg := &config.WorkspaceConfig{
		Env:    config.EnvConfig{Secrets: declared("TOP")},
		Claude: config.ClaudeConfig{Env: config.ClaudeEnvConfig{Vars: declared("CLAUDE_TOP")}},
		Repos: map[string]config.RepoOverride{
			"app": {Env: config.EnvConfig{Secrets: declared("REPO")}},
		},
		Instance: config.InstanceConfig{Env: config.EnvConfig{Vars: declared("INST")}},
	}

	got := strings.Join(reportOf(cfg), "\n")
	for _, want := range []string{
		"|env.secrets|TOP|",
		"|claude.env.vars|CLAUDE_TOP|",
		"|repos.app.env.secrets|REPO|",
		"|instance.env.vars|INST|",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
}

// TestCollectUnresolvedKeysTolerantOfNils keeps the two call sites in
// ResolveAndMergeEffectiveConfig free of guards: the worktree path passes no
// collector at all.
func TestCollectUnresolvedKeysTolerantOfNils(t *testing.T) {
	collectUnresolvedKeys(nil, keyreport.New())
	collectUnresolvedKeys(&config.WorkspaceConfig{}, nil)
}
