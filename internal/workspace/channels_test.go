package workspace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
)

// channeledConfig returns a minimal WorkspaceConfig with [channels.mesh]
// enabled. Tests that exercise the happy path use this to keep setup
// boilerplate out of each test.
func channeledConfig() *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Channels: config.ChannelsConfig{
			Mesh: &config.ChannelsMeshConfig{},
		},
	}
}

// seedRepo creates a fake cloned-repo directory under groupDir with the
// given name and a minimal .git marker so EnumerateRepos and
// enumerateRoles see it as a genuine repo.
func seedRepo(t *testing.T, instanceRoot, group, name string) string {
	t.Helper()
	repoDir := filepath.Join(instanceRoot, group, name)
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatalf("seeding repo %s/%s: %v", group, name, err)
	}
	return repoDir
}

// seedContext creates the workspace-context.md at instanceRoot with a
// minimal body so the ## Channels append path has something to work with.
func seedContext(t *testing.T, instanceRoot string) string {
	t.Helper()
	p := filepath.Join(instanceRoot, workspaceContextFile)
	if err := os.WriteFile(p, []byte("# Workspace: test-ws\n\nBody content.\n"), 0o644); err != nil {
		t.Fatalf("seeding workspace context: %v", err)
	}
	return p
}

func TestInstallChannelInfrastructure_NoopWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkspaceConfig{} // channels disabled (Mesh == nil)

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected no files written, got %v", written)
	}
	// .niwa should not exist (nothing provisioned).
	if _, err := os.Stat(filepath.Join(dir, ".niwa")); err == nil {
		t.Error(".niwa should not exist when channels are disabled")
	}
}

// TestInstallChannelInfrastructure_CreatesRoleLayout checks AC-P1, AC-P2,
// AC-R1, AC-P5, AC-P6, AC-P7, AC-P8, AC-P15: for every enumerated role
// (coordinator + topology-derived), the full per-role inbox tree is
// present, plus .niwa/tasks/, daemon.pid/log, an instance-root .mcp.json
// (project-scoped file Claude Code reads from <cwd>/.mcp.json; per-cwd,
// no parent walk-up), SKILL.md mirrored per repo, and a ## Channels
// section in workspace-context.md with the four required items.
func TestInstallChannelInfrastructure_CreatesRoleLayout(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	seedRepo(t, dir, "apps", "api")
	seedContext(t, dir)
	cfg := channeledConfig()

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("InstallChannelInfrastructure: %v", err)
	}

	expectedRoles := []string{"api", "coordinator", "web"}
	inboxSubs := []string{"", "in-progress", "cancelled", "expired", "read"}
	for _, role := range expectedRoles {
		roleDir := filepath.Join(dir, ".niwa", "roles", role, "inbox")
		for _, sub := range inboxSubs {
			p := filepath.Join(roleDir, sub)
			fi, err := os.Stat(p)
			if err != nil {
				t.Errorf("role %q missing inbox dir %s: %v", role, sub, err)
				continue
			}
			if !fi.IsDir() {
				t.Errorf("role %q inbox entry %s is not a directory", role, sub)
			}
		}
	}

	// .niwa/tasks/ must exist.
	if fi, err := os.Stat(filepath.Join(dir, ".niwa", "tasks")); err != nil || !fi.IsDir() {
		t.Errorf(".niwa/tasks missing or not a dir: %v", err)
	}

	// daemon.pid and daemon.log placeholders.
	pidPath := filepath.Join(dir, ".niwa", "daemon.pid")
	logPath := filepath.Join(dir, ".niwa", "daemon.log")
	if _, err := os.Stat(pidPath); err != nil {
		t.Errorf("daemon.pid missing: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("daemon.log missing: %v", err)
	}

	// `.mcp.json` lives at the instance root only — at the directory
	// root, not under `.claude/`. Rationale lives at the call site in
	// channels.go.
	instanceMCP := filepath.Join(dir, ".mcp.json")
	data, err := os.ReadFile(instanceMCP)
	if err != nil {
		t.Fatalf("instance .mcp.json not written: %v", err)
	}
	if !strings.Contains(string(data), "mcp-serve") {
		t.Errorf("instance .mcp.json missing mcp-serve: %s", data)
	}
	if !strings.Contains(string(data), dir) {
		t.Errorf("instance .mcp.json missing instanceRoot %q: %s", dir, data)
	}
	if !strings.Contains(string(data), "NIWA_SESSION_ROLE") {
		t.Errorf("instance .mcp.json missing NIWA_SESSION_ROLE: %s", data)
	}
	if !strings.Contains(string(data), `"coordinator"`) {
		t.Errorf("instance .mcp.json NIWA_SESSION_ROLE must be coordinator: %s", data)
	}
	var mcpDoc map[string]any
	if err := json.Unmarshal(data, &mcpDoc); err != nil {
		t.Errorf("instance .mcp.json is not valid JSON: %v", err)
	}

	// No per-repo `.mcp.json` is written, at either the legacy
	// `.claude/.mcp.json` path (which Claude Code's discovery never
	// reads) or the repo-root `.mcp.json` path (which would collide
	// destructively with any project that ships its own MCP config).
	// Sub-repo cwd launches are an out-of-spec entry point users can
	// reach with `--mcp-config=<instance>/.mcp.json` explicitly. See
	// issue #78 for the trade-off rationale.
	for _, role := range []string{"web", "api"} {
		legacyOld := filepath.Join(dir, "apps", role, ".claude", ".mcp.json")
		if _, err := os.Stat(legacyOld); err == nil {
			t.Errorf("legacy per-repo .claude/.mcp.json should not be written for %q", role)
		}
		repoRoot := filepath.Join(dir, "apps", role, ".mcp.json")
		if _, err := os.Stat(repoRoot); err == nil {
			t.Errorf("per-repo .mcp.json should not be written at repo root for %q", role)
		}
	}

	// SKILL.md at instance root and per repo.
	instanceSkill := filepath.Join(dir, ".claude", "skills", "niwa-mesh", "SKILL.md")
	skill, err := os.ReadFile(instanceSkill)
	if err != nil {
		t.Fatalf("instance SKILL.md not written: %v", err)
	}
	checkSkillShape(t, skill)

	for _, role := range []string{"web", "api"} {
		p := filepath.Join(dir, "apps", role, ".claude", "skills", "niwa-mesh", "SKILL.md")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("repo SKILL.md for %q missing: %v", role, err)
			continue
		}
		if !bytes.Equal(data, skill) {
			t.Errorf("repo SKILL.md for %q differs from instance copy (flat uniform skill)", role)
		}
	}

	// workspace-context.md ## Channels section.
	ctxData, err := os.ReadFile(filepath.Join(dir, workspaceContextFile))
	if err != nil {
		t.Fatalf("reading workspace-context.md: %v", err)
	}
	ctxStr := string(ctxData)
	if !strings.Contains(ctxStr, channelsSectionHeader) {
		t.Errorf("workspace-context.md missing ## Channels section")
	}
	if !strings.Contains(ctxStr, "Role: coordinator") {
		t.Errorf("## Channels missing Role line")
	}
	if !strings.Contains(ctxStr, "NIWA_INSTANCE_ROOT: "+dir) {
		t.Errorf("## Channels missing NIWA_INSTANCE_ROOT line")
	}
	for _, name := range niwaMCPToolNames {
		if !strings.Contains(ctxStr, name) {
			t.Errorf("## Channels missing tool name %q", name)
		}
	}
	if !strings.Contains(ctxStr, "`/niwa-mesh` skill") {
		t.Errorf("## Channels missing pointer line")
	}

	// writtenFiles should contain every installer-written file but NOT
	// runtime artifacts like .niwa/tasks/<id>/ or role inbox files.
	// workspace-context.md is tracked so destroy-time cleanup removes it
	// alongside the other managed files.
	ctxPath := filepath.Join(dir, workspaceContextFile)
	mustHave := []string{
		instanceMCP, instanceSkill, pidPath, logPath, ctxPath,
	}
	for _, p := range mustHave {
		found := false
		for _, w := range written {
			if w == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("writtenFiles missing %q; got %v", p, written)
		}
	}
}

func checkSkillShape(t *testing.T, data []byte) {
	t.Helper()

	// Must open with YAML frontmatter.
	if !bytes.HasPrefix(data, []byte("---\n")) {
		t.Errorf("SKILL.md missing opening frontmatter delimiter")
	}
	parts := bytes.SplitN(data, []byte("---\n"), 3)
	if len(parts) < 3 {
		t.Fatalf("SKILL.md missing closing frontmatter delimiter")
	}
	frontmatter := parts[1]
	body := parts[2]

	// Frontmatter: name, description, allowed-tools with 11 entries.
	fm := string(frontmatter)
	if !strings.Contains(fm, "name: niwa-mesh") {
		t.Errorf("frontmatter missing name: niwa-mesh")
	}
	if !strings.Contains(fm, "description:") {
		t.Errorf("frontmatter missing description")
	}
	if !strings.Contains(fm, "allowed-tools:") {
		t.Errorf("frontmatter missing allowed-tools")
	}
	for _, tool := range niwaMCPToolNames {
		if !strings.Contains(fm, "- "+tool) {
			t.Errorf("frontmatter allowed-tools missing %q", tool)
		}
	}

	// Combined frontmatter (name + description + allowed-tools) must be
	// under Claude Code's skillFrontmatterCharLimit cap. The cap applies
	// to the parsed values, not the raw YAML, but enforcing the raw byte
	// length is a conservative upper bound.
	if len(frontmatter) >= skillFrontmatterCharLimit {
		t.Errorf("frontmatter exceeds %d-char cap: %d bytes", skillFrontmatterCharLimit, len(frontmatter))
	}

	// Body: six required section headings per PRD R10.
	required := []string{
		"## Delegation (sync vs async)",
		"## Reporting Progress",
		"## Completion Contract",
		"## Message Vocabulary",
		"## Peer Interaction",
		"## Common Patterns",
	}
	bodyStr := string(body)
	for _, h := range required {
		if !strings.Contains(bodyStr, h) {
			t.Errorf("SKILL.md body missing section %q", h)
		}
	}

	// Common Patterns must include explicit guidance for long-running
	// tasks; the default 600s timeout silently truncates real coding
	// tasks if the LLM doesn't override it.
	for _, phrase := range []string{
		"Long-running tasks",
		"timeout_seconds",
		"Re-await loop",
	} {
		if !strings.Contains(bodyStr, phrase) {
			t.Errorf("SKILL.md Common Patterns missing long-running guidance phrase %q", phrase)
		}
	}
}

// TestInstallChannelInfrastructure_Idempotent covers AC-P10 and AC-P13:
// second apply must produce byte-identical files and no drift warning,
// with stable mtimes on the managed files.
func TestInstallChannelInfrastructure_Idempotent(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	seedContext(t, dir)
	cfg := channeledConfig()

	var written1 []string
	if err := InstallChannelInfrastructure(cfg, dir, &written1); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	skillPath := filepath.Join(dir, ".claude", "skills", "niwa-mesh", "SKILL.md")
	mcpPath := filepath.Join(dir, ".mcp.json")

	firstSkill, _ := os.ReadFile(skillPath)
	firstMCP, _ := os.ReadFile(mcpPath)

	// Record mtimes to assert mtime stability on second apply.
	firstSkillStat, err := os.Stat(skillPath)
	if err != nil {
		t.Fatalf("stat skill: %v", err)
	}
	firstMCPStat, err := os.Stat(mcpPath)
	if err != nil {
		t.Fatalf("stat mcp: %v", err)
	}

	// Give the clock a tick so mtime comparison below is unambiguous.
	time.Sleep(20 * time.Millisecond)

	var written2 []string
	if err := InstallChannelInfrastructure(cfg, dir, &written2); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	secondSkill, _ := os.ReadFile(skillPath)
	secondMCP, _ := os.ReadFile(mcpPath)

	if !bytes.Equal(firstSkill, secondSkill) {
		t.Errorf("SKILL.md content changed across applies")
	}
	if !bytes.Equal(firstMCP, secondMCP) {
		t.Errorf(".mcp.json content changed across applies")
	}

	secondSkillStat, _ := os.Stat(skillPath)
	secondMCPStat, _ := os.Stat(mcpPath)
	if !firstSkillStat.ModTime().Equal(secondSkillStat.ModTime()) {
		t.Errorf("SKILL.md mtime changed: %v -> %v (expected stable on byte-identical re-apply)",
			firstSkillStat.ModTime(), secondSkillStat.ModTime())
	}
	if !firstMCPStat.ModTime().Equal(secondMCPStat.ModTime()) {
		t.Errorf(".mcp.json mtime changed on byte-identical re-apply")
	}
}

// TestInstallChannelInfrastructure_DriftWarningOnHandEdit covers the
// AC-P13 drift path: a hand-edit to a managed file triggers a stderr
// warning on the next apply and the file is overwritten.
func TestInstallChannelInfrastructure_DriftWarningOnHandEdit(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	seedContext(t, dir)
	cfg := channeledConfig()

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	skillPath := filepath.Join(dir, ".claude", "skills", "niwa-mesh", "SKILL.md")
	original, _ := os.ReadFile(skillPath)

	// Hand-edit the skill file.
	tampered := append(append([]byte{}, original...), []byte("\n# hand-edit\n")...)
	if err := os.WriteFile(skillPath, tampered, 0o600); err != nil {
		t.Fatalf("tampering with skill: %v", err)
	}

	// Redirect stderr to a pipe to capture the drift warning.
	origStderr := os.Stderr
	rPipe, wPipe, _ := os.Pipe()
	os.Stderr = wPipe

	var written2 []string
	err := InstallChannelInfrastructure(cfg, dir, &written2)

	_ = wPipe.Close()
	os.Stderr = origStderr
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rPipe)

	if err != nil {
		t.Fatalf("second apply: %v", err)
	}

	// File restored to canonical content.
	restored, _ := os.ReadFile(skillPath)
	if !bytes.Equal(restored, original) {
		t.Errorf("hand-edit not reverted on reapply")
	}

	stderr := buf.String()
	if !strings.Contains(stderr, "drift") || !strings.Contains(stderr, skillPath) {
		t.Errorf("expected drift warning naming %q, got: %s", skillPath, stderr)
	}
}

// TestInstallChannelInfrastructure_DirAndFileModes covers AC-P14: all
// installer-written directories are mode 0700 and files are mode 0600
// regardless of umask.
func TestInstallChannelInfrastructure_DirAndFileModes(t *testing.T) {
	// Force a permissive umask so we can prove the explicit chmod path
	// is doing the work, not the umask.
	oldMask := syscall.Umask(0)
	defer syscall.Umask(oldMask)

	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	seedContext(t, dir)
	cfg := channeledConfig()

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Walk .niwa/ and assert every file is 0600 and every directory is
	// 0700.
	niwaRoot := filepath.Join(dir, ".niwa")
	err := filepath.Walk(niwaRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == niwaRoot {
			return nil
		}
		perm := info.Mode().Perm()
		if info.IsDir() {
			if perm != 0o700 {
				t.Errorf("dir %s mode = %o, want 0700", path, perm)
			}
		} else {
			if perm != 0o600 {
				t.Errorf("file %s mode = %o, want 0600", path, perm)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking .niwa/: %v", err)
	}

	// .mcp.json (instance root) and SKILL.md must all be 0600.
	for _, p := range []string{
		filepath.Join(dir, ".mcp.json"),
		filepath.Join(dir, ".claude", "skills", "niwa-mesh", "SKILL.md"),
		filepath.Join(dir, "apps", "web", ".claude", "skills", "niwa-mesh", "SKILL.md"),
	} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("stat %s: %v", p, err)
			continue
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 0600", p, fi.Mode().Perm())
		}
	}
}

// TestInstallChannelInfrastructure_NewRepoSecondApply covers AC-P9: a
// repo added after first apply gets its inbox on the second apply, and
// existing inboxes are untouched.
func TestInstallChannelInfrastructure_NewRepoSecondApply(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	seedContext(t, dir)
	cfg := channeledConfig()

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// Drop a synthetic envelope into the existing web inbox to prove
	// the second apply leaves it byte-identical (AC-P10 also).
	webInbox := filepath.Join(dir, ".niwa", "roles", "web", "inbox")
	envelopePath := filepath.Join(webInbox, "sample.json")
	envelopeBody := []byte(`{"v":1,"id":"test"}`)
	if err := os.WriteFile(envelopePath, envelopeBody, 0o600); err != nil {
		t.Fatalf("seeding envelope: %v", err)
	}

	// Add a second repo.
	seedRepo(t, dir, "apps", "api")

	var written2 []string
	if err := InstallChannelInfrastructure(cfg, dir, &written2); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	// api inbox should now exist.
	if _, err := os.Stat(filepath.Join(dir, ".niwa", "roles", "api", "inbox")); err != nil {
		t.Errorf("api inbox not created on second apply: %v", err)
	}
	// web envelope should be byte-identical.
	got, _ := os.ReadFile(envelopePath)
	if !bytes.Equal(got, envelopeBody) {
		t.Errorf("existing envelope corrupted; got %q want %q", got, envelopeBody)
	}
}

// TestMigratePre1Layout_RemovesUUIDDirs covers the migration scenario
// (scenario-7): pre-1.0 .niwa/sessions/<uuid>/ directories are removed
// with a one-shot stderr warning; sessions.json is preserved.
func TestMigratePre1Layout_RemovesUUIDDirs(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed pre-1.0 layout: sessions/<uuid>/ with a random file
	// inside, plus a sessions.json we must preserve.
	sessionsDir := filepath.Join(dir, ".niwa", "sessions")
	uuidDir := filepath.Join(sessionsDir, "deadbeef-abcd-4def-8abc-112233445566")
	if err := os.MkdirAll(uuidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envelope := filepath.Join(uuidDir, "inbox", "msg.json")
	if err := os.MkdirAll(filepath.Dir(envelope), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envelope, []byte(`{"legacy":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionsJSON := filepath.Join(sessionsDir, "sessions.json")
	preserveContent := []byte(`{"sessions":{"abc":"def"}}`)
	if err := os.WriteFile(sessionsJSON, preserveContent, 0o600); err != nil {
		t.Fatal(err)
	}

	seedContext(t, dir)
	cfg := channeledConfig()

	// Redirect stderr to capture the one-shot warning.
	origStderr := os.Stderr
	rPipe, wPipe, _ := os.Pipe()
	os.Stderr = wPipe

	var written []string
	err := InstallChannelInfrastructure(cfg, dir, &written)

	_ = wPipe.Close()
	os.Stderr = origStderr
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rPipe)

	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// UUID directory removed.
	if _, err := os.Stat(uuidDir); !os.IsNotExist(err) {
		t.Errorf("pre-1.0 uuid dir still exists: err=%v", err)
	}
	// sessions.json preserved.
	got, err := os.ReadFile(sessionsJSON)
	if err != nil {
		t.Errorf("sessions.json was removed; want preserved: %v", err)
	}
	if !bytes.Equal(got, preserveContent) {
		t.Errorf("sessions.json content changed: got %q want %q", got, preserveContent)
	}

	// Warning line emitted to stderr.
	warn := buf.String()
	if !strings.Contains(warn, "upgrading mesh layout") {
		t.Errorf("expected migration warning, got: %q", warn)
	}

	// Second apply should be a no-op migration (roles/ now exists).
	origStderr = os.Stderr
	rPipe, wPipe, _ = os.Pipe()
	os.Stderr = wPipe

	var written2 []string
	_ = InstallChannelInfrastructure(cfg, dir, &written2)

	_ = wPipe.Close()
	os.Stderr = origStderr
	buf.Reset()
	_, _ = buf.ReadFrom(rPipe)
	if strings.Contains(buf.String(), "upgrading mesh layout") {
		t.Errorf("migration warning emitted on second apply; want one-shot only: %s", buf.String())
	}
}

// TestEnumerateRoles_CollisionRejected covers AC-R2: two repos whose
// basenames collide without an explicit [channels.mesh.roles] entry
// cause apply to fail.
func TestEnumerateRoles_CollisionRejected(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	seedRepo(t, dir, "experiments", "web") // collision
	seedContext(t, dir)
	cfg := channeledConfig()

	var written []string
	err := InstallChannelInfrastructure(cfg, dir, &written)
	if err == nil {
		t.Fatalf("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "derived from multiple repo basenames") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestEnumerateRoles_CollisionResolvedByExplicit covers the other side
// of AC-R2: with an explicit [channels.mesh.roles] entry for the
// colliding name, apply succeeds.
func TestEnumerateRoles_CollisionResolvedByExplicit(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	experimentWeb := seedRepo(t, dir, "experiments", "web")
	_ = experimentWeb
	seedContext(t, dir)
	cfg := channeledConfig()
	cfg.Channels.Mesh.Roles = map[string]string{
		"web": "apps/web",
	}

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("expected success with explicit mapping, got: %v", err)
	}
}

// TestEnumerateRoles_ReservedCoordinator covers AC-R3: mapping the
// reserved name `coordinator` to a non-root path fails apply.
func TestEnumerateRoles_ReservedCoordinator(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	seedContext(t, dir)
	cfg := channeledConfig()
	cfg.Channels.Mesh.Roles = map[string]string{
		"coordinator": "apps/web",
	}

	var written []string
	err := InstallChannelInfrastructure(cfg, dir, &written)
	if err == nil {
		t.Fatalf("expected reserved-name error, got nil")
	}
	if !strings.Contains(err.Error(), "reserved") || !strings.Contains(err.Error(), "coordinator") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestEnumerateRoles_NameFormatValidation covers AC-R4: role names must
// match ^[a-z0-9][a-z0-9-]{0,31}$; names with uppercase letters or
// underscores are rejected.
func TestEnumerateRoles_NameFormatValidation(t *testing.T) {
	cases := []struct {
		name     string
		roleName string
	}{
		{"uppercase", "Web"},
		{"underscore", "api_v2"},
		{"too_long", "abcdefghijklmnopqrstuvwxyzabcdefg"}, // 33 chars
		{"starts_with_hyphen", "-web"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			seedContext(t, dir)
			cfg := channeledConfig()
			cfg.Channels.Mesh.Roles = map[string]string{
				tc.roleName: "",
			}

			var written []string
			err := InstallChannelInfrastructure(cfg, dir, &written)
			if err == nil {
				t.Fatalf("expected format error for role %q", tc.roleName)
			}
			if !strings.Contains(err.Error(), tc.roleName) {
				t.Errorf("error should name the invalid role %q: %v", tc.roleName, err)
			}
		})
	}
}

// TestInstallChannelInfrastructure_ManagedFilesScope asserts the
// installer tracks only installer-written files in writtenFiles.
// Runtime artifacts (task directories, role inbox envelopes) are NOT
// tracked.
func TestInstallChannelInfrastructure_ManagedFilesScope(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "apps", "web")
	seedContext(t, dir)
	cfg := channeledConfig()

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Simulate a runtime envelope drop and a task state.json, both of
	// which the daemon and MCP handlers produce at runtime. Neither
	// should appear in writtenFiles after a subsequent apply.
	envelope := filepath.Join(dir, ".niwa", "roles", "web", "inbox", "task-42.json")
	if err := os.WriteFile(envelope, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Join(dir, ".niwa", "tasks", "task-42")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stateJSON := filepath.Join(taskDir, "state.json")
	if err := os.WriteFile(stateJSON, []byte(`{"state":"queued"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var written2 []string
	if err := InstallChannelInfrastructure(cfg, dir, &written2); err != nil {
		t.Fatalf("reapply: %v", err)
	}

	for _, p := range written2 {
		if p == envelope {
			t.Errorf("runtime envelope tracked in writtenFiles: %s", p)
		}
		if p == stateJSON {
			t.Errorf("runtime task state tracked in writtenFiles: %s", p)
		}
	}
}

func TestInjectChannelHooks_EmptyConfig(t *testing.T) {
	cfg := &config.WorkspaceConfig{}
	injectChannelHooks(cfg, t.TempDir())
	if len(cfg.Claude.Hooks) != 0 {
		t.Errorf("expected no hooks, got %v", cfg.Claude.Hooks)
	}
}

func TestInjectChannelHooks_InjectsHooks(t *testing.T) {
	dir := t.TempDir()
	cfg := channeledConfig()
	injectChannelHooks(cfg, dir)

	if _, ok := cfg.Claude.Hooks["session_start"]; !ok {
		t.Error("session_start hook not injected")
	}
	if _, ok := cfg.Claude.Hooks["user_prompt_submit"]; !ok {
		t.Error("user_prompt_submit hook not injected")
	}

	startScripts := cfg.Claude.Hooks["session_start"][0].Scripts
	if len(startScripts) == 0 || !filepath.IsAbs(startScripts[0]) {
		t.Errorf("session_start script must be an absolute path: %v", startScripts)
	}
	if !strings.Contains(startScripts[0], "mesh-session-start.sh") {
		t.Errorf("script must reference mesh-session-start.sh, got %v", startScripts)
	}
}

func TestInjectChannelHooks_PrependToExisting(t *testing.T) {
	dir := t.TempDir()
	existingEntry := config.HookEntry{Scripts: []string{"existing.sh"}}
	cfg := channeledConfig()
	cfg.Claude.Hooks = config.HooksConfig{
		"session_start": {existingEntry},
	}
	injectChannelHooks(cfg, dir)

	entries := cfg.Claude.Hooks["session_start"]
	if len(entries) != 2 {
		t.Fatalf("expected 2 session_start entries, got %d", len(entries))
	}
	wantScript := filepath.Join(dir, ".niwa", "hooks", "mesh-session-start.sh")
	if entries[0].Scripts[0] != wantScript {
		t.Errorf("channel hook not prepended; got %q", entries[0].Scripts[0])
	}
	if entries[1].Scripts[0] != "existing.sh" {
		t.Errorf("existing hook not preserved: %v", entries[1].Scripts)
	}
}

// TestInstanceMCPConfigPath documents the path contract three call
// sites depend on: the channels installer that writes the file, the
// daemon's worker spawn that hands the path to claude via
// --mcp-config, and the functional-test coordinator launcher. If this
// constant changes (say to .niwa/.mcp.json), this is the test that
// will fail first.
func TestInstanceMCPConfigPath(t *testing.T) {
	got := InstanceMCPConfigPath("/some/instance")
	want := "/some/instance/.mcp.json"
	if got != want {
		t.Errorf("InstanceMCPConfigPath(%q) = %q, want %q", "/some/instance", got, want)
	}
}

func TestBuildMCPContent_InstanceRootEscaping(t *testing.T) {
	instanceRoot := "/some/path/with spaces"
	data, err := buildMCPContent(instanceRoot)
	if err != nil {
		t.Fatalf("buildMCPContent: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\ncontent: %s", err, data)
	}

	servers := doc["mcpServers"].(map[string]any)
	niwa := servers["niwa"].(map[string]any)
	env := niwa["env"].(map[string]any)
	got := env["NIWA_INSTANCE_ROOT"].(string)
	if got != instanceRoot {
		t.Errorf("NIWA_INSTANCE_ROOT: got %q, want %q", got, instanceRoot)
	}
}

// TestBuildMCPContent_RejectsInvalidUTF8 documents the contract that
// invalid UTF-8 in the instance root surfaces as a build error rather
// than producing malformed JSON. Reachable in production only on
// filesystems with mojibake-encoded paths; covered here as a defense
// against a regression that would silently ship a broken .mcp.json.
func TestBuildMCPContent_RejectsInvalidUTF8(t *testing.T) {
	bad := "/path/with/invalid\xff\xfeutf8"
	if _, err := buildMCPContent(bad); err == nil {
		t.Errorf("expected error for invalid UTF-8 in instance root, got nil")
	}
}

// TestWorkerMCPConfigPath documents the path convention used by
// spawnWorker (which writes the config) and the test harness (which
// may need to read it). The file lives inside the task directory so
// daemon and test can locate it with only instanceRoot + taskID.
func TestWorkerMCPConfigPath(t *testing.T) {
	got := WorkerMCPConfigPath("/inst", "abc-123")
	want := "/inst/.niwa/tasks/abc-123/worker.mcp.json"
	if got != want {
		t.Errorf("WorkerMCPConfigPath = %q, want %q", got, want)
	}
}

// TestWorkerMCPConfig_RoleAndTaskID verifies that WorkerMCPConfig bakes
// the caller-supplied role and task ID into the env block, not the
// coordinator role. This is the property that prevents Claude Code's
// env-block merge from overriding NIWA_SESSION_ROLE=coordinator into
// every worker.
func TestWorkerMCPConfig_RoleAndTaskID(t *testing.T) {
	instanceRoot := "/my/instance"
	role := "web"
	taskID := "task-42"

	data, err := WorkerMCPConfig(instanceRoot, role, taskID)
	if err != nil {
		t.Fatalf("WorkerMCPConfig: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\ncontent: %s", err, data)
	}
	servers := doc["mcpServers"].(map[string]any)
	niwa := servers["niwa"].(map[string]any)
	env := niwa["env"].(map[string]any)

	if got := env["NIWA_INSTANCE_ROOT"].(string); got != instanceRoot {
		t.Errorf("NIWA_INSTANCE_ROOT: got %q, want %q", got, instanceRoot)
	}
	if got := env["NIWA_SESSION_ROLE"].(string); got != role {
		t.Errorf("NIWA_SESSION_ROLE: got %q, want %q (must not be hardcoded coordinator)", got, role)
	}
	if got := env["NIWA_TASK_ID"].(string); got != taskID {
		t.Errorf("NIWA_TASK_ID: got %q, want %q", got, taskID)
	}
}

// TestWorkerMCPConfig_CoordinatorRole verifies that the coordinator role
// is also handled correctly — coordinator tasks spawned by the daemon
// (e.g. niwa_ask replies) must get NIWA_SESSION_ROLE=coordinator, not
// "worker" or any other default.
func TestWorkerMCPConfig_CoordinatorRole(t *testing.T) {
	data, err := WorkerMCPConfig("/inst", "coordinator", "t-99")
	if err != nil {
		t.Fatalf("WorkerMCPConfig: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	env := doc["mcpServers"].(map[string]any)["niwa"].(map[string]any)["env"].(map[string]any)
	if got := env["NIWA_SESSION_ROLE"].(string); got != "coordinator" {
		t.Errorf("NIWA_SESSION_ROLE for coordinator spawn: got %q, want %q", got, "coordinator")
	}
}

// TestWorkerMCPConfig_DistinctFromInstanceMCP confirms that a worker
// config for a non-coordinator role differs from the instance-root
// .mcp.json in exactly the NIWA_SESSION_ROLE and NIWA_TASK_ID fields.
// This guards against a future refactor accidentally collapsing the two
// templates back into one.
func TestWorkerMCPConfig_DistinctFromInstanceMCP(t *testing.T) {
	instanceRoot := "/inst"
	workerData, err := WorkerMCPConfig(instanceRoot, "backend", "t-1")
	if err != nil {
		t.Fatalf("WorkerMCPConfig: %v", err)
	}
	coordData, err := buildMCPContent(instanceRoot)
	if err != nil {
		t.Fatalf("buildMCPContent: %v", err)
	}

	var workerDoc, coordDoc map[string]any
	_ = json.Unmarshal(workerData, &workerDoc)
	_ = json.Unmarshal(coordData, &coordDoc)

	wEnv := workerDoc["mcpServers"].(map[string]any)["niwa"].(map[string]any)["env"].(map[string]any)
	cEnv := coordDoc["mcpServers"].(map[string]any)["niwa"].(map[string]any)["env"].(map[string]any)

	if wEnv["NIWA_SESSION_ROLE"] == cEnv["NIWA_SESSION_ROLE"] {
		t.Errorf("worker and coordinator configs have the same NIWA_SESSION_ROLE=%q; worker must carry its actual role", wEnv["NIWA_SESSION_ROLE"])
	}
	if _, ok := wEnv["NIWA_TASK_ID"]; !ok {
		t.Errorf("worker config is missing NIWA_TASK_ID")
	}
	if _, ok := cEnv["NIWA_TASK_ID"]; ok {
		t.Errorf("coordinator config should not have NIWA_TASK_ID")
	}
}

func TestWriteIdempotent_MatchingContentSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.txt")
	data := []byte("hello\n")

	if err := writeIdempotent(path, data, 0o600, nil); err != nil {
		t.Fatalf("first write: %v", err)
	}
	before, _ := os.Stat(path)
	time.Sleep(20 * time.Millisecond)
	if err := writeIdempotent(path, data, 0o600, nil); err != nil {
		t.Fatalf("second write: %v", err)
	}
	after, _ := os.Stat(path)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("mtime changed on byte-identical rewrite: %v -> %v", before.ModTime(), after.ModTime())
	}
}

