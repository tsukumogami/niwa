package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// Nothing maps a session to a tombstone, and a session mapped while a unit
// is tombstoned is not the unit's: the pass removes only the sessions it
// listed and vetted before the first removal, by their full ids, and its
// settlement deletes only the unit's own mapping.

const (
	lateOriginalSID  = "55555555-5555-4555-8555-555555555555"
	lateTombstoneSID = "66666666-6666-4666-8666-666666666666"
)

// claudeOwners keeps only the mappings that name the instance's own path:
// one naming the instance's tombstone is ignored.
func TestTombstoneMapping_ClaudeOwnersIgnoresAMappingNamingATombstone(t *testing.T) {
	root := canonicalTempDir(t)
	inst := filepath.Join(root, "test-ws-pass")
	tomb := filepath.Join(root, workspace.TombstoneName("test-ws-pass", "0011aabb"))
	mappings := []workspace.SessionMapping{
		{SessionID: lateTombstoneSID, InstancePath: tomb},
		{SessionID: passSessionID, InstancePath: inst},
	}
	owners := claudeOwners(mappings, []string{inst})
	if len(owners) != 1 || owners[0].SessionID != passSessionID {
		t.Fatalf("owners %+v; want only the mapping naming %s", owners, inst)
	}
}

// A mapping written for another session while the unit is tombstoned, after
// the last full vet (here at the unit's claude rm), naming the instance's
// original path or its tombstone, is left alone: the pass removes only the
// unit's listed session and settles only the unit's mapping, and both late
// mappings are there afterwards, naming what they were written with.
func TestTombstoneMapping_AMappingWrittenWhileTheUnitIsTombstonedIsLeftAlone(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	passJobDir(t, f)
	fake := &listingFake{rows: []claudeSessionRow{
		passRow(),
		{ID: lateOriginalSID[:8], SessionID: lateOriginalSID},
		{ID: lateTombstoneSID[:8], SessionID: lateTombstoneSID},
	}}
	installListingFake(t, fake)
	var removed []string
	var written bool
	prevRemove := removeClaudeSessionFunc
	removeClaudeSessionFunc = func(ctx context.Context, id string) (string, error) {
		if !written {
			written = true
			tombs, err := workspace.ListTombstones(f.root)
			if err != nil || len(tombs) != 1 {
				t.Fatalf("tombstones %v (%v) at claude rm; want one", tombs, err)
			}
			mapEphemeral(t, f.root, lateOriginalSID, f.inst, true)
			mapEphemeral(t, f.root, lateTombstoneSID, tombs[0], true)
		}
		removed = append(removed, id)
		return "", nil
	}
	t.Cleanup(func() { removeClaudeSessionFunc = prevRemove })
	destroyed := realRemoval(t)

	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 1 || len(*destroyed) != 1 {
		t.Fatalf("reaped %d, removed %v; want the unit removed\nstderr:\n%s", res.Reaped, *destroyed, f.stderr.String())
	}
	if !written {
		t.Fatal("claude rm never ran, so no late mapping was written")
	}
	if strings.Join(removed, ",") != passSessionID[:8] {
		t.Fatalf("claude rm calls %q; want only the unit's own session %s", removed, passSessionID[:8])
	}
	if mappingExists(t, f.root, passSessionID) {
		t.Error("the unit's own mapping wasn't settled")
	}
	for sid, path := range map[string]string{lateOriginalSID: f.inst, lateTombstoneSID: ""} {
		m, err := workspace.ReadSessionMapping(f.root, sid)
		if err != nil {
			t.Errorf("the late mapping for %s is gone: %v", sid, err)
			continue
		}
		if path != "" && m.InstancePath != path {
			t.Errorf("the late mapping for %s names %s; want %s", sid, m.InstancePath, path)
		}
		if path == "" && !strings.Contains(m.InstancePath, workspace.TombstonePrefix) {
			t.Errorf("the late mapping for %s names %s; want the tombstone", sid, m.InstancePath)
		}
	}
	assertNoTombstone(t, f.root)
}
