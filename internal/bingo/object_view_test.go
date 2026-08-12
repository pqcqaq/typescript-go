package bingo

import (
	"slices"
	"strings"
	"testing"
)

func testObjectViewProof(t testing.TB) ObjectViewProof {
	t.Helper()
	source := ObjectSemanticContract{SchemaVersion: ObjectSemanticContractSchemaVersion, TypeKey: typeKeyB, Identity: ObjectIdentityReference, Equality: ObjectEqualityReference, Properties: []ObjectPropertyContract{{Key: "value", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, WriteTypeKey: typeKeyA, Visibility: "public"}}}
	_, hash, err := CanonicalObjectSemanticContract(source)
	if err != nil {
		t.Fatal(err)
	}
	source.ContentHash = hash
	targetSemantic := ObjectSemanticContract{SchemaVersion: ObjectSemanticContractSchemaVersion, TypeKey: typeKeyC, Identity: ObjectIdentityReference, Equality: ObjectEqualityReference, Properties: []ObjectPropertyContract{{Key: "value", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Readonly: true, Visibility: "public"}}}
	_, hash, err = CanonicalObjectSemanticContract(targetSemantic)
	if err != nil {
		t.Fatal(err)
	}
	targetSemantic.ContentHash = hash
	relations, err := BuildTypeRelationGraph([]TypeRelationNode{{TypeKey: typeKeyA, DeclarationKey: typeKeyA}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	sourceLayout, err := PlanObjectLayout(typeKeyB, target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	targetLayout, err := PlanObjectLayout(typeKeyC, target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := BuildObjectViewProof(source, targetSemantic, relations, sourceLayout, targetLayout)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func TestObjectViewProofAndHIRGate(t *testing.T) {
	proof := testObjectViewProof(t)
	if len(proof.Mappings) != 1 || !proof.PreservesIdentity || proof.ExposesWrites {
		t.Fatalf("unexpected ObjectView proof: %#v", proof)
	}
	encoded, _, err := CanonicalObjectViewProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectViewProof(encoded); err != nil {
		t.Fatal(err)
	}
	hir := testVERT010Module()
	_, hash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hash
	gate, err := BuildObjectViewHIRGate(hir, 1, 2, proof)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err = CanonicalObjectViewHIRGate(gate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectViewHIRGate(encoded); err != nil {
		t.Fatal(err)
	}
	operation, err := BuildObjectViewOperation(gate)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err = CanonicalObjectViewOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectViewOperation(encoded); err != nil {
		t.Fatal(err)
	}
	artifact, err := BuildObjectViewHIRArtifact(gate)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err = CanonicalObjectViewHIRArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectViewHIRArtifact(encoded); err != nil {
		t.Fatal(err)
	}
	mir, err := LowerObjectViewMIR(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if mir.Binding.Allocates || !mir.Binding.PreservesIdentity || len(mir.Reads) != 1 {
		t.Fatalf("unexpected ObjectView MIR: %#v", mir)
	}
	encoded, _, err = CanonicalObjectViewMIR(mir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectViewMIR(encoded); err != nil {
		t.Fatal(err)
	}
	mir.Reads[0].SourceFieldOffset += 8
	if _, _, err := CanonicalObjectViewMIR(mir); err == nil {
		t.Fatal("accepted substituted ObjectView MIR offset")
	}
	artifact.BaseHIRHash = typeKeyA
	if _, _, err := CanonicalObjectViewHIRArtifact(artifact); err == nil {
		t.Fatal("accepted substituted ObjectView base HIR")
	}
	operation.TargetTypeKey = typeKeyA
	if _, _, err := CanonicalObjectViewOperation(operation); err == nil {
		t.Fatal("accepted substituted ObjectView operation target")
	}
	gate.SourceValueID = 5
	if _, _, err := CanonicalObjectViewHIRGate(gate); err == nil {
		t.Fatal("accepted substituted ObjectView HIR value")
	}
}

func testObjectViewAccessorMIR(t testing.TB) ObjectViewMIRModule {
	t.Helper()
	hir := testVERT011HIR(t)
	_, hash, err := CanonicalVERT011PlaceHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hash
	source := hir.PlaceRefs.ObjectContracts[0]
	targetTypeKey := strings.Repeat("d", 64)
	targetSemantic := ObjectSemanticContract{SchemaVersion: ObjectSemanticContractSchemaVersion, TypeKey: targetTypeKey, Identity: ObjectIdentityReference, Equality: ObjectEqualityReference, Properties: []ObjectPropertyContract{{Key: "result", Kind: ObjectPropertyAccessor, ReadTypeKey: typeKeyB, Readonly: true, Visibility: "public"}}}
	_, hash, err = CanonicalObjectSemanticContract(targetSemantic)
	if err != nil {
		t.Fatal(err)
	}
	targetSemantic.ContentHash = hash
	relations, err := BuildTypeRelationGraph([]TypeRelationNode{{TypeKey: typeKeyB, DeclarationKey: typeKeyB}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	sourceLayout, err := PlanObjectLayout(source.TypeKey, target, []ObjectLayoutPropertyInput{{Key: "backing", Kind: ObjectPropertyData, Representation: string(VERT011RepNullableF64)}, {Key: "result", Kind: ObjectPropertyAccessor}})
	if err != nil {
		t.Fatal(err)
	}
	targetLayout, err := PlanObjectLayout(targetTypeKey, target, []ObjectLayoutPropertyInput{{Key: "result", Kind: ObjectPropertyAccessor}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := BuildObjectViewProof(source, targetSemantic, relations, sourceLayout, targetLayout)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := BuildObjectViewHIRGate(hir, 1, 3, view)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := BuildObjectViewHIRArtifact(gate)
	if err != nil {
		t.Fatal(err)
	}
	mir, err := LowerObjectViewMIR(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return mir
}

func TestObjectViewMIRBindsAccessorReceiverAndGetter(t *testing.T) {
	mir := testObjectViewAccessorMIR(t)
	if len(mir.Reads) != 1 {
		t.Fatalf("unexpected ObjectView accessor reads: %#v", mir.Reads)
	}
	read := mir.Reads[0]
	if read.Kind != ObjectPropertyAccessor || read.ReceiverValueID != mir.Binding.ResultValueID || read.GetterSymbolKey != "symbol/get" || read.GetterSignature != VERT011GetterSignature || read.Representation != string(VERT011RepNullableF64) || !slices.Equal(read.Effects, []Effect{EffectCall, EffectRead, EffectThrow}) {
		t.Fatalf("ObjectView accessor receiver binding is incomplete: %#v", read)
	}
	encoded, _, err := CanonicalObjectViewMIR(mir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectViewMIR(encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ObjectViewMIRModule){
		"receiver": func(value *ObjectViewMIRModule) { value.Reads[0].ReceiverValueID = value.Binding.SourceValueID },
		"getter":   func(value *ObjectViewMIRModule) { value.Reads[0].GetterSymbolKey = "symbol/other" },
		"ABI":      func(value *ObjectViewMIRModule) { value.Reads[0].GetterSignature = VERT011SetterSignature },
		"effects":  func(value *ObjectViewMIRModule) { value.Reads[0].Effects = []Effect{EffectRead} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := mir
			candidate.Reads = append([]ObjectViewMIRRead(nil), mir.Reads...)
			candidate.Reads[0].Effects = slices.Clone(mir.Reads[0].Effects)
			mutate(&candidate)
			if _, _, err := CanonicalObjectViewMIR(candidate); err == nil {
				t.Fatal("accepted substituted ObjectView accessor MIR")
			}
		})
	}
}

func TestObjectViewRejectsWriteAndTamper(t *testing.T) {
	proof := testObjectViewProof(t)
	writable := proof.Target
	writable.Properties[0].Readonly = false
	writable.Properties[0].WriteTypeKey = typeKeyA
	_, hash, err := CanonicalObjectSemanticContract(writable)
	if err != nil {
		t.Fatal(err)
	}
	writable.ContentHash = hash
	if _, err := BuildObjectViewProof(proof.Source, writable, proof.Relations, proof.SourceLayout, proof.TargetLayout); err == nil {
		t.Fatal("accepted writable ObjectView target")
	}
	proof.Mappings[0].SourceFieldOffset += 8
	if _, _, err := CanonicalObjectViewProof(proof); err == nil {
		t.Fatal("accepted substituted ObjectView offset")
	}
}

func TestDecodeObjectViewRejectsUnknownAndStale(t *testing.T) {
	proof := testObjectViewProof(t)
	encoded, _, err := CanonicalObjectViewProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1)), []byte(strings.Replace(string(encoded), proof.ContentHash, strings.Repeat("0", 64), 1))} {
		if _, err := DecodeObjectViewProof(data); err == nil {
			t.Fatal("accepted tampered ObjectView")
		}
	}
}
