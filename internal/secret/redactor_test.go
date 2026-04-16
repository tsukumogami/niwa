package secret_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

func TestRedactorScrubEmpty(t *testing.T) {
	r := secret.NewRedactor()
	if got := r.Scrub(""); got != "" {
		t.Fatalf("Scrub(\"\") = %q, want empty", got)
	}
	if got := r.Scrub("no fragments registered"); got != "no fragments registered" {
		t.Fatalf("Scrub with no fragments should be identity, got %q", got)
	}
}

func TestRedactorScrubRegisteredFragment(t *testing.T) {
	r := secret.NewRedactor()
	r.Register([]byte("s3cret-token"))
	in := "error: API responded with s3cret-token during call"
	got := r.Scrub(in)
	if strings.Contains(got, "s3cret-token") {
		t.Fatalf("Scrub did not replace fragment: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("Scrub output missing placeholder: %q", got)
	}
}

// TestRedactorLongestFirst asserts AC: longest-first ordering. A
// short fragment that is a substring of a longer fragment must not
// shadow the longer match. We register "abc" (too short anyway so it
// is refused), then "abcdefghij" and "abcdef12" and check both
// longer fragments get cleanly replaced.
func TestRedactorLongestFirst(t *testing.T) {
	r := secret.NewRedactor()
	// Fragments >= 6 bytes so they actually register.
	r.Register([]byte("abcdef"))
	r.Register([]byte("abcdefghijk"))
	in := "value=abcdefghijk and prefix=abcdef only"
	got := r.Scrub(in)
	// The 11-byte fragment must match wholly before the 6-byte
	// substring is replaced. Expected:
	//   "value=*** and prefix=*** only"
	want := "value=*** and prefix=*** only"
	if got != want {
		t.Fatalf("Scrub() = %q, want %q", got, want)
	}
}

// TestRedactorSkipsShortFragment asserts AC: fragments shorter than
// six bytes are NOT registered and are NOT scrubbed. This is the
// MUST per design Security Considerations.
func TestRedactorSkipsShortFragment(t *testing.T) {
	r := secret.NewRedactor()
	r.Register([]byte("abc"))    // 3 bytes — refused
	r.Register([]byte("12345"))  // 5 bytes — refused
	r.Register([]byte("123456")) // 6 bytes — accepted (boundary)
	in := "values abc 12345 123456 present"
	got := r.Scrub(in)
	// abc and 12345 must still appear (not registered).
	if !strings.Contains(got, "abc") {
		t.Fatalf("short fragment 'abc' was scrubbed; should be refused: %q", got)
	}
	if !strings.Contains(got, "12345 ") {
		t.Fatalf("short fragment '12345' was scrubbed; should be refused: %q", got)
	}
	// 123456 must be scrubbed (exactly 6 bytes is the minimum).
	if strings.Contains(got, "123456") {
		t.Fatalf("6-byte fragment '123456' was not scrubbed: %q", got)
	}
}

func TestRedactorDeduplicatesFragments(t *testing.T) {
	r := secret.NewRedactor()
	r.Register([]byte("duplicate-fragment"))
	r.Register([]byte("duplicate-fragment"))
	r.Register([]byte("duplicate-fragment"))
	// No API to count fragments, but we can verify Scrub still
	// works and produces a single replacement per occurrence.
	got := r.Scrub("see duplicate-fragment here")
	if got != "see *** here" {
		t.Fatalf("Scrub() = %q, want %q", got, "see *** here")
	}
}

func TestRedactorRegisterValue(t *testing.T) {
	r := secret.NewRedactor()
	v := secret.New([]byte("correct-horse-battery-staple"), secret.Origin{})
	r.RegisterValue(v)
	got := r.Scrub("password is correct-horse-battery-staple, do not share")
	if strings.Contains(got, "correct-horse-battery-staple") {
		t.Fatalf("RegisterValue did not scrub plaintext: %q", got)
	}
}

func TestRedactorRegisterValueEmpty(t *testing.T) {
	r := secret.NewRedactor()
	var zero secret.Value
	r.RegisterValue(zero) // must be a no-op
	if got := r.Scrub("nothing to scrub"); got != "nothing to scrub" {
		t.Fatalf("Scrub after empty RegisterValue = %q", got)
	}
}

func TestWithRedactorAndRedactorFrom(t *testing.T) {
	ctx := context.Background()
	if got := secret.RedactorFrom(ctx); got != nil {
		t.Fatalf("RedactorFrom(empty) = %v, want nil", got)
	}
	r := secret.NewRedactor()
	ctx = secret.WithRedactor(ctx, r)
	if got := secret.RedactorFrom(ctx); got != r {
		t.Fatalf("RedactorFrom after WithRedactor returned different pointer")
	}
}

func TestRedactorFromNilContext(t *testing.T) {
	//nolint:staticcheck // deliberately passing nil to confirm defensive return.
	if got := secret.RedactorFrom(nil); got != nil {
		t.Fatalf("RedactorFrom(nil) = %v, want nil", got)
	}
}

// TestRedactorConcurrent exercises the mutex protecting fragment
// state. Run under -race to surface any data race.
func TestRedactorConcurrent(t *testing.T) {
	r := secret.NewRedactor()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			r.Register([]byte("fragment-" + string(rune('a'+i%26)) + "xxxxx"))
		}(i)
		go func() {
			defer wg.Done()
			_ = r.Scrub("some fragment-axxxxx text")
		}()
	}
	wg.Wait()
}
