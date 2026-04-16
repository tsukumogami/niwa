package secret

import (
	"bytes"
	"context"
	"sort"
	"sync"
)

// minFragmentLen is the MUST minimum length (in bytes) of a fragment
// registered with a Redactor. Fragments shorter than this collide too
// often with ordinary English/log text to be safely replaced, so the
// Redactor silently refuses them. Secrets that short must be rejected
// at resolution time with a hard error (see Decision 2 Redactor
// Implementation Notes in DESIGN-vault-integration.md).
const minFragmentLen = 6

// redactedPlaceholder is the string used to replace registered
// fragments when Scrub runs.
const redactedPlaceholder = "***"

// Redactor accumulates known secret fragments and scrubs them out of
// arbitrary strings. Redactors are safe for concurrent use.
//
// A Redactor is typically per-apply (or per-resolve-call). The vault
// resolver creates one, threads it through context.Context via
// WithRedactor, and every Wrap/Errorf call within that context
// registers new fragments on it automatically.
type Redactor struct {
	mu        sync.Mutex
	fragments [][]byte
}

// NewRedactor returns an empty Redactor.
func NewRedactor() *Redactor {
	return &Redactor{}
}

// Register adds a fragment to the Redactor's known set. Fragments
// shorter than minFragmentLen (6 bytes) are silently refused and
// will NOT be scrubbed from future strings: such fragments collide
// too often with ordinary text to be safely replaced, so the design
// requires that they be rejected at resolution time as a hard error
// instead. Duplicate fragments are deduplicated.
//
// Register copies the input so the caller may reuse or zero its
// buffer after the call returns.
func (r *Redactor) Register(fragment []byte) {
	if len(fragment) < minFragmentLen {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.fragments {
		if bytes.Equal(existing, fragment) {
			return
		}
	}
	buf := make([]byte, len(fragment))
	copy(buf, fragment)
	r.fragments = append(r.fragments, buf)
}

// RegisterValue registers a Value's plaintext bytes on the Redactor
// as a single fragment. Empty values are skipped.
func (r *Redactor) RegisterValue(v Value) {
	b := v.bytes()
	if len(b) == 0 {
		return
	}
	r.Register(b)
}

// Scrub returns s with every registered fragment replaced by "***".
// Fragments are matched longest-first so that a short fragment which
// is a substring of a longer fragment does not shadow the longer
// match. Matching is plain-substring; no word-boundary logic is
// applied.
//
// Scrub is safe to call from multiple goroutines, and safe to call
// before any fragments are registered (it returns s unchanged).
func (r *Redactor) Scrub(s string) string {
	if s == "" {
		return s
	}
	r.mu.Lock()
	if len(r.fragments) == 0 {
		r.mu.Unlock()
		return s
	}
	// Copy fragments under lock so Scrub can release the mutex
	// before doing the replacement work.
	frags := make([][]byte, len(r.fragments))
	copy(frags, r.fragments)
	r.mu.Unlock()

	// Longest-first ordering. A stable sort keeps registration
	// order for ties, which is irrelevant to correctness but keeps
	// test output deterministic.
	sort.SliceStable(frags, func(i, j int) bool {
		return len(frags[i]) > len(frags[j])
	})

	out := s
	for _, frag := range frags {
		// strings.ReplaceAll equivalent, but working off []byte
		// fragments so we don't have to re-encode.
		out = replaceAll(out, string(frag), redactedPlaceholder)
	}
	return out
}

// replaceAll is strings.ReplaceAll inlined to avoid importing the
// strings package alongside bytes — keeps the dependency footprint
// minimal and mirrors the byte-oriented design.
func replaceAll(s, old, new string) string {
	if old == "" || old == new {
		return s
	}
	var b bytes.Buffer
	b.Grow(len(s))
	for {
		i := indexOf(s, old)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(new)
		s = s[i+len(old):]
	}
}

// indexOf is a tiny substring-index helper. We avoid strings.Index
// to keep imports tight; the standard bytes-based matcher would
// require an additional allocation.
func indexOf(s, sub string) int {
	n := len(sub)
	if n == 0 {
		return 0
	}
	if n > len(s) {
		return -1
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}

// redactorKey is the typed context key under which WithRedactor
// stores the Redactor. Using an unexported type prevents collisions
// with any other package that might also stash a value under a
// similar name.
type redactorKey struct{}

// WithRedactor returns a child context that carries r. Subsequent
// calls to RedactorFrom on the returned context (or any context
// derived from it) return r.
//
// Attaching a Redactor to context.Context is a mild anti-pattern —
// Go's standard library discourages context values for non-
// request-scoped data. Per Decision 2 of the vault-integration
// design, the per-apply Redactor IS request-scoped in the HTTP-
// server sense (scoped to a single `niwa apply` invocation), so the
// anti-pattern is accepted deliberately in exchange for letting
// Wrap/Errorf pick up the active Redactor without threading it
// through every function signature in the vault resolution
// pipeline. A future go/analysis linter (deferred to post-v1) will
// catch misuse.
func WithRedactor(ctx context.Context, r *Redactor) context.Context {
	return context.WithValue(ctx, redactorKey{}, r)
}

// RedactorFrom returns the Redactor attached to ctx, or nil if none
// was attached. Callers should be tolerant of a nil return: it
// simply means no fragments have been registered yet, so Scrub is a
// no-op.
func RedactorFrom(ctx context.Context) *Redactor {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(redactorKey{}).(*Redactor)
	return r
}
