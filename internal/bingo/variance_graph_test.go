package bingo

import (
	"bytes"
	"strings"
	"testing"
)

func testVarianceContract(t testing.TB, key string, kind VarianceOccurrenceKind) VarianceContract {
	t.Helper()
	contract, err := BuildVarianceContract(key, []VarianceParameter{{ID: 1, Name: "T", Annotation: VarianceAnnotationNone, TsgoHint: VarianceHintIndependent}}, []VarianceOccurrence{{ID: 1, ParameterID: 1, Kind: kind, SourceOrder: 1, Path: key + ".value"}})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestVarianceGraphTarjanAndFixedPoint(t *testing.T) {
	positive := testVarianceContract(t, "a.Box", VarianceReadonlyProperty)
	negative := testVarianceContract(t, "b.Consumer", VarianceFunctionParameter)
	graph, err := BuildVarianceGraph([]VarianceContract{positive, negative}, []VarianceDependencyEdge{
		{ID: 1, OwnerNodeID: 1, DependencyNodeID: 2, Transform: VarianceTransformPositive, Path: "a.Box.next"},
		{ID: 2, OwnerNodeID: 2, DependencyNodeID: 1, Transform: VarianceTransformNegative, Path: "b.Consumer.callback"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Proofs) != 2 || graph.Proofs[0].SCCID != graph.Proofs[1].SCCID || graph.Proofs[0].Inferred != VarianceBoth || graph.Proofs[1].Inferred != VarianceBoth {
		t.Fatalf("unexpected mutual-recursion proof: %#v", graph.Proofs)
	}
	encoded, hash, err := CanonicalVarianceGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVarianceGraph(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, rehash, err := CanonicalVarianceGraph(*decoded)
	if err != nil || hash != rehash || !bytes.Equal(encoded, reencoded) {
		t.Fatal("variance graph does not round trip canonically")
	}
}

func TestVarianceGraphSelfRecursionAndUnknownTransform(t *testing.T) {
	positive := testVarianceContract(t, "a.Tree", VarianceReadonlyProperty)
	self, err := BuildVarianceGraph([]VarianceContract{positive}, []VarianceDependencyEdge{{ID: 1, OwnerNodeID: 1, DependencyNodeID: 1, Transform: VarianceTransformPositive, Path: "a.Tree.children"}})
	if err != nil || self.Proofs[0].Inferred != VariancePositive || self.Proofs[0].SCCID != 1 {
		t.Fatalf("unexpected self-recursion proof: %#v / %v", self.Proofs, err)
	}
	unknown, err := BuildVarianceGraph([]VarianceContract{positive}, []VarianceDependencyEdge{{ID: 1, OwnerNodeID: 1, DependencyNodeID: 1, Transform: VarianceTransformUnknown, Path: "a.Tree.residual"}})
	if err != nil || unknown.Proofs[0].Inferred != VarianceUnknown {
		t.Fatalf("unknown transform did not fail closed: %#v / %v", unknown.Proofs, err)
	}
}

func TestVarianceGraphRejectsTampering(t *testing.T) {
	contract := testVarianceContract(t, "a.Box", VarianceReadonlyProperty)
	graph, err := BuildVarianceGraph([]VarianceContract{contract}, []VarianceDependencyEdge{{ID: 1, OwnerNodeID: 1, DependencyNodeID: 1, Transform: VarianceTransformPositive, Path: "a.Box.self"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := CanonicalVarianceGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"unknown member": []byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1)),
		"stale hash":     []byte(strings.Replace(string(encoded), graph.ContentHash, strings.Repeat("0", 64), 1)),
		"forged scc":     []byte(strings.Replace(string(encoded), `"sccId":1`, `"sccId":2`, 1)),
		"forged proof":   []byte(strings.Replace(string(encoded), `"inferred":"positive"`, `"inferred":"negative"`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeVarianceGraph(data); err == nil {
				t.Fatal("expected variance graph rejection")
			}
		})
	}
	reordered := graph
	reordered.Edges = []VarianceDependencyEdge{
		{ID: 1, OwnerNodeID: 1, DependencyNodeID: 1, Transform: VarianceTransformPositive, Path: "z"},
		{ID: 2, OwnerNodeID: 1, DependencyNodeID: 1, Transform: VarianceTransformPositive, Path: "a"},
	}
	if _, _, err := CanonicalVarianceGraph(reordered); err == nil {
		t.Fatal("expected non-canonical edge rejection")
	}
}

func TestDecodeVarianceGraphRejectsOversizedInput(t *testing.T) {
	if _, err := DecodeVarianceGraph(make([]byte, maxVarianceGraphBytes+1)); err == nil {
		t.Fatal("expected oversized graph rejection")
	}
}
