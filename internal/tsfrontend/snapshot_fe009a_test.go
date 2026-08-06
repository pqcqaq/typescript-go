package tsfrontend

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestCaptureEffectRuleRegistryIsFailClosed(t *testing.T) {
	t.Parallel()
	if err := validateCaptureEffectRuleRegistry(); err != nil {
		t.Fatal(err)
	}
}

func TestSignatureEffectProofClassifiesRuntimeEffects(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
let state = 0;
class Box { constructor() {} }
export function primitive(left: number, right: number): number { return left + right; }
export function readState(): number { return state; }
export function writeState(value: number): number { state += value; return state; }
export function localOnly(value: number): number { let local = value; local++; return local; }
export function makeObject(): object { return {}; }
export function makeArray(): number[] { return []; }
export function makeClosure(): () => number { return () => state; }
export function makeBox(): Box { return new Box(); }
`)

	wantEffects := map[string][]string{
		"primitive":   {"pure"},
		"readState":   {"read"},
		"writeState":  {"read", "write"},
		"localOnly":   {"pure"},
		"makeObject":  {"alloc"},
		"makeArray":   {"alloc"},
		"makeClosure": {"alloc"},
		"makeBox":     {"alloc"},
	}
	for name, want := range wantEffects {
		signature := fe009aFunctionSignature(t, snapshot, name)
		if !slices.Equal(signature.Effects, want) {
			t.Errorf("%s effects = %v, want %v (proof %#v)", name, signature.Effects, want, signature.EffectProof)
		}
	}
	closure := fe009aFunctionSignature(t, snapshot, "makeClosure")
	if slices.Contains(closure.EffectProof.DirectEffects, "read") {
		t.Fatalf("nested closure body leaked into outer effect proof: %#v", closure.EffectProof)
	}
}

func TestSignatureEffectProofClassifiesRegexpAllocation(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function makeRegexp(): RegExp { return /effect/; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticCodeUnsupportedSyntax ||
		diagnostics[0].Stage != DiagnosticStageSubset || diagnostics[0].MessageKey != "subset.lowerer_unavailable" ||
		diagnostics[0].RequiredCapability != "runtime:regexp" {
		t.Fatalf("regexp diagnostics = %#v", diagnostics)
	}
	if err := ValidateProgramSnapshot(*snapshot); err != nil {
		t.Fatal(err)
	}
	signature := fe009aFunctionSignature(t, snapshot, "makeRegexp")
	if !slices.Equal(signature.Effects, []string{"alloc"}) {
		t.Fatalf("regexp effects = %v, proof=%#v", signature.Effects, signature.EffectProof)
	}
}

func TestSignatureEffectProofFailsClosedForUnmodeledRuntimeBehavior(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
class Accessor { get value(): number { return 1; } }
let destructuredState = 0;
export function spreadArray(input: number[]): number[] { return [...input]; }
export function iterate(input: number[]): number { let total = 0; for (const value of input) total += value; return total; }
export function destructure(input: { value: number }): number { const { value } = input; return value; }
export function destructureAlias(input: { source: number }): number { const { source: local } = input; return local; }
export function assignDestructure(input: { nested: { value: number } }): number { let result = 0; ({ nested: { value: result } } = input); return result; }
export function assignGlobal(input: { value: number }): number { ({ value: destructuredState } = input); return destructuredState; }
export function iterateDestructure(input: Array<[number, number]>): number { let left = 0, right = 0; for ([left, right] of input) {} return left + right; }
export function coerce(value: object): string { return "" + value; }
export function readAccessor(value: Accessor): number { return value.value; }
export function readElement(value: number[], index: number): number { return value[index]; }
export function typeQuery(value: number): number { type Value = typeof value; return value + 1; }
`)

	for _, name := range []string{"spreadArray", "iterate", "destructure", "destructureAlias", "assignDestructure", "assignGlobal", "iterateDestructure", "coerce", "readAccessor", "readElement"} {
		signature := fe009aFunctionSignature(t, snapshot, name)
		if !slices.Equal(signature.Effects, []string{"unknown"}) || signature.EffectProof.Complete {
			t.Errorf("%s did not fail closed: effects=%v proof=%#v", name, signature.Effects, signature.EffectProof)
		}
		if (name == "assignDestructure" || name == "iterateDestructure") && slices.Contains(signature.EffectProof.DirectEffects, "alloc") {
			t.Errorf("%s treated a destructuring pattern as allocation: %#v", name, signature.EffectProof)
		}
		if name == "assignGlobal" && !slices.Equal(signature.EffectProof.DirectEffects, []string{"read", "write"}) {
			t.Errorf("global destructuring effects = %v, want read/write", signature.EffectProof.DirectEffects)
		}
		if name == "destructureAlias" && len(signature.EffectProof.DirectEffects) != 0 {
			t.Errorf("static binding property key produced direct effects: %#v", signature.EffectProof)
		}
	}
	typeQuery := fe009aFunctionSignature(t, snapshot, "typeQuery")
	if !slices.Equal(typeQuery.Effects, []string{"pure"}) {
		t.Fatalf("type-only query polluted runtime effect proof: effects=%v proof=%#v", typeQuery.Effects, typeQuery.EffectProof)
	}
}

func TestSignatureEffectProofIgnoresJSDocTypeQuery(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"allowJs":true,"checkJs":true,"noEmit":true},"files":["main.js"]}`,
		"/project/main.js": `
let state = 0;
/** @returns {number} */
export function jsDocTypeQuery() {
    /** @type {typeof state} */
    const local = 1;
    return local + 1;
}
`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	signature := fe009aFunctionSignature(t, snapshot, "jsDocTypeQuery")
	if !slices.Equal(signature.Effects, []string{"pure"}) {
		t.Fatalf("JSDoc type query polluted runtime proof: %#v / effects=%v", signature.EffectProof, signature.Effects)
	}
}

func TestSignatureEffectProofFailsClosedForCallLikeSyntaxWithoutSignature(t *testing.T) {
	t.Parallel()

	t.Run("instanceof", func(t *testing.T) {
		snapshot := buildFE009aSnapshot(t, `
export function isInstance(value: object, constructor: Function): boolean {
    return value instanceof constructor;
}
`)
		signature := fe009aFunctionSignature(t, snapshot, "isInstance")
		if !slices.Equal(signature.Effects, []string{"unknown"}) || signature.EffectProof.Complete || len(signature.EffectProof.Calls) != 1 {
			t.Fatalf("instanceof effect proof = %#v / effects=%v", signature.EffectProof, signature.Effects)
		}
	})

	t.Run("jsx fragment", func(t *testing.T) {
		request, frontend := snapshotTestRequest(map[string]string{
			"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"jsx":"preserve"},"files":["main.tsx"]}`,
			"/project/main.tsx":      `export function jsxFragment() { return <></>; }`,
		})
		snapshot, diagnostics := frontend.Build(context.Background(), request)
		if snapshot == nil {
			t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
		}
		for _, diagnostic := range diagnostics {
			if diagnostic.Code == DiagnosticCodeInternalFailure {
				t.Fatalf("JSX fragment caused internal failure: %#v", diagnostics)
			}
		}
		if err := ValidateProgramSnapshot(*snapshot); err != nil {
			t.Fatal(err)
		}
		signature := fe009aFunctionSignature(t, snapshot, "jsxFragment")
		if !slices.Equal(signature.Effects, []string{"unknown"}) || signature.EffectProof.Complete {
			t.Fatalf("JSX fragment effect proof = %#v / effects=%v", signature.EffectProof, signature.Effects)
		}
	})
}

func TestUsingSyntaxDoesNotForgeTypeContext(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"skipLibCheck":true,"lib":["ES5","ESNext.Disposable"]},"files":["main.ts"]}`,
		"/project/main.ts":       `using resource = null; export const active = resource;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == DiagnosticCodeInternalFailure {
			t.Fatalf("using syntax caused internal failure: %#v", diagnostics)
		}
	}
}

func TestSignatureEffectProofClosesMutualRecursion(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
export function even(value: number): boolean { return value === 0 || odd(value - 1); }
export function odd(value: number): boolean { return value !== 0 && even(value - 1); }
`)
	for _, name := range []string{"even", "odd"} {
		signature := fe009aFunctionSignature(t, snapshot, name)
		if len(signature.EffectProof.Calls) != 1 || !slices.Equal(signature.Effects, []string{"pure"}) {
			t.Fatalf("%s mutual-recursion proof = %#v / effects=%v", name, signature.EffectProof, signature.Effects)
		}
	}
}

func TestSelectedOverloadOrdinalIsCallSiteLocal(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
function overloaded(value: string): number;
function overloaded(value: number): number;
function overloaded(value: string | number): number { return 1; }
class C {
    method(value: string): number;
    method(value: number): number;
    method(value: string | number): number { return 1; }
    static method(value: string): number;
    static method(value: number): number;
    static method(value: string | number): number { return 1; }
}
class A {
    constructor(value: string);
    constructor(value: number);
    constructor(value: string | number) {}
}
export function callAll(value: C): number {
    const first = overloaded(1);
    const second = overloaded("x");
    const instance = value.method(1);
    const statik = C.method("x");
    const constructed = new A(1);
    return first + second + instance + statik + (constructed ? 1 : 0);
}
`)
	file := snapshot.Files[0]
	byKind := map[string][]NodeSnapshot{}
	for _, node := range snapshot.Nodes {
		if node.File != file.ID || (node.Kind != "KindCallExpression" && node.Kind != "KindNewExpression") {
			continue
		}
		text := strings.TrimSpace(file.SourceBlob[node.Span.Start:node.Span.End])
		byKind[text] = append(byKind[text], node)
	}
	for _, source := range []string{"overloaded(1)", `overloaded("x")`, "value.method(1)", `C.method("x")`, "new A(1)"} {
		nodes := byKind[source]
		if len(nodes) != 1 {
			t.Fatalf("call %q nodes = %#v", source, nodes)
		}
		node := nodes[0]
		if node.SelectedSignature == 0 || node.SelectedOverloadOrdinal == 0 {
			t.Fatalf("call %q lacks selected overload proof: %#v", source, node)
		}
	}
	if got := byKind["overloaded(1)"][0].SelectedOverloadOrdinal; got != 2 {
		t.Fatalf("number overload ordinal = %d, want 2", got)
	}
	if got := byKind[`overloaded("x")`][0].SelectedOverloadOrdinal; got != 1 {
		t.Fatalf("string overload ordinal = %d, want 1", got)
	}
	if got := byKind["value.method(1)"][0].SelectedOverloadOrdinal; got != 2 {
		t.Fatalf("instance method overload ordinal = %d, want 2", got)
	}
	if got := byKind[`C.method("x")`][0].SelectedOverloadOrdinal; got != 1 {
		t.Fatalf("static method overload ordinal = %d, want 1", got)
	}
	if got := byKind["new A(1)"][0].SelectedOverloadOrdinal; got != 2 {
		t.Fatalf("constructor overload ordinal = %d, want 2", got)
	}
}

func TestOverloadEffectProofUsesDeclarationOwner(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
let state = 0;
class First {
    constructor(value: string);
    constructor(value: number);
    constructor(value: string | number) { state += 1; }
    method(value: string): number;
    method(value: number): number;
    method(value: string | number): number { state += 1; return state; }
    static method(value: string): number;
    static method(value: number): number;
    static method(value: string | number): number { state += 1; return state; }
}
class Second {
    constructor(value: string);
    constructor(value: number);
    constructor(value: string | number) {}
}
export function callAll(value: First): number {
    const instance = value.method(1);
    const statik = First.method(1);
    return instance + statik + (new First(1) ? 1 : 0);
}
`)
	nodes := fe009aNodeIndex(snapshot)
	signatures := make(map[SignatureID]SignatureSnapshot, len(snapshot.Signatures))
	for _, signature := range snapshot.Signatures {
		signatures[signature.ID] = signature
	}
	var firstConstructor SignatureSnapshot
	for _, callText := range []string{"value.method(1)", "First.method(1)", "new First(1)"} {
		call := fe009aNodeBySourceText(t, snapshot, callText)
		target := signatures[call.SelectedSignature]
		implementation := nodes[target.EffectProof.Implementation]
		if target.EffectProof.Kind != "body-resolved" || implementation.ID == "" || !slices.Equal(target.Effects, []string{"read", "write"}) {
			t.Fatalf("call %q overload proof = %#v / effects=%v", callText, target.EffectProof, target.Effects)
		}
		if call.Kind == "KindNewExpression" {
			if implementation.Kind != "KindConstructor" {
				t.Fatalf("constructor call implementation = %#v", implementation)
			}
			firstConstructor = target
		} else if implementation.Kind != "KindMethodDeclaration" {
			t.Fatalf("method call implementation = %#v", implementation)
		}
	}

	secondImplementation := NodeID("")
	for _, node := range snapshot.Nodes {
		if node.Kind != "KindConstructor" || !slices.ContainsFunc(node.NamedChildren, func(child NamedChildSnapshot) bool {
			return child.Role == "body"
		}) {
			continue
		}
		parent := nodes[node.Parent]
		if strings.HasPrefix(strings.TrimSpace(fe009aNodeSourceText(snapshot, parent)), "class Second") {
			secondImplementation = node.ID
			break
		}
	}
	if firstConstructor.ID == 0 || secondImplementation == "" {
		t.Fatalf("constructor proof fixtures are incomplete: first=%#v second=%q", firstConstructor, secondImplementation)
	}
	broken := fe009aCloneSnapshot(snapshot)
	index := fe009aSignatureIndex(broken.Signatures, firstConstructor.ID)
	broken.Signatures[index].EffectProof.Implementation = secondImplementation
	broken.Signatures[index].EffectProof.DirectEffects = nil
	broken.Signatures[index].EffectProof.Complete = true
	broken.Signatures[index].Effects = []string{"pure"}
	fe009aFinalizeMutation(t, &broken)
	assertSnapshotValidationError(t, broken, "does not match declaration")
}

func TestSignatureCanonicalHashIgnoresUnrelatedCallSites(t *testing.T) {
	t.Parallel()
	base := buildFE009aSnapshot(t, `
export function overloaded(value: string): number;
export function overloaded(value: number): number;
export function overloaded(value: string | number): number { return 1; }
`)
	withCall := buildFE009aSnapshot(t, `
export function overloaded(value: string): number;
export function overloaded(value: number): number;
export function overloaded(value: string | number): number { return 1; }
export const unrelated = overloaded(1);
`)
	baseHashes := fe009aSignatureHashesByDeclarationText(base)
	callHashes := fe009aSignatureHashesByDeclarationText(withCall)
	if !maps.Equal(baseHashes, callHashes) {
		t.Fatalf("declaration signature hashes changed after unrelated call: base=%v withCall=%v", baseHashes, callHashes)
	}
}

func TestSignatureEffectProofClosesResolvedCalls(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
let state = 0;
export function writeState(value: number): number { state += value; return state; }
export function callWrite(value: number): number { return writeState(value); }
export function recursive(value: number): number { return value <= 0 ? 0 : recursive(value - 1); }
declare function external(value: number): number;
export function callExternal(value: number): number { return external(value); }
`)

	writeState := fe009aFunctionSignature(t, snapshot, "writeState")
	callWrite := fe009aFunctionSignature(t, snapshot, "callWrite")
	recursive := fe009aFunctionSignature(t, snapshot, "recursive")
	callExternal := fe009aFunctionSignature(t, snapshot, "callExternal")
	if !slices.Equal(writeState.EffectProof.DirectEffects, []string{"read", "write"}) || !slices.Equal(writeState.Effects, []string{"read", "write"}) {
		t.Fatalf("writeState effect proof = %#v / %#v", writeState.EffectProof, writeState.Effects)
	}
	if len(callWrite.EffectProof.Calls) != 1 || callWrite.EffectProof.Calls[0].Signature != writeState.ID || !slices.Equal(callWrite.Effects, []string{"read", "write"}) {
		t.Fatalf("callWrite effect closure = %#v / %#v", callWrite.EffectProof, callWrite.Effects)
	}
	if len(recursive.EffectProof.Calls) != 1 || recursive.EffectProof.Calls[0].Signature != recursive.ID || !slices.Equal(recursive.Effects, []string{"pure"}) {
		t.Fatalf("recursive effect closure = %#v / %#v", recursive.EffectProof, recursive.Effects)
	}
	if len(callExternal.EffectProof.Calls) != 1 || !slices.Equal(callExternal.Effects, []string{"unknown"}) {
		t.Fatalf("external effect closure = %#v / %#v", callExternal.EffectProof, callExternal.Effects)
	}
}

func TestSignatureEffectProofResolvesOverloadImplementation(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
let state = 0;
export function overloaded(value: string): number;
export function overloaded(value: number): number;
export function overloaded(value: string | number): number { state += 1; return state; }
export function callOverload(value: number): number { return overloaded(value); }
`)
	caller := fe009aFunctionSignature(t, snapshot, "callOverload")
	if len(caller.EffectProof.Calls) != 1 {
		t.Fatalf("overload caller proof = %#v", caller.EffectProof)
	}
	targetIndex := fe009aSignatureIndex(snapshot.Signatures, caller.EffectProof.Calls[0].Signature)
	if targetIndex < 0 {
		t.Fatalf("selected overload signature %d is missing", caller.EffectProof.Calls[0].Signature)
	}
	target := snapshot.Signatures[targetIndex]
	if target.EffectProof.Kind != "body-resolved" || target.EffectProof.Implementation == "" || !slices.Equal(target.Effects, []string{"read", "write"}) || !slices.Equal(caller.Effects, []string{"read", "write"}) {
		t.Fatalf("overload implementation proof = %#v / target=%v caller=%v", target.EffectProof, target.Effects, caller.Effects)
	}
}

func TestValidateSignatureEffectProofRejectsRehashedCorruption(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
let state = 0;
export function writeState(value: number): number { state += value; return state; }
export function callWrite(value: number): number { return writeState(value); }
export function pure(value: number): number { return value; }
`)
	writeState := fe009aFunctionSignature(t, snapshot, "writeState")
	callWrite := fe009aFunctionSignature(t, snapshot, "callWrite")
	pure := fe009aFunctionSignature(t, snapshot, "pure")

	t.Run("closure", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		index := fe009aSignatureIndex(broken.Signatures, callWrite.ID)
		broken.Signatures[index].Effects = []string{"pure"}
		fe009aFinalizeMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "effect closure mismatch")
	})
	t.Run("missing call edge", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		index := fe009aSignatureIndex(broken.Signatures, callWrite.ID)
		broken.Signatures[index].EffectProof.Calls = nil
		broken.Signatures[index].Effects = []string{"pure"}
		fe009aFinalizeMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "effect call proof count")
	})
	t.Run("retargeted call edge", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		index := fe009aSignatureIndex(broken.Signatures, callWrite.ID)
		broken.Signatures[index].EffectProof.Calls[0].Signature = pure.ID
		broken.Signatures[index].Effects = []string{"pure"}
		fe009aFinalizeMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "does not match call node signature")
	})
	t.Run("deleted direct write", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		index := fe009aSignatureIndex(broken.Signatures, writeState.ID)
		broken.Signatures[index].EffectProof.DirectEffects = nil
		broken.Signatures[index].Effects = []string{"pure"}
		fe009aFinalizeMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "direct effect proof mismatch")
	})
}

func TestCaptureCompleteRespectsFunctionScope(t *testing.T) {
	t.Parallel()
	snapshot := buildFE009aSnapshot(t, `
export function ordinary(this: { value: number }, value: number): () => number {
    return () => this.value + arguments.length + (new.target ? value : 0);
}
class Base { value(): number { return 1; } }
export class Derived extends Base {
    method(): () => number { return () => super.value(); }
}
`)
	ordinary := fe009aFunctionNode(t, snapshot, "ordinary")
	if !ordinary.CaptureComplete || len(ordinary.CaptureBindings) != 0 || len(ordinary.CaptureSet) != 0 {
		t.Fatalf("ordinary capture proof = %#v", ordinary)
	}

	wantSpecial := map[string]bool{"arguments": false, "new.target": false, "super": false, "this": false}
	arrowCount := 0
	for _, node := range snapshot.Nodes {
		if node.Kind != "KindArrowFunction" {
			continue
		}
		arrowCount++
		if !node.CaptureComplete {
			t.Fatalf("arrow has no complete capture proof: %#v", node)
		}
		for _, binding := range node.CaptureBindings {
			if _, tracked := wantSpecial[binding.Kind]; tracked {
				wantSpecial[binding.Kind] = true
			}
		}
	}
	if arrowCount != 2 {
		t.Fatalf("arrow count = %d", arrowCount)
	}
	for kind, found := range wantSpecial {
		if !found {
			t.Fatalf("lexical capture %q is missing", kind)
		}
	}

	t.Run("missing completeness", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		index := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool { return node.ID == ordinary.ID })
		broken.Nodes[index].CaptureComplete = false
		fe009aFinalizeMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "complete capture proof")
	})
	t.Run("facts on non-function", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		index := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool { return node.Kind == "KindBinaryExpression" })
		if index < 0 {
			t.Fatal("fixture has no binary expression")
		}
		broken.Nodes[index].CaptureComplete = true
		broken.Nodes[index].CaptureBindings = []CaptureBindingSnapshot{{Kind: "this", Access: "read"}}
		fe009aFinalizeMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "not function-like")
	})
	t.Run("ordinary lexical this", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		index := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool { return node.ID == ordinary.ID })
		broken.Nodes[index].CaptureBindings = []CaptureBindingSnapshot{{Kind: "this", Access: "read"}}
		fe009aFinalizeMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "captures lexical this")
	})
}

func TestModuleEdgesCarryExactBindingProofs(t *testing.T) {
	t.Parallel()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"module":"esnext","moduleResolution":"bundler"},"files":["main.ts"]}`,
		"/project/main.ts": `
import primary, { type Shape as ImportedShape, value as localValue } from "./dep";
import * as namespaceValue from "./dep";
export { type Shape as ExportedShape, value as exportedValue } from "./dep";
export * as exportedNamespace from "./dep";
export * from "./dep";
void primary; void localValue; void namespaceValue;
`,
		"/project/dep.ts": `export default 1; export interface Shape { value: number } export const value = 2;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}

	mixedImportIndex := slices.IndexFunc(snapshot.ModuleEdges, func(edge ModuleEdge) bool {
		return slices.ContainsFunc(edge.Bindings, func(binding ModuleBindingSnapshot) bool { return binding.Kind == "default-import" })
	})
	if mixedImportIndex < 0 {
		t.Fatalf("mixed import edge is missing: %#v", snapshot.ModuleEdges)
	}
	mixedImport := snapshot.ModuleEdges[mixedImportIndex]
	if !mixedImport.BindingsComplete || mixedImport.Source == "" || mixedImport.SpecifierNode == "" || mixedImport.TypeOnly || !mixedImport.Value || len(mixedImport.Bindings) != 3 {
		t.Fatalf("mixed import proof = %#v", mixedImport)
	}
	for _, binding := range mixedImport.Bindings {
		if binding.AliasSymbol == "" || binding.TargetSymbol == "" || binding.TypeOnly == binding.Value {
			t.Fatalf("incomplete mixed import binding = %#v", binding)
		}
	}
	wantKinds := []string{"default-import", "named-import", "named-import", "named-reexport", "named-reexport", "namespace-import", "namespace-reexport", "export-star"}
	gotKinds := make([]string, 0)
	for _, edge := range snapshot.ModuleEdges {
		for _, binding := range edge.Bindings {
			gotKinds = append(gotKinds, binding.Kind)
		}
	}
	slices.Sort(gotKinds)
	slices.Sort(wantKinds)
	if !slices.Equal(gotKinds, wantKinds) {
		t.Fatalf("module binding kinds = %v, want %v", gotKinds, wantKinds)
	}

	t.Run("aggregate mismatch", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		broken.ModuleEdges[mixedImportIndex].TypeOnly = true
		broken.ModuleEdges[mixedImportIndex].Value = false
		fe009aFinalizeModuleMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "aggregate flags")
	})
	t.Run("malformed binding", func(t *testing.T) {
		broken := fe009aCloneSnapshot(snapshot)
		broken.ModuleEdges[mixedImportIndex].Bindings[0].LocalName = "corrupt"
		fe009aFinalizeModuleMutation(t, &broken)
		assertSnapshotValidationError(t, broken, "names or node Kind")
	})
}

func buildFE009aSnapshot(t *testing.T, source string) *ProgramSnapshot {
	t.Helper()
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       source,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	return snapshot
}

func fe009aFunctionNode(t *testing.T, snapshot *ProgramSnapshot, name string) NodeSnapshot {
	t.Helper()
	nodes := fe009aNodeIndex(snapshot)
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindFunctionDeclaration" && childText(node, "name", nodes) == name {
			return node
		}
	}
	t.Fatalf("function %q is missing", name)
	return NodeSnapshot{}
}

func fe009aFunctionSignature(t *testing.T, snapshot *ProgramSnapshot, name string) SignatureSnapshot {
	t.Helper()
	node := fe009aFunctionNode(t, snapshot, name)
	for _, signature := range snapshot.Signatures {
		if signature.Declaration == node.ID {
			return signature
		}
	}
	t.Fatalf("signature for function %q is missing", name)
	return SignatureSnapshot{}
}

func fe009aNodeIndex(snapshot *ProgramSnapshot) map[NodeID]NodeSnapshot {
	result := make(map[NodeID]NodeSnapshot, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		result[node.ID] = node
	}
	return result
}

func fe009aSignatureIndex(signatures []SignatureSnapshot, id SignatureID) int {
	return slices.IndexFunc(signatures, func(signature SignatureSnapshot) bool { return signature.ID == id })
}

func fe009aNodeBySourceText(t *testing.T, snapshot *ProgramSnapshot, text string) NodeSnapshot {
	t.Helper()
	for _, node := range snapshot.Nodes {
		if strings.TrimSpace(fe009aNodeSourceText(snapshot, node)) == text {
			return node
		}
	}
	t.Fatalf("node with source text %q is missing", text)
	return NodeSnapshot{}
}

func fe009aNodeSourceText(snapshot *ProgramSnapshot, node NodeSnapshot) string {
	for _, file := range snapshot.Files {
		if file.ID == node.File && node.Span.Start >= 0 && node.Span.End >= node.Span.Start && node.Span.End <= len(file.SourceBlob) {
			return file.SourceBlob[node.Span.Start:node.Span.End]
		}
	}
	return ""
}

func fe009aSignatureHashesByDeclarationText(snapshot *ProgramSnapshot) map[string]string {
	nodes := fe009aNodeIndex(snapshot)
	files := make(map[FileID]string, len(snapshot.Files))
	for _, file := range snapshot.Files {
		files[file.ID] = file.SourceBlob
	}
	result := make(map[string]string)
	for _, signature := range snapshot.Signatures {
		declaration, ok := nodes[signature.Declaration]
		if !ok || declaration.Kind != "KindFunctionDeclaration" || childText(declaration, "name", nodes) != "overloaded" {
			continue
		}
		source := files[declaration.File]
		result[source[declaration.Span.Start:declaration.Span.End]] = signature.CanonicalHash
	}
	return result
}

func fe009aCloneSnapshot(snapshot *ProgramSnapshot) ProgramSnapshot {
	result := *snapshot
	result.Nodes = slices.Clone(snapshot.Nodes)
	for index := range result.Nodes {
		result.Nodes[index].CaptureSet = slices.Clone(snapshot.Nodes[index].CaptureSet)
		result.Nodes[index].CaptureBindings = slices.Clone(snapshot.Nodes[index].CaptureBindings)
	}
	result.Signatures = slices.Clone(snapshot.Signatures)
	for index := range result.Signatures {
		result.Signatures[index].Effects = slices.Clone(snapshot.Signatures[index].Effects)
		result.Signatures[index].EffectProof.DirectEffects = slices.Clone(snapshot.Signatures[index].EffectProof.DirectEffects)
		result.Signatures[index].EffectProof.Calls = slices.Clone(snapshot.Signatures[index].EffectProof.Calls)
	}
	result.ModuleEdges = slices.Clone(snapshot.ModuleEdges)
	for index := range result.ModuleEdges {
		result.ModuleEdges[index].Bindings = slices.Clone(snapshot.ModuleEdges[index].Bindings)
		result.ModuleEdges[index].ImportAttributes = slices.Clone(snapshot.ModuleEdges[index].ImportAttributes)
	}
	return result
}

func fe009aFinalizeMutation(t *testing.T, snapshot *ProgramSnapshot) {
	t.Helper()
	if err := finalizeSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
}

func fe009aFinalizeModuleMutation(t *testing.T, snapshot *ProgramSnapshot) {
	t.Helper()
	snapshot.ModuleGraphDigest = digestModuleGraph(snapshot.Modules, snapshot.ModuleEdges, snapshot.ModuleSCCs)
	fe009aFinalizeMutation(t, snapshot)
}
