package codec

import (
	"reflect"
	"testing"

	"github.com/tokenmill/tokenmill/internal/technique"
)

func TestTechniqueRegistryDelegatesToCanonicalPackage(t *testing.T) {
	canonicalIDs := technique.IDs()
	if got := TechniqueIDs(); !reflect.DeepEqual(got, canonicalIDs) {
		t.Fatalf("TechniqueIDs = %v, want canonical IDs %v", got, canonicalIDs)
	}
	for _, id := range canonicalIDs {
		if !HasTechnique(id) {
			t.Errorf("canonical technique %q is not registered", id)
		}
	}
	for _, definition := range technique.Definitions() {
		if !HasTechnique(definition.CodecID) {
			t.Errorf("codec ID %q is not delegated", definition.CodecID)
		}
		for _, alias := range definition.Aliases {
			if !HasTechnique(alias) {
				t.Errorf("alias %q is not delegated", alias)
			}
		}
	}
	if HasTechnique("not-a-real-technique") {
		t.Fatal("unknown technique must not be registered")
	}

	first := TechniqueIDs()
	second := TechniqueIDs()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("TechniqueIDs is not deterministic: %v != %v", first, second)
	}
	first[0] = "mutated"
	if TechniqueIDs()[0] == "mutated" {
		t.Fatal("TechniqueIDs must return a copy")
	}
}
