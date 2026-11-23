//go:build niwa_functional

package workspace

import "testing"

func TestBorrowReleased_TrueInTheTestBuild(t *testing.T) {
	if !borrowReleased {
		t.Fatal("borrowReleased is false in the niwa_functional build")
	}
}

// In the test build the gate is open, so borrowEnabled is the cascade.
func TestBorrowEnabled_IsTheCascadeInTheTestBuild(t *testing.T) {
	for _, c := range shareGitHistoryCombos() {
		cfg := comboConfig(c.repo, c.ws)
		for _, repo := range []string{"app", "unnamed"} {
			if got, want := borrowEnabled(cfg, repo), cfg.ShareGitHistoryEnabled(repo); got != want {
				t.Errorf("borrowEnabled(%s) with repo=%s workspace=%s = %t, want the cascade's %t",
					repo, sghState(c.repo), sghState(c.ws), got, want)
			}
		}
	}
}
