package tsfrontend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

func TestFrontendBuildProducesDeterministicValidatedSnapshot(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"module":"preserve"},"files":["main.ts"]}`,
		"/project/main.ts": `
const outer = 2;
export function pick(value: string): string;
export function pick(value: number): number;
export function pick(value: string | number): string | number { return value; }
export function narrow(value: string | number): string | number {
    if (typeof value === "string") return value;
    return value;
}
export function add(value: number): number { return outer + value; }
export const selected: number = pick(1);
export const widened: number = 1 as number;
`,
	}
	request, frontend := snapshotTestRequest(files)
	first, firstDiagnostics := frontend.Build(context.Background(), request)
	if first == nil {
		t.Fatalf("first snapshot is nil: %#v", firstDiagnostics)
	}
	if DiagnosticsHaveErrors(firstDiagnostics) {
		t.Fatalf("first build diagnostics: %#v", firstDiagnostics)
	}
	if err := ValidateProgramSnapshot(*first); err != nil {
		t.Fatal(err)
	}
	second, secondDiagnostics := frontend.Build(context.Background(), request)
	if second == nil || DiagnosticsHaveErrors(secondDiagnostics) {
		t.Fatalf("second snapshot/diagnostics = %#v / %#v", second, secondDiagnostics)
	}
	firstBytes, err := first.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash != second.ContentHash || !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("snapshot changed across builds: %s / %s", first.ContentHash, second.ContentHash)
	}
	if bytes.Contains(firstBytes, []byte("/project/")) || bytes.Contains(firstBytes, []byte(`:\\`)) {
		t.Fatalf("snapshot contains a machine path:\n%s", firstBytes)
	}
	if err := rejectInternalPointerKinds(reflect.ValueOf(*first), "ProgramSnapshot"); err != nil {
		t.Fatal(err)
	}
	if len(first.Types) == 0 || len(first.Symbols) == 0 || len(first.Signatures) == 0 {
		t.Fatalf("semantic tables are incomplete: types=%d symbols=%d signatures=%d", len(first.Types), len(first.Symbols), len(first.Signatures))
	}
	if first.ModuleGraphDigest == "" || len(first.Modules) != 1 || len(first.ModuleSCCs) != 1 {
		t.Fatalf("module graph is incomplete: digest=%q modules=%#v sccs=%#v", first.ModuleGraphDigest, first.Modules, first.ModuleSCCs)
	}
	if !slices.ContainsFunc(first.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == ast.KindCallExpression.String() && node.SelectedSignature != 0
	}) {
		t.Fatal("resolved call signature was not captured")
	}
	if !slices.ContainsFunc(first.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == ast.KindAsExpression.String() && node.AssertionTarget != 0 && node.AssertionAssignable
	}) {
		t.Fatal("checker assertion assignability proof was not captured")
	}
	if !slices.ContainsFunc(first.Nodes, func(node NodeSnapshot) bool {
		return ast.IsFunctionLikeKind(ast.Kind(node.KindValue)) && len(node.CaptureSet) != 0
	}) {
		t.Fatal("function capture set was not captured")
	}
	if !slices.ContainsFunc(first.Nodes, func(node NodeSnapshot) bool { return node.Flow.Narrowed }) {
		t.Fatal("flow narrowing facts were not captured")
	}
}

func TestFrontendBuildDetachesReturnedDiagnosticsFromSealedSnapshot(t *testing.T) {
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function makeRegexp(): RegExp { return /effect/; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || len(diagnostics) != 1 || len(diagnostics[0].Arguments) == 0 {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	before, err := snapshot.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	originalArgument := snapshot.Diagnostics[0].Arguments[0]
	diagnostics[0].Arguments[0] = "tampered-after-build"
	if snapshot.Diagnostics[0].Arguments[0] != originalArgument {
		t.Fatal("returned diagnostics mutate the sealed snapshot")
	}
	after, err := snapshot.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("sealed snapshot bytes changed through returned diagnostics")
	}
	if err := ValidateProgramSnapshot(*snapshot); err != nil {
		t.Fatalf("sealed snapshot became invalid: %v", err)
	}
}

func TestAsyncFunctionSnapshotIsDeterministic(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true,"target":"ES2020","module":"ESNext","moduleResolution":"Bundler","lib":["ES2020"],"skipLibCheck":true},"files":["main.ts"]}`,
		"/project/main.ts": `export async function answer(): Promise<number> {
  return 42;
}`,
	})

	first, firstDiagnostics := frontend.Build(context.Background(), request)
	second, secondDiagnostics := frontend.Build(context.Background(), request)
	if first == nil || second == nil {
		t.Fatalf("snapshot is nil: first=%v second=%v", frontendDiagnosticCodes(firstDiagnostics), frontendDiagnosticCodes(secondDiagnostics))
	}
	firstBytes, err := first.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		path, left, right := firstSnapshotDifference(reflect.ValueOf(*first), reflect.ValueOf(*second), "ProgramSnapshot")
		t.Fatalf("snapshot changed across builds at %s: first=%s second=%s", path, left, right)
	}
}

func TestBundledStandardLibraryTypesAreOpaque(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"target":"ES2020","lib":["ES2020"]},"files":["main.ts"]}`,
		"/project/main.ts":       `export const values: Array<number> = [1]; export declare const answer: Promise<number>;`,
	})

	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	symbolNames := make(map[SymbolID]string, len(snapshot.Symbols))
	for _, symbol := range snapshot.Symbols {
		symbolNames[symbol.ID] = symbol.Name
	}
	for _, name := range []string{"Array", "Promise"} {
		index := slices.IndexFunc(snapshot.Types, func(record TypeSnapshot) bool {
			return symbolNames[record.Symbol] == name && len(record.TypeArguments) == 1
		})
		if index < 0 {
			t.Fatalf("snapshot has no instantiated %s type", name)
		}
		record := snapshot.Types[index]
		if len(record.TypeArguments) != 1 {
			t.Fatalf("%s type arguments = %#v", name, record.TypeArguments)
		}
		if len(record.Properties) != 0 || len(record.BaseTypes) != 0 || len(record.CallSignatures) != 0 || len(record.ConstructSignatures) != 0 || len(record.IndexInfos) != 0 {
			t.Fatalf("%s expanded bundled stdlib graph: %#v", name, record)
		}
	}
}

func TestSnapshotCapturesGenericCallInstantiation(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"target":"ES2020","lib":["ES5"]},"files":["main.ts"]}`,
		"/project/main.ts":       `function identity<T>(value: T): T { return value; } export const answer: number = identity<number>(42);`,
	})

	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	callIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == ast.KindCallExpression.String() && node.SelectedSignature != 0
	})
	if callIndex < 0 {
		t.Fatal("generic call has no selected signature")
	}
	signatureIndex := slices.IndexFunc(snapshot.Signatures, func(signature SignatureSnapshot) bool {
		return signature.ID == snapshot.Nodes[callIndex].SelectedSignature
	})
	if signatureIndex < 0 {
		t.Fatal("selected signature is missing")
	}
	signature := snapshot.Signatures[signatureIndex]
	if len(signature.InstantiatedTypeArguments) != 1 {
		t.Fatalf("instantiated type arguments = %#v", signature.InstantiatedTypeArguments)
	}
	typeIndex := slices.IndexFunc(snapshot.Types, func(record TypeSnapshot) bool {
		return record.ID == signature.InstantiatedTypeArguments[0]
	})
	if typeIndex < 0 {
		t.Fatal("instantiated type is missing")
	}
	if snapshot.Types[typeIndex].DebugText != "number" {
		t.Fatalf("instantiated type = %#v", snapshot.Types[typeIndex])
	}
}

func firstSnapshotDifference(left, right reflect.Value, path string) (string, string, string) {
	if left.Type() != right.Type() {
		return path, left.Type().String(), right.Type().String()
	}
	switch left.Kind() {
	case reflect.Struct:
		for index := 0; index < left.NumField(); index++ {
			fieldPath := path + "." + left.Type().Field(index).Name
			if !reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface()) {
				return firstSnapshotDifference(left.Field(index), right.Field(index), fieldPath)
			}
		}
	case reflect.Slice, reflect.Array:
		if left.Len() != right.Len() {
			return path + ".length", fmt.Sprint(left.Len()), fmt.Sprint(right.Len())
		}
		for index := 0; index < left.Len(); index++ {
			if !reflect.DeepEqual(left.Index(index).Interface(), right.Index(index).Interface()) {
				return firstSnapshotDifference(left.Index(index), right.Index(index), fmt.Sprintf("%s[%d]", path, index))
			}
		}
	default:
		return path, fmt.Sprintf("%#v", left.Interface()), fmt.Sprintf("%#v", right.Interface())
	}
	return path, fmt.Sprintf("%#v", left.Interface()), fmt.Sprintf("%#v", right.Interface())
}

func TestCanonicalSemanticHashesIgnoreCaptureKeysAndUnrelatedTypes(t *testing.T) {
	t.Parallel()
	first := map[string]*pendingType{
		"temporary-a": {key: "temporary-a", kind: "object", scalar: "object|stable", elementKeys: []string{"temporary-a"}},
	}
	firstHashes, _ := canonicalSemanticHashes(first, nil, nil)

	second := map[string]*pendingType{
		"renamed":   {key: "renamed", kind: "object", scalar: "object|stable", elementKeys: []string{"renamed"}},
		"unrelated": {key: "unrelated", kind: "intrinsic", scalar: "intrinsic|number"},
	}
	secondHashes, _ := canonicalSemanticHashes(second, nil, nil)
	if firstHashes["temporary-a"] != secondHashes["renamed"] {
		t.Fatalf("recursive type hash depends on capture key or unrelated graph: %s / %s", firstHashes["temporary-a"], secondHashes["renamed"])
	}

	property := SymbolID("symbol_property")
	numberGraph := map[string]*pendingType{
		"object": {key: "object", kind: "object", scalar: "anonymous", propertyKeys: []SymbolID{property}},
		"value":  {key: "value", kind: "intrinsic", scalar: "number"},
	}
	stringGraph := map[string]*pendingType{
		"object": {key: "object", kind: "object", scalar: "anonymous", propertyKeys: []SymbolID{property}},
		"value":  {key: "value", kind: "intrinsic", scalar: "string"},
	}
	numberHashes, _ := canonicalSemanticHashes(numberGraph, nil, map[SymbolID]*pendingSymbol{property: {id: property, typeKey: "value"}})
	stringHashes, _ := canonicalSemanticHashes(stringGraph, nil, map[SymbolID]*pendingSymbol{property: {id: property, typeKey: "value"}})
	if numberHashes["object"] == stringHashes["object"] {
		t.Fatal("property value type does not participate in object type identity")
	}
}

func TestCanonicalSemanticHashesIncludePropertySignatureFacts(t *testing.T) {
	t.Parallel()
	property := SymbolID("property")
	number := map[string]*pendingType{
		"object": {
			key: "object", kind: "object", scalar: "anonymous", propertyKeys: []SymbolID{property},
			propertyFacts: []pendingProperty{{symbol: property, readKey: "number", writeKey: "number", optional: false, readonly: false, hasGetter: true, hasSetter: true, visibility: "public"}},
		},
		"number": {key: "number", kind: "intrinsic", scalar: "number"},
	}
	stringReadOnly := map[string]*pendingType{
		"object": {
			key: "object", kind: "object", scalar: "anonymous", propertyKeys: []SymbolID{property},
			propertyFacts: []pendingProperty{{symbol: property, readKey: "string", writeKey: "string", optional: true, readonly: true, hasGetter: true, hasSetter: false, visibility: "private", privateIdentity: "class#property"}},
		},
		"string": {key: "string", kind: "intrinsic", scalar: "string"},
	}
	numberHashes, _ := canonicalSemanticHashes(number, nil, nil)
	stringHashes, _ := canonicalSemanticHashes(stringReadOnly, nil, nil)
	if numberHashes["object"] == stringHashes["object"] {
		t.Fatal("property read/write and visibility facts do not participate in type identity")
	}

	firstSignature := map[string]*pendingSignature{
		"signature": {key: "signature", parameters: []SymbolID{"parameter"}, parameterFacts: []pendingParameter{{symbol: "parameter", typeKey: "number", optional: false, rest: false}}, parameterTypeKeys: []string{"number"}, effects: []string{"pure"}},
	}
	secondSignature := map[string]*pendingSignature{
		"signature": {key: "signature", parameters: []SymbolID{"parameter"}, parameterFacts: []pendingParameter{{symbol: "parameter", typeKey: "number", optional: true, rest: true}}, parameterTypeKeys: []string{"number"}, effects: []string{"call", "write"}},
	}
	types := map[string]*pendingType{"number": {key: "number", kind: "intrinsic", scalar: "number"}}
	_, firstSignatureHashes := canonicalSemanticHashes(types, firstSignature, nil)
	_, secondSignatureHashes := canonicalSemanticHashes(types, secondSignature, nil)
	if firstSignatureHashes["signature"] == secondSignatureHashes["signature"] {
		t.Fatal("parameter optional/rest and effects do not participate in signature identity")
	}
}

func TestValidateProgramSnapshotRejectsBrokenReferenceAndDigest(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export const value: number = 1;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}

	broken := *snapshot
	broken.Nodes = slices.Clone(snapshot.Nodes)
	broken.Nodes[0].Parent = "missing-node"
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "missing parent") {
		t.Fatalf("broken parent validation error = %v", err)
	}

	broken = *snapshot
	broken.ModuleGraphDigest = strings.Repeat("0", 64)
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "module graph digest mismatch") {
		t.Fatalf("broken module digest validation error = %v", err)
	}
}

func TestSnapshotV2CarriesLoweringPayloadsAndSourceBlob(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function add(left: number, right: number): number { return left + right; } export const widened: number = 1 as number;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	if snapshot.SchemaVersion != SnapshotSchemaVersion {
		t.Fatalf("schema version = %d, want %d", snapshot.SchemaVersion, SnapshotSchemaVersion)
	}
	file := snapshot.Files[0]
	if file.SourceBlob == "" || file.ContentHash == "" {
		t.Fatalf("source blob/content hash missing: %#v", file)
	}
	for _, node := range snapshot.Nodes {
		if node.SyntaxPayload.Tag != node.Kind || len(node.NamedChildren) != len(node.Children) {
			t.Fatalf("node payload is not lowering-ready: %#v", node)
		}
	}
	binaryIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == ast.KindBinaryExpression.String() })
	if binaryIndex < 0 {
		t.Fatal("binary expression is missing")
	}
	roles := make(map[string]NodeID)
	for _, child := range snapshot.Nodes[binaryIndex].NamedChildren {
		roles[child.Role] = child.Node
	}
	for _, role := range []string{"left", "operator", "right"} {
		if roles[role] == "" {
			t.Fatalf("binary expression has no %q child role: %#v", role, roles)
		}
	}
	for _, typ := range snapshot.Types {
		if typ.TypePayload.Tag != typ.Kind || typ.TypePayload.Scalar == "" {
			t.Fatalf("type payload is not lowering-ready: %#v", typ)
		}
	}
}

func TestSnapshotV2CarriesSemanticProofFacts(t *testing.T) {
	t.Parallel()
	snapshot := semanticFactsTestSnapshot(t)

	propertyType := slices.IndexFunc(snapshot.Types, func(record TypeSnapshot) bool {
		return len(record.Properties) != 0 && len(record.PropertyFacts) == len(record.Properties)
	})
	if propertyType < 0 {
		t.Fatal("snapshot has no populated property facts")
	}
	if !slices.ContainsFunc(snapshot.Types, func(record TypeSnapshot) bool {
		return slices.ContainsFunc(record.PropertyFacts, func(property PropertySnapshot) bool {
			return property.Visibility == "private" && property.PrivateIdentity != ""
		})
	}) {
		t.Fatal("snapshot has no private property identity")
	}

	signatureIndex := slices.IndexFunc(snapshot.Signatures, func(signature SignatureSnapshot) bool {
		return len(signature.Parameters) == 2 && len(signature.ParameterFacts) == len(signature.Parameters) && signature.HasRest
	})
	if signatureIndex < 0 {
		t.Fatal("snapshot has no populated optional/rest parameter facts")
	}
	signature := snapshot.Signatures[signatureIndex]
	if !signature.ParameterFacts[0].Optional || !signature.ParameterFacts[1].Optional || !signature.ParameterFacts[1].Rest {
		t.Fatalf("parameter facts = %#v", signature.ParameterFacts)
	}
	if !slices.Contains(signature.Effects, "read") || !slices.Contains(signature.Effects, "write") {
		t.Fatalf("signature effects = %#v", signature.Effects)
	}

	assertionIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return len(node.AssertionChain) == 2 })
	if assertionIndex < 0 {
		t.Fatal("snapshot has no two-step assertion chain")
	}
	assertion := snapshot.Nodes[assertionIndex]
	if assertion.AssertionChain[0].TargetType != assertion.AssertionChain[1].SourceType ||
		assertion.DeclaredType != assertion.AssertionChain[0].SourceType ||
		assertion.AssertionTarget != assertion.AssertionChain[1].TargetType {
		t.Fatalf("assertion chain is not continuous: %#v", assertion)
	}

	nonNullIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.NonNullProof.Present })
	if nonNullIndex < 0 {
		t.Fatal("snapshot has no non-null proof")
	}
	nonNull := snapshot.Nodes[nonNullIndex]
	if nonNull.DeclaredType != nonNull.NonNullProof.OperandType || nonNull.NarrowedType != nonNull.NonNullProof.ResultType ||
		nonNull.Flow.ProofKind != nonNull.NonNullProof.ProofKind {
		t.Fatalf("non-null proof is not tied to flow facts: %#v", nonNull)
	}

	captureIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return len(node.CaptureSet) != 0 })
	if captureIndex < 0 || !slices.ContainsFunc(snapshot.Nodes[captureIndex].CaptureBindings, func(binding CaptureBindingSnapshot) bool {
		return binding.Kind == "binding" && binding.Access == "readwrite"
	}) {
		t.Fatalf("runtime capture facts = %#v", snapshot.Nodes[captureIndex].CaptureBindings)
	}
}

func TestValidateProgramSnapshotRejectsSemanticFactCorruption(t *testing.T) {
	t.Parallel()
	snapshot := semanticFactsTestSnapshot(t)

	assertionIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return len(node.AssertionChain) == 2 })
	nonNullIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.NonNullProof.Present })
	captureIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return len(node.CaptureSet) != 0 })
	propertyIndex := slices.IndexFunc(snapshot.Types, func(record TypeSnapshot) bool { return len(record.PropertyFacts) != 0 })
	signatureIndex := slices.IndexFunc(snapshot.Signatures, func(signature SignatureSnapshot) bool { return len(signature.ParameterFacts) == 2 && signature.HasRest })
	if assertionIndex < 0 || nonNullIndex < 0 || captureIndex < 0 || propertyIndex < 0 || signatureIndex < 0 {
		t.Fatalf("semantic fixture is incomplete: assertion=%d nonNull=%d capture=%d property=%d signature=%d", assertionIndex, nonNullIndex, captureIndex, propertyIndex, signatureIndex)
	}

	t.Run("assertion continuity", func(t *testing.T) {
		broken := *snapshot
		broken.Nodes = slices.Clone(snapshot.Nodes)
		broken.Nodes[assertionIndex].AssertionChain = slices.Clone(snapshot.Nodes[assertionIndex].AssertionChain)
		chain := broken.Nodes[assertionIndex].AssertionChain
		if chain[0].SourceType == chain[0].TargetType {
			t.Fatalf("fixture does not distinguish the first assertion source and target: %#v", chain)
		}
		chain[1].SourceType = chain[0].SourceType
		assertSnapshotValidationError(t, broken, "assertion chain is discontinuous")
	})

	t.Run("assertion representation enum", func(t *testing.T) {
		broken := *snapshot
		broken.Nodes = slices.Clone(snapshot.Nodes)
		broken.Nodes[assertionIndex].AssertionChain = slices.Clone(snapshot.Nodes[assertionIndex].AssertionChain)
		broken.Nodes[assertionIndex].AssertionChain[0].RepresentationProof = "unchecked"
		assertSnapshotValidationError(t, broken, "invalid representation proof")
	})

	t.Run("non-null flow", func(t *testing.T) {
		broken := *snapshot
		broken.Nodes = slices.Clone(snapshot.Nodes)
		broken.Nodes[nonNullIndex].NonNullProof.ResultType = broken.Nodes[nonNullIndex].NonNullProof.OperandType
		assertSnapshotValidationError(t, broken, "non-null result type")
	})

	t.Run("capture set", func(t *testing.T) {
		broken := *snapshot
		broken.Nodes = slices.Clone(snapshot.Nodes)
		broken.Nodes[captureIndex].CaptureSet = nil
		assertSnapshotValidationError(t, broken, "capture set does not match")
	})

	t.Run("property list", func(t *testing.T) {
		broken := *snapshot
		broken.Types = slices.Clone(snapshot.Types)
		broken.Types[propertyIndex].PropertyFacts = slices.Clone(snapshot.Types[propertyIndex].PropertyFacts[:len(snapshot.Types[propertyIndex].PropertyFacts)-1])
		assertSnapshotValidationError(t, broken, "property fact count")
	})

	t.Run("property enum", func(t *testing.T) {
		broken := *snapshot
		broken.Types = slices.Clone(snapshot.Types)
		broken.Types[propertyIndex].PropertyFacts = slices.Clone(snapshot.Types[propertyIndex].PropertyFacts)
		broken.Types[propertyIndex].PropertyFacts[0].Visibility = "package"
		assertSnapshotValidationError(t, broken, "invalid visibility")
	})

	t.Run("parameter list", func(t *testing.T) {
		broken := *snapshot
		broken.Signatures = slices.Clone(snapshot.Signatures)
		broken.Signatures[signatureIndex].ParameterFacts = slices.Clone(snapshot.Signatures[signatureIndex].ParameterFacts[:1])
		assertSnapshotValidationError(t, broken, "parameter fact count")
	})

	t.Run("effect enum", func(t *testing.T) {
		broken := *snapshot
		broken.Signatures = slices.Clone(snapshot.Signatures)
		broken.Signatures[signatureIndex].Effects = []string{"network"}
		assertSnapshotValidationError(t, broken, "invalid effect")
	})

	t.Run("effect order", func(t *testing.T) {
		broken := *snapshot
		broken.Signatures = slices.Clone(snapshot.Signatures)
		broken.Signatures[signatureIndex].Effects = []string{"write", "read"}
		assertSnapshotValidationError(t, broken, "canonical order")
	})
}

func TestValidateProgramSnapshotRejectsPayloadTreeAndFlowCorruption(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function add(left: number, right: number): number { return left + right; } export const widened: number = 1 as number;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	broken := *snapshot
	broken.Nodes = slices.Clone(snapshot.Nodes)
	rootIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Parent == "" && len(node.Children) != 0 })
	if rootIndex < 0 {
		t.Fatal("fixture root unexpectedly has no children")
	}
	broken.Nodes[rootIndex].NamedChildren = slices.Clone(snapshot.Nodes[rootIndex].NamedChildren)
	if len(broken.Nodes[rootIndex].NamedChildren) > 1 {
		broken.Nodes[rootIndex].NamedChildren[0].Role = broken.Nodes[rootIndex].NamedChildren[1].Role
	} else {
		broken.Nodes[rootIndex].NamedChildren[0].Node = "missing-child"
	}
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "named child") {
		t.Fatalf("payload validation error = %v", err)
	}

	broken = *snapshot
	broken.Nodes = slices.Clone(snapshot.Nodes)
	broken.Nodes[rootIndex].Children = append(slices.Clone(snapshot.Nodes[rootIndex].Children), snapshot.Nodes[rootIndex].Children[0])
	if err := ValidateProgramSnapshot(broken); err == nil || !(strings.Contains(err.Error(), "duplicate child") || strings.Contains(err.Error(), "reverse child edge")) {
		t.Fatalf("tree validation error = %v", err)
	}

	broken = *snapshot
	broken.Nodes = slices.Clone(snapshot.Nodes)
	narrowed := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool { return node.Flow.NarrowedTypeHash != "" })
	if narrowed < 0 {
		t.Fatal("fixture has no flow fact")
	}
	broken.Nodes[narrowed].Flow.NarrowedTypeHash = "deadbeef"
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "flow narrowed hash") {
		t.Fatalf("flow validation error = %v", err)
	}

	broken = *snapshot
	broken.Nodes = slices.Clone(snapshot.Nodes)
	assertion := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool { return node.AssertionTarget != 0 })
	if assertion < 0 {
		t.Fatal("fixture has no assertion proof")
	}
	broken.Nodes[assertion].AssertionTarget = TypeID(len(snapshot.Types) + 100)
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "assertion target") {
		t.Fatalf("assertion target validation error = %v", err)
	}

	broken = *snapshot
	broken.Types = slices.Clone(snapshot.Types)
	broken.Types[0].TypePayload.Tag = "corrupt"
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "type payload") {
		t.Fatalf("type payload validation error = %v", err)
	}

	broken = *snapshot
	broken.Files = slices.Clone(snapshot.Files)
	broken.Files[0].SourceBlob += "\n"
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "source blob hash mismatch") {
		t.Fatalf("source blob validation error = %v", err)
	}

	broken = *snapshot
	broken.Config.BingoDigest = "deadbeef"
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "Bingo options digest mismatch") {
		t.Fatalf("config digest validation error = %v", err)
	}

	broken = *snapshot
	broken.Provenance.KindManifestHash = "not-a-digest"
	if err := ValidateProgramSnapshot(broken); err == nil || !strings.Contains(err.Error(), "provenance digest") {
		t.Fatalf("provenance validation error = %v", err)
	}
}

func TestValidateProgramSnapshotRejectsUnreachableNode(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	broken := *snapshot
	broken.Nodes = slices.Clone(snapshot.Nodes)
	broken.Origins = slices.Clone(snapshot.Origins)
	leafIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == "KindNumericLiteral" && len(node.Children) == 0
	})
	if leafIndex < 0 {
		leafIndex = slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool {
			return node.Kind == "KindPlusToken" && len(node.Children) == 0
		})
	}
	if leafIndex < 0 {
		t.Fatal("fixture has no cloneable leaf node")
	}
	clone := snapshot.Nodes[leafIndex]
	clone.ID = "unreachable-node"
	clone.Parent = ""
	clone.Origin = "unreachable-origin"
	broken.Nodes = append(broken.Nodes, clone)
	broken.Origins = append(broken.Origins, OriginSnapshot{ID: clone.Origin, Node: clone.ID, Span: clone.Span})
	if err := finalizeSnapshot(&broken); err != nil {
		t.Fatal(err)
	}
	assertSnapshotValidationError(t, broken, "unreachable node")
}

func TestDecodeProgramSnapshotReadsLegacyV1ButDoesNotAuthorizeLowering(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export const answer: number = 42;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	legacy := *snapshot
	legacy.SchemaVersion = LegacySnapshotSchemaVersion
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProgramSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != LegacySnapshotSchemaVersion {
		t.Fatalf("decoded legacy schema = %d", decoded.SchemaVersion)
	}
	if err := ValidateProgramSnapshot(*decoded); err == nil || !strings.Contains(err.Error(), "unsupported snapshot schema") {
		t.Fatalf("legacy snapshot unexpectedly accepted for lowering: %v", err)
	}
}

func TestCheckerCaptureHelpersPropagatePanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "checker operation test failed") {
			t.Fatalf("checker panic was not propagated: %v", recovered)
		}
	}()
	checkedCheckerCall("test", func() bool { panic("synthetic checker failure") })
}

func semanticFactsTestSnapshot(t *testing.T) *ProgramSnapshot {
	t.Helper()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts": `
export interface Shape { readonly id?: number; value: string; }
export class Secret { #value = 1; reveal(): number { return this.#value; } }
let outer = 1;
export function mutate(value?: number, ...rest: number[]): number {
    outer += value ?? 0;
    return outer + rest.length;
}
declare const maybe: number | null;
declare const text: string;
export const asserted = text as unknown as number;
export const present = maybe!;
`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	if err := ValidateProgramSnapshot(*snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertSnapshotValidationError(t *testing.T, snapshot ProgramSnapshot, contains string) {
	t.Helper()
	wireErr := frontendwire.ValidateProgramSnapshot(snapshot)
	if wireErr == nil || !strings.Contains(wireErr.Error(), contains) {
		t.Fatalf("wire snapshot validation error = %v, want substring %q", wireErr, contains)
	}
	compatErr := ValidateProgramSnapshot(snapshot)
	if compatErr == nil || compatErr.Error() != wireErr.Error() {
		t.Fatalf("tsfrontend validator did not preserve wire error: got %v, want %v", compatErr, wireErr)
	}
}

func snapshotTestRequest(files map[string]string) (BuildRequest, *Frontend) {
	fs := vfstest.FromMap(files, true)
	frontend := NewFrontend(bundled.WrapFS(fs), bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
	return BuildRequest{
		ConfigPath:       "/project/tsconfig.json",
		CurrentDirectory: "/project",
		FileSystem:       fs,
	}, frontend
}

func rejectInternalPointerKinds(value reflect.Value, path string) error {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Map, reflect.Func, reflect.Chan:
		return &pointerKindError{path: path, kind: value.Kind()}
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return rejectInternalPointerKinds(value.Elem(), path+".(interface)")
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if err := rejectInternalPointerKinds(value.Field(index), path+"."+value.Type().Field(index).Name); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if err := rejectInternalPointerKinds(value.Index(index), path+"[]"); err != nil {
				return err
			}
		}
	}
	return nil
}

type pointerKindError struct {
	path string
	kind reflect.Kind
}

func (e *pointerKindError) Error() string {
	return e.path + " contains forbidden " + e.kind.String()
}
