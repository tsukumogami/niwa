package functional

import (
	"os"
	"os/exec"
	"testing"
)

// TestCI_AuthorityRunsWithoutKoto pins that CI runs the authority tests on a
// host with no koto: the feature promises to hold with niwa alone, so CI must
// never prove it on a runner where koto could be standing in. It runs only
// when CI is set (GitHub Actions sets it), because developers commonly have
// koto installed. The Test job reaches it through `go test -race ./...`, in the
// same run as the authority unit tests, and `make test-functional` runs the
// authority features, `niwa authority check`'s among them (its scenarios also
// strip koto from the PATH niwa runs with); a runner image that later gains
// koto turns this red.
func TestCI_AuthorityRunsWithoutKoto(t *testing.T) {
	if _, set := os.LookupEnv("CI"); !set {
		t.Skip("CI is not set; the no-koto check runs only in CI")
	}
	if path, err := exec.LookPath("koto"); err == nil {
		t.Fatalf("authority tests must run without koto, but koto resolves on PATH at %s", path)
	}
}
