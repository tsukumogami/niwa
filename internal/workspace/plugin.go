package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const repoRefPrefix = "repo:"

// ResolveMarketplaceSource resolves a marketplace source string to a value
// suitable for marketplace resolution. GitHub refs (org/repo) pass through
// unchanged. repo: refs are resolved to absolute paths using the repoIndex
// (repo name -> on-disk path).
func ResolveMarketplaceSource(source string, repoIndex map[string]string) (string, error) {
	if !strings.HasPrefix(source, repoRefPrefix) {
		return source, nil
	}

	ref := strings.TrimPrefix(source, repoRefPrefix)
	slashIdx := strings.IndexByte(ref, '/')
	if slashIdx < 0 {
		return "", fmt.Errorf("invalid repo ref %q: expected \"repo:<name>/<path>\"", source)
	}

	repoName := ref[:slashIdx]
	filePath := ref[slashIdx+1:]

	repoDir, ok := repoIndex[repoName]
	if !ok {
		return "", fmt.Errorf("marketplace %q: repo %q is not managed by this workspace", source, repoName)
	}

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return "", fmt.Errorf("marketplace %q: repo %q has not been cloned", source, repoName)
	}

	absPath := filepath.Join(repoDir, filePath)

	if err := checkContainment(absPath, repoDir); err != nil {
		return "", fmt.Errorf("marketplace %q: path escapes repo directory", source)
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return "", fmt.Errorf("marketplace %q: file not found at %s", source, absPath)
	}

	return absPath, nil
}
