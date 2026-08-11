//go:build llvm20 && linux

package bingomir

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/tsfrontend"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

func TestLLVM20FirstSlicePipelineProducesDeterministicFinalMIR(t *testing.T) {
	fs := vfstest.FromMap(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function add(left: number, right: number): number { return left + right; }`,
	}, true)
	frontend := tsfrontend.NewFrontend(bundled.WrapFS(fs), bundled.LibPath(), tsfrontend.TypeScriptGoCommit, tsfrontend.StandardLibraryHash)
	snapshot, diagnostics := frontend.Build(context.Background(), tsfrontend.BuildRequest{
		ConfigPath:       "/project/tsconfig.json",
		CurrentDirectory: "/project",
		FileSystem:       fs,
	})
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("capture add snapshot: snapshot=%v diagnostics=%#v", snapshot != nil, diagnostics)
	}
	frontendSnapshot, err := tsfrontend.NewFrontendSnapshot(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	options := tsfrontend.DefaultBingoOptions()
	options.TargetTriple = llvmbackend.FirstSliceTriple
	plan, err := tsfrontend.ResolveBuildPlan(frontendSnapshot, options)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(snapshot.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	runtimeManifest, err := os.ReadFile("../targetcontext/testdata/runtime-manifest.json")
	if err != nil {
		t.Fatal(err)
	}

	first, firstExecution, err := ExecuteFirstSliceMIR(context.Background(), *snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	second, secondExecution, err := ExecuteFirstSliceMIR(context.Background(), *snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.CanonicalBoundBytes()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.CanonicalBoundBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) || first.ContentHash != second.ContentHash {
		t.Fatal("identical first-slice inputs produced different final MIR")
	}
	if first.BoundCapabilityClosure == nil || first.BoundCapabilityClosure.Bindings == nil || len(first.BoundCapabilityClosure.Bindings) != 0 {
		t.Fatalf("number-add bound capability closure = %#v", first.BoundCapabilityClosure)
	}
	if len(firstExecution.Dumps) != len(bingo.PassSpecs()) || len(secondExecution.Dumps) != len(firstExecution.Dumps) {
		t.Fatalf("pass dump counts = %d / %d, want %d", len(firstExecution.Dumps), len(secondExecution.Dumps), len(bingo.PassSpecs()))
	}
	for index := range firstExecution.Dumps {
		if firstExecution.Dumps[index].Pass != bingo.PassSpecs()[index].ID ||
			firstExecution.Dumps[index].ContentHash != secondExecution.Dumps[index].ContentHash {
			t.Fatalf("pass %d is nondeterministic or out of order", index)
		}
	}
	if firstExecution.State.Schema != "verified-mir-v3" || !slices.Contains(firstExecution.State.Facts, "final-mir") ||
		!slices.Contains(firstExecution.State.Facts, "bound-capability-closure") {
		t.Fatalf("final MIR state = %#v", firstExecution.State)
	}
	firstEmission, err := machine.EmitFirstSliceObject(first)
	if err != nil {
		t.Fatal(err)
	}
	secondEmission, err := machine.EmitFirstSliceObject(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstEmission.LLVMIR, secondEmission.LLVMIR) || !bytes.Equal(firstEmission.Object, secondEmission.Object) ||
		firstEmission.ContentHash != secondEmission.ContentHash {
		t.Fatal("identical verified MIR produced different LLVM/object artifacts")
	}
	llvmText := string(firstEmission.LLVMIR)
	for _, expected := range []string{"define double @add(double %left, double %right)", "fadd double %left, %right", "nounwind"} {
		if !strings.Contains(llvmText, expected) {
			t.Fatalf("LLVM output missing %q:\n%s", expected, llvmText)
		}
	}
	if strings.Contains(llvmText, "fadd fast") {
		t.Fatalf("first-slice LLVM enabled fast-math:\n%s", llvmText)
	}
	if _, err := firstEmission.CanonicalBytes(); err != nil {
		t.Fatalf("canonical emission manifest: %v", err)
	}

	resolverIndex := slices.IndexFunc(firstExecution.Dumps, func(dump bingo.PassDump) bool { return dump.Pass == bingo.PassResolveTarget })
	if resolverIndex < 0 {
		t.Fatal("resolver dump is missing")
	}
	resolverDump := firstExecution.Dumps[resolverIndex]
	resolverState := bingo.PassState{Schema: resolverDump.Schema, Facts: resolverDump.Facts, Artifact: resolverDump.Artifact, Artifacts: resolverDump.Artifacts}
	representationSpec := bingo.PassSpecs()[resolverIndex+1]
	representationHandler := targetAwareHandlers()[bingo.PassRepresentationPlan]
	if err := representationHandler.PreVerify(context.Background(), representationSpec, 1, resolverState); err != nil {
		t.Fatalf("valid representation join no longer verifies: %v", err)
	}
	typedArtifact, ok := resolverState.NamedArtifact(bingo.PassArtifactTypedHIR)
	if !ok {
		t.Fatal("typed HIR artifact is missing")
	}
	typed, err := ast2bingo.DecodePrimitiveTypedHIRArtifact(typedArtifact.Payload)
	if err != nil {
		t.Fatal(err)
	}
	typed.FrontendSnapshotHash = strings.Repeat("f", 64)
	typed.HIR.Provenance.FrontendSnapshotHash = typed.FrontendSnapshotHash
	typed.HIR.ContentHash = ""
	_, hirHash, err := bingo.CanonicalHIR(typed.HIR)
	if err != nil {
		t.Fatal(err)
	}
	typed.HIR.ContentHash = hirHash
	typedBytes, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := bingo.NewPassArtifact(bingo.PassArtifactTypedHIR, "hir-v8", typedBytes)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := slices.Clone(resolverState.Artifacts.Artifacts)
	for index := range artifacts {
		if artifacts[index].Name == bingo.PassArtifactTypedHIR {
			artifacts[index] = replacement
		}
	}
	tamperedEnvelope, err := bingo.NewPassArtifactEnvelope(artifacts...)
	if err != nil {
		t.Fatal(err)
	}
	tampered := resolverState
	tampered.Artifacts = &tamperedEnvelope
	if err := representationHandler.PreVerify(context.Background(), representationSpec, 1, tampered); err == nil || !strings.Contains(err.Error(), "do not join") {
		t.Fatalf("rehashed HIR/BuildPlan substitution error = %v", err)
	}
}

func TestLLVM20ApplicationMainUsesVersionedProgramABISymbol(t *testing.T) {
	fs := vfstest.FromMap(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function main(): number { return 255; }`,
	}, true)
	frontend := tsfrontend.NewFrontend(bundled.WrapFS(fs), bundled.LibPath(), tsfrontend.TypeScriptGoCommit, tsfrontend.StandardLibraryHash)
	snapshot, diagnostics := frontend.Build(context.Background(), tsfrontend.BuildRequest{ConfigPath: "/project/tsconfig.json", CurrentDirectory: "/project", FileSystem: fs})
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("capture application snapshot: snapshot=%v diagnostics=%#v", snapshot != nil, diagnostics)
	}
	frontendSnapshot, err := tsfrontend.NewFrontendSnapshot(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	options := tsfrontend.DefaultBingoOptions()
	options.TargetTriple = llvmbackend.FirstSliceTriple
	plan, err := tsfrontend.ResolveBuildPlan(frontendSnapshot, options)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(snapshot.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	runtimeManifest, err := os.ReadFile("../targetcontext/testdata/runtime-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	finalMIR, _, err := ExecuteFirstSliceMIR(context.Background(), *snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(finalMIR.Functions) != 1 || finalMIR.Functions[0].Name != "main" || !finalMIR.Functions[0].Exported {
		t.Fatalf("application final MIR = %#v", finalMIR.Functions)
	}
	emission, err := machine.EmitFirstSliceObject(finalMIR)
	if err != nil {
		t.Fatal(err)
	}
	llvmText := string(emission.LLVMIR)
	if !strings.Contains(llvmText, "define double @bingo_program_main_v1()") || strings.Contains(llvmText, "define double @main(") {
		t.Fatalf("application LLVM entrypoint is not collision-free:\n%s", llvmText)
	}
}

func TestLLVM20Phase2ChoosePipelineEmitsStrictBooleanBranch(t *testing.T) {
	fs := vfstest.FromMap(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function choose(flag: boolean, left: number, right: number): number { if (flag) { return left; } return right; }`,
	}, true)
	frontend := tsfrontend.NewFrontend(bundled.WrapFS(fs), bundled.LibPath(), tsfrontend.TypeScriptGoCommit, tsfrontend.StandardLibraryHash)
	snapshot, diagnostics := frontend.Build(context.Background(), tsfrontend.BuildRequest{ConfigPath: "/project/tsconfig.json", CurrentDirectory: "/project", FileSystem: fs})
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("capture choose snapshot: snapshot=%v diagnostics=%#v", snapshot != nil, diagnostics)
	}
	frontendSnapshot, err := tsfrontend.NewFrontendSnapshot(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	options := tsfrontend.DefaultBingoOptions()
	options.TargetTriple = llvmbackend.FirstSliceTriple
	plan, err := tsfrontend.ResolveBuildPlan(frontendSnapshot, options)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(snapshot.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	runtimeManifest, err := os.ReadFile("../targetcontext/testdata/runtime-manifest.json")
	if err != nil {
		t.Fatal(err)
	}

	finalMIR, execution, err := ExecuteFirstSliceMIR(context.Background(), *snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	secondMIR, _, err := ExecuteFirstSliceMIR(context.Background(), *snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	finalBytes, err := finalMIR.CanonicalBoundBytes()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := secondMIR.CanonicalBoundBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(finalBytes, secondBytes) || finalMIR.ContentHash != secondMIR.ContentHash {
		t.Fatal("identical choose inputs produced different final MIR")
	}
	function := finalMIR.Functions[0]
	if function.Name != "choose" || len(function.Parameters) != 3 || function.Parameters[0].Type != bingo.RepI1 || function.Parameters[1].Type != bingo.RepF64 || function.Parameters[2].Type != bingo.RepF64 || len(function.Blocks) != 3 {
		t.Fatalf("choose final MIR = %#v", function)
	}
	if entry := function.Blocks[0].Terminator; entry.Kind != "condbranch" || entry.Value != 1 || !slices.Equal(entry.Successors, []bingo.BlockID{2, 3}) {
		t.Fatalf("choose final MIR entry = %#v", entry)
	}
	representationArtifact, ok := execution.State.NamedArtifact(bingo.PassArtifactRepresentationPlan)
	if !ok {
		t.Fatal("choose representation plan sidecar is missing")
	}
	representation, err := bingo.DecodeRepresentationPlan(representationArtifact.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(representation.Bindings) != 2 || representation.Bindings[0].RepType != bingo.RepI1 || representation.Bindings[1].RepType != bingo.RepF64 {
		t.Fatalf("choose representation bindings = %#v", representation.Bindings)
	}
	emission, err := machine.EmitFirstSliceObject(finalMIR)
	if err != nil {
		t.Fatal(err)
	}
	secondEmission, err := machine.EmitFirstSliceObject(secondMIR)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(emission.LLVMIR, secondEmission.LLVMIR) || !bytes.Equal(emission.Object, secondEmission.Object) || emission.ContentHash != secondEmission.ContentHash {
		t.Fatal("identical choose MIR produced different LLVM/object artifacts")
	}
	llvmText := string(emission.LLVMIR)
	for _, expected := range []string{
		"define double @choose(i8 %flag, double %left, double %right)",
		"icmp ult i8 %flag, 2",
		"call void @llvm.trap()",
		"trunc i8 %flag to i1",
		"br i1 %flag.i1",
		"ret double %left",
		"ret double %right",
		"nounwind",
	} {
		if !strings.Contains(llvmText, expected) {
			t.Fatalf("choose LLVM output missing %q:\n%s", expected, llvmText)
		}
	}
	if len(emission.Object) < 4 || string(emission.Object[:4]) != "\x7fELF" {
		t.Fatalf("choose emission is not an ELF object: %d bytes", len(emission.Object))
	}
}
