package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
)

// Lineage resource attributes: what niwa puts on a dispatched worker's
// telemetry so the worker can be traced back to the dispatch that made it.
//
// Every value reaches the worker through OTEL_RESOURCE_ATTRIBUTES, a single
// comma-separated list the worker's telemetry reads its resource attributes
// from. Whichever layer sets that variable last replaces the whole list, and
// the developer's own user settings may already set it. So niwa never sets its
// own entries alone: it composes one value holding the attributes it inherited
// followed by its own, and a layer that wins with that value keeps both.
//
// This file resolves the inputs, validates every value, and composes the list.
// It does not decide where the value goes; the dispatch path does.

// The six lineage attribute names, in the order niwa writes them. They are
// fixed: other tools read them by name, so a rename here breaks every reader
// silently. lineageAttributeNames pins the order and a test pins the list.
const (
	attrDispatchID      = "niwa.dispatch.id"
	attrDispatchSlug    = "niwa.dispatch.slug"
	attrParentSessionID = "niwa.parent.session.id"
	attrRequestedSkill  = "niwa.requested.skill"
	attrBriefID         = "niwa.brief.id"
	attrWorkspace       = "niwa.workspace"
)

var lineageAttributeNames = []string{
	attrDispatchID,
	attrDispatchSlug,
	attrParentSessionID,
	attrRequestedSkill,
	attrBriefID,
	attrWorkspace,
}

// lineageNamespace is the prefix niwa owns. An inherited entry under it is
// replaced by niwa's own value, or dropped when niwa sets none, so a worker
// never carries a stale lineage attribute from whatever launched niwa.
const lineageNamespace = "niwa."

// lineageValuePattern is the alphabet every niwa value must fit. It leaves out
// the characters that would split or corrupt the list (',', '=', '%', spaces,
// quotes), so no value niwa writes can add an attribute or break the ones
// beside it. A value outside it is dropped, not truncated or encoded.
var lineageValuePattern = regexp.MustCompile(`^[A-Za-z0-9._:/@-]{1,128}$`)

// parentSessionPattern is the shape niwa accepts for a calling session's id,
// the same one it already accepts for an agent's session ids elsewhere.
var parentSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// requestedSkillPattern is `<plugin>:<name>`.
var requestedSkillPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*:[a-z0-9][a-z0-9-]*$`)

// maxLineageFileBytes caps the two files lineage reads: the brief and the
// developer's settings file. Both are small documents; anything larger is not
// what it claims to be, and reading it whole would be the wrong response.
const maxLineageFileBytes = 1 << 20

// lineageInputs are the six values before validation. An empty field means
// the value doesn't exist for this dispatch, and its attribute is left out.
type lineageInputs struct {
	DispatchID      string
	Slug            string
	ParentSessionID string
	RequestedSkill  string
	BriefID         string
	Workspace       string
}

// lineageEntries renders the inputs as `key=value` entries in the fixed order.
// An empty value is left out. A value outside lineageValuePattern is left out
// too, and warn gets one line naming the attribute -- never the value, which
// may be input the developer didn't mean to publish.
func lineageEntries(in lineageInputs, warn io.Writer) []string {
	values := map[string]string{
		attrDispatchID:      in.DispatchID,
		attrDispatchSlug:    in.Slug,
		attrParentSessionID: in.ParentSessionID,
		attrRequestedSkill:  in.RequestedSkill,
		attrBriefID:         in.BriefID,
		attrWorkspace:       in.Workspace,
	}
	entries := make([]string, 0, len(lineageAttributeNames))
	for _, name := range lineageAttributeNames {
		v := values[name]
		if v == "" {
			continue
		}
		if !lineageValuePattern.MatchString(v) {
			fmt.Fprintf(warn, "niwa dispatch: %s left out: its value doesn't fit the attribute alphabet\n", name)
			continue
		}
		entries = append(entries, name+"="+v)
	}
	return entries
}

// newDispatchID returns a version-4 UUID read from r, the dispatch's identity.
// It is independent of the instance token: the token only has to be unique in
// one workspace's directory, and this has to be unique everywhere the
// dispatch's telemetry ends up.
func newDispatchID(r io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("reading randomness for the dispatch id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// lineageSlug is the dispatch slug as an attribute value. The instance slug
// separates words with '_', which the attribute alphabet leaves out, so each
// '_' is written as '-'. The slug has no '-' of its own (sanitizeInstanceSlug
// collapses every other character to '_'), so the mapping is one-to-one.
func lineageSlug(slug string) string {
	return strings.ReplaceAll(slug, "_", "-")
}

// lineageWorkspace is the workspace attribute's value: the name in the
// workspace's own configuration, the same for every instance and every
// machine -- not the instance's directory name, and not a local alias a
// workspace was initialized under, which only names the instance directories.
// A configuration that couldn't be read leaves the attribute out, and warn
// says so.
func lineageWorkspace(cfg *config.WorkspaceConfig, warn io.Writer) string {
	if cfg == nil {
		fmt.Fprintf(warn, "niwa dispatch: %s left out: the workspace configuration couldn't be read\n", attrWorkspace)
		return ""
	}
	return cfg.Workspace.Name
}

// lineageParentSessionID is the calling session's id, read from the variable
// the agent's launch spec names, or "" when it's unset or not id-shaped. niwa
// doesn't guess a parent from the process tree: a wrong parent is worse than
// none in a lineage tree.
func lineageParentSessionID(spec agentplan.LaunchSpec, getenv func(string) string) string {
	if spec.Lineage.ParentSessionEnv == "" {
		return ""
	}
	id := getenv(spec.Lineage.ParentSessionEnv)
	if !parentSessionPattern.MatchString(id) {
		return ""
	}
	return id
}

// validateRequestedSkill checks a --skill value.
func validateRequestedSkill(skill string) error {
	if skill == "" || requestedSkillPattern.MatchString(skill) {
		return nil
	}
	return fmt.Errorf("--skill %q is not of the form <plugin>:<name> (lowercase letters, digits and dashes)", skill)
}

// briefDigest is a brief's content identity: never its path or file name.
func briefDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// readBriefFile reads a --brief file. It must resolve, symlinks followed, to a
// regular file inside the workspace root of at most maxLineageFileBytes: the
// digest of whatever it reads leaves the machine on the worker's telemetry, so
// it must not be pointed at an arbitrary file, and a FIFO or device must not
// be able to hang the dispatch.
func readBriefFile(path, workspaceRoot string) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("--brief %s: %w", path, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, fmt.Errorf("--brief %s: %w", path, err)
	}
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving the workspace root: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("--brief %s: the file must be inside the workspace", path)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("--brief %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("--brief %s: not a regular file", path)
	}
	if info.Size() > maxLineageFileBytes {
		return nil, fmt.Errorf("--brief %s: larger than %d bytes", path, maxLineageFileBytes)
	}
	return os.ReadFile(resolved)
}

// errInheritedAttributes is returned when an inherited value can't be carried
// safely. Its message names the source and quotes nothing: the value is the
// developer's own configuration and may hold anything.
type errInheritedAttributes struct{ source string }

func (e errInheritedAttributes) Error() string {
	return fmt.Sprintf("the resource attributes in %s aren't a list niwa can carry; leaving the worker's attributes as they are", e.source)
}

// userSettingsAttributes returns OTEL_RESOURCE_ATTRIBUTES from the env block of
// the agent's user settings file (located by its launch spec's Lineage), and whether the file set it. Only the env
// object is decoded, and only that one key is used. A missing file, or a file
// without the key, is not an error: it contributes nothing. A file that isn't a
// regular file under the size cap holding valid JSON, or a key that isn't a
// string, is errInheritedAttributes.
func userSettingsAttributes(spec agentplan.LaunchSpec, home string, getenv func(string) string) (string, bool, error) {
	if spec.Lineage.SettingsFile == "" {
		return "", false, nil
	}
	dir := ""
	if spec.Lineage.SettingsHomeEnv != "" {
		dir = getenv(spec.Lineage.SettingsHomeEnv)
	}
	if dir == "" {
		if home == "" || len(spec.Lineage.SettingsHomePath) == 0 {
			return "", false, nil
		}
		dir = filepath.Join(append([]string{home}, spec.Lineage.SettingsHomePath...)...)
	}
	path := filepath.Join(dir, spec.Lineage.SettingsFile)
	source := "the user settings file"

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxLineageFileBytes {
		return "", false, errInheritedAttributes{source}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, errInheritedAttributes{source}
	}
	var doc struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", false, errInheritedAttributes{source}
	}
	raw, ok := doc.Env["OTEL_RESOURCE_ATTRIBUTES"]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, errInheritedAttributes{source}
	}
	return value, true, nil
}

// attributeEntry is one inherited `key=value` entry: its key, and the entry
// exactly as it was written, which is what gets carried.
type attributeEntry struct {
	key, raw string
}

// splitInheritedAttributes splits an inherited value into entries and checks
// each against the rule the worker's own attribute parser applies. One entry
// that fails it makes that parser discard every attribute, the user's
// included, so niwa refuses to carry a value with such an entry rather than
// add its own beside it.
func splitInheritedAttributes(value, source string) ([]attributeEntry, error) {
	var out []attributeEntry
	for _, raw := range strings.Split(value, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		key, val, found := strings.Cut(raw, "=")
		if !found || key == "" || strings.Contains(val, "=") || len(key) > 255 || len(val) > 255 ||
			!attributeParserSafe(key) || !attributeParserSafe(val) {
			return nil, errInheritedAttributes{source}
		}
		out = append(out, attributeEntry{key: key, raw: raw})
	}
	return out, nil
}

// attributeParserSafe reports whether s uses only printable ASCII other than
// ',', ';', '\' and '"' -- the characters the attribute parser accepts.
func attributeParserSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 33 || c > 126 || c == ',' || c == ';' || c == '\\' || c == '"' {
			return false
		}
	}
	return true
}

// composeResourceAttributes builds the one value the worker gets: the entries
// from the user settings value in their order, then each entry from niwa's own
// environment whose key the settings value doesn't hold (user settings are
// where the owning layer writes, so they win a conflict), with every entry in
// niwa's namespace removed from both, then niwa's own entries. Inherited
// entries are carried byte for byte.
//
// It returns "" when there is nothing to send, and errInheritedAttributes when
// an inherited value can't be carried; the caller then sends nothing at all,
// so the worker keeps exactly what it would have had without niwa.
func composeResourceAttributes(settingsValue, envValue string, own []string) (string, error) {
	fromSettings, err := splitInheritedAttributes(settingsValue, "the user settings file")
	if err != nil {
		return "", err
	}
	fromEnv, err := splitInheritedAttributes(envValue, "the dispatching environment")
	if err != nil {
		return "", err
	}
	held := make(map[string]bool, len(fromSettings))
	var out []string
	for _, e := range fromSettings {
		held[e.key] = true
		if !strings.HasPrefix(e.key, lineageNamespace) {
			out = append(out, e.raw)
		}
	}
	for _, e := range fromEnv {
		if held[e.key] || strings.HasPrefix(e.key, lineageNamespace) {
			continue
		}
		held[e.key] = true
		out = append(out, e.raw)
	}
	out = append(out, own...)
	return strings.Join(out, ","), nil
}
