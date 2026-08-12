package bingomir

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func functionThunkPipelineFixture(t testing.TB) (frontendwire.ProgramSnapshot, bingo.CompilerBuildIdentity, []byte, buildplan.Plan) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/functionthunk/frontend-snapshot.json")
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
	replay, err := ast2bingo.ReplayFunctionThunkSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileStatic, buildplan.BackendRequest{Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Runtime: "core-es2020", GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber, BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR}, LLVMMajor: llvmbackend.LockedLLVMMajor})
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program, identity, encoded, plan
}

func TestLowerFunctionThunkReplayRejectsProductionSubstitution(t *testing.T) {
	snapshot, identity, encoded, plan := functionThunkPipelineFixture(t)
	if _, err := LowerFunctionThunk(snapshot, identity, plan, nil); err == nil || !strings.Contains(err.Error(), "target machine is nil") {
		t.Fatalf("snapshot error = %v", err)
	}
	if _, err := LowerFunctionThunkReplay(append(encoded, '\n'), identity, plan, nil); err == nil || !strings.Contains(err.Error(), "target machine is nil") {
		t.Fatalf("artifact error = %v", err)
	}
	tampered := bytes.Replace(encoded, []byte(`"assignmentNodeId":"`), []byte(`"assignmentNodeId":"other`), 1)
	if _, err := LowerFunctionThunkReplay(tampered, identity, plan, nil); err == nil || !strings.Contains(err.Error(), "replay artifact") {
		t.Fatalf("tampered error = %v", err)
	}
	other, err := ast2bingo.NewCompilerBuildIdentity(snapshot.Provenance.TypeScriptGoCommit, strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerFunctionThunkReplay(encoded, other, plan, nil); err == nil || !strings.Contains(err.Error(), "current compiler identity") {
		t.Fatalf("identity error = %v", err)
	}
	badPlan := plan
	badPlan.FrontendHash = strings.Repeat("c", 64)
	if _, err := LowerFunctionThunkReplay(encoded, identity, badPlan, nil); err == nil || !strings.Contains(err.Error(), "BuildPlan") {
		t.Fatalf("plan error = %v", err)
	}
	interop, err := buildplan.New(snapshot.ContentHash, frontendwire.ProfileInterop, plan.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerFunctionThunkReplay(encoded, identity, interop, nil); err == nil || !strings.Contains(err.Error(), "static profile") {
		t.Fatalf("profile error = %v", err)
	}
	if _, err := ExecuteFunctionThunkReplay(encoded, identity, interop, nil); err == nil {
		t.Fatal("execution accepted interop plan")
	}
}
