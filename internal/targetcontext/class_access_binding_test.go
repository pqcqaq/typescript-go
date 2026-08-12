package targetcontext

import (
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func TestLowerClassAccessMIRBindsExactTargetContext(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/classaccess/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, frontend.Program.Provenance.TypeScriptGoCommit)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ast2bingo.ReplayClassAccessSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolveTargetContext(validBuildPlan(), validToolchainManifest(), runtimeManifestFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	context := resolution.Context
	context.FrontendHash = replay.FrontendSnapshotHash
	context.ContentHash, err = targetContextContentHash(context)
	if err != nil {
		t.Fatal(err)
	}
	module, err := LowerClassAccessMIR(replay.HIR, context)
	if err != nil {
		t.Fatal(err)
	}
	if module.Target.TargetContextHash != context.ContentHash || module.Target.Triple != context.Triple || module.Target.DataLayoutHash != context.DataLayoutHash {
		t.Fatalf("access MIR target = %#v, context = %#v", module.Target, context)
	}
	layout, err := PlanClassAccessLayout(module, context)
	if err != nil {
		t.Fatal(err)
	}
	if layout.MIRHash != module.ContentHash || layout.Base.TypeKey != replay.Contract.Classes[0].InstanceTypeKey || layout.Derived.TypeKey != replay.Contract.Classes[1].InstanceTypeKey {
		t.Fatalf("access layout is not joined to MIR/classes: %#v", layout)
	}
	bound, err := BindClassAccessMIR(layout, context, resolution.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if bound.LayoutHash != layout.ContentHash || bound.TargetContextHash != context.ContentHash || len(bound.Closure.Bindings) != len(bingo.ClassAccessLogicalCapabilities()) || bound.GCSafety.Slots[0].TraceLayoutHash != layout.Derived.ContentHash {
		t.Fatalf("access bound MIR is not closed over layout/context/capabilities: %#v", bound)
	}
	tamperedMIR := module
	tamperedMIR.Target.DataLayoutHash = replay.Contract.ContentHash
	_, tamperedHash, err := bingo.CanonicalClassAccessMIR(tamperedMIR)
	if err != nil {
		t.Fatal(err)
	}
	tamperedMIR.ContentHash = tamperedHash
	if _, err := PlanClassAccessLayout(tamperedMIR, context); err == nil {
		t.Fatal("substituted MIR target was accepted by layout join")
	}

	wrong := context
	wrong.FrontendHash = validBuildPlan().FrontendHash
	wrong.ContentHash, err = targetContextContentHash(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LowerClassAccessMIR(replay.HIR, wrong); err == nil {
		t.Fatal("access HIR bound to a substituted frontend context")
	}
}
