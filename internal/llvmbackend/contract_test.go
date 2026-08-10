package llvmbackend

import (
	"testing"
)

func TestFirstSliceManifestRejectsTampering(t *testing.T) {
	t.Parallel()
	layout := newDataLayout(FirstSliceTriple, FirstSliceDataLayout, 64, 64, 8, true)
	manifest := newToolchainManifest(layout)
	if err := ValidateToolchainManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.DataLayout.LayoutString = "e-p:32:32"
	if err := ValidateToolchainManifest(manifest); err == nil {
		t.Fatal("tampered data layout was accepted")
	}
}

func TestFirstSliceConstantsAreLocked(t *testing.T) {
	t.Parallel()
	if FirstSliceTriple != "x86_64-unknown-linux-gnu" || FirstSliceCPU != "generic" || LockedLLVMMajor != 20 || LockedLLVMVersion != "20.1.8" {
		t.Fatalf("unexpected first-slice target constants")
	}
}
