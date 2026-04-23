package config

import "testing"

func TestPopulateMirror_PopulatesFromSourceURL(t *testing.T) {
	e := &RegistryEntry{SourceURL: "tsukumogami/niwa:.niwa@v1.2.0"}
	if !e.PopulateMirror() {
		t.Error("expected change")
	}
	if e.SourceOwner != "tsukumogami" || e.SourceRepo != "niwa" {
		t.Errorf("owner/repo: %s/%s", e.SourceOwner, e.SourceRepo)
	}
	if e.SourceSubpath != ".niwa" {
		t.Errorf("subpath: %q", e.SourceSubpath)
	}
	if e.SourceRef != "v1.2.0" {
		t.Errorf("ref: %q", e.SourceRef)
	}
	if e.SourceHost != "" {
		t.Errorf("host should default empty for github.com slugs, got %q", e.SourceHost)
	}
}

func TestPopulateMirror_NoChangeWhenAlreadyCorrect(t *testing.T) {
	e := &RegistryEntry{
		SourceURL:   "tsukumogami/niwa",
		SourceOwner: "tsukumogami",
		SourceRepo:  "niwa",
	}
	if e.PopulateMirror() {
		t.Error("expected no change for correct mirror")
	}
}

func TestPopulateMirror_OverwritesInconsistentMirror(t *testing.T) {
	e := &RegistryEntry{
		SourceURL:     "tsukumogami/niwa:.niwa",
		SourceOwner:   "wrong",
		SourceRepo:    "wrong",
		SourceSubpath: "wrong",
	}
	if !e.PopulateMirror() {
		t.Error("expected change to reconcile mirror with canonical SourceURL")
	}
	if e.SourceOwner != "tsukumogami" || e.SourceRepo != "niwa" || e.SourceSubpath != ".niwa" {
		t.Errorf("mirror not reconciled: %+v", e)
	}
}

func TestPopulateMirror_EmptyURL(t *testing.T) {
	e := &RegistryEntry{}
	if e.PopulateMirror() {
		t.Error("expected no change for empty URL")
	}
}

func TestPopulateMirror_MalformedURLLeavesMirrorEmpty(t *testing.T) {
	e := &RegistryEntry{SourceURL: "not a valid slug at all !!"}
	if e.PopulateMirror() {
		t.Error("expected no change for malformed URL")
	}
	if e.SourceOwner != "" || e.SourceRepo != "" {
		t.Errorf("malformed URL should leave mirror empty: %+v", e)
	}
}

func TestPopulateMirror_NilSafe(t *testing.T) {
	var e *RegistryEntry
	if e.PopulateMirror() {
		t.Error("nil receiver should report no change")
	}
}
