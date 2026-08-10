package firstslicelink

import (
	"bytes"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/targetcontext"
)

func TestResponseFileIsStableAndPathFree(t *testing.T) {
	first := responseFileBytes()
	second := responseFileBytes()
	if !bytes.Equal(first, second) {
		t.Fatal("response file changed between builds")
	}
	text := string(first)
	if strings.Contains(text, "ts2bin-first-slice-link-") || strings.Contains(text, ":\\") || strings.Contains(text, "/tmp/") {
		t.Fatalf("response file contains a temporary path: %q", text)
	}
	for _, required := range []string{"-fuse-ld=lld", "-Wl,--build-id=none", "-Wl,--no-undefined", "libbingo_runtime.a"} {
		if !strings.Contains(text, required) {
			t.Fatalf("response file missing %q: %s", required, text)
		}
	}
}

func TestLinkArtifactRejectsTamperedBytes(t *testing.T) {
	artifact, err := newLinkArtifact(
		LinkRequest{
			Emission: llvmbackend.FirstSliceEmission{
				ContentHash: strings.Repeat("a", 64),
			},
			Runtime: targetcontext.RuntimeManifest{
				ContentHash: strings.Repeat("b", 64),
				Target:      targetcontext.RuntimeTarget{Triple: llvmbackend.FirstSliceTriple},
			},
		},
		"Ubuntu clang version 20.1.8",
		"LLD 20.1.8",
		[]byte("response"),
		[]byte("map\nlibbingo_runtime.a\n"),
		[]byte("ELF"),
	)
	if err != nil {
		t.Fatal(err)
	}
	artifact.Executable[0] ^= 0xff
	if err := VerifyLinkArtifact(artifact); err == nil || !strings.Contains(err.Error(), "executable bytes") {
		t.Fatalf("tampered executable error = %v", err)
	}
}

func TestValidateBitsRejectsAmbiguousInputs(t *testing.T) {
	for _, value := range []string{"", "1", "0000000000000000\n", "00000000000000xz"} {
		if err := validateBits(value); err == nil {
			t.Fatalf("validateBits accepted %q", value)
		}
	}
	if err := validateBits("8000000000000000"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyLinkMapAllowsMultipleMembersFromOneArchive(t *testing.T) {
	response := []byte("libbingo_runtime.a\n")
	linkMap := []byte(strings.Join([]string{
		"libbingo_runtime.a(runtime.one.o) .text",
		"libbingo_runtime.a(runtime.two.o) bingo_rt_abi_version_v1",
	}, "\n"))
	if err := verifyLinkMap(response, linkMap); err != nil {
		t.Fatal(err)
	}
	if err := verifyLinkMap([]byte("libbingo_runtime.a\nlibbingo_runtime.a\n"), linkMap); err == nil {
		t.Fatal("duplicate runtime response input was accepted")
	}
}
