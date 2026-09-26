package workspace

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestIsPermanentCloneError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"transient could-not-read", errors.New("cloning git@github.com:o/r.git: exit status 128\nfatal: Could not read from remote repository."), false},
		{"transient timeout", errors.New("ssh: connect to host github.com port 22: Operation timed out"), false},
		{"transient connection reset", errors.New("fatal: the remote end hung up unexpectedly\nConnection reset by peer"), false},
		{"permanent repo not found", errors.New("remote: Repository not found.\nfatal: repository 'https://github.com/o/r.git/' not found"), true},
		{"permanent repo not found ssh uppercase", errors.New("ERROR: Repository not found."), true},
		{"permanent permission denied", errors.New("git@github.com: Permission denied (publickey)."), true},
		{"permanent auth failed", errors.New("fatal: Authentication failed for 'https://github.com/o/r.git/'"), true},
		{"permanent invalid creds", errors.New("remote: Invalid username or password."), true},
		{"permanent bad branch", errors.New("fatal: Remote branch nope not found in upstream origin"), true},
		{"permanent could-not-read-username", errors.New("fatal: could not read Username for 'https://github.com': terminal prompts disabled"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPermanentCloneError(tt.err); got != tt.want {
				t.Errorf("isPermanentCloneError(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// withFastBackoff swaps cloneBackoff for near-zero waits so retry tests do not
// sleep for real seconds, restoring the original on cleanup. The length is
// preserved so attempt-count behavior is unchanged.
func withFastBackoff(t *testing.T) {
	t.Helper()
	orig := cloneBackoff
	cloneBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { cloneBackoff = orig })
}

func TestCloneWithRetry_TransientThenSuccess(t *testing.T) {
	withFastBackoff(t)
	calls := 0
	a := &Applier{
		Reporter: NewReporter(io.Discard),
		cloneRepo: func(_ context.Context, _, _, _ string, _ *Reporter) (bool, error) {
			calls++
			if calls < 3 {
				return false, errors.New("fatal: Could not read from remote repository.")
			}
			return true, nil
		},
	}
	cloned, retries, err := a.cloneWithRetry(context.Background(), cloneJob{}, a.Reporter)
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if !cloned {
		t.Errorf("expected cloned=true")
	}
	if retries != 2 {
		t.Errorf("expected retries=2, got %d", retries)
	}
	if calls != 3 {
		t.Errorf("expected 3 clone attempts, got %d", calls)
	}
}

func TestCloneWithRetry_PermanentFailsFast(t *testing.T) {
	withFastBackoff(t)
	calls := 0
	a := &Applier{
		Reporter: NewReporter(io.Discard),
		cloneRepo: func(_ context.Context, _, _, _ string, _ *Reporter) (bool, error) {
			calls++
			return false, errors.New("ERROR: Repository not found.")
		},
	}
	_, retries, err := a.cloneWithRetry(context.Background(), cloneJob{}, a.Reporter)
	if err == nil {
		t.Fatal("expected error for permanent failure")
	}
	if retries != 0 {
		t.Errorf("expected retries=0 (no retry on permanent), got %d", retries)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 attempt on permanent failure, got %d", calls)
	}
}

func TestCloneWithRetry_ExhaustsAttempts(t *testing.T) {
	withFastBackoff(t)
	calls := 0
	a := &Applier{
		Reporter: NewReporter(io.Discard),
		cloneRepo: func(_ context.Context, _, _, _ string, _ *Reporter) (bool, error) {
			calls++
			return false, errors.New("fatal: Could not read from remote repository.")
		},
	}
	_, _, err := a.cloneWithRetry(context.Background(), cloneJob{}, a.Reporter)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// len(cloneBackoff)+1 attempts total.
	if want := len(cloneBackoff) + 1; calls != want {
		t.Errorf("expected %d attempts, got %d", want, calls)
	}
}

func TestCloneWithRetry_ContextCancelStopsRetry(t *testing.T) {
	withFastBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	a := &Applier{
		Reporter: NewReporter(io.Discard),
		cloneRepo: func(_ context.Context, _, _, _ string, _ *Reporter) (bool, error) {
			calls++
			cancel() // cancel mid-flight; the loop should not retry after this
			return false, errors.New("fatal: Could not read from remote repository.")
		},
	}
	_, _, err := a.cloneWithRetry(ctx, cloneJob{}, a.Reporter)
	if err == nil {
		t.Fatal("expected error when context cancelled")
	}
	if calls != 1 {
		t.Errorf("expected 1 attempt before cancel short-circuits, got %d", calls)
	}
}
