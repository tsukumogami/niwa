package github

// StatusError carries a non-2xx HTTP status from GitHub API calls in a
// way that callers can recover via errors.As. The Message field holds
// the exact wrapped text each call site would have produced today, so
// the type can be introduced without changing user-visible error text.
//
// Each construction site precomputes the message so Error() stays
// trivial (no need to switch on Method+StatusCode here). The
// StatusCode field lets classifiers branch on 401/403/404 without
// re-parsing the message string.
type StatusError struct {
	StatusCode int
	Message    string
	URL        string
}

// Error returns the precomputed Message verbatim. Construction sites
// own the wording so callers can preserve byte-for-byte compatibility
// with prior fmt.Errorf-based formatting.
func (e *StatusError) Error() string { return e.Message }
