package config

import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/secret"
)

func TestMaybeSecretZeroValue(t *testing.T) {
	var m MaybeSecret
	if m.IsSecret() {
		t.Errorf("zero MaybeSecret should not be a secret")
	}
	if m.String() != "" {
		t.Errorf("zero MaybeSecret String() = %q, want empty", m.String())
	}
}

func TestMaybeSecretPlainString(t *testing.T) {
	m := MaybeSecret{Plain: "hello"}
	if m.IsSecret() {
		t.Errorf("plain MaybeSecret should not be a secret")
	}
	if m.String() != "hello" {
		t.Errorf("plain MaybeSecret String() = %q, want %q", m.String(), "hello")
	}
}

func TestMaybeSecretSecretRedacts(t *testing.T) {
	m := MaybeSecret{
		Secret: secret.New([]byte("sekret"), secret.Origin{Key: "API_KEY"}),
	}
	if !m.IsSecret() {
		t.Errorf("MaybeSecret with non-empty Secret should be a secret")
	}
	if m.String() != "***" {
		t.Errorf("secret MaybeSecret String() = %q, want %q", m.String(), "***")
	}
}

func TestMaybeSecretTOMLDecode(t *testing.T) {
	type fixture struct {
		Vars map[string]MaybeSecret `toml:"vars"`
	}

	input := `
[vars]
PLAIN = "literal"
VAULT_REF = "vault://team-vault/API_KEY"
`
	var f fixture
	if _, err := toml.Decode(input, &f); err != nil {
		t.Fatalf("toml.Decode: %v", err)
	}
	if got := f.Vars["PLAIN"].Plain; got != "literal" {
		t.Errorf("Vars[PLAIN].Plain = %q, want %q", got, "literal")
	}
	if f.Vars["PLAIN"].IsSecret() {
		t.Errorf("Vars[PLAIN].IsSecret() = true, want false")
	}
	if got := f.Vars["VAULT_REF"].Plain; got != "vault://team-vault/API_KEY" {
		t.Errorf("Vars[VAULT_REF].Plain = %q, want %q", got, "vault://team-vault/API_KEY")
	}
	// The parser does not promote a vault URI to IsSecret — only the
	// resolver does.
	if f.Vars["VAULT_REF"].IsSecret() {
		t.Errorf("parser must leave IsSecret() = false on vault:// input")
	}
}

func TestMaybeSecretMarshalTextRedactsSecret(t *testing.T) {
	m := MaybeSecret{
		Secret: secret.New([]byte("sekret"), secret.Origin{}),
	}
	out, err := m.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(out) != "***" {
		t.Errorf("MarshalText for secret = %q, want %q", string(out), "***")
	}
}

func TestMaybeSecretMarshalTextPlain(t *testing.T) {
	m := MaybeSecret{Plain: "open"}
	out, err := m.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(out) != "open" {
		t.Errorf("MarshalText for plain = %q, want %q", string(out), "open")
	}
}
