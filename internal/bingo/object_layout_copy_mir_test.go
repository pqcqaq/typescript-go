package bingo

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func testObjectLayoutCopyMIR(t testing.TB) ObjectLayoutCopyMIRArtifact {
	t.Helper()
	module, err := LowerObjectLayoutCopyMIR(testObjectLayoutCopyHIRArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func TestObjectLayoutCopyMIRCanonicalRoundTrip(t *testing.T) {
	module := testObjectLayoutCopyMIR(t)
	if module.ReturnValueID != 2 || len(module.Instructions) != 3 || module.Instructions[0].Operation != "gc.alloc.target" || !module.Instructions[0].MaySafepoint || module.Instructions[1].Operation != "field.load.source" || module.Instructions[2].Operation != "field.store.target" {
		t.Fatalf("unexpected copy MIR: %#v", module)
	}
	encoded, _, err := CanonicalObjectLayoutCopyMIR(module)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectLayoutCopyMIR(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestObjectLayoutCopyMIRRejectsSubstitution(t *testing.T) {
	base := testObjectLayoutCopyMIR(t)
	tests := map[string]func(*ObjectLayoutCopyMIRArtifact){
		"target": func(value *ObjectLayoutCopyMIRArtifact) { value.TargetTriple = "other" },
		"layout": func(value *ObjectLayoutCopyMIRArtifact) { value.DataLayoutHash = strings.Repeat("a", 64) },
		"return": func(value *ObjectLayoutCopyMIRArtifact) { value.ReturnValueID = 3 },
		"order": func(value *ObjectLayoutCopyMIRArtifact) {
			value.Instructions[1], value.Instructions[2] = value.Instructions[2], value.Instructions[1]
		},
		"operand":   func(value *ObjectLayoutCopyMIRArtifact) { value.Instructions[2].Operands[0] = 1 },
		"offset":    func(value *ObjectLayoutCopyMIRArtifact) { value.Instructions[1].FieldOffset++ },
		"safepoint": func(value *ObjectLayoutCopyMIRArtifact) { value.Instructions[0].MaySafepoint = false },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Instructions = append([]ObjectLayoutCopyMIRInstruction(nil), base.Instructions...)
			for i := range value.Instructions {
				value.Instructions[i].Operands = slices.Clone(base.Instructions[i].Operands)
				value.Instructions[i].RelationPath = slices.Clone(base.Instructions[i].RelationPath)
				value.Instructions[i].Effects = slices.Clone(base.Instructions[i].Effects)
			}
			mutate(&value)
			value.ContentHash = ""
			if _, _, err := CanonicalObjectLayoutCopyMIR(value); err == nil {
				t.Fatal("substituted copy MIR was accepted")
			}
		})
	}
}

func TestObjectLayoutCopyBoundRoundTripAndTamper(t *testing.T) {
	bound, err := NewObjectLayoutCopyBoundArtifact(testObjectLayoutCopyMIR(t), typeKeyA, typeKeyB, testObjectLayoutCopyBindings())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := CanonicalObjectLayoutCopyBoundArtifact(bound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectLayoutCopyBoundArtifact(encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ObjectLayoutCopyBoundArtifact){
		"context":   func(value *ObjectLayoutCopyBoundArtifact) { value.TargetContextHash = "bad" },
		"catalog":   func(value *ObjectLayoutCopyBoundArtifact) { value.CatalogHash = "bad" },
		"logical":   func(value *ObjectLayoutCopyBoundArtifact) { value.Bindings[0].LogicalName = "rt.gc.collect" },
		"symbol":    func(value *ObjectLayoutCopyBoundArtifact) { value.Bindings[0].SymbolName = "" },
		"signature": func(value *ObjectLayoutCopyBoundArtifact) { value.Bindings[0].SignatureHash = "bad" },
		"missing":   func(value *ObjectLayoutCopyBoundArtifact) { value.Bindings = value.Bindings[1:] },
	} {
		t.Run(name, func(t *testing.T) {
			value := bound
			mutate(&value)
			value.ContentHash = ""
			if _, _, err := CanonicalObjectLayoutCopyBoundArtifact(value); err == nil {
				t.Fatal("substituted bound copy was accepted")
			}
		})
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeObjectLayoutCopyBoundArtifact(unknown); err == nil {
		t.Fatal("bound copy accepted unknown member")
	}
}

func FuzzDecodeObjectLayoutCopyMIR(f *testing.F) {
	encoded, _, err := CanonicalObjectLayoutCopyMIR(testObjectLayoutCopyMIR(f))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) <= maxObjectLayoutCopyMIRBytes+1 {
			_, _ = DecodeObjectLayoutCopyMIR(data)
		}
	})
}

func FuzzDecodeObjectLayoutCopyBound(f *testing.F) {
	bound, err := NewObjectLayoutCopyBoundArtifact(testObjectLayoutCopyMIR(f), typeKeyA, typeKeyB, testObjectLayoutCopyBindings())
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalObjectLayoutCopyBoundArtifact(bound)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) <= maxObjectLayoutCopyBoundBytes+1 {
			_, _ = DecodeObjectLayoutCopyBoundArtifact(data)
		}
	})
}

func testObjectLayoutCopyBindings() []BoundCapability {
	requirements := ObjectLayoutCopyCapabilityRequirements()
	bindings := make([]BoundCapability, len(requirements))
	for index, requirement := range requirements {
		bindings[index] = BoundCapability{LogicalName: requirement, SymbolName: "symbol." + string(requirement), SignatureHash: typeKeyC}
	}
	return bindings
}
