package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
)

// These tests never read or write a real ~/.codex. Every one of them either
// passes a scratch directory as the developer home or points the agent's own
// directory override at one.

// mcpConfigOf builds a workspace config declaring one server.
func mcpConfigOf(name string, srv config.MCPServerConfig) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{name: srv}},
	}
}

// TestServersResolveToLiteralValues checks the one property everything
// downstream rests on: what leaves this package is already resolved, so nothing
// generated relies on an agent expanding anything.
func TestServersResolveToLiteralValues(t *testing.T) {
	cfg := mcpConfigOf("files", config.MCPServerConfig{
		Command: "npx",
		Args:    []string{"-y", "server"},
		Env: map[string]config.MaybeSecret{
			"PLAIN":  {Plain: "value"},
			"SECRET": {Secret: secret.New([]byte("s3cr3t"), secret.Origin{Key: "SECRET"})},
		},
	})

	servers, warnings := MCPServersFromConfig(cfg)
	if len(servers) != 1 {
		t.Fatalf("resolved %d servers, want 1", len(servers))
	}
	if len(warnings) != 0 {
		t.Errorf("resolving plain and resolved values reported %v", warnings)
	}
	if got := servers[0].Env["PLAIN"]; got != "value" {
		t.Errorf("PLAIN = %q, want value", got)
	}
	if got := servers[0].Env["SECRET"]; got != "s3cr3t" {
		t.Errorf("SECRET did not resolve to its plaintext, got %q", got)
	}
}

// TestUnresolvedValuesAreLeftOutAndReported keeps a credential that never
// resolved from being written as an empty string or as the reference it still
// is. Both would reach a session, and only one of the two failure modes even
// names itself.
func TestUnresolvedValuesAreLeftOutAndReported(t *testing.T) {
	cfg := mcpConfigOf("files", config.MCPServerConfig{
		Command: "server",
		Env: map[string]config.MaybeSecret{
			"MARKED":  {Unresolved: &config.Unresolved{Cause: config.UnresolvedCause("provider unreachable")}},
			"LITERAL": {Plain: "vault://team/api-key"},
			"KEPT":    {Plain: "fine"},
		},
	})

	servers, warnings := MCPServersFromConfig(cfg)
	if len(servers) != 1 {
		t.Fatalf("resolved %d servers, want 1", len(servers))
	}
	env := servers[0].Env
	if _, present := env["MARKED"]; present {
		t.Error("a value the resolver could not supply was written anyway")
	}
	if _, present := env["LITERAL"]; present {
		t.Error("an unresolved vault reference was written as a literal")
	}
	if env["KEPT"] != "fine" {
		t.Errorf("KEPT = %q, want fine", env["KEPT"])
	}
	if len(warnings) != 2 {
		t.Fatalf("reported %d omissions, want 2: %v", len(warnings), warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w, "mcp.servers.files.env.") {
			t.Errorf("an omission report does not name the key it dropped: %q", w)
		}
	}
}

// writeDeveloperConfig plants a config file at the path the spec resolves to
// under home.
func writeDeveloperConfig(t *testing.T, home, content string) string {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// TestDeveloperNamesAreReadReadOnly covers the collision read in every shape it
// can find the developer's file in. The read never fails an apply: what a
// missing, unreadable, or malformed file costs is the collision check itself,
// and that is reported rather than silently skipped.
func TestDeveloperNamesAreReadReadOnly(t *testing.T) {
	spec := agentplan.For(agent.AgentCodex).MCPCollisionSpec()

	t.Run("absent", func(t *testing.T) {
		names, warning := ReadDeclaredMCPNames(t.TempDir(), spec)
		if len(names) != 0 || warning != "" {
			t.Errorf("an absent config produced names %v and warning %q", names, warning)
		}
	})

	t.Run("defined names", func(t *testing.T) {
		home := t.TempDir()
		writeDeveloperConfig(t, home, "[mcp_servers.files]\ncommand = \"mine\"\n\n[mcp_servers.other]\ncommand = \"mine\"\n")
		names, warning := ReadDeclaredMCPNames(home, spec)
		if warning != "" {
			t.Errorf("a readable config reported %q", warning)
		}
		if len(names) != 2 || names[0] != "files" || names[1] != "other" {
			t.Errorf("names = %v, want [files other]", names)
		}
	})

	t.Run("malformed degrades to a report", func(t *testing.T) {
		home := t.TempDir()
		path := writeDeveloperConfig(t, home, "[mcp_servers.files\ncommand = \"mine\"\n")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the fixture: %v", err)
		}

		names, warning := ReadDeclaredMCPNames(home, spec)
		if len(names) != 0 {
			t.Errorf("a malformed config produced names %v", names)
		}
		if !strings.Contains(warning, path) {
			t.Errorf("the degraded read is not reported with its path: %q", warning)
		}

		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Error("the developer's own config was modified by a read-only check")
		}
	})

	t.Run("table of the wrong shape", func(t *testing.T) {
		home := t.TempDir()
		writeDeveloperConfig(t, home, "mcp_servers = \"nonsense\"\n")
		names, warning := ReadDeclaredMCPNames(home, spec)
		if len(names) != 0 || warning == "" {
			t.Errorf("names = %v, warning = %q; want no names and a report", names, warning)
		}
	})

	t.Run("the agent's own directory override wins", func(t *testing.T) {
		home := t.TempDir()
		writeDeveloperConfig(t, home, "[mcp_servers.from-home]\ncommand = \"mine\"\n")

		override := t.TempDir()
		if err := os.WriteFile(filepath.Join(override, "config.toml"), []byte("[mcp_servers.from-override]\ncommand = \"mine\"\n"), 0o600); err != nil {
			t.Fatalf("writing the override config: %v", err)
		}
		t.Setenv(spec.ConfigDirEnv, override)

		names, warning := ReadDeclaredMCPNames(home, spec)
		if warning != "" {
			t.Errorf("reported %q", warning)
		}
		if len(names) != 1 || names[0] != "from-override" {
			t.Errorf("names = %v, want [from-override]: the file a session reads is the one to check", names)
		}
	})

	t.Run("an agent with no such layer reads nothing", func(t *testing.T) {
		home := t.TempDir()
		writeDeveloperConfig(t, home, "[mcp_servers.files]\ncommand = \"mine\"\n")
		names, warning := ReadDeclaredMCPNames(home, agentplan.For(agent.AgentClaude).MCPCollisionSpec())
		if len(names) != 0 || warning != "" {
			t.Errorf("names = %v, warning = %q; a zero spec asks for nothing", names, warning)
		}
	})
}

// codexServers is the declaration the install tests generate from.
func codexServers() []agentplan.MCPServer {
	return []agentplan.MCPServer{{
		Name:    "files",
		Command: "npx",
		Args:    []string{"-y", "server"},
		Env:     map[string]string{"TOKEN": "abc"},
	}}
}

// TestInstallWritesTheGeneratedConfiguration is the end-to-end write: the file
// lands where the producer said, at the mode the producer said, with the
// exclude pattern that keeps it from making the tree read dirty -- and a second
// apply changes nothing.
func TestInstallWritesTheGeneratedConfiguration(t *testing.T) {
	repo := t.TempDir()
	producer := agentplan.For(agent.AgentCodex)

	install, err := InstallMCPConfig(agentplan.MCPInRepo, repo, codexServers(), nil, producer)
	if err != nil {
		t.Fatalf("InstallMCPConfig: %v", err)
	}
	path := filepath.Join(repo, ".codex", "config.toml")
	if len(install.Written) != 1 || install.Written[0] != path {
		t.Fatalf("wrote %v, want [%s]", install.Written, path)
	}
	if len(install.Excludes) != 1 || install.Excludes[0] != ".codex/config.toml" {
		t.Errorf("excludes = %v, want [.codex/config.toml]", install.Excludes)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600: the generated file can carry resolved secret material", perm)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(first), "[mcp_servers.files]") {
		t.Errorf("the generated file does not define the declared server:\n%s", first)
	}

	if _, err := InstallMCPConfig(agentplan.MCPInRepo, repo, codexServers(), nil, producer); err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading %s: %v", path, err)
	}
	if string(second) != string(first) {
		t.Error("re-applying rewrote the generated file with different bytes")
	}
}

// TestValidationFailureWritesNoFile is the property the measured whole-config
// failure demands: a declaration niwa cannot express leaves nothing behind at
// all, so a session that loaded fine before still loads fine after.
func TestValidationFailureWritesNoFile(t *testing.T) {
	repo := t.TempDir()
	servers := []agentplan.MCPServer{
		{Name: "ok", Command: "server"},
		{Name: "streamed", Transport: agentplan.MCPTransportSSE, URL: "https://h/mcp"},
	}

	if _, err := InstallMCPConfig(agentplan.MCPInRepo, repo, servers, nil, agentplan.For(agent.AgentCodex)); err == nil {
		t.Fatal("an unmappable declaration installed without an error")
	}
	if _, err := os.Stat(filepath.Join(repo, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("a validation failure left a file behind (stat error: %v); one malformed entry fails the whole config load", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".codex")); !os.IsNotExist(err) {
		t.Error("a validation failure created the configuration directory")
	}
}

// TestCollisionFailsTheInstall wires the collision read to the generation: a
// name the developer's own configuration defines stops the write rather than
// merging into a server neither definition describes.
func TestCollisionFailsTheInstall(t *testing.T) {
	home := t.TempDir()
	writeDeveloperConfig(t, home, "[mcp_servers.files]\ncommand = \"mine\"\nargs = [\"--theirs\"]\n")

	producer := agentplan.For(agent.AgentCodex)
	existing, warning := ReadDeclaredMCPNames(home, producer.MCPCollisionSpec())
	if warning != "" {
		t.Fatalf("the collision read reported %q", warning)
	}

	repo := t.TempDir()
	_, err := InstallMCPConfig(agentplan.MCPInRepo, repo, codexServers(), existing, producer)
	if err == nil {
		t.Fatal("a colliding name installed without an error")
	}
	if !strings.Contains(err.Error(), "files") {
		t.Errorf("the error does not name the colliding server: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".codex", "config.toml")); !os.IsNotExist(statErr) {
		t.Error("a refused collision still wrote a configuration")
	}
}

// TestARepositorysOwnConfigurationIsLeftAlone is the ownership rule at the
// write: a committed file at niwa's name keeps its bytes, the refusal is
// reported, and the path is exempted so the cleanup pass cannot undo the
// refusal.
func TestARepositorysOwnConfigurationIsLeftAlone(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	path := filepath.Join(dir, "config.toml")
	const theirs = "# the repository's own project layer\nmodel = \"o3\"\n"
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	install, err := InstallMCPConfig(agentplan.MCPInRepo, repo, codexServers(), nil, agentplan.For(agent.AgentCodex))
	if err != nil {
		t.Fatalf("InstallMCPConfig: %v", err)
	}
	if len(install.Written) != 0 {
		t.Errorf("wrote %v over a file niwa did not write", install.Written)
	}
	if len(install.Exempt) != 1 || install.Exempt[0] != path {
		t.Errorf("exempt = %v, want [%s]", install.Exempt, path)
	}
	if len(install.Warnings) != 1 || !strings.Contains(install.Warnings[0], path) {
		t.Errorf("the refusal is not reported with its path: %v", install.Warnings)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != theirs {
		t.Errorf("the repository's own file was modified: %q", after)
	}
}

// TestGeneratedConfigurationIsRefreshedInPlace is the other half of the
// ownership rule: niwa's own prior document carries its marker, so a re-apply
// updates it rather than refusing forever.
func TestGeneratedConfigurationIsRefreshedInPlace(t *testing.T) {
	repo := t.TempDir()
	producer := agentplan.For(agent.AgentCodex)
	if _, err := InstallMCPConfig(agentplan.MCPInRepo, repo, codexServers(), nil, producer); err != nil {
		t.Fatalf("first install: %v", err)
	}

	changed := codexServers()
	changed[0].Command = "uvx"
	if _, err := InstallMCPConfig(agentplan.MCPInRepo, repo, changed, nil, producer); err != nil {
		t.Fatalf("second install: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repo, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("reading the refreshed file: %v", err)
	}
	if !strings.Contains(string(data), "uvx") {
		t.Errorf("the generated file was not refreshed:\n%s", data)
	}
}

// TestInstanceRootTakesTheClaudeDocument checks the other scope end to end,
// including that the Codex producer writes nothing at a root no Codex session
// reads configuration from.
func TestInstanceRootTakesTheClaudeDocument(t *testing.T) {
	root := t.TempDir()

	install, err := InstallMCPConfig(agentplan.MCPAtInstanceRoot, root, codexServers(), nil, agentplan.For(agent.AgentClaude))
	if err != nil {
		t.Fatalf("InstallMCPConfig(claude): %v", err)
	}
	path := filepath.Join(root, ".mcp.json")
	if len(install.Written) != 1 || install.Written[0] != path {
		t.Fatalf("wrote %v, want [%s]", install.Written, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(data), "\"mcpServers\"") {
		t.Errorf("the generated document is not an .mcp.json:\n%s", data)
	}

	codex, err := InstallMCPConfig(agentplan.MCPAtInstanceRoot, root, codexServers(), nil, agentplan.For(agent.AgentCodex))
	if err != nil {
		t.Fatalf("InstallMCPConfig(codex): %v", err)
	}
	if len(codex.Written) != 0 {
		t.Errorf("the Codex producer wrote %v at an instance root, where it reads no configuration", codex.Written)
	}
}

// TestVerbatimDestinationsAreCollectedOnce feeds the compatibility report: the
// destinations of every file table, deduplicated and ordered, because the
// report names one of them.
func TestVerbatimDestinationsAreCollectedOnce(t *testing.T) {
	got := mcpVerbatimDestinations(
		map[string]string{"mcp.json": ".mcp.json", "notes.md": "notes.local.md"},
		map[string]string{"mcp.json": ".mcp.json"},
		nil,
	)
	want := []string{".mcp.json", "notes.local.md"}
	if len(got) != len(want) {
		t.Fatalf("destinations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("destinations = %v, want %v", got, want)
		}
	}
}
