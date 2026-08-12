package llvmbackend

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func testDigest(value string) string { return strings.Repeat(value, 64) }

func testHIRProvenanceForBackend(t testing.TB) bingo.HIRProvenance {
	t.Helper()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(bingo.ClassAccessLogicalCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	return bingo.HIRProvenance{
		FrontendSnapshotSchemaVersion: bingo.HIRFrontendSnapshotSchemaVersion,
		FrontendSnapshotHash:          testDigest("1"), SourceContentHash: testDigest("2"),
		CompilerBuildIdentity: bingo.CompilerBuildIdentity{UpstreamCommit: strings.Repeat("3", 40), ForkCommit: strings.Repeat("4", 40), LoweringSchema: "test", LoweringHash: testDigest("5")},
		StandardLibraryHash:   testDigest("6"), KindManifestHash: testDigest("7"), LogicalCapabilityRequirementsDigest: digest,
	}
}

func testClassAccessBackendPlan(t testing.TB) ClassAccessBackendPlan {
	t.Helper()
	contract := bingo.ClassAccessContract{
		SchemaVersion: bingo.ClassAccessContractSchemaVersion,
		Classes:       []bingo.ClassAccessClass{{ID: 1, SymbolKey: "class/Base", InstanceTypeKey: testDigest("a")}, {ID: 2, SymbolKey: "class/Derived", InstanceTypeKey: testDigest("b"), BaseClassID: 1}},
		Members: []bingo.ClassAccessMember{
			{ID: 1, OwnerClassID: 1, SymbolKey: "field/private", Name: "secret", Kind: bingo.ClassAccessField, Visibility: bingo.ClassMemberPrivate, PrivateIdentity: "field/private"},
			{ID: 2, OwnerClassID: 1, SymbolKey: "field/protected", Name: "value", Kind: bingo.ClassAccessField, Visibility: bingo.ClassMemberProtected},
			{ID: 3, OwnerClassID: 1, SymbolKey: "method/base", Name: "readSecret", Kind: bingo.ClassAccessMethod, Visibility: bingo.ClassMemberPublic},
			{ID: 4, OwnerClassID: 2, SymbolKey: "method/derived", Name: "readValue", Kind: bingo.ClassAccessMethod, Visibility: bingo.ClassMemberPublic},
		},
	}
	_, hash, err := bingo.CanonicalClassAccessContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	hir, err := bingo.NewClassAccessHIR(testHIRProvenanceForBackend(t), contract)
	if err != nil {
		t.Fatal(err)
	}
	target, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerClassAccessMIR(hir, bingo.ClassAccessMIRTarget{TargetContextHash: testDigest("c"), Triple: target.Triple, DataLayoutHash: testDigest("d"), LLVMDataLayoutHash: target.DataLayoutHash})
	if err != nil {
		t.Fatal(err)
	}
	layout, err := bingo.PlanClassAccessLayout(mir, target)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanClassAccessBackend(layout)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestClassAccessBackendPlanMapsAuthorizedOffsetsAndCallees(t *testing.T) {
	plan := testClassAccessBackendPlan(t)
	if plan.Accesses[0].FieldOffset != plan.Layout.Base.Properties[0].FieldOffset || plan.Accesses[1].FieldOffset != plan.Layout.Derived.Properties[1].FieldOffset {
		t.Fatal("backend plan did not use the authorized field offsets")
	}
	if plan.Accesses[2].CalleeSymbolKey != plan.Layout.MIR.Authorizations[2].MemberSymbolKey || plan.Accesses[3].CalleeSymbolKey != plan.Layout.MIR.Authorizations[3].MemberSymbolKey {
		t.Fatal("backend plan did not preserve authorized method callees")
	}
	if plan.Accesses[0].FunctionID != 3 || plan.Accesses[1].FunctionID != 4 || plan.Accesses[2].FunctionID != 5 || plan.Accesses[3].FunctionID != 5 {
		t.Fatal("backend plan did not consume execution CFG order")
	}
	encoded, hash, err := CanonicalClassAccessBackendPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClassAccessBackendPlan(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("backend plan decode = %#v / %v", decoded, err)
	}
}

func TestClassAccessBackendPlanRejectsSubstitution(t *testing.T) {
	for name, mutate := range map[string]func(*ClassAccessBackendPlan){
		"layout hash":   func(p *ClassAccessBackendPlan) { p.LayoutHash = testDigest("e") },
		"authorization": func(p *ClassAccessBackendPlan) { p.Accesses[0].AuthorizationID = 2 },
		"function":      func(p *ClassAccessBackendPlan) { p.Accesses[0].FunctionID = 1 },
		"instruction":   func(p *ClassAccessBackendPlan) { p.Accesses[0].InstructionID = 4 },
		"field offset":  func(p *ClassAccessBackendPlan) { p.Accesses[0].FieldOffset += 8 },
		"member symbol": func(p *ClassAccessBackendPlan) { p.Accesses[1].MemberSymbolKey = "other" },
		"field callee":  func(p *ClassAccessBackendPlan) { p.Accesses[0].CalleeSymbolKey = "forged" },
		"method offset": func(p *ClassAccessBackendPlan) { p.Accesses[2].FieldOffset = 24 },
		"method callee": func(p *ClassAccessBackendPlan) { p.Accesses[2].CalleeSymbolKey = "forged" },
	} {
		t.Run(name, func(t *testing.T) {
			plan := testClassAccessBackendPlan(t)
			mutate(&plan)
			if err := VerifyClassAccessBackendPlan(plan); err == nil {
				t.Fatal("substituted backend plan accepted")
			}
		})
	}
}
