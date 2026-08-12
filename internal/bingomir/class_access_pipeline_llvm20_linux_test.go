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

func TestExecuteClassAccessRunsCanonicalSourceToObject(t *testing.T) {
	caseDirectory := filepath.Join("..", "..", "testdata", "ts2bin", "classaccess")
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
		Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR}, LLVMMajor: llvmbackend.LockedLLVMMajor,
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
	result, err := ExecuteClassAccess(frontend.Program, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replay.HIR.ContentHash != result.MIR.HIRHash || result.MIR.ContentHash != result.Layout.MIRHash || result.Layout.ContentHash != result.BackendPlan.LayoutHash || result.BoundMIR.LayoutHash != result.Layout.ContentHash || result.BoundMIR.TargetContextHash != result.Resolution.Context.ContentHash || result.MIR.Target.TargetContextHash != result.Resolution.Context.ContentHash || result.Emission.MIRContentHash != result.BoundMIR.ContentHash || len(result.Emission.Object) == 0 {
		t.Fatalf("OBJ-003b access lowering provenance did not form one chain: %#v", result)
	}
}
