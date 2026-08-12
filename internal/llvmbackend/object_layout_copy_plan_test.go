package llvmbackend

import (
	"bytes"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func backendObjectLayoutCopyBound(t testing.TB) bingo.ObjectLayoutCopyBoundArtifact {
	t.Helper()
	source := bingo.ObjectSemanticContract{SchemaVersion: 1, TypeKey: strings.Repeat("1", 64), Identity: bingo.ObjectIdentityReference, Equality: bingo.ObjectEqualityReference, Properties: []bingo.ObjectPropertyContract{{Key: "value", Kind: bingo.ObjectPropertyData, ReadTypeKey: strings.Repeat("2", 64), WriteTypeKey: strings.Repeat("2", 64), Visibility: "public"}}}
	_, h, _ := bingo.CanonicalObjectSemanticContract(source)
	source.ContentHash = h
	targetSemantic := bingo.ObjectSemanticContract{SchemaVersion: 1, TypeKey: strings.Repeat("3", 64), Identity: bingo.ObjectIdentityReference, Equality: bingo.ObjectEqualityReference, Properties: []bingo.ObjectPropertyContract{{Key: "value", Kind: bingo.ObjectPropertyData, ReadTypeKey: strings.Repeat("2", 64), Readonly: true, Visibility: "public"}}}
	_, h, _ = bingo.CanonicalObjectSemanticContract(targetSemantic)
	targetSemantic.ContentHash = h
	relations, _ := bingo.BuildTypeRelationGraph([]bingo.TypeRelationNode{{TypeKey: strings.Repeat("2", 64), DeclarationKey: strings.Repeat("2", 64)}}, nil)
	target, _ := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	sourceLayout, _ := bingo.PlanObjectLayout(source.TypeKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	targetLayout, _ := bingo.PlanObjectLayout(targetSemantic.TypeKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	copyContract, err := bingo.BuildObjectLayoutCopyContract(source, targetSemantic, relations, sourceLayout, targetLayout)
	if err != nil {
		t.Fatal(err)
	}
	hir, err := bingo.BuildObjectLayoutCopyHIRArtifact(copyContract)
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerObjectLayoutCopyMIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	requirements := bingo.ObjectLayoutCopyCapabilityRequirements()
	bindings := make([]bingo.BoundCapability, len(requirements))
	symbols := map[bingo.RuntimeCapabilityID]string{"rt.gc.alloc": "bingo_gc_alloc_v1", "rt.gc.frame.link": "bingo_gc_frame_link_v1", "rt.gc.frame.unlink": "bingo_gc_frame_unlink_v1", "rt.gc.root.publish": "bingo_gc_root_publish_v1", "rt.gc.root.reload": "bingo_gc_root_reload_v1", "rt.gc.root.store": "bingo_gc_root_store_v1"}
	for i, requirement := range requirements {
		bindings[i] = bingo.BoundCapability{LogicalName: requirement, SymbolName: symbols[requirement], SignatureHash: strings.Repeat("4", 64)}
	}
	bound, err := bingo.NewObjectLayoutCopyBoundArtifact(mir, strings.Repeat("5", 64), strings.Repeat("6", 64), bindings)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func TestObjectLayoutCopyBackendPlanRoundTripAndTamper(t *testing.T) {
	plan, err := BuildObjectLayoutCopyBackendPlan(backendObjectLayoutCopyBound(t))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Allocates || !plan.NewIdentity || plan.InvokesAccessors || plan.UsesBitcast || len(plan.RuntimeCalls) != 6 || plan.RuntimeCalls[3] != "bingo_gc_alloc_v1" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	encoded, _, err := CanonicalObjectLayoutCopyBackendPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectLayoutCopyBackendPlan(encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ObjectLayoutCopyBackendPlan){"identity": func(v *ObjectLayoutCopyBackendPlan) { v.NewIdentity = false }, "bitcast": func(v *ObjectLayoutCopyBackendPlan) { v.UsesBitcast = true }, "offset": func(v *ObjectLayoutCopyBackendPlan) { v.SourceOffset++ }, "call": func(v *ObjectLayoutCopyBackendPlan) { v.RuntimeCalls[3] = "forged" }} {
		t.Run(name, func(t *testing.T) {
			value := plan
			value.RuntimeCalls = append([]string(nil), plan.RuntimeCalls...)
			mutate(&value)
			value.ContentHash = ""
			if _, _, err := CanonicalObjectLayoutCopyBackendPlan(value); err == nil {
				t.Fatal("substitution accepted")
			}
		})
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeObjectLayoutCopyBackendPlan(unknown); err == nil {
		t.Fatal("unknown accepted")
	}
}

func FuzzDecodeObjectLayoutCopyBackendPlan(f *testing.F) {
	plan, err := BuildObjectLayoutCopyBackendPlan(backendObjectLayoutCopyBound(f))
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, _ := CanonicalObjectLayoutCopyBackendPlan(plan)
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) <= maxObjectLayoutCopyBackendPlanBytes+1 {
			_, _ = DecodeObjectLayoutCopyBackendPlan(data)
		}
	})
}
