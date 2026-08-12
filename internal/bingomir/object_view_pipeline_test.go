package bingomir

import (
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func TestLowerObjectViewUsesCanonicalFrontendReplay(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/objectview/frontend-snapshot.json")
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
	result, err := LowerObjectView(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.MIRHash != result.Replay.MIR.ContentHash || result.Plan.SourceOffset != result.Replay.View.Mappings[0].SourceFieldOffset || result.Plan.Allocates || len(result.Plan.RuntimeCalls) != 0 {
		t.Fatalf("ObjectView pipeline lost source proof: %#v", result.Plan)
	}
	if _, err := ExecuteObjectView(frontend.Program, identity, nil); err == nil {
		t.Fatal("ObjectView pipeline accepted nil TargetMachine")
	}
}
