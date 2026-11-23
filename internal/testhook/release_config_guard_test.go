package testhook

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The release build must never carry the niwa_functional tag: with it, a
// release binary would borrow from the host mirror and answer the test
// hooks. go test ./... runs in CI's Test job, so this is the CI check on the
// release configuration.

// testTagLines returns the lines of the goreleaser config at path that name
// the niwa_functional tag outside a comment, in any form a build could take
// it: a tags list entry, -tags in flags, or GOFLAGS in env.
func testTagLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var hits []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.Contains(stripYAMLComment(line), "niwa_functional") {
			hits = append(hits, strings.TrimSpace(line))
		}
	}
	return hits, sc.Err()
}

// stripYAMLComment drops a YAML comment: a # at the start of the line or
// after whitespace. A # inside a value (a URL fragment, say) stays.
func stripYAMLComment(line string) string {
	for i, r := range line {
		if r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

func TestGuard_ReleaseConfigNeverPassesTheTestTag(t *testing.T) {
	path := filepath.Join(moduleRoot(t), ".goreleaser.yaml")
	hits, err := testTagLines(path)
	if err != nil {
		t.Fatalf("reading the release configuration: %v", err)
	}
	for _, line := range hits {
		t.Errorf(".goreleaser.yaml builds with the niwa_functional tag (%q); a release binary must never be a test build", line)
	}
}

// The guard catches the tag added to a copy of the real file, in each place
// goreleaser takes build tags from, and ignores it in a comment.
func TestGuard_ReleaseConfigCheckCatchesTheTag(t *testing.T) {
	real, err := os.ReadFile(filepath.Join(moduleRoot(t), ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const anchor = "    flags:\n      - -trimpath\n"
	if !bytes.Contains(real, []byte(anchor)) {
		t.Fatalf("the release configuration no longer has %q; update this test's anchor", anchor)
	}
	cases := map[string]struct {
		insert string
		caught bool
	}{
		"flags -tags=":   {"      - -tags=niwa_functional\n", true},
		"flags -tags ":   {"      - -tags niwa_functional\n", true},
		"flags list":     {"      - -tags=netgo,niwa_functional\n", true},
		"tags field":     {"    tags:\n      - niwa_functional\n", true},
		"env GOFLAGS":    {"    env:\n      - GOFLAGS=-tags=niwa_functional\n", true},
		"comment only":   {"      # never add niwa_functional here\n", false},
		"inline comment": {"      - -buildvcs=false # not niwa_functional\n", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			copied := bytes.Replace(real, []byte(anchor), []byte(anchor+tc.insert), 1)
			path := filepath.Join(t.TempDir(), ".goreleaser.yaml")
			if err := os.WriteFile(path, copied, 0o644); err != nil {
				t.Fatal(err)
			}
			hits, err := testTagLines(path)
			if err != nil {
				t.Fatal(err)
			}
			if caught := len(hits) > 0; caught != tc.caught {
				t.Fatalf("check caught = %t (lines %q), want %t", caught, hits, tc.caught)
			}
		})
	}
}
