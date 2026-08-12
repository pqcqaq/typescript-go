//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/firstsliceoracle"
	llvm "tinygo.org/x/go-llvm"
)

func TestVERT010LLVMUsesBoundCapabilitiesAndSurvivesO2(t *testing.T) {
	bound := testVERT010BoundMIR(t)
	llvm.InitializeAllTargetInfos()
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	target, err := llvm.GetTargetFromTriple(FirstSliceTriple)
	if err != nil {
		t.Fatal(err)
	}
	machine := target.CreateTargetMachine(FirstSliceTriple, FirstSliceCPU, "", llvm.CodeGenLevelNone, llvm.RelocPIC, llvm.CodeModelSmall)
	defer machine.Dispose()
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("vert010-test")
	defer module.Dispose()
	module.SetTarget(FirstSliceTriple)
	module.SetDataLayout(FirstSliceDataLayout)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitVERT010Function(ctx, builder, module, bound); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O0 verify: %v", err)
	}
	ir := module.String()
	for _, binding := range bound.Closure.Bindings {
		if !strings.Contains(ir, "@"+binding.SymbolName+"(") {
			t.Fatalf("bound capability symbol %q is absent from LLVM", binding.SymbolName)
		}
	}
	for _, descriptor := range []string{"@vert010.shape", "@vert010.property.value", "@vert010.trace"} {
		if !strings.Contains(ir, descriptor+" = private constant") {
			t.Fatalf("private descriptor %q is absent from LLVM", descriptor)
		}
	}
	if strings.Contains(ir, "@bingo_gc_") {
		t.Fatal("LLVM lowering bypassed the bound capability symbols")
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("default<O2>", machine, options); err != nil {
		t.Fatalf("O2 passes: %v", err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O2 verify: %v", err)
	}
	optimized := module.String()
	for _, binding := range bound.Closure.Bindings {
		if !strings.Contains(optimized, "@"+binding.SymbolName+"(") {
			t.Fatalf("O2 removed required capability call %q", binding.SymbolName)
		}
	}
}

func TestTargetMachineEmitsVERT010ELFObject(t *testing.T) {
	bound := testVERT010BoundMIR(t)
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	emission, err := machine.EmitVERT010Object(bound)
	if err != nil {
		t.Fatal(err)
	}
	if emission.MIRContentHash != bound.ContentHash || len(emission.LLVMIR) == 0 || len(emission.Object) < 4 {
		t.Fatalf("invalid VERT-010 emission identity: %#v", emission)
	}
	if emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatal("VERT-010 target machine did not emit an ELF object")
	}
}

func TestVERT010ObjectLinksAndRunsAgainstRuntime(t *testing.T) {
	bound := testVERT010BoundMIR(t)
	runtimeSymbols := map[bingo.RuntimeCapabilityID]string{
		"rt.gc.alloc":        "bingo_gc_alloc_v1",
		"rt.gc.frame.link":   "bingo_gc_frame_link_v1",
		"rt.gc.frame.unlink": "bingo_gc_frame_unlink_v1",
		"rt.gc.root.clear":   "bingo_gc_root_clear_v1",
		"rt.gc.root.publish": "bingo_gc_root_publish_v1",
		"rt.gc.root.reload":  "bingo_gc_root_reload_v1",
		"rt.gc.root.store":   "bingo_gc_root_store_v1",
		"rt.gc.safepoint":    "bingo_gc_safepoint_v1",
	}
	bindings := make([]bingo.BoundCapability, len(bound.MIR.LogicalCapabilityRequirements))
	for index, requirement := range bound.MIR.LogicalCapabilityRequirements {
		bindings[index] = bingo.BoundCapability{LogicalName: requirement, SymbolName: runtimeSymbols[requirement], SignatureHash: strings.Repeat("f", 64)}
	}
	var err error
	bound, err = bingo.NewVERT010BoundMIR(bound.MIR, bound.TargetContextHash, bound.Closure.AvailableCapabilityCatalogHash, bindings)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	emission, err := machine.EmitVERT010Object(bound)
	if err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "target", "first-slice"))
	archive := filepath.Join(runtimeDirectory, "cargo", FirstSliceTriple, "release", "libbingo_runtime.a")
	harness := filepath.Join(runtimeDirectory, "bingo_object_alias_harness.o")
	for _, path := range []string{archive, harness} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("runtime artifact %s is unavailable: %v", path, err)
		}
	}
	workspace := t.TempDir()
	objectPath := filepath.Join(workspace, "objectalias.o")
	executable := filepath.Join(workspace, "objectalias")
	if err := os.WriteFile(objectPath, emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(clang, "--target="+FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", objectPath, harness, archive, "-ldl", "-lpthread", "-lm", "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link VERT-010 executable: %v: %s", err, output)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := firstsliceoracle.OpenNode(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ input, want string }{
		{"0000000000000000", "3ff0000000000000"},
		{"8000000000000000", "3ff0000000000000"},
		{"3ff0000000000000", "4000000000000000"},
		{"7ff0000000000000", "7ff0000000000000"},
		{"7ff8000000000042", "7ff8000000000042"},
	} {
		output, err := exec.Command(executable, test.input).CombinedOutput()
		if err != nil {
			t.Fatalf("objectAlias(%s): %v: %s", test.input, err, output)
		}
		if got := strings.TrimSpace(string(output)); got != test.want {
			t.Fatalf("objectAlias(%s) = %s, want %s", test.input, got, test.want)
		}
		nodeResult, err := oracle.ObjectAlias(t.Context(), test.input)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(nodeResult.Output)); got != test.want || got != strings.TrimSpace(string(output)) {
			t.Fatalf("Node objectAlias(%s) = %s, native = %s, want %s", test.input, got, strings.TrimSpace(string(output)), test.want)
		}
	}
}

func testVERT010BoundMIR(t *testing.T) bingo.VERT010BoundMIR {
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
	hir := bingo.HIRModule{
		SchemaVersion: bingo.VERT010HIRSchemaVersion,
		Provenance: bingo.HIRProvenance{
			FrontendSnapshotSchemaVersion: bingo.HIRFrontendSnapshotSchemaVersion, FrontendSnapshotHash: strings.Repeat("a", 64), SourceContentHash: strings.Repeat("b", 64),
			CompilerBuildIdentity: bingo.CompilerBuildIdentity{UpstreamCommit: strings.Repeat("a", 40), ForkCommit: strings.Repeat("b", 40), LoweringSchema: "test", LoweringHash: strings.Repeat("c", 64)},
			StandardLibraryHash:   strings.Repeat("d", 64), KindManifestHash: strings.Repeat("e", 64), LogicalCapabilityRequirementsDigest: digest,
		},
		LogicalCapabilityRequirements: requirements,
		ObjectTypes:                   []bingo.HIRObjectType{objectType},
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
	_, hirHash, err := bingo.CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		t.Fatal(err)
	}
	hir.ContentHash = hirHash
	targetLayout, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := bingo.PlanObjectLayout(objectType.TypeKey, targetLayout, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerVERT010MIR(hir, layout)
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]bingo.BoundCapability, len(requirements))
	for index, requirement := range requirements {
		bindings[index] = bingo.BoundCapability{LogicalName: requirement, SymbolName: "test_" + strings.NewReplacer(".", "_").Replace(string(requirement)), SignatureHash: strings.Repeat("f", 64)}
	}
	bound, err := bingo.NewVERT010BoundMIR(mir, strings.Repeat("1", 64), strings.Repeat("2", 64), bindings)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}
