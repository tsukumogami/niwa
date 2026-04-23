// Package testfault provides a test-only fault-injection seam.
//
// Production code calls Maybe(label) at well-defined points along
// the fetch + extract + swap pipeline. The function returns nil
// unless the NIWA_TEST_FAULT environment variable contains a fault
// spec for the matching label.
//
// Spec format:
//
//	NIWA_TEST_FAULT=<spec>@<label>[,<spec>@<label>]...
//
// where <spec> is one of:
//
//	error:<message>           always returns an error with that message
//	truncate-after:<bytes>    used by stream consumers; production code
//	                          interprets this when copying bytes
//
// In production builds the only effect is a single os.Getenv lookup
// per Maybe call (negligible overhead). The seam exists to satisfy
// the PRD's Test Strategy commitment to fault-injection coverage of
// the snapshot pipeline.
package testfault

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// envVar names the environment variable controlling fault injection.
const envVar = "NIWA_TEST_FAULT"

// Maybe returns a non-nil error when NIWA_TEST_FAULT contains a fault
// spec matching label. Returns nil when the env var is unset, the
// spec is malformed, or no spec matches the given label.
//
// Production code calls this at fault-injection points; tests set
// the env var to trigger specific failures.
func Maybe(label string) error {
	spec := os.Getenv(envVar)
	if spec == "" {
		return nil
	}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		at := strings.LastIndexByte(entry, '@')
		if at < 0 {
			continue
		}
		entryLabel := entry[at+1:]
		if entryLabel != label {
			continue
		}
		return parseFault(entry[:at], label)
	}
	return nil
}

// TruncateAfter returns the byte count from a "truncate-after:N@label"
// spec when present, or -1 otherwise. Stream consumers (e.g., the
// tarball fetcher) consult this to decide when to close their reader
// during a fault-injection scenario.
func TruncateAfter(label string) int64 {
	spec := os.Getenv(envVar)
	if spec == "" {
		return -1
	}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		at := strings.LastIndexByte(entry, '@')
		if at < 0 {
			continue
		}
		if entry[at+1:] != label {
			continue
		}
		body := entry[:at]
		if !strings.HasPrefix(body, "truncate-after:") {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimPrefix(body, "truncate-after:"), 10, 64)
		if err != nil {
			return -1
		}
		return n
	}
	return -1
}

func parseFault(body, label string) error {
	switch {
	case strings.HasPrefix(body, "error:"):
		msg := strings.TrimPrefix(body, "error:")
		if msg == "" {
			msg = "test fault"
		}
		return fmt.Errorf("testfault: %s (label=%q)", msg, label)
	case strings.HasPrefix(body, "truncate-after:"):
		// truncate-after is consulted via TruncateAfter; Maybe reports nil
		// so the production code can handle the truncation explicitly.
		return nil
	default:
		// Unknown fault spec — ignored. Tests should fail loudly via
		// other assertions if a typo silently disabled the fault.
		return nil
	}
}
