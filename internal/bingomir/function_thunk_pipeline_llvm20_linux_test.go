//go:build llvm20 && cgo && linux

package bingomir

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestExecuteFunctionThunkReplayBindsObservedTarget(t *testing.T) {
	_, identity, replay, plan := functionThunkPipelineFixture(t)
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	result, err := ExecuteFunctionThunkReplay(replay, identity, plan, machine)
	if err != nil {
		t.Fatal(err)
	}
	if result.MIR.TargetTriple != machine.Manifest().TargetTriple || result.MIR.DataLayoutHash != machine.Manifest().DataLayout.ContentHash || result.Emission.MIRContentHash != result.BackendPlan.ContentHash || len(result.Emission.Object) < 4 || string(result.Emission.Object[1:4]) != "ELF" {
		t.Fatalf("invalid function thunk production result: %#v", result)
	}
	if !strings.Contains(string(result.Emission.LLVMIR), result.BackendPlan.FunctionName) {
		t.Fatal("production emission is missing thunk wrapper")
	}
}
