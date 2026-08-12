//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	llvm "tinygo.org/x/go-llvm"
)

func TestFunctionThunkLLVMAtO0AndO2(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan, err := BuildFunctionThunkBackendPlan(backendFunctionThunkMIR(t, machine.Manifest().DataLayout.ContentHash))
	if err != nil {
		t.Fatal(err)
	}
	llvm.InitializeAllTargetInfos()
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	target, err := llvm.GetTargetFromTriple(FirstSliceTriple)
	if err != nil {
		t.Fatal(err)
	}
	tm := target.CreateTargetMachine(FirstSliceTriple, FirstSliceCPU, "", llvm.CodeGenLevelNone, llvm.RelocPIC, llvm.CodeModelSmall)
	defer tm.Dispose()
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("function-thunk-test")
	defer module.Dispose()
	module.SetTarget(FirstSliceTriple)
	module.SetDataLayout(FirstSliceDataLayout)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitFunctionThunkFunction(ctx, builder, module, plan); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	assertFunctionThunkLLVM(t, module.String(), plan)
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("default<O2>", tm, options); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	assertFunctionThunkLLVM(t, module.String(), plan)
}

func TestFunctionThunkELFAndNativeMatchesNode(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan, err := BuildFunctionThunkBackendPlan(backendFunctionThunkMIR(t, machine.Manifest().DataLayout.ContentHash))
	if err != nil {
		t.Fatal(err)
	}
	emission, err := machine.EmitFunctionThunkObject(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(emission.Object) < 4 || emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatal("function thunk emitter did not produce ELF")
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	objectPath, executable := filepath.Join(workspace, "function-thunk.o"), filepath.Join(workspace, "function-thunk")
	if err := os.WriteFile(objectPath, emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "harness", "function_thunk_identity.c"))
	command := exec.Command(clang, "--target="+FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", objectPath, harness, "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link function thunk: %v: %s", err, output)
	}
	native, err := exec.Command(executable).CombinedOutput()
	if err != nil {
		t.Fatalf("run function thunk: %v: %s", err, native)
	}
	oracle, err := exec.Command(node, "-e", `const env={}; const value={}; const source=(e,v)=>[e===env,v]; const [same,result]=source(env,value); process.stdout.write(Number(same)+" "+Number(result===value)+"\n")`).CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if string(native) != string(oracle) || strings.TrimSpace(string(native)) != "1 1" {
		t.Fatalf("function thunk native=%q Node=%q", native, oracle)
	}
}

func assertFunctionThunkLLVM(t testing.TB, ir string, plan FunctionThunkBackendPlan) {
	t.Helper()
	for _, required := range []string{plan.FunctionName, "extractvalue", "source.code", "source.environment", "call ptr", "ret ptr"} {
		if !strings.Contains(ir, required) {
			t.Fatalf("function thunk LLVM missing %q:\n%s", required, ir)
		}
	}
	for _, forbidden := range []string{" bitcast ", "malloc", "bingo_gc_", "alloca"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("function thunk LLVM contains forbidden %q:\n%s", forbidden, ir)
		}
	}
}
