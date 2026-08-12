//go:build llvm20 && cgo && linux

package bingomir

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestExecuteObjectLayoutCopySnapshotAndReplayFormOneProductionChain(t *testing.T) {
	snapshot, identity, replayBytes, plan := objectLayoutCopyPipelineFixture(t)
	runtimeManifest, err := os.ReadFile(filepath.Join("..", "targetcontext", "testdata", "runtime-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := llvmbackend.OpenFirstSliceTargetMachine()
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()

	fromSnapshot, err := ExecuteObjectLayoutCopy(snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	fromReplay, err := ExecuteObjectLayoutCopyReplay(replayBytes, identity, plan, machine, runtimeManifest)
	if err != nil {
		t.Fatal(err)
	}
	assertObjectLayoutCopyProductionChain(t, fromSnapshot)
	assertObjectLayoutCopyProductionChain(t, fromReplay)
	if fromSnapshot.Replay.ContentHash != fromReplay.Replay.ContentHash || fromSnapshot.BackendPlan.ContentHash != fromReplay.BackendPlan.ContentHash || !bytes.Equal(fromSnapshot.Emission.Object, fromReplay.Emission.Object) {
		t.Fatal("snapshot and replay object-layout-copy production paths diverged")
	}

	decoded, err := ast2bingo.DecodeObjectLayoutCopyReplay(replayBytes)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != fromReplay.Replay.ContentHash {
		t.Fatal("production replay identity differs from strict artifact decoder")
	}
}

func assertObjectLayoutCopyProductionChain(t testing.TB, result ObjectLayoutCopyPipelineResult) {
	t.Helper()
	if result.Replay.FrontendSnapshotHash != result.Resolution.Context.FrontendHash ||
		result.Replay.MIR.ContentHash != result.Bound.MIRHash ||
		result.Bound.MIR.ContentHash != result.Replay.MIR.ContentHash ||
		result.Bound.TargetContextHash != result.Resolution.Context.ContentHash ||
		result.Bound.CatalogHash != result.Resolution.Catalog.ContentHash ||
		result.BackendPlan.BoundHash != result.Bound.ContentHash ||
		result.Emission.MIRContentHash != result.BackendPlan.ContentHash {
		t.Fatalf("object-layout-copy production provenance is not one chain: %#v", result)
	}
	want := bingo.ObjectLayoutCopyCapabilityRequirements()
	if len(result.Bound.Bindings) != len(want) {
		t.Fatalf("bound capability count=%d want=%d", len(result.Bound.Bindings), len(want))
	}
	for index, logical := range want {
		if result.Bound.Bindings[index].LogicalName != logical || result.Bound.Bindings[index].SymbolName == "" || result.Bound.Bindings[index].SignatureHash == "" {
			t.Fatalf("invalid capability binding %d: %#v", index, result.Bound.Bindings[index])
		}
	}
	if len(result.Emission.Object) < 4 || result.Emission.Object[0] != 0x7f || string(result.Emission.Object[1:4]) != "ELF" {
		t.Fatal("object-layout-copy production pipeline did not emit ELF")
	}
}
