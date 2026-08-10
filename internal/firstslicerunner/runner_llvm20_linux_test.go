//go:build llvm20 && linux

package firstslicerunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/irartifact"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestLLVM20StaticCoreRunsManifestCase(t *testing.T) {
	caseDirectory := filepath.Join("..", "..", "testdata", "ts2bin", "lowering")
	caseData, err := irartifact.LoadCase(caseDirectory, true)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(caseData.Frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(machine.Close)
	runtimeDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "target", "first-slice"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeArchive, err := filepath.Abs(filepath.Join(runtimeDirectory, "cargo", "x86_64-unknown-linux-gnu", "release", "libbingo_runtime.a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runtimeArchive); err != nil {
		t.Fatalf("runtime archive is not built: %v", err)
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	lld, err := exec.LookPath("ld.lld-20")
	if err != nil {
		lld, err = exec.LookPath("ld.lld")
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := RunCase(context.Background(), caseDirectory, identity, machine, Options{
		RuntimeDirectory: runtimeDirectory, RuntimeArchivePath: runtimeArchive,
		OutputDirectory: t.TempDir(), Clang: clang, LLD: lld,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Executions) != 2 || report.Executions[0].Name != "negative-zero-plus-negative-zero" || report.Executions[1].Name != "one-plus-two" {
		t.Fatalf("unexpected static-core report: %#v", report)
	}
	if _, err := report.CanonicalBytes(); err != nil {
		t.Fatal(err)
	}
}
