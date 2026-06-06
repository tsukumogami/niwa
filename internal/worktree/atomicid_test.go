package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReserveID_SuccessFirstTry(t *testing.T) {
	dir := t.TempDir()
	id, err := ReserveID(dir, func() (string, error) { return "abc123", nil }, func(id string) string { return id + ".json" })
	if err != nil {
		t.Fatalf("ReserveID: %v", err)
	}
	if id != "abc123" {
		t.Errorf("id = %q, want abc123", id)
	}
	if _, err := os.Stat(filepath.Join(dir, "abc123.json")); err != nil {
		t.Errorf("placeholder not created: %v", err)
	}
}

func TestReserveID_RetryOnCollision(t *testing.T) {
	dir := t.TempDir()
	// Pre-create the first ID's placeholder so the first attempt collides.
	if err := os.WriteFile(filepath.Join(dir, "first.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	calls := 0
	gen := func() (string, error) {
		calls++
		if calls == 1 {
			return "first", nil
		}
		return "second", nil
	}
	id, err := ReserveID(dir, gen, func(id string) string { return id + ".json" })
	if err != nil {
		t.Fatalf("ReserveID: %v", err)
	}
	if id != "second" {
		t.Errorf("id = %q, want second (retry after collision)", id)
	}
	if calls != 2 {
		t.Errorf("gen called %d times, want 2 (one collision + one success)", calls)
	}
}

func TestReserveID_ExhaustionAfterFiveCollisions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fixed.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ReserveID(dir, func() (string, error) { return "fixed", nil }, func(id string) string { return id + ".json" })
	if err == nil {
		t.Fatal("ReserveID returned nil error after 5 collisions, want error")
	}
	if !strings.Contains(err.Error(), "failed to generate unique") || !strings.Contains(err.Error(), "ID after 5 attempts") {
		t.Errorf("error message = %q, want match of 'failed to generate unique .* ID after 5 attempts'", err.Error())
	}
}

func TestReserveID_GeneratorErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	sentinel := errors.New("rng dead")
	_, err := ReserveID(dir, func() (string, error) { return "", sentinel }, func(id string) string { return id + ".json" })
	if err == nil {
		t.Fatal("want generator error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("want errors.Is(err, sentinel)=true, got err=%v", err)
	}
}

func TestReserveID_NonExistFilesystemErrorPropagates(t *testing.T) {
	// Use a non-existent directory so the underlying open returns
	// ENOENT (which is not EEXIST) and the retry loop bails.
	bogus := filepath.Join(t.TempDir(), "does", "not", "exist")
	_, err := ReserveID(bogus, func() (string, error) { return "x", nil }, func(id string) string { return id + ".json" })
	if err == nil {
		t.Fatal("want filesystem error, got nil")
	}
	if !strings.Contains(err.Error(), "reserving ID") {
		t.Errorf("error message = %q, want prefix 'reserving ID'", err.Error())
	}
}
