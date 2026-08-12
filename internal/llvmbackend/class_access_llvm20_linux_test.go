//go:build llvm20 && cgo && linux

package llvmbackend

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func testClassAccessBoundForLLVM(t testing.TB) bingo.ClassAccessBoundMIR {
	t.Helper()
	plan := testClassAccessBackendPlan(t)
	requirements := plan.Layout.MIR.HIR.LogicalCapabilityRequirements
	bindings := make([]bingo.BoundCapability, 0, len(requirements))
	for _, requirement := range requirements {
		bindings = append(bindings, bingo.BoundCapability{LogicalName: requirement, SymbolName: "rt_" + string(requirement), SignatureHash: testDigest("f")})
	}
	bound, err := bingo.NewClassAccessBoundMIR(plan.Layout, plan.Layout.MIR.Target.TargetContextHash, testDigest("e"), bindings)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func TestClassAccessLLVMModuleUsesBoundExecutionAndGC(t *testing.T) {
	bound := testClassAccessBoundForLLVM(t)
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	emission, err := machine.EmitClassAccessObject(bound)
	if err != nil {
		t.Fatal(err)
	}
	text := string(emission.LLVMIR)
	for _, needle := range []string{"define double @classAccess", "classaccess.DerivedVault.constructor", "rt.gc.alloc", "rt.gc.safepoint", "fadd"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("classaccess LLVM missing %q", needle)
		}
	}
	if len(emission.Object) == 0 {
		t.Fatal("classaccess LLVM emitter produced empty object")
	}
}
