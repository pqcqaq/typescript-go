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

func TestObjectLayoutCopyLLVMUsesExactRootedAllocationProtocolAtO0AndO2(t *testing.T) {
	plan, err := BuildObjectLayoutCopyBackendPlan(backendObjectLayoutCopyBound(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	module := ctx.NewModule("copy-test")
	defer module.Dispose()
	module.SetTarget(FirstSliceTriple)
	module.SetDataLayout(FirstSliceDataLayout)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitObjectLayoutCopyFunction(ctx, builder, module, plan); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	assertObjectLayoutCopyRootedIR(t, module.String())
	llvm.InitializeAllTargetInfos()
	llvm.InitializeAllTargets()
	llvm.InitializeAllTargetMCs()
	target, err := llvm.GetTargetFromTriple(FirstSliceTriple)
	if err != nil {
		t.Fatal(err)
	}
	machine := target.CreateTargetMachine(FirstSliceTriple, FirstSliceCPU, "", llvm.CodeGenLevelDefault, llvm.RelocPIC, llvm.CodeModelSmall)
	defer machine.Dispose()
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("default<O2>", machine, options); err != nil {
		t.Fatalf("O2 passes: %v", err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("O2 verify: %v", err)
	}
	assertObjectLayoutCopyRootedIR(t, module.String())
}

func assertObjectLayoutCopyRootedIR(t testing.TB, ir string) {
	t.Helper()
	for _, fragment := range []string{"@object.layout.copy.target.shape = private constant", "@bingo_gc_frame_link_v1(", "@bingo_gc_root_store_v1(", "@bingo_gc_root_publish_v1(", "@bingo_gc_alloc_v1(", "@bingo_gc_root_reload_v1(", "@bingo_gc_frame_unlink_v1(", "load double", "store double"} {
		if !strings.Contains(ir, fragment) {
			t.Fatalf("LLVM missing %q", fragment)
		}
	}
	previous := -1
	for _, call := range []string{"call i32 @bingo_gc_frame_link_v1(", "call i32 @bingo_gc_root_store_v1(", "call i32 @bingo_gc_root_publish_v1(", "call i32 @bingo_gc_alloc_v1(", "call i32 @bingo_gc_root_reload_v1(", "call i32 @bingo_gc_frame_unlink_v1("} {
		index := strings.Index(ir[previous+1:], call)
		if index < 0 {
			t.Fatalf("LLVM rooted call order is missing %q", call)
		}
		previous += index + 1
	}
	if strings.Contains(ir, "bitcast") || strings.Contains(ir, "bingo_gc_safepoint_v1") {
		t.Fatalf("LLVM contains forbidden operation:\n%s", ir)
	}
}

func TestTargetMachineEmitsObjectLayoutCopyELFObject(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan, err := BuildObjectLayoutCopyBackendPlan(backendObjectLayoutCopyBound(t))
	if err != nil {
		t.Fatal(err)
	}
	emission, err := machine.EmitObjectLayoutCopyObject(plan)
	if err != nil {
		t.Fatal(err)
	}
	if emission.MIRContentHash != plan.ContentHash || len(emission.Object) < 4 || emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatal("invalid copy ELF emission")
	}
}

func TestObjectLayoutCopyNativeMatchesNodeNewIdentity(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan, err := BuildObjectLayoutCopyBackendPlan(backendObjectLayoutCopyBound(t))
	if err != nil {
		t.Fatal(err)
	}
	emission, err := machine.EmitObjectLayoutCopyObject(plan)
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
	runtimeArchive, err := filepath.Abs(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "target", "first-slice", "cargo", "x86_64-unknown-linux-gnu", "release", "libbingo_runtime.a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runtimeArchive); err != nil {
		t.Fatalf("runtime archive is not built: %v", err)
	}
	workspace := t.TempDir()
	objectPath := filepath.Join(workspace, "object-layout-copy.o")
	executable := filepath.Join(workspace, "object-layout-copy")
	if err := os.WriteFile(objectPath, emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "harness", "object_layout_copy_bits.c"))
	include := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "include"))
	command := exec.Command(clang, "--target="+FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", "-I"+include, objectPath, harness, runtimeArchive, "-lpthread", "-ldl", "-lm", "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link object-layout-copy executable: %v: %s", err, output)
	}
	sourceOffset := strconv.FormatUint(uint64(plan.SourceOffset), 10)
	targetOffset := strconv.FormatUint(uint64(plan.TargetOffset), 10)
	for _, input := range []string{"0000000000000000", "8000000000000000", "3ff0000000000000", "7ff0000000000000", "7ff8000000000042"} {
		native, err := exec.Command(executable, sourceOffset, targetOffset, input).CombinedOutput()
		if err != nil {
			t.Fatalf("object-layout-copy(%s): %v: %s", input, err, native)
		}
		node, err := oracle.ObjectLayoutCopy(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.TrimSpace(string(native)), strings.TrimSpace(string(node.Output)); got != "1:"+input || want != "1:"+input || got != want {
			t.Fatalf("object-layout-copy(%s): native=%s Node=%s", input, got, want)
		}
	}
}
