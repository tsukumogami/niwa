package store

import (
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// TestFileStemCoversEveryIdentityField walks vault.Identity by
// reflection, so a field added to the struct but not to fileStem fails
// here: changing any one field must change both the file name hash and
// the identity echo, and the echo must carry one entry per field.
func TestFileStemCoversEveryIdentityField(t *testing.T) {
	base := vault.Identity{}
	bv := reflect.ValueOf(&base).Elem()
	for i := 0; i < bv.NumField(); i++ {
		if bv.Field(i).Kind() != reflect.String {
			t.Fatalf("Identity.%s is %s; this test only knows string fields", bv.Type().Field(i).Name, bv.Field(i).Kind())
		}
		bv.Field(i).SetString("base")
	}
	baseStem, baseEcho := fileStem(base)
	if len(baseEcho) != bv.NumField() {
		t.Errorf("echo has %d entries, Identity has %d fields", len(baseEcho), bv.NumField())
	}

	for i := 0; i < bv.NumField(); i++ {
		name := bv.Type().Field(i).Name
		t.Run(name, func(t *testing.T) {
			other := base
			reflect.ValueOf(&other).Elem().Field(i).SetString("changed")
			stem, echo := fileStem(other)
			if stem == baseStem {
				t.Errorf("changing %s leaves the file stem unchanged", name)
			}
			if reflect.DeepEqual(echo, baseEcho) {
				t.Errorf("changing %s leaves the identity echo unchanged", name)
			}
		})
	}
}
