//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/firstsliceoracle"
	llvm "tinygo.org/x/go-llvm"
)

func TestObjectViewLLVMIsReadonlyIdentityLoadAtO0AndO2(t *testing.T) {
	plan, err := BuildObjectViewBackendPlan(backendObjectViewMIR(t))
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
	machine := target.CreateTargetMachine(FirstSliceTriple, FirstSliceCPU, "", llvm.CodeGenLevelNone, llvm.RelocPIC, llvm.CodeModelSmall)
	defer machine.Dispose()
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("object-view-test")
	defer module.Dispose()
	module.SetTarget(FirstSliceTriple)
	module.SetDataLayout(FirstSliceDataLayout)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitObjectViewFunction(ctx, builder, module, plan); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O0 verify: %v", err)
	}
	assertObjectViewLLVM(t, module.String(), plan)
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("default<O2>", machine, options); err != nil {
		t.Fatalf("O2 passes: %v", err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O2 verify: %v", err)
	}
	assertObjectViewLLVM(t, module.String(), plan)
}

func TestTargetMachineEmitsObjectViewELFObject(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan, err := BuildObjectViewBackendPlan(backendObjectViewMIR(t))
	if err != nil {
		t.Fatal(err)
	}
	emission, err := machine.EmitObjectViewObject(plan)
	if err != nil {
		t.Fatal(err)
	}
	if emission.MIRContentHash != plan.ContentHash || len(emission.LLVMIR) == 0 || len(emission.Object) < 4 {
		t.Fatalf("invalid ObjectView emission: %#v", emission)
	}
	if emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatal("ObjectView emitter did not produce ELF")
	}
}

func TestObjectAccessorViewLLVMJoinsVerifiedGetter(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan, err := BuildObjectViewBackendPlan(backendObjectAccessorViewMIR(t))
	if err != nil {
		t.Fatal(err)
	}
	emission, err := machine.EmitObjectViewObject(plan)
	if err != nil {
		t.Fatal(err)
	}
	ir := string(emission.LLVMIR)
	for _, required := range []string{plan.FunctionName, "objectview.getter." + plan.GetterSymbolKey, "backing.payload", "backing.tag", "call void"} {
		if !strings.Contains(ir, required) {
			t.Fatalf("ObjectView accessor LLVM is missing %q:\n%s", required, ir)
		}
	}
	for _, forbidden := range []string{"malloc", "bingo_gc_", " bitcast "} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("ObjectView accessor LLVM contains forbidden %q:\n%s", forbidden, ir)
		}
	}
	if len(emission.Object) < 4 || emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatal("ObjectView accessor emitter did not produce ELF")
	}
}

func TestObjectAccessorViewNativeMatchesNodeTagAndPayload(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan, err := BuildObjectViewBackendPlan(backendObjectAccessorViewMIR(t))
	if err != nil {
		t.Fatal(err)
	}
	emission, err := machine.EmitObjectViewObject(plan)
	if err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := firstsliceoracle.OpenNode(t.Context(), nodePath)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	objectPath := filepath.Join(workspace, "accessor-view.o")
	executable := filepath.Join(workspace, "accessor-view")
	if err := os.WriteFile(objectPath, emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "harness", "object_accessor_view_bits.c"))
	command := exec.Command(clang, "--target="+FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", objectPath, harness, "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link accessor-view: %v: %s", err, output)
	}
	offset := strconv.FormatUint(uint64(plan.BackingOffset), 10)
	for _, test := range []struct{ tag, native, bits string }{{"number", "0", "0000000000000000"}, {"number", "0", "8000000000000000"}, {"number", "0", "7ff8000000000042"}, {"null", "1", "3ff0000000000000"}, {"undefined", "2", "4000000000000000"}} {
		native, err := exec.Command(executable, offset, test.native, test.bits).CombinedOutput()
		if err != nil {
			t.Fatalf("accessor-view %s: %v: %s", test.tag, err, native)
		}
		node, err := oracle.ObjectAccessorView(t.Context(), test.tag, test.bits)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.TrimSpace(string(native)), strings.TrimSpace(string(node.Output)); got != want {
			t.Fatalf("accessor-view %s/%s: native=%s Node=%s", test.tag, test.bits, got, want)
		}
	}
}

func TestObjectViewNativeMatchesNodeIdentityRead(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan, err := BuildObjectViewBackendPlan(backendObjectViewMIR(t))
	if err != nil {
		t.Fatal(err)
	}
	emission, err := machine.EmitObjectViewObject(plan)
	if err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := firstsliceoracle.OpenNode(t.Context(), nodePath)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	objectPath := filepath.Join(workspace, "object-view.o")
	executable := filepath.Join(workspace, "object-view")
	if err := os.WriteFile(objectPath, emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "harness", "object_view_bits.c"))
	command := exec.Command(clang, "--target="+FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", objectPath, harness, "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link ObjectView executable: %v: %s", err, output)
	}
	offset := strconv.FormatUint(uint64(plan.SourceOffset), 10)
	for _, input := range []string{"0000000000000000", "8000000000000000", "3ff0000000000000", "7ff0000000000000", "7ff8000000000042"} {
		native, err := exec.Command(executable, offset, input).CombinedOutput()
		if err != nil {
			t.Fatalf("ObjectView(%s): %v: %s", input, err, native)
		}
		nativeBits := strings.TrimSpace(string(native))
		node, err := oracle.ObjectView(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if nodeBits := strings.TrimSpace(string(node.Output)); nativeBits != input || nodeBits != input || nativeBits != nodeBits {
			t.Fatalf("ObjectView(%s): native = %s, Node = %s", input, nativeBits, nodeBits)
		}
	}
}

func assertObjectViewLLVM(t testing.TB, ir string, plan ObjectViewBackendPlan) {
	t.Helper()
	for _, forbidden := range []string{" bitcast ", " call ", " alloca ", "malloc", "bingo_gc_"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("ObjectView LLVM contains forbidden %q:\n%s", forbidden, ir)
		}
	}
	if !strings.Contains(ir, "getelementptr inbounds i8") || !strings.Contains(ir, "load double") || !strings.Contains(ir, plan.FunctionName) {
		t.Fatalf("ObjectView LLVM is missing verified read:\n%s", ir)
	}
}
