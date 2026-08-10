//go:build llvm20 && cgo && linux

package llvmbackend

import "testing"

func TestLLVM20TargetMachineVerifiesAndEmitsProbeObject(t *testing.T) {
	machine, err := OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	manifest := machine.Manifest()
	if manifest.DataLayout.LayoutString != FirstSliceDataLayout {
		t.Fatalf("data layout = %q, want %q", manifest.DataLayout.LayoutString, FirstSliceDataLayout)
	}
	first, err := machine.EmitProbeObject()
	if err != nil {
		t.Fatal(err)
	}
	second, err := machine.EmitProbeObject()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 4 || string(first[:4]) != "\x7fELF" {
		t.Fatalf("probe is not an ELF object: %d bytes", len(first))
	}
	if string(first) != string(second) {
		t.Fatal("probe object bytes changed between emissions")
	}
}
