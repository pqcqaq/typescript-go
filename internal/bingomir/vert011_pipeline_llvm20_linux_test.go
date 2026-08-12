//go:build llvm20 && cgo && linux

package bingomir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestExecuteVERT011RunsCanonicalSourceToObjectPipeline(t *testing.T) {
	caseDirectory := filepath.Join("..", "..", "testdata", "ts2bin", "propertynullishassign")
	snapshotBytes, err := os.ReadFile(filepath.Join(caseDirectory, "frontend-snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileStatic, buildplan.BackendRequest{
		Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Features: []string{}, Runtime: "core-es2020",
		GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber, BoundsCheck: frontendwire.BoundsCheckOn,
		Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR, frontendwire.EmitLLVM, frontendwire.EmitObject}, LLVMMajor: llvmbackend.LockedLLVMMajor,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeManifest, err := os.ReadFile(filepath.Join("..", "targetcontext", "testdata", "runtime-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	result, err := ExecuteVERT011(frontend.Program, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replay.HIR.ContentHash != result.MIR.HIRHash || result.BoundMIR.MIR.ContentHash != result.MIR.ContentHash || result.Emission.MIRContentHash != result.BoundMIR.ContentHash {
		t.Fatalf("VERT-011 pipeline provenance did not form one chain")
	}
	if len(result.Emission.Object) < 4 || result.Emission.Object[0] != 0x7f || string(result.Emission.Object[1:4]) != "ELF" {
		t.Fatal("VERT-011 pipeline did not emit an ELF object")
	}
}
