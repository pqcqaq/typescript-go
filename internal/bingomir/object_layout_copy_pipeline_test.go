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

func objectLayoutCopyPipelineFixture(t testing.TB) (frontendwire.ProgramSnapshot, bingo.CompilerBuildIdentity, []byte, buildplan.Plan) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/objectlayoutcopy/frontend-snapshot.json")
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
	replay, err := ast2bingo.ReplayObjectLayoutCopySnapshot(frontend.Program, identity)
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

func TestLowerObjectLayoutCopyReplayRejectsPipelineSubstitution(t *testing.T) {
	snapshot, identity, encoded, plan := objectLayoutCopyPipelineFixture(t)
	if _, err := LowerObjectLayoutCopy(snapshot, identity, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "target machine is nil") {
		t.Fatalf("nil machine error=%v", err)
	}
	if _, err := LowerObjectLayoutCopyReplay(append(encoded, '\n'), identity, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "target machine is nil") {
		t.Fatalf("artifact nil machine error=%v", err)
	}
	tampered := bytes.Replace(encoded, []byte(`"sourceLiteralNodeId":"`), []byte(`"sourceLiteralNodeId":"tampered`), 1)
	if _, err := LowerObjectLayoutCopyReplay(tampered, identity, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "replay artifact") {
		t.Fatalf("tampered error=%v", err)
	}
	other, err := ast2bingo.NewCompilerBuildIdentity(snapshot.Provenance.TypeScriptGoCommit, strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerObjectLayoutCopyReplay(encoded, other, plan, nil, nil); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("identity error=%v", err)
	}
	badPlan := plan
	badPlan.FrontendHash = strings.Repeat("c", 64)
	if _, err := LowerObjectLayoutCopyReplay(encoded, identity, badPlan, nil, nil); err == nil || !strings.Contains(err.Error(), "BuildPlan") {
		t.Fatalf("plan error=%v", err)
	}
	interop, err := buildplan.New(snapshot.ContentHash, frontendwire.ProfileInterop, plan.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerObjectLayoutCopyReplay(encoded, identity, interop, nil, nil); err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("profile error=%v", err)
	}
}
