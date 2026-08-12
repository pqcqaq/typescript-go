package bingo

import (
	"strings"
	"testing"
)

func testHIRVarianceConversionProof(t testing.TB) HIRVarianceConversionProof {
	t.Helper()
	declaration, sourceArgument, targetArgument := strings.Repeat("d", 64), strings.Repeat("e", 64), strings.Repeat("f", 64)
	contract, err := BuildVarianceContract(declaration, []VarianceParameter{{ID: 1, Name: "T", Annotation: VarianceAnnotationOut, TsgoHint: VarianceHintCovariant}}, []VarianceOccurrence{{ID: 1, ParameterID: 1, Kind: VarianceReadonlyProperty, SourceOrder: 1, Path: "Box.value"}})
	if err != nil {
		t.Fatal(err)
	}
	variance, err := BuildVarianceGraph([]VarianceContract{contract}, nil)
	if err != nil {
		t.Fatal(err)
	}
	relations, err := BuildTypeRelationGraph([]TypeRelationNode{{TypeKey: typeKeyB, DeclarationKey: declaration, ArgumentKeys: []string{sourceArgument}}, {TypeKey: typeKeyC, DeclarationKey: declaration, ArgumentKeys: []string{targetArgument}}, {TypeKey: sourceArgument, DeclarationKey: sourceArgument}, {TypeKey: targetArgument, DeclarationKey: targetArgument}}, []TypeRelationEdge{{SubTypeKey: sourceArgument, SuperTypeKey: targetArgument, Path: "Dog extends Animal"}})
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
	proof, err := BuildHIRVarianceConversionProof(2, declaration, typeKeyB, typeKeyC, variance, relations, sourceLayout, targetLayout)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func TestHIRVarianceGateBindsCanonicalHIR(t *testing.T) {
	hir := testVERT010Module()
	encoded, hash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	_ = encoded
	hir.ContentHash = hash
	proof := testHIRVarianceConversionProof(t)
	gate, err := BuildHIRVarianceGate(hir, 1, proof)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err = CanonicalHIRVarianceGate(gate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeHIRVarianceGate(encoded); err != nil {
		t.Fatal(err)
	}
	gate.FunctionID = 2
	if _, _, err := CanonicalHIRVarianceGate(gate); err == nil {
		t.Fatal("accepted missing HIR function")
	}
}

func TestHIRVarianceGateRejectsSourceSubstitution(t *testing.T) {
	hir := testVERT010Module()
	_, hash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hash
	proof := testHIRVarianceConversionProof(t)
	proof.SourceTypeKey = typeKeyC
	if _, err := BuildHIRVarianceGate(hir, 1, proof); err == nil {
		t.Fatal("accepted source type substitution")
	}
}

func TestHIRVarianceConversionRejectsUnsafePolarityAndLayout(t *testing.T) {
	valid := testHIRVarianceConversionProof(t)
	for name, kind := range map[string]VarianceOccurrenceKind{"invariant": VarianceWritableProperty, "unknown": VarianceResidual} {
		t.Run(name, func(t *testing.T) {
			contract, err := BuildVarianceContract(valid.DeclarationKey, []VarianceParameter{{ID: 1, Name: "T", Annotation: VarianceAnnotationNone, TsgoHint: VarianceHintIndependent}}, []VarianceOccurrence{{ID: 1, ParameterID: 1, Kind: kind, SourceOrder: 1, Path: "Box.value"}})
			if err != nil {
				t.Fatal(err)
			}
			graph, err := BuildVarianceGraph([]VarianceContract{contract}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := BuildHIRVarianceConversionProof(valid.HIRValueID, valid.DeclarationKey, valid.SourceTypeKey, valid.TargetTypeKey, graph, valid.RelationGraph, valid.SourceLayout, valid.TargetLayout); err == nil {
				t.Fatalf("accepted %s conversion", name)
			}
		})
	}
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	mismatch, err := PlanObjectLayout(valid.TargetTypeKey, target, []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildHIRVarianceConversionProof(valid.HIRValueID, valid.DeclarationKey, valid.SourceTypeKey, valid.TargetTypeKey, valid.VarianceGraph, valid.RelationGraph, valid.SourceLayout, mismatch); err == nil {
		t.Fatal("accepted physical layout mismatch")
	}
}
