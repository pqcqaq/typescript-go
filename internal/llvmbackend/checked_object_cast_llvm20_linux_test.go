//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"strings"
	"testing"

	llvm "tinygo.org/x/go-llvm"
)

func TestCheckedObjectCastLLVMHasCheckedIdentityCFGAtO0AndO2(t *testing.T) {
	plan, err := BuildCheckedObjectCastBackendPlan(backendCheckedObjectCastBound(t))
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
	module := ctx.NewModule("checked-cast-test")
	defer module.Dispose()
	module.SetTarget(FirstSliceTriple)
	module.SetDataLayout(FirstSliceDataLayout)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitCheckedObjectCastFunction(ctx, builder, module, plan); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	assertCheckedCastLLVM(t, module.String())
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("default<O2>", machine, options); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	assertCheckedCastLLVM(t, module.String())
}

func assertCheckedCastLLVM(t *testing.T, ir string) {
	t.Helper()
	for _, required := range []string{"bingo_checked_object_cast_v1", "bingo_shape_matches_v1", "checkedcast.target.shape", "outputs.invalid", "shape.status", "shape.match", "match.invalid", "cast.value"} {
		if !strings.Contains(ir, required) {
			t.Fatalf("checked-cast LLVM missing %q:\n%s", required, ir)
		}
	}
	for _, forbidden := range []string{"malloc", "memcpy", "bitcast"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("checked-cast LLVM contains forbidden %q:\n%s", forbidden, ir)
		}
	}
}
