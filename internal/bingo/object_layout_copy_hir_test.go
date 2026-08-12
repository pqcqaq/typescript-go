package bingo

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func testObjectLayoutCopyHIRArtifact(t testing.TB) ObjectLayoutCopyHIRArtifact {
	t.Helper()
	artifact, err := BuildObjectLayoutCopyHIRArtifact(testObjectLayoutCopyContract(t))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func TestObjectLayoutCopyHIRMaterializesAllocationLoadsStoresAndRoots(t *testing.T) {
	artifact := testObjectLayoutCopyHIRArtifact(t)
	if artifact.ReturnValueID != 2 || len(artifact.Operations) != 3 || artifact.Operations[0].Kind != "object.copy.target.alloc" || artifact.Operations[1].Kind != "object.copy.source.load" || artifact.Operations[2].Kind != "object.copy.target.store" {
		t.Fatalf("unexpected object layout copy HIR: %#v", artifact)
	}
	if !slices.Equal(artifact.Operations[1].Operands, []ValueID{1}) || !slices.Equal(artifact.Operations[2].Operands, []ValueID{2, 3}) {
		t.Fatalf("unexpected object layout copy operands: %#v", artifact.Operations)
	}
	instructions := artifact.GCSafety.Blocks[0].Instructions
	if len(instructions) != 9 || instructions[2].Kind != GCOpRootStore || instructions[2].Value != 1 || instructions[4].Kind != GCOpSafepoint || instructions[5].Kind != GCOpRootReload || instructions[6].Kind != GCOpRefDef || instructions[6].Value != 2 {
		t.Fatalf("unexpected object layout copy GC safety: %#v", artifact.GCSafety)
	}
	encoded, _, err := CanonicalObjectLayoutCopyHIRArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectLayoutCopyHIRArtifact(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestObjectLayoutCopyHIRRejectsSubstitution(t *testing.T) {
	base := testObjectLayoutCopyHIRArtifact(t)
	tests := map[string]func(*ObjectLayoutCopyHIRArtifact){
		"capability": func(value *ObjectLayoutCopyHIRArtifact) { value.RequiredCapability = "rt.gc.collect" },
		"return":     func(value *ObjectLayoutCopyHIRArtifact) { value.ReturnValueID = 3 },
		"order": func(value *ObjectLayoutCopyHIRArtifact) {
			value.Operations[1], value.Operations[2] = value.Operations[2], value.Operations[1]
		},
		"operand":        func(value *ObjectLayoutCopyHIRArtifact) { value.Operations[2].Operands[0] = 1 },
		"offset":         func(value *ObjectLayoutCopyHIRArtifact) { value.Operations[1].SourceFieldOffset++ },
		"representation": func(value *ObjectLayoutCopyHIRArtifact) { value.Operations[2].TargetRepresentation = "gc-ref" },
		"effect":         func(value *ObjectLayoutCopyHIRArtifact) { value.Operations[1].Effects = []Effect{EffectWrite} },
		"root value":     func(value *ObjectLayoutCopyHIRArtifact) { value.GCSafety.Blocks[0].Instructions[2].Value = 2 },
		"safepoint": func(value *ObjectLayoutCopyHIRArtifact) {
			value.GCSafety.Blocks[0].Instructions[4].SafepointKind = "poll"
		},
		"trace layout": func(value *ObjectLayoutCopyHIRArtifact) { value.GCSafety.Slots[0].TraceLayoutHash = typeKeyA },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Operations = append([]ObjectLayoutCopyHIROperation(nil), base.Operations...)
			for index := range value.Operations {
				value.Operations[index].Operands = slices.Clone(base.Operations[index].Operands)
				value.Operations[index].RelationPath = slices.Clone(base.Operations[index].RelationPath)
				value.Operations[index].Effects = slices.Clone(base.Operations[index].Effects)
			}
			encodedGC, _, err := CanonicalGCSafetyPlan(base.GCSafety)
			if err != nil {
				t.Fatal(err)
			}
			gc, err := DecodeGCSafetyPlan(encodedGC)
			if err != nil {
				t.Fatal(err)
			}
			value.GCSafety = *gc
			mutate(&value)
			value.ContentHash = ""
			if _, _, err := CanonicalObjectLayoutCopyHIRArtifact(value); err == nil {
				t.Fatal("substituted object layout copy HIR was accepted")
			}
		})
	}
}

func TestObjectLayoutCopyHIRRejectsReferenceMappingUntilBarrierLoweringExists(t *testing.T) {
	view := testObjectViewProof(t)
	sourceLayout, err := PlanObjectLayout(view.Source.TypeKey, view.SourceLayout.Target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		t.Fatal(err)
	}
	targetLayout, err := PlanObjectLayout(view.Target.TypeKey, view.TargetLayout.Target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		t.Fatal(err)
	}
	copyContract, err := BuildObjectLayoutCopyContract(view.Source, view.Target, view.Relations, sourceLayout, targetLayout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildObjectLayoutCopyHIRArtifact(copyContract); err == nil || !strings.Contains(err.Error(), "requires f64") {
		t.Fatalf("reference copy HIR error = %v", err)
	}
}

func TestObjectLayoutCopyHIRStrictDecode(t *testing.T) {
	artifact := testObjectLayoutCopyHIRArtifact(t)
	encoded, _, err := CanonicalObjectLayoutCopyHIRArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	stale := bytes.Replace(encoded, []byte(artifact.ContentHash), []byte(strings.Repeat("a", 64)), 1)
	for name, data := range map[string][]byte{"unknown": unknown, "stale": stale, "oversize": make([]byte, maxObjectLayoutCopyHIRBytes+1)} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeObjectLayoutCopyHIRArtifact(data); err == nil {
				t.Fatal("invalid object layout copy HIR was accepted")
			}
		})
	}
}

func FuzzDecodeObjectLayoutCopyHIRArtifact(f *testing.F) {
	artifact := testObjectLayoutCopyHIRArtifact(f)
	encoded, _, err := CanonicalObjectLayoutCopyHIRArtifact(artifact)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectLayoutCopyHIRBytes+1 {
			return
		}
		artifact, err := DecodeObjectLayoutCopyHIRArtifact(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectLayoutCopyHIRArtifact(*artifact)
		if err != nil {
			t.Fatalf("accepted object layout copy HIR is not canonical: %v", err)
		}
		if _, err := DecodeObjectLayoutCopyHIRArtifact(canonical); err != nil {
			t.Fatalf("canonical object layout copy HIR does not round trip: %v", err)
		}
	})
}
