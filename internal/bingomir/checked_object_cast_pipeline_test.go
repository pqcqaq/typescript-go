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

func checkedObjectCastReplayFixture(t *testing.T) (frontendwire.ProgramSnapshot, bingo.CompilerBuildIdentity, []byte) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/checkedobjectcast/frontend-snapshot.json")
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
	replay, err := ast2bingo.ReplayCheckedObjectCastSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program, identity, encoded
}

func TestLowerCheckedObjectCastReplayJoinsCanonicalArtifact(t *testing.T) {
	snapshot, identity, encoded := checkedObjectCastReplayFixture(t)
	plan := buildplan.Plan{FrontendHash: snapshot.ContentHash, Profile: frontendwire.ProfileInterop}
	_, snapshotErr := LowerCheckedObjectCast(snapshot, identity, plan, nil, nil)
	_, artifactErr := LowerCheckedObjectCastReplay(append(encoded, '\n'), identity, plan, nil, nil)
	if snapshotErr == nil || artifactErr == nil || snapshotErr.Error() != artifactErr.Error() || !strings.Contains(artifactErr.Error(), "target machine is nil") {
		t.Fatalf("snapshot/artifact lowering diverged: snapshot=%v artifact=%v", snapshotErr, artifactErr)
	}
}

func TestLowerCheckedObjectCastReplayRejectsArtifactSubstitution(t *testing.T) {
	snapshot, identity, encoded := checkedObjectCastReplayFixture(t)
	plan := buildplan.Plan{FrontendHash: snapshot.ContentHash, Profile: frontendwire.ProfileInterop}

	tampered := bytes.Replace(encoded, []byte(`"targetPropertyKey":"value"`), []byte(`"targetPropertyKey":"other"`), 1)
	if _, err := LowerCheckedObjectCastReplay(tampered, identity, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "replay artifact") {
		t.Fatalf("tampered replay error = %v", err)
	}

	otherIdentity, err := ast2bingo.NewCompilerBuildIdentity(snapshot.Provenance.TypeScriptGoCommit, strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerCheckedObjectCastReplay(encoded, otherIdentity, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "current compiler identity") {
		t.Fatalf("substituted compiler identity error = %v", err)
	}

	plan.FrontendHash = strings.Repeat("c", 64)
	if _, err := LowerCheckedObjectCastReplay(encoded, identity, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "BuildPlan does not bind") {
		t.Fatalf("substituted BuildPlan error = %v", err)
	}

	plan.FrontendHash = snapshot.ContentHash
	plan.Profile = frontendwire.ProfileStatic
	if _, err := LowerCheckedObjectCastReplay(encoded, identity, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "requires the interop profile") {
		t.Fatalf("static artifact plan error = %v", err)
	}
	if _, err := ExecuteCheckedObjectCastReplay(encoded, identity, plan, nil, nil); err == nil {
		t.Fatal("artifact execution accepted static profile")
	}
}

func TestLowerCheckedObjectCastRejectsUnboundProductionInputs(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/checkedobjectcast/frontend-snapshot.json")
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
	plan := buildplan.Plan{FrontendHash: frontend.Program.ContentHash, Profile: frontendwire.ProfileInterop}
	_, err = LowerCheckedObjectCast(frontend.Program, identity, plan, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "target machine is nil") {
		t.Fatalf("nil checked object cast target error = %v", err)
	}
	plan.FrontendHash = strings.Repeat("b", 64)
	_, err = LowerCheckedObjectCast(frontend.Program, identity, plan, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "BuildPlan does not bind frontend snapshot") {
		t.Fatalf("substituted checked object cast frontend error = %v", err)
	}
	if _, err := ExecuteCheckedObjectCast(frontend.Program, identity, plan, nil, nil); err == nil {
		t.Fatal("checked object cast execution accepted unbound production inputs")
	}
	plan.FrontendHash = frontend.Program.ContentHash
	plan.Profile = frontendwire.ProfileStatic
	_, err = LowerCheckedObjectCast(frontend.Program, identity, plan, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "requires the interop profile") {
		t.Fatalf("checked object cast accepted static profile: %v", err)
	}
}

func TestLowerCheckedObjectCastRejectsLockedManifestCapabilityGap(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/checkedobjectcast/frontend-snapshot.json")
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
	plan, err := buildplan.New(frontend.Program.ContentHash, frontendwire.ProfileInterop, buildplan.BackendRequest{
		Target: llvmbackend.FirstSliceTriple, CPU: llvmbackend.FirstSliceCPU, Runtime: "core-es2020", GC: frontendwire.GCTracing,
		Exceptions: frontendwire.ExceptionsNone, Overflow: frontendwire.OverflowJSNumber, BoundsCheck: frontendwire.BoundsCheckOn,
		Emit: []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR}, LLVMMajor: llvmbackend.LockedLLVMMajor,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeManifest, err := os.ReadFile("../targetcontext/testdata/runtime-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = LowerCheckedObjectCast(frontend.Program, identity, plan, nil, runtimeManifest)
	if err == nil || !strings.Contains(err.Error(), "target machine is nil") {
		t.Fatalf("checked object cast interop plan accepted nil target: %v", err)
	}
}
