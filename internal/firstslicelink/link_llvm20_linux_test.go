//go:build llvm20 && linux

package firstslicelink

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
	"github.com/microsoft/typescript-go/internal/targetcontext"
)

func TestLLVM20FirstSliceLinksAndRunsDeterministically(t *testing.T) {
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
	mir, err := irartifact.LoadMIR(context.Background(), caseDirectory, identity, machine)
	if err != nil {
		t.Fatal(err)
	}
	emission, err := machine.EmitFirstSliceObject(mir)
	if err != nil {
		t.Fatal(err)
	}
	runtimeManifest, err := targetcontext.DecodeRuntimeManifest(caseData.RuntimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "runtime", "bingo-rt", "target", "first-slice"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDirectory, runtimeManifest.Artifacts.HarnessObject.File)); err != nil {
		t.Fatalf("runtime artifacts are not built; run runtime/bingo-rt/scripts/build-first-slice.sh: %v", err)
	}
	runtimeArchive, err := filepath.Abs(filepath.Join(runtimeDirectory, "cargo", runtimeManifest.Target.Triple, "release", runtimeManifest.Artifacts.UmbrellaArchive.File))
	if err != nil {
		t.Fatal(err)
	}
	clang, err := exec.LookPath("clang-20")
	if err != nil {
		t.Fatal(err)
	}
	lld, err := firstTool("ld.lld-20", "ld.lld")
	if err != nil {
		t.Fatal(err)
	}

	artifacts := make([]LinkArtifact, 2)
	outputs := make([]string, 2)
	for index := range artifacts {
		outputs[index] = filepath.Join(t.TempDir(), "add-harness")
		artifacts[index], err = LinkFirstSlice(context.Background(), LinkRequest{
			Emission:           emission,
			Runtime:            *runtimeManifest,
			RuntimeDirectory:   runtimeDirectory,
			RuntimeArchivePath: runtimeArchive,
			OutputPath:         outputs[index],
			Clang:              clang,
			LLD:                lld,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if artifacts[0].ResponseFileHash != artifacts[1].ResponseFileHash ||
		artifacts[0].LinkMapHash != artifacts[1].LinkMapHash ||
		artifacts[0].ExecutableHash != artifacts[1].ExecutableHash ||
		artifacts[0].ContentHash != artifacts[1].ContentHash {
		t.Fatal("identical verified inputs produced different link artifacts")
	}
	result, err := RunFirstSlice(context.Background(), outputs[0], "3ff0000000000000", "4000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Output) != "4008000000000000\n" {
		t.Fatalf("1 + 2 output = %q", result.Output)
	}
}

func firstTool(names ...string) (string, error) {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}
