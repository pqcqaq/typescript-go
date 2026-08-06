package tsfrontend

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestKindManifestIsFreshCompleteAndStable(t *testing.T) {
	t.Parallel()

	document, err := LoadKindManifest()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(document.Kinds), 351; got != want {
		t.Fatalf("manifest rows = %d, want %d", got, want)
	}
	if document.Kinds[0].Kind != ast.Kind(0).String() || document.Kinds[0].KindValue != 0 {
		t.Fatalf("first manifest row = %#v", document.Kinds[0])
	}
	last := document.Kinds[len(document.Kinds)-1]
	if last.Kind != ast.Kind(ast.KindCount-1).String() || last.KindValue != int16(ast.KindCount-1) {
		t.Fatalf("last manifest row = %#v", last)
	}

	levels := map[KindSupportLevel]int{}
	for _, entry := range document.Kinds {
		levels[entry.PlannedLevels[0]]++
		if entry.GateHandler == "" || entry.Feature == "" || entry.Capability == "" {
			t.Fatalf("incomplete manifest entry %s: %#v", entry.Kind, entry)
		}
	}
	for _, level := range []KindSupportLevel{KindSupportS0, KindSupportS1, KindSupportS2, KindSupportCompileOnly, KindSupportPlanned, KindSupportReject} {
		if levels[level] == 0 {
			t.Fatalf("manifest has no %s rows: %#v", level, levels)
		}
	}

	a, err := CanonicalKindManifestJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalKindManifestJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) || KindManifestDigest() == "" {
		t.Fatal("manifest canonical bytes or digest is unstable/empty")
	}
	entries, err := KindManifest()
	if err != nil {
		t.Fatal(err)
	}
	mutatedLevel := KindSupportS0
	if entries[0].PlannedLevels[0] == mutatedLevel {
		mutatedLevel = KindSupportReject
	}
	entries[0].PlannedLevels[0] = mutatedLevel
	fresh, err := KindManifest()
	if err != nil {
		t.Fatal(err)
	}
	if fresh[0].PlannedLevels[0] == mutatedLevel {
		t.Fatal("KindManifest returned shared mutable planned levels")
	}
	if !reflect.DeepEqual(document.Kinds[len(document.Kinds)-1], fresh[len(fresh)-1]) {
		t.Fatal("detached manifest changed unexpectedly")
	}
}

func TestKindManifestValidationRejectsSchemaOmissionAndHandlerDrift(t *testing.T) {
	t.Parallel()

	document, err := LoadKindManifest()
	if err != nil {
		t.Fatal(err)
	}
	document.SchemaVersion++
	if err := ValidateKindManifestDocument(document); err == nil {
		t.Fatal("schema drift was accepted")
	}

	document, _ = LoadKindManifest()
	document.Kinds = document.Kinds[:len(document.Kinds)-1]
	if err := ValidateKindManifestDocument(document); err == nil {
		t.Fatal("manifest omission was accepted")
	}

	document, _ = LoadKindManifest()
	document.Kinds[1].LoweringPlan = ""
	if err := ValidateKindManifestDocument(document); err == nil {
		t.Fatal("handler drift was accepted")
	}

}

// TestKindManifestGateHandlersAreBound is the FE-005 coverage mechanism: it
// walks every AST Kind row through the real subset-gate dispatch path rather
// than inventing one fixture ID per AST Kind.
func TestKindManifestGateHandlersAreBound(t *testing.T) {
	t.Parallel()

	document, err := LoadKindManifest()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateKindGateHandlerRegistry(kindGateHandlerRegistry); err != nil {
		t.Fatal(err)
	}
	used := make(map[string]struct{}, len(kindGateHandlerRegistry))
	facts := subsetFacts{
		types:      map[TypeID]TypeSnapshot{},
		symbols:    map[SymbolID]SymbolSnapshot{},
		signatures: map[SignatureID]SignatureSnapshot{},
		nodes:      map[NodeID]NodeSnapshot{},
	}
	for _, entry := range document.Kinds {
		definition, ok := lookupKindGateHandler(entry.GateHandler)
		if !ok || definition.Handle == nil {
			t.Fatalf("Kind %s has unbound gate handler %q", entry.Kind, entry.GateHandler)
		}
		if !slices.Contains(definition.Decisions, entry.DefaultDecision) {
			t.Fatalf("Kind %s gate handler %q does not cover %q", entry.Kind, entry.GateHandler, entry.DefaultDecision)
		}
		used[definition.Name] = struct{}{}
		node := NodeSnapshot{Kind: entry.Kind, KindValue: entry.KindValue}
		_ = gateKindNode(node, entry, ProfileStatic, facts)
	}
	for _, definition := range kindGateHandlerRegistry {
		if _, ok := used[definition.Name]; !ok {
			t.Errorf("registered gate handler %q has no manifest rows", definition.Name)
		}
	}
}

func TestKindManifestRejectsUnknownAndUnboundGateHandlers(t *testing.T) {
	t.Parallel()

	document, err := LoadKindManifest()
	if err != nil {
		t.Fatal(err)
	}
	document.Kinds[0].GateHandler = "gateMissing"
	if err := ValidateKindManifestDocument(document); err == nil || !strings.Contains(err.Error(), "unknown gate handler") {
		t.Fatalf("unknown gate handler error = %v", err)
	}

	registry := slices.Clone(kindGateHandlerRegistry)
	registry[0].Handle = nil
	if err := validateKindGateHandlerRegistry(registry); err == nil || !strings.Contains(err.Error(), "unbound") {
		t.Fatalf("unbound gate handler error = %v", err)
	}
}

func TestKindManifestIncludesRequiredFeatureRows(t *testing.T) {
	t.Parallel()

	document, err := LoadKindManifest()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]KindGateDecision{
		"async":              KindDecisionReject,
		"exceptions":         KindDecisionReject,
		"jsx":                KindDecisionDeferFeature,
		"using":              KindDecisionReject,
		"unsafe-assertion":   KindDecisionAcceptDesugar,
		"non-null-assertion": KindDecisionAcceptDesugar,
	}
	seen := make(map[string]bool, len(want))
	for _, entry := range document.Kinds {
		if decision, ok := want[entry.Feature]; ok {
			if entry.DefaultDecision != decision {
				t.Fatalf("feature %q has wrong row: %#v", entry.Feature, entry)
			}
			seen[entry.Feature] = true
		}
	}
	for feature := range want {
		if !seen[feature] {
			t.Errorf("required feature %q is absent", feature)
		}
	}
}
