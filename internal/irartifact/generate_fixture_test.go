package irartifact

import (
	"bytes"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func TestFirstSliceCaseBuildPlanMatchesSnapshot(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/lowering/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildplan.New(frontend.ContentHash, frontendwire.ProfileStatic, buildplan.BackendRequest{
		Target:      "x86_64-unknown-linux-gnu",
		CPU:         "generic",
		Features:    []string{},
		Runtime:     "core-es2020",
		GC:          frontendwire.GCTracing,
		Exceptions:  frontendwire.ExceptionsNone,
		Overflow:    frontendwire.OverflowJSNumber,
		BoundsCheck: frontendwire.BoundsCheckOn,
		Emit:        []frontendwire.EmitArtifact{frontendwire.EmitHIR, frontendwire.EmitMIR},
		LLVMMajor:   20,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := plan.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../testdata/ts2bin/lowering/build-plan.json")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := buildplan.Decode(committed)
	if err != nil {
		t.Fatal(err)
	}
	committedCanonical, err := decoded.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, committedCanonical) {
		t.Fatalf("committed build plan is stale:\nwant:\n%s\ngot:\n%s", encoded, committedCanonical)
	}
	if _, err := LoadCase("../../testdata/ts2bin/lowering", true); err != nil {
		t.Fatalf("load committed first-slice case: %v", err)
	}
}
