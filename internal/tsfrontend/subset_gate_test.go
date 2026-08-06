package tsfrontend

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

func TestRunSubsetGateAcceptsValidatedSnapshot(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	got := RunSubsetGate(*snapshot)
	if got == nil || len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want non-nil empty", got)
	}
}

func TestRunSubsetGateRejectsUnvalidatedAndMalformedSnapshots(t *testing.T) {
	valid := buildReplayAddSnapshot(t)
	malformed := *valid
	malformed.Nodes = slices.Clone(valid.Nodes)
	malformed.Nodes[0].Parent = "missing-node"

	tests := []struct {
		name     string
		snapshot ProgramSnapshot
		want     string
	}{
		{name: "unvalidated", snapshot: ProgramSnapshot{SchemaVersion: SnapshotSchemaVersion}, want: "unsupported Bingo options schema"},
		{name: "malformed", snapshot: malformed, want: "missing parent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := RunSubsetGate(test.snapshot)
			if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticCodeInternalFailure || diagnostics[0].MessageKey != "subset.snapshot_invalid" {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
			if len(diagnostics[0].Arguments) != 1 || !strings.Contains(diagnostics[0].Arguments[0], test.want) {
				t.Fatalf("arguments = %#v, want validation error containing %q", diagnostics[0].Arguments, test.want)
			}
		})
	}
}

func TestValidatedProgramSnapshotDoesNotAliasInput(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	validated, err := newValidatedProgramSnapshot(*snapshot)
	if err != nil {
		t.Fatal(err)
	}

	snapshot.Nodes[0].Parent = "missing-node"
	if err := ValidateProgramSnapshot(validated.snapshot); err != nil {
		t.Fatalf("validated snapshot changed through its input alias: %v", err)
	}
	if diagnostics := runSubsetGate(validated); diagnostics == nil || len(diagnostics) != 0 {
		t.Fatalf("validated snapshot diagnostics = %#v, want non-nil empty", diagnostics)
	}
}

func TestSubsetGateRejectsMissingKindManifestHash(t *testing.T) {
	t.Parallel()

	diagnostics := runSubsetGateFixture(ProgramSnapshot{SchemaVersion: SnapshotSchemaVersion})
	if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticCodeInternalFailure || diagnostics[0].MessageKey != "subset.kind_manifest_hash_missing" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestRunSubsetGateRejectsUnclassifiedKindAndValueDrift(t *testing.T) {
	t.Parallel()

	snapshot := gateSnapshot(NodeSnapshot{ID: "node/unclassified", Kind: "KindCount", KindValue: int16(ast.KindCount), Span: Span{File: "main.ts", Start: 2, End: 4}})
	got := runSubsetGateFixture(snapshot)
	if len(got) != 1 || got[0].Code != DiagnosticCodeUnclassifiedASTKind || got[0].MessageKey != "snapshot.unclassified_ast_kind" {
		t.Fatalf("diagnostics = %#v", got)
	}

	snapshot.Nodes[0] = NodeSnapshot{ID: "node/value", Kind: ast.KindUnknown.String(), KindValue: 99, Span: Span{File: "main.ts", Start: 2, End: 4}}
	got = runSubsetGateFixture(snapshot)
	if len(got) != 1 || got[0].Code != DiagnosticCodeUnclassifiedASTKind {
		t.Fatalf("value drift diagnostics = %#v", got)
	}
}

func TestRunSubsetGateCoversAnyUnknownAndUnsafeAssertions(t *testing.T) {
	t.Parallel()

	anyNode := NodeSnapshot{ID: "node/any", Kind: ast.KindPropertyAccessExpression.String(), KindValue: int16(ast.KindPropertyAccessExpression), Span: Span{File: "any.ts", Start: 1, End: 2}, DeclaredType: 1}
	unknownNode := NodeSnapshot{ID: "node/unknown", Kind: ast.KindCallExpression.String(), KindValue: int16(ast.KindCallExpression), Span: Span{File: "unknown.ts", Start: 1, End: 2}, DeclaredType: 2}
	assertionNode := NodeSnapshot{ID: "node/assert", Kind: ast.KindAsExpression.String(), KindValue: int16(ast.KindAsExpression), Span: Span{File: "assert.ts", Start: 1, End: 2}, DeclaredType: 3, NarrowedType: 4, Flow: FlowFactSnapshot{Narrowed: true}}
	snapshot := gateSnapshot(anyNode, unknownNode, assertionNode)
	snapshot.Types = []TypeSnapshot{
		{ID: 1, Kind: "any", CanonicalHash: "any", DebugText: "any"},
		{ID: 2, Kind: "unknown", CanonicalHash: "unknown", DebugText: "unknown"},
		{ID: 3, Kind: "literal", CanonicalHash: "number", DebugText: "1"},
		{ID: 4, Kind: "object", CanonicalHash: "string", DebugText: "string"},
	}
	got := runSubsetGateFixture(snapshot)
	byNode := make(map[string]Diagnostic)
	for _, diagnostic := range got {
		byNode[diagnostic.EntityID] = diagnostic
	}
	if byNode["node/any"].MessageKey != "subset.any_type" || byNode["node/any"].Code != DiagnosticCodeUnsupportedSyntax {
		t.Fatalf("any diagnostic = %#v", byNode["node/any"])
	}
	if byNode["node/unknown"].MessageKey != "subset.unknown_unchecked" || byNode["node/unknown"].Code != DiagnosticCodeUnsupportedSyntax {
		t.Fatalf("unknown diagnostic = %#v", byNode["node/unknown"])
	}
	if byNode["node/assert"].Code != DiagnosticCodeUnsafeAssertionChain {
		t.Fatalf("assertion diagnostic = %#v", byNode["node/assert"])
	}
}

func TestRunSubsetGateRequiresExplicitNonNullProof(t *testing.T) {
	t.Parallel()

	base := NodeSnapshot{
		ID: "node/non-null", Kind: ast.KindNonNullExpression.String(), KindValue: int16(ast.KindNonNullExpression),
		Span: Span{File: "nonnull.ts", Start: 1, End: 3}, DeclaredType: 1, NarrowedType: 2,
		Flow: FlowFactSnapshot{Narrowed: true, ProofKind: "checker-flow"},
	}
	snapshot := gateSnapshot(base)
	snapshot.Types = []TypeSnapshot{
		{ID: 1, Kind: "union", CanonicalHash: "number-or-null", ElementTypes: []TypeID{2, 3}},
		{ID: 2, Kind: "number", CanonicalHash: "number"},
		{ID: 3, Kind: "null", CanonicalHash: "null", Flags: uint32(checker.TypeFlagsNull)},
	}
	assertRejected := func(label string) {
		t.Helper()
		diagnostics := runSubsetGateFixture(snapshot)
		if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticCodeUnprovenNonNullAssertion {
			t.Fatalf("%s diagnostics = %#v", label, diagnostics)
		}
	}
	assertRejected("flow-only")

	snapshot.Nodes[0].NonNullProof = NonNullProofSnapshot{
		Present: true, OperandType: 1, ResultType: 2, ProofKind: "assertion-strip", RemovedNull: true,
	}
	assertRejected("assertion-strip")

	snapshot.Nodes[0].DeclaredType = 2
	snapshot.Nodes[0].NonNullProof = NonNullProofSnapshot{
		Present: true, OperandType: 2, ResultType: 2, ProofKind: "redundant-non-null",
	}
	if diagnostics := runSubsetGateFixture(snapshot); len(diagnostics) != 0 {
		t.Fatalf("redundant proven non-null diagnostics = %#v", diagnostics)
	}
}

func TestRunSubsetGateChecksEveryAssertionChainStep(t *testing.T) {
	t.Parallel()

	node := NodeSnapshot{
		ID: "node/assert-chain", Kind: ast.KindAsExpression.String(), KindValue: int16(ast.KindAsExpression),
		Span: Span{File: "assert.ts", Start: 1, End: 20}, DeclaredType: 1, NarrowedType: 3, AssertionTarget: 3,
		AssertionAssignable: true,
		AssertionChain: []AssertionProofSnapshot{
			{SourceType: 1, TargetType: 2, Assignable: true, RepresentationProof: "source-assignable"},
			{SourceType: 2, TargetType: 3, Assignable: true, RepresentationProof: "source-assignable"},
		},
	}
	snapshot := gateSnapshot(node)
	snapshot.Types = []TypeSnapshot{
		{ID: 1, Kind: "literal", CanonicalHash: "one", DebugText: "1"},
		{ID: 2, Kind: "number", CanonicalHash: "number", DebugText: "number"},
		{ID: 3, Kind: "number", CanonicalHash: "number-alias", DebugText: "number"},
		{ID: 4, Kind: "unknown", CanonicalHash: "unknown", DebugText: "unknown"},
	}
	if diagnostics := runSubsetGateFixture(snapshot); len(diagnostics) != 0 {
		t.Fatalf("safe assertion chain diagnostics = %#v", diagnostics)
	}

	snapshot.Nodes[0].AssertionChain[1].SourceType = 4
	snapshot.Nodes[0].AssertionChain[1].OpenType = "unknown"
	snapshot.Nodes[0].AssertionChain[1].RepresentationProof = "open-type"
	diagnostics := runSubsetGateFixture(snapshot)
	if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticCodeUnsafeAssertionChain {
		t.Fatalf("open assertion chain diagnostics = %#v", diagnostics)
	}
}

func TestRunSubsetGateUsesRepresentationTypeClosure(t *testing.T) {
	t.Parallel()

	cleanArray := NodeSnapshot{ID: "node/clean-array", Kind: ast.KindArrayLiteralExpression.String(), KindValue: int16(ast.KindArrayLiteralExpression), Span: Span{File: "arrays.ts", Start: 1, End: 2}, DeclaredType: 1}
	anyArray := NodeSnapshot{ID: "node/any-array", Kind: ast.KindArrayLiteralExpression.String(), KindValue: int16(ast.KindArrayLiteralExpression), Span: Span{File: "arrays.ts", Start: 3, End: 4}, DeclaredType: 4}
	errorNode := NodeSnapshot{ID: "node/error", Kind: ast.KindConstructor.String(), KindValue: int16(ast.KindConstructor), Span: Span{File: "arrays.ts", Start: 5, End: 6}, DeclaredType: 6}
	snapshot := gateSnapshot(cleanArray, anyArray, errorNode)
	snapshot.Types = []TypeSnapshot{
		{ID: 1, Kind: "object", CanonicalHash: "array-number", TypeArguments: []TypeID{2}, Properties: []SymbolID{"ambient-method"}},
		{ID: 2, Kind: "intrinsic", CanonicalHash: "number"},
		{ID: 3, Kind: "any", CanonicalHash: "ambient-any"},
		{ID: 4, Kind: "object", CanonicalHash: "array-any", TypeArguments: []TypeID{5}},
		{ID: 5, Kind: "any", CanonicalHash: "explicit-any"},
		{ID: 6, Kind: "any", CanonicalHash: "checker-error", NotLowerableReason: "checker-error-type"},
	}
	snapshot.Symbols = []SymbolSnapshot{{ID: "ambient-method", Type: 3}}
	diagnostics := runSubsetGateFixture(snapshot)
	if len(diagnostics) != 1 || diagnostics[0].EntityID != "node/any-array" || diagnostics[0].MessageKey != "subset.any_type" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestRunSubsetGateFollowsProjectSignaturesButNotAmbientSignatures(t *testing.T) {
	t.Parallel()

	projectFunction := NodeSnapshot{ID: "node/project", Kind: ast.KindFunctionDeclaration.String(), KindValue: int16(ast.KindFunctionDeclaration), Span: Span{File: "main.ts", Start: 1, End: 10}, DeclaredType: 1}
	ambientFunction := NodeSnapshot{ID: "node/ambient", Kind: ast.KindFunctionDeclaration.String(), KindValue: int16(ast.KindFunctionDeclaration), Span: Span{File: "main.ts", Start: 11, End: 20}, DeclaredType: 3}
	snapshot := gateSnapshot(projectFunction, ambientFunction)
	snapshot.Types = []TypeSnapshot{
		{ID: 1, Kind: "object", CanonicalHash: "project-function", CallSignatures: []SignatureID{1}},
		{ID: 2, Kind: "any", CanonicalHash: "any"},
		{ID: 3, Kind: "object", CanonicalHash: "ambient-function", CallSignatures: []SignatureID{2}},
	}
	snapshot.Symbols = []SymbolSnapshot{
		{ID: "project-parameter", Declarations: []NodeID{"node/project"}, Type: 2},
		{ID: "ambient-parameter", Type: 2},
	}
	snapshot.Signatures = []SignatureSnapshot{
		{ID: 1, Declaration: "node/project", Parameters: []SymbolID{"project-parameter"}},
		{ID: 2, Parameters: []SymbolID{"ambient-parameter"}},
	}

	diagnostics := runSubsetGateFixture(snapshot)
	if len(diagnostics) != 1 || diagnostics[0].EntityID != "node/project" || diagnostics[0].MessageKey != "subset.any_type" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestRunSubsetGateRejectsOnlyExportedGenericValueSignatures(t *testing.T) {
	t.Parallel()

	exportedNode := NodeSnapshot{ID: "node/exported", Kind: ast.KindFunctionDeclaration.String(), KindValue: int16(ast.KindFunctionDeclaration), Span: Span{File: "main.ts", Start: 1, End: 10}}
	privateNode := NodeSnapshot{ID: "node/private", Kind: ast.KindFunctionDeclaration.String(), KindValue: int16(ast.KindFunctionDeclaration), Span: Span{File: "main.ts", Start: 11, End: 20}}
	snapshot := gateSnapshot(exportedNode, privateNode)
	snapshot.Types = []TypeSnapshot{
		{ID: 1, Kind: "object", CanonicalHash: "generic-function", CallSignatures: []SignatureID{1}},
		{ID: 2, Kind: "typeParameter", CanonicalHash: "T", NotLowerableReason: "unresolved-type-parameter"},
	}
	snapshot.Symbols = []SymbolSnapshot{
		{ID: "local-export", Flags: uint32(ast.SymbolFlagsExportValue), ExportSymbol: "exported", Declarations: []NodeID{"node/exported"}},
		{ID: "exported", Declarations: []NodeID{"node/exported"}, ValueDeclaration: "node/exported", Type: 1},
		{ID: "private", Declarations: []NodeID{"node/private"}, ValueDeclaration: "node/private", Type: 1},
		{ID: "parameter", Declarations: []NodeID{"node/exported"}, Type: 2},
	}
	snapshot.Signatures = []SignatureSnapshot{{ID: 1, Declaration: "node/exported", Parameters: []SymbolID{"parameter"}, TypeParameters: []TypeID{2}, ReturnType: 2}}

	diagnostics := runSubsetGateFixture(snapshot)
	if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticCodeUnresolvedGeneric || diagnostics[0].EntityID != "node/exported" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}

	snapshot.Symbols[0].Flags = uint32(ast.SymbolFlagsFunction)
	if diagnostics := runSubsetGateFixture(snapshot); len(diagnostics) != 0 {
		t.Fatalf("private generic diagnostics = %#v", diagnostics)
	}
}

func TestRunSubsetGateRejectsUnsafeFeatureRowsWithStableDiagnostics(t *testing.T) {
	t.Parallel()

	nodes := []NodeSnapshot{
		{ID: "node/async", Kind: ast.KindFunctionDeclaration.String(), KindValue: int16(ast.KindFunctionDeclaration), Span: Span{File: "features.ts", Start: 1, End: 2}, ModifierBits: uint32(ast.ModifierFlagsAsync)},
		{ID: "node/eh", Kind: ast.KindTryStatement.String(), KindValue: int16(ast.KindTryStatement), Span: Span{File: "features.ts", Start: 3, End: 4}},
		{ID: "node/jsx", Kind: ast.KindJsxElement.String(), KindValue: int16(ast.KindJsxElement), Span: Span{File: "features.ts", Start: 5, End: 6}},
		{ID: "node/using", Kind: ast.KindVariableDeclarationList.String(), KindValue: int16(ast.KindVariableDeclarationList), Span: Span{File: "features.ts", Start: 7, End: 8}, NodeFlags: uint32(ast.NodeFlagsUsing)},
		{ID: "node/dynamic", Kind: ast.KindWithStatement.String(), KindValue: int16(ast.KindWithStatement), Span: Span{File: "features.ts", Start: 9, End: 10}},
		{ID: "node/non-null", Kind: ast.KindNonNullExpression.String(), KindValue: int16(ast.KindNonNullExpression), Span: Span{File: "features.ts", Start: 11, End: 12}},
	}
	got := runSubsetGateFixture(gateSnapshot(nodes...))
	if len(got) != len(nodes) {
		t.Fatalf("diagnostics = %#v", got)
	}
	byNode := make(map[string]Diagnostic, len(got))
	for _, diagnostic := range got {
		byNode[diagnostic.EntityID] = diagnostic
	}
	for _, node := range nodes[:5] {
		if byNode[string(node.ID)].Code != DiagnosticCodeUnsupportedSyntax {
			t.Errorf("feature %s diagnostic = %#v", node.ID, byNode[string(node.ID)])
		}
		if byNode[string(node.ID)].MessageKey != "subset.feature_unavailable" {
			t.Errorf("feature %s message key = %q", node.ID, byNode[string(node.ID)].MessageKey)
		}
	}
	if byNode["node/non-null"].Code != DiagnosticCodeUnprovenNonNullAssertion {
		t.Fatalf("non-null diagnostic = %#v", byNode["node/non-null"])
	}

	repeat := runSubsetGateFixture(gateSnapshot(nodes...))
	if !reflect.DeepEqual(got, repeat) {
		t.Fatal("subset gate output changed between identical runs")
	}
}

func TestRunSubsetGateRejectsDynamicImportModuleEdge(t *testing.T) {
	t.Parallel()

	snapshot := gateSnapshot()
	snapshot.ModuleEdges = []ModuleEdge{{
		Importer: "module/main", Specifier: "./lazy", Kind: "dynamic-import",
		Span: Span{File: "main.ts", Start: 12, End: 20}, Value: true, DeferredEvaluation: true,
	}}
	diagnostics := runSubsetGateFixture(snapshot)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	diagnostic := diagnostics[0]
	if diagnostic.Code != DiagnosticCodeUnsupportedSyntax || diagnostic.MessageKey != "subset.feature_unavailable" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	if diagnostic.PrimarySpan.File != "main.ts" || diagnostic.RequiredCapability != "dynamic-module-loader" {
		t.Fatalf("dynamic import context = %#v", diagnostic)
	}
}

func gateSnapshot(nodes ...NodeSnapshot) ProgramSnapshot {
	return ProgramSnapshot{
		SchemaVersion: SnapshotSchemaVersion,
		Provenance:    ProvenanceSnapshot{KindManifestHash: KindManifestDigest()},
		Config:        ConfigSnapshot{Bingo: BingoOptions{Profile: ProfileStatic}},
		Nodes:         nodes,
		Types:         []TypeSnapshot{},
	}
}

func runSubsetGateFixture(snapshot ProgramSnapshot) []Diagnostic {
	return runSubsetGate(validatedProgramSnapshot{snapshot: snapshot})
}
