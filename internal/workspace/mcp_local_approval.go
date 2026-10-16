package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/tsukumogami/niwa/internal/config"
)

// mirrorMCPApprovalsToLocal copies the enabledMcpjsonServers list niwa wrote
// into the instance root's .claude/settings.json into the root's
// .claude/settings.local.json as well.
//
// Claude Code honors the approval in a project settings.json only once the
// directory carries workspace trust, and it reads that trust from its user
// config. A background session is served by a pre-started spare process that
// loaded the user config before niwa trusted the new instance root, so it
// sees an untrusted directory, ignores the project approval, and stops on the
// per-server prompt. The local settings file is read when the session starts
// in the instance, so an approval there does not depend on the trust flag.
//
// The root's settings.local.json is not a niwa-managed file: Claude Code and
// `claude plugin ... --scope local` write it too. So the names are merged in,
// every other key is preserved, and names already present (including ones a
// person approved at the prompt) are never removed. A file that can't be
// parsed is left alone.
func mirrorMCPApprovalsToLocal(instanceRoot string) error {
	shared, err := readJSONSettings(filepath.Join(instanceRoot, ".claude", "settings.json"))
	if err != nil || shared == nil {
		return err
	}
	want := stringList(shared[config.EnabledMcpjsonServersKey])
	if len(want) == 0 {
		return nil
	}

	localPath := filepath.Join(instanceRoot, ".claude", "settings.local.json")
	local, err := readJSONSettings(localPath)
	if err != nil {
		return err
	}
	if local == nil {
		local = map[string]any{}
	}

	have := stringList(local[config.EnabledMcpjsonServersKey])
	merged := approvedMCPServerList(append(slices.Clone(have), want...))
	sortedHave := slices.Clone(have)
	sort.Strings(sortedHave)
	if slices.Equal(merged, sortedHave) {
		return nil
	}
	local[config.EnabledMcpjsonServersKey] = merged

	data, err := json.MarshalIndent(local, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	tmp := localPath + ".niwa-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, localPath)
}

// readJSONSettings reads a JSON settings document. A missing file returns
// (nil, nil); an unparseable one returns an error so the caller leaves it
// untouched.
func readJSONSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return doc, nil
}

// stringList returns the string elements of a decoded JSON array, ignoring
// anything else.
func stringList(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
