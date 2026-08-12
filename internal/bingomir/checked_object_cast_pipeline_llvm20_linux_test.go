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

func TestExecuteCheckedObjectCastStopsAtLockedRuntimeProfileGap(t *testing.T) {
	caseDirectory := filepath.Join("..", "..", "testdata", "ts2bin", "checkedobjectcast")
	data, err := os.ReadFile(filepath.Join(caseDirectory, "frontend-snapshot.json"))
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
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileInterop, buildplan.BackendRequest{Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Runtime: "core-es2020", GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber, BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR}, LLVMMajor: llvmbackend.LockedLLVMMajor})
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
	replay, err := ast2bingo.ReplayCheckedObjectCastSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	replayBytes, err := replay.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	for name, execute := range map[string]func() error{
		"snapshot": func() error {
			_, err := ExecuteCheckedObjectCast(frontend.Program, identity, plan, machine, runtimeManifest)
			return err
		},
		"artifact": func() error {
			_, err := ExecuteCheckedObjectCastReplay(replayBytes, identity, plan, machine, runtimeManifest)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := execute()
			if err == nil || !strings.Contains(err.Error(), "does not match runtime manifest") {
				t.Fatalf("checked object cast did not fail closed on locked runtime profile gap: %v", err)
			}
		})
	}
}
