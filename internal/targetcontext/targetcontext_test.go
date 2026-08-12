package targetcontext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestRuntimeManifestStrictIdentityAndHashes(t *testing.T) {
	data := runtimeManifestFixture(t)
	manifest, err := DecodeRuntimeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, bytes.TrimSpace(data)) {
		t.Fatalf("runtime manifest canonical bytes changed: %s", canonical)
	}
	for name, mutate := range map[string]func(*RuntimeManifest){
		"source hash":          func(value *RuntimeManifest) { value.SourceHash = strings.Repeat("0", 64) },
		"ABI schema hash":      func(value *RuntimeManifest) { value.ABISchemaHash = strings.Repeat("1", 64) },
		"implementation hash":  func(value *RuntimeManifest) { value.Capabilities[0].ImplementationHash = strings.Repeat("2", 64) },
		"signature hash":       func(value *RuntimeManifest) { value.Capabilities[0].SignatureHash = strings.Repeat("3", 64) },
		"harness hash":         func(value *RuntimeManifest) { value.Artifacts.HarnessObject.SHA256 = strings.Repeat("4", 64) },
		"compute harness hash": func(value *RuntimeManifest) { value.Artifacts.ComputeHarnessObject.SHA256 = strings.Repeat("4", 64) },
		"application startup hash": func(value *RuntimeManifest) {
			value.Artifacts.ApplicationStartupObject.SHA256 = strings.Repeat("4", 64)
		},
		"application startup file": func(value *RuntimeManifest) { value.Artifacts.ApplicationStartupObject.File = "bingo_add_harness.o" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *manifest
			candidate.Artifacts = manifest.Artifacts
			if manifest.Artifacts.ComputeHarnessObject != nil {
				computeHarness := *manifest.Artifacts.ComputeHarnessObject
				candidate.Artifacts.ComputeHarnessObject = &computeHarness
			}
			candidate.Capabilities = append([]RuntimeCapability(nil), manifest.Capabilities...)
			candidate.Capabilities[0] = manifest.Capabilities[0]
			mutate(&candidate)
			if _, err := DecodeRuntimeManifest(mustJSON(t, candidate)); err == nil {
				t.Fatal("tampered runtime manifest was accepted")
			}
		})
	}
	rehashed := *manifest
	rehashed.Capabilities = append([]RuntimeCapability(nil), manifest.Capabilities...)
	rehashed.Artifacts.UmbrellaArchive.SHA256 = strings.Repeat("4", 64)
	for index := range rehashed.Capabilities {
		rehashed.Capabilities[index].ImplementationHash = rehashed.Artifacts.UmbrellaArchive.SHA256
	}
	rehashed.ContentHash, err = runtimeManifestContentHash(rehashed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRuntimeManifest(mustJSON(t, rehashed)); err == nil || !strings.Contains(err.Error(), "not locked") {
		t.Fatalf("rehashed substituted runtime error = %v", err)
	}
	unknown := strings.Replace(string(data), `"contentHash":`, `"unknown":true,"contentHash":`, 1)
	if _, err := DecodeRuntimeManifest([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown runtime field error = %v", err)
	}
}

func TestResolveTargetContextBindsRealManifestFacts(t *testing.T) {
	plan := validBuildPlan()
	toolchain := validToolchainManifest()
	runtime := runtimeManifestFixture(t)
	resolution, err := resolveTargetContext(plan, toolchain, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Context.FrontendHash != plan.FrontendHash || resolution.Context.RequestHash != plan.ContentHash {
		t.Fatalf("request provenance = %#v", resolution.Context)
	}
	if resolution.Context.LLVMDataLayout != llvmbackend.FirstSliceDataLayout || resolution.Context.DataLayoutHash != toolchain.DataLayout.ContentHash {
		t.Fatalf("LLVM data layout provenance = %#v", resolution.Context)
	}
	if len(resolution.Catalog.Capabilities) != len(staticRuntimeCapabilities) || resolution.Catalog.Capabilities[0].LogicalName != "rt.abi.version" || resolution.Catalog.Capabilities[len(resolution.Catalog.Capabilities)-1].LogicalName != "rt.gc.write_barrier" {
		t.Fatalf("available catalog = %#v", resolution.Catalog)
	}
	if resolution.Context.AvailableCapabilityCatalogHash != resolution.Catalog.ContentHash {
		t.Fatalf("catalog hash was not bound into context")
	}
}

func TestBindPropertyAccessMIRRejectsLockedCatalogWithoutDynamicCapability(t *testing.T) {
	resolution, err := resolveTargetContext(validBuildPlan(), validToolchainManifest(), runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]bingo.PropertyAccessHIRInput, 0, 4)
	for _, item := range []struct {
		name   string
		domain bingo.PropertyKeyDomain
		keys   []string
		source string
	}{{"direct", bingo.PropertyKeyDirect, []string{"left"}, ""}, {"dynamic", bingo.PropertyKeyUnknown, nil, "source"}, {"finite", bingo.PropertyKeyLiteralUnion, []string{"left", "right"}, ""}, {"literal", bingo.PropertyKeyLiteral, []string{"right"}, ""}} {
		admission, err := bingo.BuildPropertyAccessAdmission(strings.Repeat("1", 64), item.domain, item.keys, bingo.PropertyAccessInterop, item.source)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, bingo.PropertyAccessHIRInput{FunctionName: item.name, AccessNodeID: item.name, ReceiverTypeHash: strings.Repeat("1", 64), KeyTypeHash: strings.Repeat("2", 64), Admission: admission})
	}
	hir, err := bingo.BuildPropertyAccessHIRArtifact(strings.Repeat("3", 64), strings.Repeat("4", 64), inputs)
	if err != nil {
		t.Fatal(err)
	}
	abi, err := bingo.BuildDynamicValueABIContract()
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerPropertyAccessMIR(hir, resolution.Context.Triple, resolution.Context.DataLayoutHash, abi)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindPropertyAccessMIR(mir, resolution.Context, resolution.Catalog); err == nil || !strings.Contains(err.Error(), `runtime capability "rt.dynamic.property_load" is unavailable`) {
		t.Fatalf("binding error = %v", err)
	}
}

func TestBindObjectLayoutCopyUsesLockedAllocationCapability(t *testing.T) {
	resolution, err := resolveTargetContext(validBuildPlan(), validToolchainManifest(), runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	hir, err := bingo.BuildObjectLayoutCopyHIRArtifact(testTargetObjectLayoutCopy(t))
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerObjectLayoutCopyMIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindObjectLayoutCopy(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.Bindings) != len(bingo.ObjectLayoutCopyCapabilityRequirements()) || bound.Bindings[0].LogicalName != "rt.gc.alloc" || bound.Bindings[0].SymbolName != "bingo_gc_alloc_v1" || bound.TargetContextHash != resolution.Context.ContentHash || bound.CatalogHash != resolution.Catalog.ContentHash {
		t.Fatalf("unexpected object layout copy binding: %#v", bound)
	}
	tampered := mir
	tampered.DataLayoutHash = strings.Repeat("0", 64)
	if _, err := BindObjectLayoutCopy(tampered, resolution.Context, resolution.Catalog); err == nil {
		t.Fatal("copy binding accepted substituted DataLayout")
	}
}

func testTargetObjectLayoutCopy(t *testing.T) bingo.ObjectLayoutCopyContract {
	t.Helper()
	source := bingo.ObjectSemanticContract{SchemaVersion: bingo.ObjectSemanticContractSchemaVersion, TypeKey: strings.Repeat("1", 64), Identity: bingo.ObjectIdentityReference, Equality: bingo.ObjectEqualityReference, Properties: []bingo.ObjectPropertyContract{{Key: "value", Kind: bingo.ObjectPropertyData, ReadTypeKey: strings.Repeat("2", 64), WriteTypeKey: strings.Repeat("2", 64), Visibility: "public"}}}
	_, hash, err := bingo.CanonicalObjectSemanticContract(source)
	if err != nil {
		t.Fatal(err)
	}
	source.ContentHash = hash
	targetSemantic := bingo.ObjectSemanticContract{SchemaVersion: bingo.ObjectSemanticContractSchemaVersion, TypeKey: strings.Repeat("3", 64), Identity: bingo.ObjectIdentityReference, Equality: bingo.ObjectEqualityReference, Properties: []bingo.ObjectPropertyContract{{Key: "value", Kind: bingo.ObjectPropertyData, ReadTypeKey: strings.Repeat("2", 64), Readonly: true, Visibility: "public"}}}
	_, hash, err = bingo.CanonicalObjectSemanticContract(targetSemantic)
	if err != nil {
		t.Fatal(err)
	}
	targetSemantic.ContentHash = hash
	relations, err := bingo.BuildTypeRelationGraph([]bingo.TypeRelationNode{{TypeKey: strings.Repeat("2", 64), DeclarationKey: strings.Repeat("2", 64)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	sourceLayout, err := bingo.PlanObjectLayout(source.TypeKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	targetLayout, err := bingo.PlanObjectLayout(targetSemantic.TypeKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := bingo.BuildObjectLayoutCopyContract(source, targetSemantic, relations, sourceLayout, targetLayout)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestBindVERT010MIRSelectsExactRuntimeImplementations(t *testing.T) {
	resolution, err := resolveTargetContext(validBuildPlan(), validToolchainManifest(), runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	hir := testTargetVERT010HIR(t)
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := bingo.PlanObjectLayout(hir.ObjectTypes[0].TypeKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerVERT010MIR(hir, layout)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindVERT010MIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.Closure.Bindings) != len(bingo.VERT010LogicalCapabilities()) {
		t.Fatalf("bound capability count = %d", len(bound.Closure.Bindings))
	}
	for index, binding := range bound.Closure.Bindings {
		want := resolution.Catalog.Capabilities[slices.IndexFunc(resolution.Catalog.Capabilities, func(capability AvailableCapability) bool { return capability.LogicalName == binding.LogicalName })]
		if binding.SymbolName != want.SymbolName || binding.SignatureHash != want.SignatureHash || binding.LogicalName != bingo.VERT010LogicalCapabilities()[index] {
			t.Fatalf("binding %d = %#v, want %#v", index, binding, want)
		}
	}
}

func TestBindVERT011MIRSelectsExactRuntimeImplementations(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/propertynullishassign/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity("86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee", "9a53ae50f6da67c9b3948b239d8292967e42422b")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ast2bingo.ReplayVERT011Snapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := bingo.PlanObjectLayout(replay.HIR.PlaceRefs.Places[0].ObjectTypeKey, target, []bingo.ObjectLayoutPropertyInput{
		{Key: "backing", Kind: bingo.ObjectPropertyData, Representation: "nullable-f64"},
		{Key: "result", Kind: bingo.ObjectPropertyAccessor},
	})
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerVERT011MIR(replay.HIR, layout)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolveTargetContext(validBuildPlan(), validToolchainManifest(), runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindVERT011MIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	for index, binding := range bound.Closure.Bindings {
		catalogIndex := slices.IndexFunc(resolution.Catalog.Capabilities, func(capability AvailableCapability) bool {
			return capability.LogicalName == binding.LogicalName
		})
		if catalogIndex < 0 {
			t.Fatalf("bound capability %q is absent", binding.LogicalName)
		}
		want := resolution.Catalog.Capabilities[catalogIndex]
		if binding.SymbolName != want.SymbolName || binding.SignatureHash != want.SignatureHash || binding.LogicalName != bingo.VERT010LogicalCapabilities()[index] {
			t.Fatalf("VERT-011 binding %d = %#v, want %#v", index, binding, want)
		}
	}
}

func TestBindVERT012MIRSelectsExactRuntimeImplementations(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/closurecounter/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity("86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee", "9a53ae50f6da67c9b3948b239d8292967e42422b")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ast2bingo.ReplayVERT012Snapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	cellKey, environmentKey := bingo.VERT012LayoutTypeKeys(replay.Contract.ContentHash)
	cell, err := bingo.PlanObjectLayout(cellKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := bingo.PlanObjectLayout(environmentKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "cell", Kind: bingo.ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerVERT012MIR(replay.HIR, cell, environment)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolveTargetContext(validBuildPlan(), validToolchainManifest(), runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindVERT012MIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.Closure.Bindings) != len(bingo.VERT012LogicalCapabilities()) {
		t.Fatalf("VERT-012 binding count = %d", len(bound.Closure.Bindings))
	}
	for index, binding := range bound.Closure.Bindings {
		catalogIndex := slices.IndexFunc(resolution.Catalog.Capabilities, func(capability AvailableCapability) bool {
			return capability.LogicalName == binding.LogicalName
		})
		if catalogIndex < 0 {
			t.Fatalf("bound capability %q is absent", binding.LogicalName)
		}
		want := resolution.Catalog.Capabilities[catalogIndex]
		if binding.SymbolName != want.SymbolName || binding.SignatureHash != want.SignatureHash || binding.LogicalName != bingo.VERT012LogicalCapabilities()[index] {
			t.Fatalf("VERT-012 binding %d = %#v, want %#v", index, binding, want)
		}
	}
}

func testTargetVERT010HIR(t *testing.T) bingo.HIRModule {
	t.Helper()
	requirements := bingo.VERT010LogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		t.Fatal(err)
	}
	origin := bingo.Origin{File: "/project/objectalias.ts", Start: 1, End: 2}
	objectType := bingo.HIRObjectType{TypeKey: strings.Repeat("b", 64), Properties: []bingo.HIRObjectProperty{{Key: "value", SymbolKey: "symbol/value", SourceTypeKey: strings.Repeat("d", 64), Type: bingo.TypeNumber, Mutable: true, Required: true}}}
	objectType.SemanticContractHash, err = bingo.VERT010ObjectSemanticContractHash(objectType)
	if err != nil {
		t.Fatal(err)
	}
	empty := []bingo.RuntimeCapabilityID{}
	module := bingo.HIRModule{
		SchemaVersion:                 bingo.VERT010HIRSchemaVersion,
		Provenance:                    bingo.HIRProvenance{FrontendSnapshotSchemaVersion: bingo.HIRFrontendSnapshotSchemaVersion, FrontendSnapshotHash: strings.Repeat("a", 64), SourceContentHash: strings.Repeat("b", 64), CompilerBuildIdentity: bingo.CompilerBuildIdentity{UpstreamCommit: strings.Repeat("a", 40), ForkCommit: strings.Repeat("b", 40), LoweringSchema: "test", LoweringHash: strings.Repeat("c", 64)}, StandardLibraryHash: strings.Repeat("d", 64), KindManifestHash: strings.Repeat("e", 64), LogicalCapabilityRequirementsDigest: digest},
		LogicalCapabilityRequirements: requirements, ObjectTypes: []bingo.HIRObjectType{objectType},
		Functions: []bingo.HIRFunction{{ID: 1, Name: "objectAlias", Exported: true, ReturnType: bingo.TypeNumber, Origin: origin, Parameters: []bingo.HIRParameter{{Name: "value", Value: 1, Type: bingo.TypeNumber, Origin: origin}}, Blocks: []bingo.HIRBlock{{ID: 1, Operations: []bingo.HIROp{
			{ID: 2, Kind: "object.alloc", Type: bingo.TypeObject, Effect: bingo.EffectAllocate, ObjectTypeKey: objectType.TypeKey, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin},
			{ID: 3, Kind: "object.field.init", Type: bingo.TypeObject, Operands: []bingo.ValueID{2, 1}, Effect: bingo.EffectWrite, ObjectTypeKey: objectType.TypeKey, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: empty, Origin: origin},
			{ID: 4, Kind: "object.alias", Type: bingo.TypeObject, Operands: []bingo.ValueID{3}, Effect: bingo.EffectPure, ObjectTypeKey: objectType.TypeKey, LogicalCapabilityRequirements: empty, Origin: origin},
			{ID: 5, Kind: "object.field.load", Type: bingo.TypeNumber, Operands: []bingo.ValueID{4}, Effect: bingo.EffectRead, ObjectTypeKey: objectType.TypeKey, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: empty, Origin: origin},
			{ID: 6, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: "3ff0000000000000", Effect: bingo.EffectPure, LogicalCapabilityRequirements: empty, Origin: origin},
			{ID: 7, Kind: "binary", Type: bingo.TypeNumber, Operands: []bingo.ValueID{5, 6}, Operator: "+", Effect: bingo.EffectPure, LogicalCapabilityRequirements: empty, Origin: origin},
			{ID: 8, Kind: "object.field.store", Type: bingo.TypeObject, Operands: []bingo.ValueID{4, 7}, Effect: bingo.EffectWrite, ObjectTypeKey: objectType.TypeKey, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: empty, Origin: origin},
			{ID: 9, Kind: "object.field.load", Type: bingo.TypeNumber, Operands: []bingo.ValueID{3}, Effect: bingo.EffectRead, ObjectTypeKey: objectType.TypeKey, PropertySymbolKey: "symbol/value", LogicalCapabilityRequirements: empty, Origin: origin},
		}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 9, Origin: origin}}}}},
	}
	_, hash, err := bingo.CanonicalVERT010ObjectHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	return module
}

func TestBindVERT013aMIRSelectsExactRuntimeImplementations(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/classcounter/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity("86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee", "9a53ae50f6da67c9b3948b239d8292967e42422b")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ast2bingo.ReplayVERT013aSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := bingo.PlanObjectLayout(replay.Contract.Classes[0].InstanceTypeKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerVERT013aMIR(replay.HIR, layout)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolveTargetContext(validBuildPlan(), validToolchainManifest(), runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindVERT013aMIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.Closure.Bindings) != len(bingo.VERT013aLogicalCapabilities()) {
		t.Fatalf("VERT-013a binding count = %d", len(bound.Closure.Bindings))
	}
	for index, binding := range bound.Closure.Bindings {
		catalogIndex := slices.IndexFunc(resolution.Catalog.Capabilities, func(capability AvailableCapability) bool { return capability.LogicalName == binding.LogicalName })
		if catalogIndex < 0 {
			t.Fatalf("bound capability %q is absent", binding.LogicalName)
		}
		want := resolution.Catalog.Capabilities[catalogIndex]
		if binding.SymbolName != want.SymbolName || binding.SignatureHash != want.SignatureHash || binding.LogicalName != bingo.VERT013aLogicalCapabilities()[index] {
			t.Fatalf("VERT-013a binding %d = %#v, want %#v", index, binding, want)
		}
	}
	tampered := mir
	tampered.Layout.Target.DataLayoutHash = strings.Repeat("0", 64)
	if _, err := BindVERT013aMIR(tampered, resolution.Context, resolution.Catalog); err == nil {
		t.Fatal("VERT-013a accepted substituted target layout")
	}
}

func TestResolveTargetContextRejectsUnsupportedRequests(t *testing.T) {
	base := validBuildPlan()
	toolchain := validToolchainManifest()
	runtime := runtimeManifestFixture(t)
	tests := []struct {
		name   string
		mutate func(*buildplan.Plan)
	}{
		{name: "target", mutate: func(plan *buildplan.Plan) { plan.Backend.Target = "x86_64-pc-windows-msvc" }},
		{name: "cpu", mutate: func(plan *buildplan.Plan) { plan.Backend.CPU = "znver4" }},
		{name: "feature", mutate: func(plan *buildplan.Plan) { plan.Backend.Features = []string{"+avx2"} }},
		{name: "runtime", mutate: func(plan *buildplan.Plan) { plan.Backend.Runtime = "core-esnext" }},
		{name: "gc", mutate: func(plan *buildplan.Plan) { plan.Backend.GC = frontendwire.GCArc }},
		{name: "bounds", mutate: func(plan *buildplan.Plan) { plan.Backend.BoundsCheck = frontendwire.BoundsCheckOff }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			test.mutate(&plan)
			rehashBuildPlan(&plan)
			if _, err := resolveTargetContext(plan, toolchain, runtime); err == nil {
				t.Fatal("unsupported request was accepted")
			}
		})
	}
}

func TestResolveTargetContextAdmitsInteropContractButRejectsStaticRuntime(t *testing.T) {
	plan := validBuildPlan()
	plan.Profile = frontendwire.ProfileInterop
	rehashBuildPlan(&plan)
	_, err := resolveTargetContext(plan, validToolchainManifest(), runtimeManifestFixture(t))
	if err == nil || !strings.Contains(err.Error(), "does not match runtime manifest") {
		t.Fatalf("interop plan with static runtime error = %v", err)
	}
	if strings.Contains(err.Error(), "unavailable for the first-slice target") {
		t.Fatalf("interop profile was rejected by the target contract instead of runtime identity: %v", err)
	}
}

func TestInteropRuntimeManifestRequiresAuthoritativeIdentity(t *testing.T) {
	manifest := interopRuntimeManifestCandidate(t)
	if err := ValidateRuntimeManifest(manifest); err == nil || !strings.Contains(err.Error(), "no authoritative manifest identity") {
		t.Fatalf("canonical interop manifest error = %v", err)
	}
}

func TestInteropTargetManifestHashMatchesRuntimeBuildInputs(t *testing.T) {
	type profileOverlay struct {
		Profile      string              `json:"profile"`
		Capabilities []RuntimeCapability `json:"capabilities"`
	}

	runtimeRoot := filepath.Join("..", "..", "..", "runtime", "bingo-rt")
	baselineBytes, err := os.ReadFile(filepath.Join(runtimeRoot, "manifests", "first-slice-target.json"))
	if err != nil {
		t.Fatal(err)
	}
	overlayBytes, err := os.ReadFile(filepath.Join(runtimeRoot, "manifests", "first-slice-interop-overlay.json"))
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]any
	var overlay profileOverlay
	if err := json.Unmarshal(baselineBytes, &baseline); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(overlayBytes, &overlay); err != nil {
		t.Fatal(err)
	}
	if baseline["profile"] != string(frontendwire.ProfileStatic) || overlay.Profile != string(frontendwire.ProfileInterop) {
		t.Fatalf("unexpected runtime build profiles: baseline=%q overlay=%q", baseline["profile"], overlay.Profile)
	}
	capabilities, ok := baseline["capabilities"].([]any)
	if !ok {
		t.Fatal("baseline capabilities are not an array")
	}
	baseline["profile"] = overlay.Profile
	for _, capability := range overlay.Capabilities {
		capabilities = append(capabilities, map[string]any{
			"logicalName": capability.LogicalName, "symbolName": capability.SymbolName,
			"abiVersion": capability.ABIVersion, "signature": capability.Signature,
			"effects": capability.Effects, "requiredFeatures": capability.RequiredFeatures,
		})
	}
	slices.SortFunc(capabilities, func(left, right any) int {
		leftCapability, leftOK := left.(map[string]any)
		rightCapability, rightOK := right.(map[string]any)
		if !leftOK || !rightOK {
			t.Fatal("runtime capability is not an object")
		}
		return strings.Compare(fmt.Sprint(leftCapability["logicalName"]), fmt.Sprint(rightCapability["logicalName"]))
	})
	baseline["capabilities"] = capabilities
	hash, err := canonicalHash(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if hash != InteropTargetManifestHash {
		t.Fatalf("interop target manifest hash = %s, want %s", hash, InteropTargetManifestHash)
	}
}

func TestInteropRuntimeManifestCapabilityClosureIsStrict(t *testing.T) {
	base := interopRuntimeManifestCandidate(t)
	tests := map[string]func(*RuntimeManifest){
		"missing": func(value *RuntimeManifest) { value.Capabilities = value.Capabilities[1:] },
		"extra": func(value *RuntimeManifest) {
			value.Capabilities = append(value.Capabilities, value.Capabilities[len(value.Capabilities)-1])
		},
		"symbol":    func(value *RuntimeManifest) { value.Capabilities[0].SymbolName = "other" },
		"signature": func(value *RuntimeManifest) { value.Capabilities[0].Signature = "other" },
		"effects":   func(value *RuntimeManifest) { value.Capabilities[0].Effects = []bingo.PassEffect{bingo.PassEffectRead} },
		"order": func(value *RuntimeManifest) {
			value.Capabilities[0], value.Capabilities[1] = value.Capabilities[1], value.Capabilities[0]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Capabilities = append([]RuntimeCapability(nil), base.Capabilities...)
			mutate(&candidate)
			candidate.ContentHash, _ = runtimeManifestContentHash(candidate)
			if err := ValidateRuntimeManifest(candidate); err == nil || strings.Contains(err.Error(), "no authoritative manifest identity") {
				t.Fatalf("interop capability substitution reached identity gate: %v", err)
			}
		})
	}
}

func interopRuntimeManifestCandidate(t *testing.T) RuntimeManifest {
	t.Helper()
	manifest, err := DecodeRuntimeManifest(runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Profile = string(frontendwire.ProfileInterop)
	manifest.TargetManifestHash = InteropTargetManifestHash
	wanted, _ := runtimeCapabilitiesForProfile(manifest.Profile)
	manifest.Capabilities = make([]RuntimeCapability, len(wanted))
	for index, capability := range wanted {
		signatureHash, err := canonicalHash(capability.signature)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Capabilities[index] = RuntimeCapability{LogicalName: capability.logicalName, SymbolName: capability.symbolName, ABIVersion: "1.0.0", Signature: capability.signature, Effects: slices.Clone(capability.effects), RequiredFeatures: []string{}, SignatureHash: signatureHash, ImplementationHash: manifest.Artifacts.UmbrellaArchive.SHA256}
	}
	manifest.ContentHash, err = runtimeManifestContentHash(*manifest)
	if err != nil {
		t.Fatal(err)
	}
	return *manifest
}

func TestTargetContextRejectsProfileSubstitutionAcrossPublishedIdentity(t *testing.T) {
	resolution, err := resolveTargetContext(validBuildPlan(), validToolchainManifest(), runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	context := resolution.Context
	context.Profile = string(frontendwire.ProfileInterop)
	context.ContentHash, err = targetContextContentHash(context)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTargetContext(context); err == nil || !strings.Contains(err.Error(), "published runtime profile identity") {
		t.Fatalf("rehashed profile substitution error = %v", err)
	}
}

func TestResolveTargetPassPreservesHIRAndRejectsManifestSubstitution(t *testing.T) {
	plan := validBuildPlan()
	toolchain := validToolchainManifest()
	runtime := runtimeManifestFixture(t)
	inputs, err := newResolverInputArtifacts(plan, toolchain, runtime)
	if err != nil {
		t.Fatal(err)
	}
	hir, err := bingo.NewPassArtifact(bingo.PassArtifactTypedHIR, "hir-v8", []byte(`{"logicalCapabilityRequirements":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := bingo.NewPassArtifactEnvelope(append(inputs, hir)...)
	if err != nil {
		t.Fatal(err)
	}
	spec := resolveTargetPassSpec(t)
	initial := bingo.PassState{Schema: spec.InputSchema, Facts: append([]string(nil), spec.ReadsFacts...), Artifact: hir.Payload, Artifacts: &envelope}
	handler, err := newResolveTargetPassHandler(toolchain)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.PreVerify(context.Background(), spec, 1, initial); err != nil {
		t.Fatal(err)
	}
	result, err := handler.Run(context.Background(), spec, 1, initial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.PostVerify(context.Background(), spec, 1, initial, result.State); err != nil {
		t.Fatal(err)
	}
	if got, ok := result.State.NamedArtifact(bingo.PassArtifactTypedHIR); !ok || !bytes.Equal(got.Payload, hir.Payload) {
		t.Fatal("resolver did not preserve typed HIR sidecar")
	}
	if _, ok := result.State.NamedArtifact(bingo.PassArtifactAvailableCapabilityCatalog); !ok {
		t.Fatal("resolver did not emit available capability catalog")
	}
	if _, ok := result.State.NamedArtifact(bingo.PassArtifactRepresentationPlan); ok {
		t.Fatal("resolver emitted a representation plan before the join pass")
	}
	if slices.Contains(result.State.Facts, "bound-capability-closure") {
		t.Fatal("resolver claimed a MIR-derived bound capability closure")
	}

	otherHIR, err := bingo.NewPassArtifact(bingo.PassArtifactTypedHIR, "hir-v8", []byte(`{"tamperedButOpaque":true}`))
	if err != nil {
		t.Fatal(err)
	}
	otherArtifacts := make([]bingo.PassArtifact, 0, len(envelope.Artifacts))
	for _, artifact := range envelope.Artifacts {
		if artifact.Name == bingo.PassArtifactTypedHIR {
			otherArtifacts = append(otherArtifacts, otherHIR)
		} else {
			otherArtifacts = append(otherArtifacts, artifact)
		}
	}
	otherEnvelope, err := bingo.NewPassArtifactEnvelope(otherArtifacts...)
	if err != nil {
		t.Fatal(err)
	}
	otherInput := initial
	otherInput.Artifact = otherHIR.Payload
	otherInput.Artifacts = &otherEnvelope
	otherResult, err := handler.Run(context.Background(), spec, 1, otherInput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(otherResult.State.Artifact, result.State.Artifact) {
		t.Fatal("resolver target context changed after opaque HIR payload changed")
	}
	firstCatalog, _ := result.State.NamedArtifact(bingo.PassArtifactAvailableCapabilityCatalog)
	otherCatalog, _ := otherResult.State.NamedArtifact(bingo.PassArtifactAvailableCapabilityCatalog)
	if !bytes.Equal(firstCatalog.Payload, otherCatalog.Payload) {
		t.Fatal("resolver available catalog changed after opaque HIR payload changed")
	}

	tamperedToolchain, err := bingo.NewPassArtifact(bingo.PassArtifactToolchainManifest, "toolchain-manifest-v1", []byte(`{"schemaVersion":1}`))
	if err != nil {
		t.Fatal(err)
	}
	tamperedArtifacts := make([]bingo.PassArtifact, 0, len(envelope.Artifacts))
	for _, artifact := range envelope.Artifacts {
		if artifact.Name == bingo.PassArtifactToolchainManifest {
			tamperedArtifacts = append(tamperedArtifacts, tamperedToolchain)
		} else {
			tamperedArtifacts = append(tamperedArtifacts, artifact)
		}
	}
	tamperedEnvelope, err := bingo.NewPassArtifactEnvelope(tamperedArtifacts...)
	if err != nil {
		t.Fatal(err)
	}
	tamperedInput := initial
	tamperedInput.Artifacts = &tamperedEnvelope
	if err := handler.PreVerify(context.Background(), spec, 1, tamperedInput); err == nil {
		t.Fatal("substituted toolchain manifest was accepted")
	}

	tamperedOutput := result.State
	tamperedContext := append([]byte(nil), result.State.Artifact...)
	tamperedContext = bytes.Replace(tamperedContext, []byte(`"pointerWidth":64`), []byte(`"pointerWidth":32`), 1)
	tamperedTarget, err := bingo.NewPassArtifact(bingo.PassArtifactTargetContext, "target-context-v1", tamperedContext)
	if err != nil {
		t.Fatal(err)
	}
	tamperedOutputArtifacts := make([]bingo.PassArtifact, 0, len(result.State.Artifacts.Artifacts))
	for _, artifact := range result.State.Artifacts.Artifacts {
		if artifact.Name == bingo.PassArtifactTargetContext {
			tamperedOutputArtifacts = append(tamperedOutputArtifacts, tamperedTarget)
		} else {
			tamperedOutputArtifacts = append(tamperedOutputArtifacts, artifact)
		}
	}
	tamperedOutputEnvelope, err := bingo.NewPassArtifactEnvelope(tamperedOutputArtifacts...)
	if err != nil {
		t.Fatal(err)
	}
	tamperedOutput.Artifact = tamperedContext
	tamperedOutput.Artifacts = &tamperedOutputEnvelope
	if _, err := handler.PostVerify(context.Background(), spec, 1, initial, tamperedOutput); err == nil {
		t.Fatal("tampered target context output was accepted")
	}
}

func resolveTargetPassSpec(t *testing.T) bingo.PassSpec {
	t.Helper()
	for _, spec := range bingo.PassSpecs() {
		if spec.ID == bingo.PassResolveTarget {
			return spec
		}
	}
	t.Fatal("missing resolve target pass spec")
	return bingo.PassSpec{}
}

func runtimeManifestFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("testdata", "runtime-manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validBuildPlan() buildplan.Plan {
	plan := buildplan.Plan{
		SchemaVersion: buildplan.SchemaVersion,
		FrontendHash:  strings.Repeat("a", 64),
		Profile:       frontendwire.ProfileStatic,
		Backend: buildplan.BackendRequest{
			Target:      llvmbackend.FirstSliceTriple,
			CPU:         llvmbackend.FirstSliceCPU,
			Features:    []string{},
			Runtime:     LockedRuntimeName,
			GC:          frontendwire.GCTracing,
			Exceptions:  frontendwire.ExceptionsNone,
			Overflow:    frontendwire.OverflowJSNumber,
			BoundsCheck: frontendwire.BoundsCheckOn,
			Emit:        []frontendwire.EmitArtifact{frontendwire.EmitHIR},
			LLVMMajor:   llvmbackend.LockedLLVMMajor,
		},
	}
	rehashBuildPlan(&plan)
	return plan
}

func rehashBuildPlan(plan *buildplan.Plan) {
	input := struct {
		SchemaVersion uint32                   `json:"schemaVersion"`
		FrontendHash  string                   `json:"frontendHash"`
		Profile       frontendwire.Profile     `json:"profile"`
		Backend       buildplan.BackendRequest `json:"backend"`
	}{plan.SchemaVersion, plan.FrontendHash, plan.Profile, plan.Backend}
	plan.ContentHash = sha256JSON(input)
}

func validToolchainManifest() llvmbackend.ToolchainManifest {
	layout := llvmbackend.DataLayout{
		SchemaVersion: llvmbackend.DataLayoutSchemaVersion,
		Triple:        llvmbackend.FirstSliceTriple,
		LayoutString:  llvmbackend.FirstSliceDataLayout,
		PointerBits:   64,
		LittleEndian:  true,
		F64Bits:       64,
		F64ABIAlign:   8,
	}
	layout.ContentHash = sha256JSON(struct {
		SchemaVersion uint32 `json:"schemaVersion"`
		Triple        string `json:"triple"`
		LayoutString  string `json:"layoutString"`
		PointerBits   uint32 `json:"pointerBits"`
		LittleEndian  bool   `json:"littleEndian"`
		F64Bits       uint32 `json:"f64Bits"`
		F64ABIAlign   uint32 `json:"f64AbiAlign"`
	}{layout.SchemaVersion, layout.Triple, layout.LayoutString, layout.PointerBits, layout.LittleEndian, layout.F64Bits, layout.F64ABIAlign})
	manifest := llvmbackend.ToolchainManifest{
		SchemaVersion:   llvmbackend.ToolchainManifestSchemaVersion,
		LLVMVersion:     llvmbackend.LockedLLVMVersion,
		LLVMMajor:       llvmbackend.LockedLLVMMajor,
		TargetTriple:    llvmbackend.FirstSliceTriple,
		CPU:             llvmbackend.FirstSliceCPU,
		Features:        []string{},
		ObjectFormat:    "elf",
		RelocationModel: "pic",
		CodeModel:       "small",
		OptLevel:        "none",
		DataLayout:      layout,
	}
	manifest.ContentHash = sha256JSON(struct {
		SchemaVersion   uint32                 `json:"schemaVersion"`
		LLVMVersion     string                 `json:"llvmVersion"`
		LLVMMajor       int                    `json:"llvmMajor"`
		TargetTriple    string                 `json:"targetTriple"`
		CPU             string                 `json:"cpu"`
		Features        []string               `json:"features,omitempty"`
		ObjectFormat    string                 `json:"objectFormat"`
		RelocationModel string                 `json:"relocationModel"`
		CodeModel       string                 `json:"codeModel"`
		OptLevel        string                 `json:"optLevel"`
		DataLayout      llvmbackend.DataLayout `json:"dataLayout"`
	}{manifest.SchemaVersion, manifest.LLVMVersion, manifest.LLVMMajor, manifest.TargetTriple, manifest.CPU, manifest.Features, manifest.ObjectFormat, manifest.RelocationModel, manifest.CodeModel, manifest.OptLevel, manifest.DataLayout})
	return manifest
}

func sha256JSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
