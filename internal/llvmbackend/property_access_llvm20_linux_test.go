//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"strings"
	"testing"

	llvm "tinygo.org/x/go-llvm"
)

func TestPropertyAccessLLVMAtO0AndO2(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan := propertyAccessBackendPlanForLayout(t, machine.Manifest().DataLayout.ContentHash)
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
	module := ctx.NewModule("property-access-test")
	defer module.Dispose()
	module.SetTarget(machine.Manifest().TargetTriple)
	module.SetDataLayout(machine.Manifest().DataLayout.LayoutString)
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	if err := emitPropertyAccessFunction(ctx, builder, module, plan); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	assertPropertyAccessLLVM(t, module.String(), plan)
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("default<O2>", tm, options); err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	assertPropertyAccessLLVM(t, module.String(), plan)
	emission, err := machine.EmitPropertyAccessObject(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(emission.Object) < 4 || emission.Object[0] != 0x7f || string(emission.Object[1:4]) != "ELF" {
		t.Fatal("property access emitter did not produce ELF")
	}
}

func TestPropertyAccessLLVMRejectsTargetSubstitution(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	plan := propertyAccessBackendPlanForLayout(t, strings.Repeat("d", 64))
	if _, err := machine.EmitPropertyAccessObject(plan); err == nil || !strings.Contains(err.Error(), "target does not match observed TargetMachine") {
		t.Fatalf("expected target substitution rejection, got %v", err)
	}
}

func assertPropertyAccessLLVM(t testing.TB, ir string, plan PropertyAccessBackendPlan) {
	t.Helper()
	for _, required := range []string{plan.EntrySymbol, plan.RuntimeSymbol, "status.is.success", "failure", "store { i32, i32, i64 } zeroinitializer", "ret i32 %status"} {
		if !strings.Contains(ir, required) {
			t.Fatalf("property access LLVM missing %q:\n%s", required, ir)
		}
	}
	if strings.Count(ir, "call i32 @"+plan.RuntimeSymbol) != 1 {
		t.Fatalf("property access LLVM runtime call count is not one:\n%s", ir)
	}
	for _, forbidden := range []string{"malloc", "bingo_gc_", "bingo_host_number_record_register", "invoke ", "landingpad"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("property access LLVM contains forbidden %q:\n%s", forbidden, ir)
		}
	}
}
