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

func TestVERT013bLLVMUsesDirectSuperAndDerivedReceiver(t *testing.T) {
	bound := testVERT013bBoundMIR(t)
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
	module := ctx.NewModule("vert013b-test")
	defer module.Dispose()
	module.SetTarget(FirstSliceTriple)
	module.SetDataLayout(FirstSliceDataLayout)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitVERT013bModule(ctx, builder, module, bound); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O0 verify: %v", err)
	}
	ir := module.String()
	for _, fragment := range []string{
		"define private ptr @vert013b.Counter.constructor(ptr",
		"define private ptr @vert013b.StepCounter.constructor(double",
		"define private double @vert013b.StepCounter.increment(ptr",
		"define double @derivedCounter(double",
		"call ptr @vert013b.Counter.constructor",
		"call double @vert013b.StepCounter.increment",
		"first.method.receiver.reloaded",
		"second.method.receiver.reloaded",
	} {
		if !strings.Contains(ir, fragment) {
			t.Fatalf("VERT-013b LLVM evidence %q absent", fragment)
		}
	}
	for _, binding := range bound.Closure.Bindings {
		if !strings.Contains(ir, "@"+binding.SymbolName+"(") {
			t.Fatalf("bound capability %q absent", binding.SymbolName)
		}
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
	if optimized := module.String(); !strings.Contains(optimized, "define double @derivedCounter") || !strings.Contains(optimized, "fadd double") {
		t.Fatal("O2 removed VERT-013b observable computation")
	}
}

func TestVERT013bObjectLinksRunsAndMatchesNode(t *testing.T) {
	bound := testVERT013bBoundMIR(t)
	runtimeSymbols := map[bingo.RuntimeCapabilityID]string{"rt.gc.alloc": "bingo_gc_alloc_v1", "rt.gc.frame.link": "bingo_gc_frame_link_v1", "rt.gc.frame.unlink": "bingo_gc_frame_unlink_v1", "rt.gc.root.clear": "bingo_gc_root_clear_v1", "rt.gc.root.publish": "bingo_gc_root_publish_v1", "rt.gc.root.reload": "bingo_gc_root_reload_v1", "rt.gc.root.store": "bingo_gc_root_store_v1", "rt.gc.safepoint": "bingo_gc_safepoint_v1"}
	bindings := make([]bingo.BoundCapability, len(bound.MIR.LogicalCapabilityRequirements))
	for i, requirement := range bound.MIR.LogicalCapabilityRequirements {
		bindings[i] = bingo.BoundCapability{LogicalName: requirement, SymbolName: runtimeSymbols[requirement], SignatureHash: strings.Repeat("f", 64)}
	}
	var err error
	bound, err = bingo.NewVERT013bBoundMIR(bound.MIR, bound.TargetContextHash, bound.Closure.AvailableCapabilityCatalogHash, bindings)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	emission, err := machine.EmitVERT013bObject(bound)
	if err != nil {
		t.Fatal(err)
	}
	if len(emission.Object) < 4 || emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatal("invalid VERT-013b ELF object")
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join("..", "..", "..", "runtime", "bingo-rt", "target", "first-slice", "cargo", FirstSliceTriple, "release", "libbingo_runtime.a")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("runtime archive unavailable: %v", err)
	}
	workspace := t.TempDir()
	objectPath, harnessPath, executable := filepath.Join(workspace, "derived.o"), filepath.Join(workspace, "harness.c"), filepath.Join(workspace, "derived")
	if err := os.WriteFile(objectPath, emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harnessPath, []byte(vert013bHarnessSource), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(clang, "--target="+FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", objectPath, harnessPath, archive, "-ldl", "-lpthread", "-lm", "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link VERT-013b: %v: %s", err, output)
	}
	inputs := [][2]string{{"0000000000000000", "3ff0000000000000"}, {"8000000000000000", "4000000000000000"}, {"3ff0000000000000", "bff0000000000000"}, {"7ff0000000000000", "3ff0000000000000"}, {"7ff8000000000042", "4000000000000000"}}
	for _, input := range inputs {
		native, err := exec.Command(executable, input[0], input[1]).CombinedOutput()
		if err != nil {
			t.Fatalf("native(%v): %v: %s", input, err, native)
		}
		nodeOutput, err := exec.Command(node, "-e", vert013bNodeOracle, input[0], input[1]).CombinedOutput()
		if err != nil {
			t.Fatalf("Node(%v): %v: %s", input, err, nodeOutput)
		}
		if strings.TrimSpace(string(native)) != strings.TrimSpace(string(nodeOutput)) {
			t.Fatalf("VERT-013b native/Node mismatch for %v: %s / %s", input, native, nodeOutput)
		}
	}
}

func testVERT013bBoundMIR(t testing.TB) bingo.VERT013bBoundMIR {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/derivedcounter/frontend-snapshot.json")
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
	replay, err := ast2bingo.ReplayVERT013bSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := bingo.PlanVERT013bLayout(replay.Contract, target)
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerVERT013bMIR(replay.HIR, layout)
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]bingo.BoundCapability, len(mir.LogicalCapabilityRequirements))
	for i, requirement := range mir.LogicalCapabilityRequirements {
		bindings[i] = bingo.BoundCapability{LogicalName: requirement, SymbolName: "test_" + strings.NewReplacer(".", "_").Replace(string(requirement)), SignatureHash: strings.Repeat("f", 64)}
	}
	bound, err := bingo.NewVERT013bBoundMIR(mir, strings.Repeat("1", 64), strings.Repeat("2", 64), bindings)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

const vert013bHarnessSource = "#include <inttypes.h>\n#include <stdint.h>\n#include <stdio.h>\n#include <stdlib.h>\n#include <string.h>\nextern double derivedCounter(double,double);\nint main(int argc,char **argv){if(argc!=3||strlen(argv[1])!=16||strlen(argv[2])!=16)return 2;uint64_t a=strtoull(argv[1],0,16),b=strtoull(argv[2],0,16),r;double start,step;memcpy(&start,&a,8);memcpy(&step,&b,8);double value=derivedCounter(start,step);memcpy(&r,&value,8);printf(\"%016\" PRIx64 \"\\n\",r);return 0;}\n"
const vert013bNodeOracle = "const cv=x=>{const b=new ArrayBuffer(8),v=new DataView(b);v.setBigUint64(0,BigInt('0x'+x),false);return v.getFloat64(0,false)};const start=cv(process.argv[1]),step=cv(process.argv[2]);class Counter{value=0;constructor(x){this.value=x}increment(){this.value+=1;return this.value}}class StepCounter extends Counter{step=1;constructor(x,s){super(x);this.step=s}increment(){this.value+=this.step;return this.value}}const c=new StepCounter(start,step),result=c.increment()+c.increment(),b=new ArrayBuffer(8),v=new DataView(b);v.setFloat64(0,result,false);console.log(v.getBigUint64(0,false).toString(16).padStart(16,'0'));"
