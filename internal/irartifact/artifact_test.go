package irartifact

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func TestCanonicalHIRRoundTripAndStrictVerification(t *testing.T) {
	module := testHIR(t)
	data, err := CanonicalHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeHIR(data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalHIR(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, second) {
		t.Fatal("HIR canonical bytes changed after strict decode")
	}
	tamperedHash := decoded
	tamperedHash.ContentHash = strings.Repeat("0", 64)
	if _, err := CanonicalHIR(tamperedHash); err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("canonical encoder repaired a tampered HIR hash: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	tampered, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeHIR(tampered); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown HIR member was accepted: %v", err)
	}
}

func TestSchemaAwareHIRDiffSeparatesProvenanceAndStructure(t *testing.T) {
	left := testHIR(t)
	right := left
	right.Functions = append([]bingo.HIRFunction(nil), left.Functions...)
	right.Functions[0].Name = "sum"
	right = rehashHIR(t, right)
	leftBytes, err := CanonicalHIR(left)
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := CanonicalHIR(right)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Diff(KindHIR, leftBytes, rightBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !report.SchemaEqual || !report.ProvenanceEqual || !report.Compatible || report.Equal {
		t.Fatalf("structural diff compatibility flags = %#v", report)
	}
	if !containsDifferencePath(report.Differences, "$.functions[0].name") {
		t.Fatalf("structural differences = %#v", report.Differences)
	}

	right = left
	right.Provenance.SourceContentHash = strings.Repeat("b", 64)
	right = rehashHIR(t, right)
	rightBytes, err = CanonicalHIR(right)
	if err != nil {
		t.Fatal(err)
	}
	report, err = Diff(KindHIR, leftBytes, rightBytes)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProvenanceEqual || report.Compatible || report.Equal {
		t.Fatalf("provenance diff compatibility flags = %#v", report)
	}
	if len(report.Differences) == 0 || !strings.HasPrefix(report.Differences[0].Path, "$.provenance") {
		t.Fatalf("provenance difference was not reported first: %#v", report.Differences)
	}
}

func containsDifferencePath(differences []Difference, path string) bool {
	for _, difference := range differences {
		if difference.Path == path {
			return true
		}
	}
	return false
}

func TestRenderHIRTextIsDeterministic(t *testing.T) {
	module := testHIR(t)
	first, err := RenderHIRText(module)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderHIRText(module)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.Contains(first, "function 1 \"add\"") || !strings.Contains(first, "value 3 binary number") {
		t.Fatalf("unexpected HIR text output:\n%s", first)
	}
}

func testHIR(t *testing.T) bingo.HIRModule {
	t.Helper()
	requirementsDigest, err := bingo.LogicalCapabilityRequirementsDigest([]bingo.RuntimeCapabilityID{})
	if err != nil {
		t.Fatal(err)
	}
	module := bingo.HIRModule{
		SchemaVersion: bingo.HIRSchemaVersion,
		Provenance: bingo.HIRProvenance{
			FrontendSnapshotSchemaVersion:       bingo.HIRFrontendSnapshotSchemaVersion,
			FrontendSnapshotHash:                strings.Repeat("1", 64),
			SourceContentHash:                   strings.Repeat("2", 64),
			CompilerBuildIdentity:               bingo.CompilerBuildIdentity{UpstreamCommit: strings.Repeat("3", 40), ForkCommit: strings.Repeat("4", 40), LoweringSchema: "bingo-hir-lowering-v6", LoweringHash: strings.Repeat("5", 64)},
			StandardLibraryHash:                 strings.Repeat("6", 64),
			KindManifestHash:                    strings.Repeat("7", 64),
			LogicalCapabilityRequirementsDigest: requirementsDigest,
		},
		LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{},
		Functions: []bingo.HIRFunction{{
			ID: 1, Name: "add", ReturnType: bingo.TypeNumber,
			Origin: bingo.Origin{File: "main.ts", Start: 0, End: 10},
			Parameters: []bingo.HIRParameter{
				{Name: "left", Value: 1, Type: bingo.TypeNumber, Origin: bingo.Origin{File: "main.ts", Start: 4, End: 8}},
				{Name: "right", Value: 2, Type: bingo.TypeNumber, Origin: bingo.Origin{File: "main.ts", Start: 9, End: 14}},
			},
			Blocks: []bingo.HIRBlock{{
				ID: 1,
				Operations: []bingo.HIROp{{
					ID: 3, Kind: "binary", Type: bingo.TypeNumber, Operands: []bingo.ValueID{1, 2}, Operator: "+", Effect: bingo.EffectPure,
					LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: bingo.Origin{File: "main.ts", Start: 20, End: 29},
				}},
				Terminator: bingo.HIRTerminator{Kind: "return", Value: 3, Origin: bingo.Origin{File: "main.ts", Start: 30, End: 39}},
			}},
		}},
	}
	_, hash, err := bingo.CanonicalHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	return module
}

func rehashHIR(t *testing.T, module bingo.HIRModule) bingo.HIRModule {
	t.Helper()
	_, hash, err := bingo.CanonicalHIR(module)
	if err != nil {
		t.Fatal(err)
	}
	module.ContentHash = hash
	return module
}
