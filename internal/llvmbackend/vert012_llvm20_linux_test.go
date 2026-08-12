//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	llvm "tinygo.org/x/go-llvm"
)

func TestVERT012LLVMUsesBoundCapabilitiesTraceAndIndirectCalls(t *testing.T) {
	bound := testVERT012BoundMIR(t)
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
	module := ctx.NewModule("vert012-test")
	defer module.Dispose()
	module.SetTarget(FirstSliceTriple)
	module.SetDataLayout(FirstSliceDataLayout)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitVERT012Module(ctx, builder, module, bound); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O0 verify: %v", err)
	}
	ir := module.String()
	for _, binding := range bound.Closure.Bindings {
		if !strings.Contains(ir, "@"+binding.SymbolName+"(") {
			t.Fatalf("bound capability %q absent", binding.SymbolName)
		}
	}
	for _, fragment := range []string{"@vert012.cell.shape = private constant", "@vert012.environment.shape = private constant", "@vert012.environment.trace.offsets = private constant", "define private double @vert012.closure.increment", "call double %closure.code.extract", "environment.cell.barrier.status"} {
		if !strings.Contains(ir, fragment) {
			t.Fatalf("VERT-012 LLVM evidence %q absent", fragment)
		}
	}
	if strings.Contains(ir, "@bingo_gc_") {
		t.Fatal("VERT-012 LLVM bypassed bound symbols")
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
			t.Fatalf("O2 removed capability %q", binding.SymbolName)
		}
	}
	if !strings.Contains(optimized, "define double @closureCounter") || !strings.Contains(optimized, "fadd double") {
		t.Fatal("O2 removed VERT-012 observable closure computation")
	}
}

func TestTargetMachineEmitsVERT012ELFObject(t *testing.T) {
	bound := testVERT012BoundMIR(t)
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	emission, err := machine.EmitVERT012Object(bound)
	if err != nil {
		t.Fatal(err)
	}
	if emission.MIRContentHash != bound.ContentHash || len(emission.Object) < 4 || emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatal("invalid VERT-012 ELF emission")
	}
}

func TestVERT012ObjectLinksRunsAndMatchesNode(t *testing.T) {
	bound := testVERT012BoundMIR(t)
	runtimeSymbols := map[bingo.RuntimeCapabilityID]string{
		"rt.gc.alloc": "bingo_gc_alloc_v1", "rt.gc.frame.link": "bingo_gc_frame_link_v1",
		"rt.gc.frame.unlink": "bingo_gc_frame_unlink_v1", "rt.gc.root.clear": "bingo_gc_root_clear_v1",
		"rt.gc.root.publish": "bingo_gc_root_publish_v1", "rt.gc.root.reload": "bingo_gc_root_reload_v1",
		"rt.gc.root.store": "bingo_gc_root_store_v1", "rt.gc.safepoint": "bingo_gc_safepoint_v1",
		"rt.gc.write_barrier": "bingo_gc_write_barrier_v1",
	}
	bindings := make([]bingo.BoundCapability, len(bound.MIR.LogicalCapabilityRequirements))
	for index, requirement := range bound.MIR.LogicalCapabilityRequirements {
		bindings[index] = bingo.BoundCapability{LogicalName: requirement, SymbolName: runtimeSymbols[requirement], SignatureHash: strings.Repeat("f", 64)}
	}
	var err error
	bound, err = bingo.NewVERT012BoundMIR(bound.MIR, bound.TargetContextHash, bound.Closure.AvailableCapabilityCatalogHash, bindings)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	emission, err := machine.EmitVERT012Object(bound)
	if err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "target", "first-slice"))
	archive := filepath.Join(runtimeDirectory, "cargo", FirstSliceTriple, "release", "libbingo_runtime.a")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("runtime archive unavailable: %v", err)
	}
	workspace := t.TempDir()
	objectPath, harnessPath, executable := filepath.Join(workspace, "closure.o"), filepath.Join(workspace, "harness.c"), filepath.Join(workspace, "closure")
	if err := os.WriteFile(objectPath, emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessPath, []byte(vert012HarnessSource), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(clang, "--target="+FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", objectPath, harnessPath, archive, "-ldl", "-lpthread", "-lm", "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link VERT-012: %v: %s", err, output)
	}
	for _, input := range []string{"0000000000000000", "8000000000000000", "3ff0000000000000", "7ff0000000000000", "7ff8000000000042"} {
		native, err := exec.Command(executable, input).CombinedOutput()
		if err != nil {
			t.Fatalf("native(%s): %v: %s", input, err, native)
		}
		nodeOutput, err := exec.Command(node, "-e", vert012NodeOracle, input).CombinedOutput()
		if err != nil {
			t.Fatalf("Node(%s): %v: %s", input, err, nodeOutput)
		}
		if strings.TrimSpace(string(native)) != strings.TrimSpace(string(nodeOutput)) {
			t.Fatalf("VERT-012 native/Node mismatch for %s: %s / %s", input, native, nodeOutput)
		}
	}
}

const vert012HarnessSource = "#include <inttypes.h>\n#include <stdint.h>\n#include <stdio.h>\n#include <stdlib.h>\n#include <string.h>\nextern double closureCounter(double);\nint main(int argc, char **argv) {\n if (argc != 2 || strlen(argv[1]) != 16) return 2;\n char *end = NULL; uint64_t input = strtoull(argv[1], &end, 16);\n if (end == NULL || *end != 0) return 2;\n double value; memcpy(&value, &input, sizeof(value));\n double result = closureCounter(value); uint64_t bits; memcpy(&bits, &result, sizeof(bits));\n printf(\"%016\" PRIx64 \"\\n\", bits); return 0;\n}\n"

const vert012NodeOracle = "const bits=BigInt('0x'+process.argv[1]);const b=new ArrayBuffer(8);const v=new DataView(b);v.setBigUint64(0,bits,false);const start=v.getFloat64(0,false);function makeCounter(x){let count=x;return()=>{count+=1;return count;};}const inc=makeCounter(start);const result=inc()+inc();v.setFloat64(0,result,false);console.log(v.getBigUint64(0,false).toString(16).padStart(16,'0'));"

func testVERT012BoundMIR(t testing.TB) bingo.VERT012BoundMIR {
	t.Helper()
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
	bindings := make([]bingo.BoundCapability, len(mir.LogicalCapabilityRequirements))
	for index, requirement := range mir.LogicalCapabilityRequirements {
		bindings[index] = bingo.BoundCapability{LogicalName: requirement, SymbolName: "test_" + strings.NewReplacer(".", "_").Replace(string(requirement)), SignatureHash: strings.Repeat("f", 64)}
	}
	bound, err := bingo.NewVERT012BoundMIR(mir, strings.Repeat("1", 64), strings.Repeat("2", 64), bindings)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}
