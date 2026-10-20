package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteInstanceContext(t *testing.T) {
	root := guardInstance(t)
	var out bytes.Buffer
	if err := writeInstanceContext(&out, root); err != nil {
		t.Fatal(err)
	}
	var inj sessionStartInjection
	if err := json.Unmarshal(out.Bytes(), &inj); err != nil {
		t.Fatalf("output is not the SessionStart JSON: %v\n%s", err, out.String())
	}
	if inj.HookSpecificOutput.HookEventName != hookEventSessionStart {
		t.Errorf("hookEventName = %q", inj.HookSpecificOutput.HookEventName)
	}
	text := inj.HookSpecificOutput.AdditionalContext
	for _, want := range []string{"This directory is a niwa instance root.", "public/niwa", "public/koto", "<repo>/<slug>"} {
		if !strings.Contains(text, want) {
			t.Errorf("context lacks %q:\n%s", want, text)
		}
	}

	out.Reset()
	if err := writeInstanceContext(&out, t.TempDir()); err != nil || out.Len() != 0 {
		t.Errorf("outside an instance: want no output, got %q (%v)", out.String(), err)
	}
}

func TestBuildSessionStartInjection_IncludesRootContext(t *testing.T) {
	root := guardInstance(t)
	encoded, err := buildSessionStartInjection(root, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var inj sessionStartInjection
	if err := json.Unmarshal(encoded, &inj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inj.HookSpecificOutput.AdditionalContext, "This directory is a niwa instance root.") {
		t.Errorf("ephemeral injection should carry the layout context:\n%s", inj.HookSpecificOutput.AdditionalContext)
	}
}
