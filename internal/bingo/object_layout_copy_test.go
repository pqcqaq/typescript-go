package bingo

import (
	"strings"
	"testing"
)

func testObjectLayoutCopyContract(t testing.TB) ObjectLayoutCopyContract {
	t.Helper()
	view := testObjectViewProof(t)
	contract, err := BuildObjectLayoutCopyContract(view.Source, view.Target, view.Relations, view.SourceLayout, view.TargetLayout)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestObjectLayoutCopyCanonicalRoundTrip(t *testing.T) {
	contract := testObjectLayoutCopyContract(t)
	if !contract.AllocatesTarget || contract.PreservesIdentity || contract.InvokesAccessors || len(contract.Mappings) != 1 {
		t.Fatalf("unexpected copy contract: %#v", contract)
	}
	encoded, hash, err := CanonicalObjectLayoutCopyContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	if hash != contract.ContentHash {
		t.Fatalf("copy hash = %q, want %q", hash, contract.ContentHash)
	}
	decoded, err := DecodeObjectLayoutCopyContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != contract.ContentHash {
		t.Fatalf("decoded copy hash = %q", decoded.ContentHash)
	}
}

func TestObjectLayoutCopyRejectsPolicyAndMappingSubstitution(t *testing.T) {
	tests := map[string]func(*ObjectLayoutCopyContract){
		"no allocation":         func(value *ObjectLayoutCopyContract) { value.AllocatesTarget = false },
		"identity preservation": func(value *ObjectLayoutCopyContract) { value.PreservesIdentity = true },
		"accessor invocation":   func(value *ObjectLayoutCopyContract) { value.InvokesAccessors = true },
		"source offset":         func(value *ObjectLayoutCopyContract) { value.Mappings[0].SourceFieldOffset++ },
		"target offset":         func(value *ObjectLayoutCopyContract) { value.Mappings[0].TargetFieldOffset++ },
		"source representation": func(value *ObjectLayoutCopyContract) { value.Mappings[0].SourceRepresentation = "gc-ref" },
		"target representation": func(value *ObjectLayoutCopyContract) { value.Mappings[0].TargetRepresentation = "gc-ref" },
		"relation path":         func(value *ObjectLayoutCopyContract) { value.Mappings[0].ReadRelationPath = []string{"forged"} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := testObjectLayoutCopyContract(t)
			mutate(&value)
			if _, _, err := CanonicalObjectLayoutCopyContract(value); err == nil {
				t.Fatal("substituted copy contract was accepted")
			}
		})
	}
}

func TestObjectLayoutCopyRejectsUnsupportedPropertySurfaces(t *testing.T) {
	tests := map[string]func(*ObjectSemanticContract){
		"optional":  func(value *ObjectSemanticContract) { value.Properties[0].Optional = true },
		"accessor":  func(value *ObjectSemanticContract) { value.Properties[0].Kind = ObjectPropertyAccessor },
		"protected": func(value *ObjectSemanticContract) { value.Properties[0].Visibility = "protected" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			view := testObjectViewProof(t)
			target := view.Target
			target.Properties = append([]ObjectPropertyContract(nil), target.Properties...)
			mutate(&target)
			_, hash, err := CanonicalObjectSemanticContract(target)
			if err != nil {
				t.Fatal(err)
			}
			target.ContentHash = hash
			if _, err := BuildObjectLayoutCopyContract(view.Source, target, view.Relations, view.SourceLayout, view.TargetLayout); err == nil {
				t.Fatal("unsupported copy surface was accepted")
			}
		})
	}
}

func TestObjectLayoutCopyStrictDecode(t *testing.T) {
	encoded, _, err := CanonicalObjectLayoutCopyContract(testObjectLayoutCopyContract(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"unknown":  []byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1)),
		"stale":    []byte(strings.Replace(string(encoded), `"schemaVersion":1`, `"schemaVersion":2`, 1)),
		"oversize": make([]byte, maxObjectLayoutCopyBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeObjectLayoutCopyContract(data); err == nil {
				t.Fatal("invalid copy contract was accepted")
			}
		})
	}
}
