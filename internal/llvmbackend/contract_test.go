package llvmbackend

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func TestFirstSliceEmissionBindsLLVMAndObjectBytes(t *testing.T) {
	t.Parallel()
	layout := newDataLayout(FirstSliceTriple, FirstSliceDataLayout, 64, 64, 8, true)
	manifest := newToolchainManifest(layout)
	emission, err := newFirstSliceEmission(
		bingo.FirstSliceMIRArtifact{ContentHash: strings.Repeat("a", 64)},
		manifest,
		[]byte("verified llvm"),
		[]byte("elf object"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emission.CanonicalBytes(); err != nil {
		t.Fatal(err)
	}
	emission.Object[0] ^= 0xff
	if err := VerifyFirstSliceEmission(emission); err == nil || !strings.Contains(err.Error(), "object bytes") {
		t.Fatalf("tampered object error = %v", err)
	}
}

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

func TestFirstSliceManifestStrictRoundTrip(t *testing.T) {
	t.Parallel()
	layout := newDataLayout(FirstSliceTriple, FirstSliceDataLayout, 64, 64, 8, true)
	manifest := newToolchainManifest(layout)
	data, err := manifest.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeToolchainManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != manifest.ContentHash || decoded.DataLayout.ContentHash != layout.ContentHash {
		t.Fatalf("decoded manifest = %#v", decoded)
	}
	unknown := strings.Replace(string(data), `"contentHash":`, `"unknown":true,"contentHash":`, 1)
	if _, err := DecodeToolchainManifest([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field error = %v", err)
	}
	layoutData, err := layout.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeDataLayout(layoutData); err != nil {
		t.Fatal(err)
	}
}

func TestFirstSliceConstantsAreLocked(t *testing.T) {
	t.Parallel()
	if FirstSliceTriple != "x86_64-unknown-linux-gnu" || FirstSliceCPU != "generic" || LockedLLVMMajor != 20 || LockedLLVMVersion != "20.1.8" {
		t.Fatalf("unexpected first-slice target constants")
	}
}
