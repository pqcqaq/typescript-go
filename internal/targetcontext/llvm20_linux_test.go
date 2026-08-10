//go:build llvm20 && cgo && linux

package targetcontext

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestResolveTargetContextUsesLLVMTargetMachineDataLayout(t *testing.T) {
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	resolution, err := ResolveTargetContext(validBuildPlan(), machine, runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Context.LLVMDataLayout != resolution.Toolchain.DataLayout.LayoutString {
		t.Fatalf("context data layout = %q, observed = %q", resolution.Context.LLVMDataLayout, resolution.Toolchain.DataLayout.LayoutString)
	}
	if resolution.Context.DataLayoutHash != resolution.Toolchain.DataLayout.ContentHash {
		t.Fatalf("context data layout hash = %q, observed = %q", resolution.Context.DataLayoutHash, resolution.Toolchain.DataLayout.ContentHash)
	}
}
