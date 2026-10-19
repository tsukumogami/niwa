package claudetrust

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, home, body string) string {
	t.Helper()
	path := filepath.Join(home, configBasename)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIsTrusted(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	instance := filepath.Join(root, "instance")
	if err := os.MkdirAll(instance, 0o755); err != nil {
		t.Fatal(err)
	}
	rootKey, err := Key(root)
	if err != nil {
		t.Fatal(err)
	}
	instanceKey, err := Key(instance)
	if err != nil {
		t.Fatal(err)
	}
	env := Env{Home: home}

	if trusted, known := IsTrusted(env, instance); trusted || !known {
		t.Errorf("no config file: got trusted=%v known=%v, want untrusted and known", trusted, known)
	}

	writeConfig(t, home, `{"projects":{"`+instanceKey+`":{"hasTrustDialogAccepted":true}}}`)
	if trusted, known := IsTrusted(env, instance); !trusted || !known {
		t.Errorf("instance trusted: got trusted=%v known=%v", trusted, known)
	}

	writeConfig(t, home, `{"projects":{"`+rootKey+`":{"hasTrustDialogAccepted":true}}}`)
	if trusted, known := IsTrusted(env, instance); !trusted || !known {
		t.Errorf("trusted ancestor: got trusted=%v known=%v, want inherited trust", trusted, known)
	}

	writeConfig(t, home, `{"projects":{"`+instanceKey+`":{"hasTrustDialogAccepted":false}}}`)
	if trusted, known := IsTrusted(env, instance); trusted || !known {
		t.Errorf("explicitly untrusted: got trusted=%v known=%v", trusted, known)
	}

	path := writeConfig(t, home, `{not json`)
	if _, known := IsTrusted(env, instance); known {
		t.Error("an unparseable config must report unknown")
	}
	before, _ := os.ReadFile(path)
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("IsTrusted must not modify the config file")
	}

	if _, known := IsTrusted(Env{}, instance); known {
		t.Error("no home must report unknown")
	}
}

func TestIsTrusted_PrefersLegacyFile(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	key, err := Key(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, home, `{"projects":{"`+key+`":{"hasTrustDialogAccepted":true}}}`)
	legacyDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, legacyConfigBasename), []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if trusted, known := IsTrusted(Env{Home: home}, dir); trusted || !known {
		t.Errorf("legacy file present: got trusted=%v known=%v, want what the legacy file says", trusted, known)
	}
}
