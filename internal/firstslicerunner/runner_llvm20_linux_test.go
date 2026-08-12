//go:build llvm20 && linux

package firstslicerunner

import (
	"bytes"
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
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		directory   string
		entryPoint  string
		executions  int
		rejectsByte bool
	}{
		{name: "add", directory: "lowering", entryPoint: "add", executions: 3},
		{name: "choose", directory: "choose", entryPoint: "choose", executions: 2, rejectsByte: true},
		{name: "classify", directory: "classify", entryPoint: "classify", executions: 5},
		{name: "calllocal", directory: "calllocal", entryPoint: "compute", executions: 3},
		{name: "loop", directory: "loop", entryPoint: "compute", executions: 4},
		{name: "coalesce", directory: "coalesce", entryPoint: "coalesce", executions: 5, rejectsByte: true},
		{name: "coalesceassign", directory: "coalesceassign", entryPoint: "coalesceAssign", executions: 3, rejectsByte: true},
		{name: "stringlength", directory: "stringlength", entryPoint: "stringLength", executions: 5, rejectsByte: true},
		{name: "objectalias", directory: "objectalias", entryPoint: "objectAlias", executions: 5},
		{name: "propertynullishassign", directory: "propertynullishassign", entryPoint: "propertyNullishAssign", executions: 7, rejectsByte: true},
		{name: "closurecounter", directory: "closurecounter", entryPoint: "closureCounter", executions: 5},
		{name: "classcounter", directory: "classcounter", entryPoint: "classCounter", executions: 5},
		{name: "derivedcounter", directory: "derivedcounter", entryPoint: "derivedCounter", executions: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			caseDirectory := filepath.Join("..", "..", "testdata", "ts2bin", test.directory)
			caseData, err := irartifact.LoadCase(caseDirectory, true)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := ast2bingo.NewCompilerBuildIdentity(caseData.Frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
			if err != nil {
				t.Fatal(err)
			}
			report, err := RunCase(context.Background(), caseDirectory, identity, machine, Options{
				RuntimeDirectory: runtimeDirectory, RuntimeArchivePath: runtimeArchive,
				OutputDirectory: t.TempDir(), Clang: clang, LLD: lld, Node: node,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !report.OK || report.EntryPoint != test.entryPoint || len(report.Executions) != test.executions || report.NonCanonicalRejected != test.rejectsByte {
				t.Fatalf("unexpected static-core report: %#v", report)
			}
			if _, err := report.CanonicalBytes(); err != nil {
				t.Fatal(err)
			}
			if test.name == "objectalias" || test.name == "propertynullishassign" || test.name == "closurecounter" || test.name == "classcounter" || test.name == "derivedcounter" {
				second, err := RunCase(context.Background(), caseDirectory, identity, machine, Options{
					RuntimeDirectory: runtimeDirectory, RuntimeArchivePath: runtimeArchive,
					OutputDirectory: t.TempDir(), Clang: clang, LLD: lld, Node: node,
				})
				if err != nil {
					t.Fatal(err)
				}
				firstBytes, err := report.CanonicalBytes()
				if err != nil {
					t.Fatal(err)
				}
				secondBytes, err := second.CanonicalBytes()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(firstBytes, secondBytes) {
					t.Fatalf("%s repeated source-to-ELF report was not deterministic", test.name)
				}
			}
		})
	}
}
