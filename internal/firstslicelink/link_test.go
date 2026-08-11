package firstslicelink

import (
	"bytes"
	"path/filepath"
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

func TestChooseResponseFileSelectsChooseHarness(t *testing.T) {
	text := string(responseFileBytes("choose"))
	if !strings.Contains(text, "bingo_choose_harness.o") || strings.Contains(text, "bingo_add_harness.o") {
		t.Fatalf("choose response file selected wrong harness: %s", text)
	}
}

func TestClassifyResponseFileSelectsClassifyHarness(t *testing.T) {
	text := string(responseFileBytes("classify"))
	if !strings.Contains(text, "bingo_classify_harness.o") || strings.Contains(text, "bingo_add_harness.o") {
		t.Fatalf("classify response file selected wrong harness: %s", text)
	}
}

func TestComputeResponseFileSelectsComputeHarness(t *testing.T) {
	text := string(responseFileBytes("compute"))
	if !strings.Contains(text, "bingo_compute_harness.o") || strings.Contains(text, "bingo_add_harness.o") || strings.Contains(text, "bingo_choose_harness.o") {
		t.Fatalf("compute response file selected wrong harness: %s", text)
	}
}

func TestApplicationResponseFileSelectsStartupObject(t *testing.T) {
	text := string(responseFileBytes("main"))
	if !strings.Contains(text, "bingo_application_startup.o") || strings.Contains(text, "bingo_add_harness.o") {
		t.Fatalf("application response file selected wrong startup: %s", text)
	}
}

func TestApplicationLinkRequestRequiresProgramABISymbol(t *testing.T) {
	request := LinkRequest{
		EntryPoint: "main",
		Emission:   llvmbackend.FirstSliceEmission{LLVMIR: []byte("define double @main() { ret double 0.0 }")},
		Runtime: targetcontext.RuntimeManifest{Target: targetcontext.RuntimeTarget{
			Triple: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, ObjectFormat: "elf",
		}},
		RuntimeDirectory: t.TempDir(), OutputPath: filepath.Join(t.TempDir(), "app"), Clang: "clang-20", LLD: "ld.lld-20",
	}
	if err := validateRequest(request); err == nil || !strings.Contains(err.Error(), "does not contain entry point") {
		t.Fatalf("application request with C main symbol error = %v", err)
	}
	request.Emission.LLVMIR = []byte("define double @bingo_program_main_v1() { ret double 0.0 }")
	if err := validateRequest(request); err != nil {
		t.Fatalf("application ABI symbol rejected: %v", err)
	}
}

func TestCoalesceResponseFileSelectsCoalesceHarness(t *testing.T) {
	text := string(responseFileBytes("coalesce"))
	if !strings.Contains(text, "bingo_coalesce_harness.o") || strings.Contains(text, "bingo_add_harness.o") || strings.Contains(text, "bingo_choose_harness.o") {
		t.Fatalf("coalesce response file selected wrong harness: %s", text)
	}
}

func TestCoalesceAssignResponseFileSelectsCoalesceAssignHarness(t *testing.T) {
	text := string(responseFileBytes("coalesceAssign"))
	if !strings.Contains(text, "bingo_coalesce_assign_harness.o") || strings.Contains(text, "bingo_coalesce_harness.o") {
		t.Fatalf("coalesce assignment response file selected wrong harness: %s", text)
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
