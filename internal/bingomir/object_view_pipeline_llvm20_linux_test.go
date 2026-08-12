//go:build llvm20 && cgo && linux

package bingomir

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/firstsliceoracle"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestExecuteObjectViewRunsSnapshotToNativeNodeDifferential(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/objectview/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	result, err := ExecuteObjectView(frontend.Program, identity, machine)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Emission.Object) < 4 || result.Emission.Object[0] != 0x7f || string(result.Emission.Object[1:4]) != "ELF" {
		t.Fatal("ObjectView source pipeline did not emit ELF")
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := firstsliceoracle.OpenNode(t.Context(), nodePath)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	objectPath := filepath.Join(workspace, "object-view.o")
	executable := filepath.Join(workspace, "object-view")
	if err := os.WriteFile(objectPath, result.Emission.Object, 0o600); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Clean(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "harness", "object_view_bits.c"))
	command := exec.Command(clang, "--target="+llvmbackend.FirstSliceTriple, "-fuse-ld=lld", "-no-pie", "-Wl,--build-id=none", "-Wl,--no-undefined", objectPath, harness, "-o", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("link ObjectView source pipeline: %v: %s", err, output)
	}
	offset := strconv.FormatUint(uint64(result.Plan.SourceOffset), 10)
	for _, input := range []string{"0000000000000000", "8000000000000000", "3ff0000000000000", "7ff0000000000000", "7ff8000000000042"} {
		native, err := exec.Command(executable, offset, input).CombinedOutput()
		if err != nil {
			t.Fatalf("ObjectView(%s): %v: %s", input, err, native)
		}
		node, err := oracle.ObjectView(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if nativeBits, nodeBits := strings.TrimSpace(string(native)), strings.TrimSpace(string(node.Output)); nativeBits != input || nodeBits != input || nativeBits != nodeBits {
			t.Fatalf("ObjectView(%s): native = %s, Node = %s", input, nativeBits, nodeBits)
		}
	}
}
