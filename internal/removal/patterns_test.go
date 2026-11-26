package removal

import "testing"

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, rel string
		isDir, want  bool
	}{
		// No slash: a base name anywhere.
		{"*.swp", "a.swp", false, true},
		{"*.swp", "src/deep/a.swp", false, true},
		{"*.swp", "a.swpx", false, false},
		{".DS_Store", "x/.DS_Store", false, true},
		{"*~", "notes.txt~", false, true},
		{".#*", "dir/.#lock", false, true},
		// A slash: matched from the root.
		{"docs/*.md", "docs/a.md", false, true},
		{"docs/*.md", "src/docs/a.md", false, false},
		{"docs/*.md", "docs/sub/a.md", false, false},
		// "**" stands for any number of directories.
		{"**/gen/*.go", "gen/a.go", false, true},
		{"**/gen/*.go", "x/y/gen/a.go", false, true},
		{"out/**/*.log", "out/a.log", false, true},
		{"out/**/*.log", "out/a/b/c.log", false, true},
		{"out/**/*.log", "other/a.log", false, false},
		// A trailing slash: a directory and everything under it.
		{"__pycache__/", "__pycache__", true, true},
		{"__pycache__/", "pkg/__pycache__/m.pyc", false, true},
		{"__pycache__/", "__pycache__", false, false},
		{".pytest_cache/", "a/.pytest_cache/v/cache", false, true},
		{"build/out/", "build/out/x.o", false, true},
		{"build/out/", "src/build/out/x.o", false, false},
		// A pattern matching a parent directory covers what is under it.
		{"tmp", "tmp/a/b.txt", false, true},
		{"", "a", false, false},
	}
	for _, c := range cases {
		if got := matchPattern(c.pattern, c.rel, c.isDir); got != c.want {
			t.Errorf("matchPattern(%q, %q, dir=%v) = %v, want %v", c.pattern, c.rel, c.isDir, got, c.want)
		}
	}
}

func TestShippedDisposablePatterns(t *testing.T) {
	want := []string{"*.swp", "*.swo", "*~", ".#*", ".DS_Store", "__pycache__/", ".pytest_cache/"}
	if len(ShippedDisposable) != len(want) {
		t.Fatalf("shipped patterns %v, want %v", ShippedDisposable, want)
	}
	for i := range want {
		if ShippedDisposable[i] != want[i] {
			t.Fatalf("shipped patterns %v, want %v", ShippedDisposable, want)
		}
	}
}
