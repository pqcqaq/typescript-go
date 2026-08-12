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

func propertyAccessPipelineFixture(t testing.TB) (frontendwire.ProgramSnapshot, bingo.CompilerBuildIdentity, []byte, buildplan.Plan) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/propertyaccessadmission/frontend-snapshot.json")
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
	replay, err := ast2bingo.ReplayPropertyAccessAdmissionSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileInterop, buildplan.BackendRequest{Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Features: []string{}, Runtime: "core-es2020", GC: frontendwire.GCTracing, Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber, BoundsCheck: frontendwire.BoundsCheckOn, Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR}, LLVMMajor: llvmbackend.LockedLLVMMajor})
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program, identity, encoded, plan
}

func TestLowerPropertyAccessReplayRejectsProductionSubstitution(t *testing.T) {
	snapshot, identity, encoded, plan := propertyAccessPipelineFixture(t)
	if _, err := LowerPropertyAccess(snapshot, identity, plan, nil); err == nil || !strings.Contains(err.Error(), "target machine is nil") {
		t.Fatalf("snapshot error = %v", err)
	}
	if _, err := LowerPropertyAccessReplay(encoded, identity, plan, nil); err == nil || !strings.Contains(err.Error(), "target machine is nil") {
		t.Fatalf("replay error = %v", err)
	}
	tampered := bytes.Replace(encoded, []byte(`"functionName":"direct"`), []byte(`"functionName":"other"`), 1)
	if _, err := LowerPropertyAccessReplay(tampered, identity, plan, nil); err == nil || !strings.Contains(err.Error(), "replay artifact") {
		t.Fatalf("tamper error = %v", err)
	}
	staticPlan, err := buildplan.New(snapshot.ContentHash, frontendwire.ProfileStatic, plan.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerPropertyAccessReplay(encoded, identity, staticPlan, nil); err == nil || !strings.Contains(err.Error(), "interop profile") {
		t.Fatalf("profile error = %v", err)
	}
	other, err := ast2bingo.NewCompilerBuildIdentity(snapshot.Provenance.TypeScriptGoCommit, strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerPropertyAccessReplay(encoded, other, plan, nil); err == nil || !strings.Contains(err.Error(), "current compiler identity") {
		t.Fatalf("identity error = %v", err)
	}
}

func TestResolveAndBindPropertyAccessRejectsStaticProfile(t *testing.T) {
	snapshot, _, _, plan := propertyAccessPipelineFixture(t)
	plan.Profile = frontendwire.ProfileStatic
	plan, err := buildplan.New(snapshot.ContentHash, plan.Profile, plan.Backend)
	if err != nil {
		t.Fatal(err)
	}
	lowered := PropertyAccessLoweringResult{Replay: ast2bingo.PropertyAccessAdmissionReplay{FrontendSnapshotHash: snapshot.ContentHash}}
	if _, err := ResolveAndBindPropertyAccess(lowered, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "requires the interop profile") {
		t.Fatalf("static profile error = %v", err)
	}
}
