package llvmbackend

import (
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func backendObjectViewMIR(t testing.TB) bingo.ObjectViewMIRModule {
	t.Helper()
	keyA, keyB, keyC := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	origin := bingo.Origin{File: "object.ts", Start: 1, End: 2}
	requirements := bingo.VERT010LogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		t.Fatal(err)
	}
	typ := bingo.HIRObjectType{TypeKey: keyB, Properties: []bingo.HIRObjectProperty{{Key: "value", SymbolKey: "symbol/value", SourceTypeKey: keyA, Type: bingo.TypeNumber, Mutable: true, Required: true}}}
	typ.SemanticContractHash, err = bingo.VERT010ObjectSemanticContractHash(typ)
	if err != nil {
		t.Fatal(err)
	}
	hir := bingo.HIRModule{SchemaVersion: bingo.VERT010HIRSchemaVersion, Provenance: bingo.HIRProvenance{FrontendSnapshotSchemaVersion: bingo.HIRFrontendSnapshotSchemaVersion, FrontendSnapshotHash: strings.Repeat("5", 64), SourceContentHash: strings.Repeat("6", 64), CompilerBuildIdentity: bingo.CompilerBuildIdentity{UpstreamCommit: strings.Repeat("1", 40), ForkCommit: strings.Repeat("2", 40), LoweringSchema: "bingo-hir-lowering-v8", LoweringHash: strings.Repeat("4", 64)}, StandardLibraryHash: strings.Repeat("7", 64), KindManifestHash: strings.Repeat("8", 64), LogicalCapabilityRequirementsDigest: digest}, LogicalCapabilityRequirements: requirements, ObjectTypes: []bingo.HIRObjectType{typ}, Functions: []bingo.HIRFunction{{ID: 1, Name: "objectAlias", Exported: true, ReturnType: bingo.TypeNumber, Origin: origin, Parameters: []bingo.HIRParameter{{Name: "value", Value: 1, Type: bingo.TypeNumber, Origin: origin}}, Blocks: []bingo.HIRBlock{{ID: 1, Operations: []bingo.HIROp{{ID: 2, Kind: "object.alloc", Type: bingo.TypeObject, Effect: bingo.EffectAllocate, ObjectTypeKey: keyB, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin}, {ID: 3, Kind: "object.field.init", Type: bingo.TypeObject, Operands: []bingo.ValueID{2, 1}, Effect: bingo.EffectWrite, ObjectTypeKey: keyB, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: origin}, {ID: 4, Kind: "object.alias", Type: bingo.TypeObject, Operands: []bingo.ValueID{3}, Effect: bingo.EffectPure, ObjectTypeKey: keyB, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: origin}, {ID: 5, Kind: "object.field.load", Type: bingo.TypeNumber, Operands: []bingo.ValueID{4}, Effect: bingo.EffectRead, ObjectTypeKey: keyB, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: origin}, {ID: 6, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: "3ff0000000000000", Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: origin}, {ID: 7, Kind: "binary", Type: bingo.TypeNumber, Operands: []bingo.ValueID{5, 6}, Operator: "+", Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: origin}, {ID: 8, Kind: "object.field.store", Type: bingo.TypeObject, Operands: []bingo.ValueID{4, 7}, Effect: bingo.EffectWrite, ObjectTypeKey: keyB, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: origin}, {ID: 9, Kind: "object.field.load", Type: bingo.TypeNumber, Operands: []bingo.ValueID{3}, Effect: bingo.EffectRead, ObjectTypeKey: keyB, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: origin}}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 9, Origin: origin}}}}}}
	_, hash, err := bingo.CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hash
	source := backendObjectContract(t, keyB, keyA, false)
	targetSemantic := backendObjectContract(t, keyC, keyA, true)
	relations, err := bingo.BuildTypeRelationGraph([]bingo.TypeRelationNode{{TypeKey: keyA, DeclarationKey: keyA}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	layoutTarget, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	sourceLayout, err := bingo.PlanObjectLayout(keyB, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	targetLayout, err := bingo.PlanObjectLayout(keyC, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := bingo.BuildObjectViewProof(source, targetSemantic, relations, sourceLayout, targetLayout)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := bingo.BuildObjectViewHIRGate(hir, 1, 2, view)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := bingo.BuildObjectViewHIRArtifact(gate)
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerObjectViewMIR(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return mir
}

func backendObjectContract(t testing.TB, typeKey, readKey string, readonly bool) bingo.ObjectSemanticContract {
	t.Helper()
	property := bingo.ObjectPropertyContract{Key: "value", Kind: bingo.ObjectPropertyData, ReadTypeKey: readKey, Readonly: readonly, Visibility: "public"}
	if !readonly {
		property.WriteTypeKey = readKey
	}
	contract := bingo.ObjectSemanticContract{SchemaVersion: bingo.ObjectSemanticContractSchemaVersion, TypeKey: typeKey, Identity: bingo.ObjectIdentityReference, Equality: bingo.ObjectEqualityReference, Properties: []bingo.ObjectPropertyContract{property}}
	_, hash, err := bingo.CanonicalObjectSemanticContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	return contract
}

func TestObjectViewBackendPlan(t *testing.T) {
	plan, err := BuildObjectViewBackendPlan(backendObjectViewMIR(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Allocates || len(plan.RuntimeCalls) != 0 || plan.Representation != "f64" {
		t.Fatalf("unexpected backend plan: %#v", plan)
	}
	encoded, _, err := CanonicalObjectViewBackendPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectViewBackendPlan(encoded); err != nil {
		t.Fatal(err)
	}
	plan.SourceOffset += 8
	if _, _, err := CanonicalObjectViewBackendPlan(plan); err == nil {
		t.Fatal("accepted substituted backend offset")
	}
}

func TestObjectViewBackendPlanRejectsUnverifiedInput(t *testing.T) {
	if _, err := BuildObjectViewBackendPlan(bingo.ObjectViewMIRModule{}); err == nil {
		t.Fatal("accepted empty ObjectView MIR")
	}
}

func backendObjectAccessorViewMIR(t testing.TB) bingo.ObjectViewMIRModule {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/objectaccessorview/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ast2bingo.ReplayObjectAccessorViewSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	return replay.MIR
}

func TestObjectAccessorViewBackendPlanBindsGetterAndBacking(t *testing.T) {
	plan, err := BuildObjectViewBackendPlan(backendObjectAccessorViewMIR(t))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Accessor || plan.GetterSymbolKey == "" || plan.BackingOffset == 0 || plan.Representation != string(bingo.VERT011RepNullableF64) || plan.FunctionName != "bingo_object_view_read_accessor_v1" || plan.Allocates || len(plan.RuntimeCalls) != 0 {
		t.Fatalf("unexpected accessor backend plan: %#v", plan)
	}
	encoded, _, err := CanonicalObjectViewBackendPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeObjectViewBackendPlan(encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ObjectViewBackendPlan){
		"backing offset": func(value *ObjectViewBackendPlan) { value.BackingOffset += 8 },
		"getter":         func(value *ObjectViewBackendPlan) { value.GetterSymbolKey = "node_other" },
		"function ABI":   func(value *ObjectViewBackendPlan) { value.FunctionName = "bingo_object_view_read_v1" },
		"representation": func(value *ObjectViewBackendPlan) { value.Representation = "f64" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := plan
			mutate(&candidate)
			if _, _, err := CanonicalObjectViewBackendPlan(candidate); err == nil {
				t.Fatal("accepted substituted accessor backend plan")
			}
		})
	}
}
