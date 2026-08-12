package ast2bingo

import (
	"bytes"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadVarianceSnapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/variance/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestReplayVarianceSnapshotIsDeterministic(t *testing.T) {
	snapshot := loadVarianceSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayVarianceSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayVarianceSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	left, err := first.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) || len(first.Contracts) != 6 {
		t.Fatalf("variance replay is not deterministic or complete: %d contracts", len(first.Contracts))
	}
	direct := map[bingo.VariancePolarity]int{}
	graph := map[bingo.VariancePolarity]int{}
	for _, contract := range first.Contracts {
		if len(contract.Proofs) != 1 {
			t.Fatalf("unexpected variance contract: %#v", contract)
		}
		direct[contract.Proofs[0].Inferred]++
	}
	for _, proof := range first.Graph.Proofs {
		graph[proof.Inferred]++
	}
	if direct[bingo.VarianceBoth] != 1 || direct[bingo.VarianceNegative] != 1 || direct[bingo.VariancePositive] != 3 || direct[bingo.VarianceUnused] != 1 {
		t.Fatalf("unexpected direct variance proofs: %#v", direct)
	}
	if len(first.Graph.Edges) != 3 || graph[bingo.VarianceBoth] != 3 || graph[bingo.VarianceNegative] != 1 || graph[bingo.VariancePositive] != 2 {
		t.Fatalf("unexpected recursive variance graph: edges=%#v proofs=%#v", first.Graph.Edges, first.Graph.Proofs)
	}
}

func TestReplayVarianceSnapshotRejectsContentTamper(t *testing.T) {
	snapshot := loadVarianceSnapshot(t)
	snapshot.Types[0].Kind = "tampered"
	if _, err := ReplayVarianceSnapshot(snapshot, testCompilerIdentity(t, snapshot)); err == nil {
		t.Fatal("variance replay accepted tampered snapshot")
	}
}

func TestVarianceReplayRejectsForgedContractProof(t *testing.T) {
	snapshot := loadVarianceSnapshot(t)
	result, err := ReplayVarianceSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	result.Contracts[0].Proofs[0].Inferred = bingo.VarianceUnknown
	if _, err := result.CanonicalBytes(); err == nil {
		t.Fatal("variance replay accepted forged proof")
	}
}

func TestVarianceHintParsesTsgoEncoding(t *testing.T) {
	tests := map[string]bingo.VarianceHint{
		"out":                 bingo.VarianceHintCovariant,
		"in":                  bingo.VarianceHintContravariant,
		"in out":              bingo.VarianceHintInvariant,
		"[bivariant]":         bingo.VarianceHintBivariant,
		"[independent]":       bingo.VarianceHintIndependent,
		"out (unmeasurable)":  bingo.VarianceHintUnmeasurable,
		"in out (unreliable)": bingo.VarianceHintUnreliable,
	}
	for input, want := range tests {
		if got := varianceHint(input, 0); got != want {
			t.Fatalf("varianceHint(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVarianceReplayBuildsHIRConversionAdmission(t *testing.T) {
	snapshot := loadVarianceSnapshot(t)
	result, err := ReplayVarianceSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, typ := range snapshot.Types {
		keys[typ.DebugText] = typ.CanonicalHash
	}
	dog, animal := keys["Dog"], keys["Animal"]
	dogBox, animalBox := keys["ReadonlyBox<Dog>"], keys["ReadonlyBox<Animal>"]
	if dog == "" || animal == "" || dogBox == "" || animalBox == "" {
		t.Fatalf("missing relation fixture keys: %#v", keys)
	}
	path, err := bingo.FindTypeRelationPath(result.RelationGraph, dog, animal)
	if err != nil || len(path) < 2 || path[0] != dog || path[len(path)-1] != animal {
		t.Fatalf("missing Dog -> Animal relation: %#v / %v", path, err)
	}
	declaration := ""
	for _, node := range result.RelationGraph.Nodes {
		if node.TypeKey == dogBox {
			declaration = node.DeclarationKey
		}
	}
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	sourceLayout, err := bingo.PlanObjectLayout(dogBox, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		t.Fatal(err)
	}
	targetLayout, err := bingo.PlanObjectLayout(animalBox, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := bingo.BuildHIRVarianceConversionProof(1, declaration, dogBox, animalBox, result.Graph, result.RelationGraph, sourceLayout, targetLayout)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := bingo.CanonicalHIRVarianceConversionProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeHIRVarianceConversionProof(encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.BuildHIRVarianceConversionProof(1, declaration, animalBox, dogBox, result.Graph, result.RelationGraph, targetLayout, sourceLayout); err == nil {
		t.Fatal("accepted reversed covariant conversion")
	}
	proof.Parameters[0].RelationPath[0] = animal
	if _, _, err := bingo.CanonicalHIRVarianceConversionProof(proof); err == nil {
		t.Fatal("accepted substituted relation path")
	}
}
