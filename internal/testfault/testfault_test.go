package testfault

import (
	"strings"
	"testing"
)

func TestMaybe_DefaultNoOp(t *testing.T) {
	t.Setenv(envVar, "")
	if err := Maybe("any-label"); err != nil {
		t.Errorf("Maybe with empty env should return nil, got %v", err)
	}
}

func TestMaybe_SingleFault(t *testing.T) {
	t.Setenv(envVar, "error:boom@fetch-tarball")
	err := Maybe("fetch-tarball")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not contain message", err.Error())
	}
	if !strings.Contains(err.Error(), "fetch-tarball") {
		t.Errorf("error %q does not contain label", err.Error())
	}
}

func TestMaybe_LabelMismatch(t *testing.T) {
	t.Setenv(envVar, "error:boom@fetch-tarball")
	if err := Maybe("snapshot-swap"); err != nil {
		t.Errorf("expected nil for mismatched label, got %v", err)
	}
}

func TestMaybe_MultipleFaults(t *testing.T) {
	t.Setenv(envVar, "error:boom1@fetch-tarball,error:boom2@snapshot-swap")
	err1 := Maybe("fetch-tarball")
	if err1 == nil || !strings.Contains(err1.Error(), "boom1") {
		t.Errorf("first fault: %v", err1)
	}
	err2 := Maybe("snapshot-swap")
	if err2 == nil || !strings.Contains(err2.Error(), "boom2") {
		t.Errorf("second fault: %v", err2)
	}
	if err := Maybe("extract-entry"); err != nil {
		t.Errorf("non-matching label: %v", err)
	}
}

func TestMaybe_TruncateAfterReturnsNilFromMaybe(t *testing.T) {
	// truncate-after is a stream-side concern; Maybe should not
	// return an error for it.
	t.Setenv(envVar, "truncate-after:1024@fetch-tarball")
	if err := Maybe("fetch-tarball"); err != nil {
		t.Errorf("truncate-after should not produce a Maybe error, got %v", err)
	}
}

func TestTruncateAfter(t *testing.T) {
	t.Setenv(envVar, "truncate-after:1024@fetch-tarball")
	if got := TruncateAfter("fetch-tarball"); got != 1024 {
		t.Errorf("TruncateAfter = %d, want 1024", got)
	}
	if got := TruncateAfter("snapshot-swap"); got != -1 {
		t.Errorf("TruncateAfter for non-matching label = %d, want -1", got)
	}
}

func TestTruncateAfter_Unset(t *testing.T) {
	t.Setenv(envVar, "")
	if got := TruncateAfter("anything"); got != -1 {
		t.Errorf("TruncateAfter unset = %d, want -1", got)
	}
}

func TestMaybe_MalformedSpecIgnored(t *testing.T) {
	t.Setenv(envVar, "this-has-no-at-sign")
	if err := Maybe("any"); err != nil {
		t.Errorf("malformed spec should be ignored, got %v", err)
	}
	t.Setenv(envVar, "unknown-spec-type:foo@fetch-tarball")
	if err := Maybe("fetch-tarball"); err != nil {
		t.Errorf("unknown spec type should be ignored, got %v", err)
	}
}

func TestMaybe_EmptyMessageDefaults(t *testing.T) {
	t.Setenv(envVar, "error:@fetch-tarball")
	err := Maybe("fetch-tarball")
	if err == nil || !strings.Contains(err.Error(), "test fault") {
		t.Errorf("empty error message should default, got %v", err)
	}
}
