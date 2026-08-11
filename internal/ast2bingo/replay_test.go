package ast2bingo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/tsfrontend"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

func TestReplaySerializedAddProducesVerifiedHIR(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)
	serialized, err := snapshot.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}

	first, err := ReplaySerializedSnapshot(serialized, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplaySerializedSnapshot(serialized, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("serialized replay is not deterministic:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if err := bingo.VerifyHIR(first.HIR); err != nil {
		t.Fatalf("verify replayed HIR: %v", err)
	}
	if !isDigest(first.HIR.ContentHash) || !isDigest(first.ContentHash) {
		t.Fatalf("replay hashes are incomplete: HIR=%q replay=%q", first.HIR.ContentHash, first.ContentHash)
	}
	requirementsDigest, err := bingo.LogicalCapabilityRequirementsDigest([]bingo.RuntimeCapabilityID{})
	if err != nil {
		t.Fatal(err)
	}
	if first.FrontendSnapshotHash != snapshot.ContentHash || first.HIR.Provenance != primitiveHIRProvenance(*snapshot, identity, requirementsDigest) {
		t.Fatalf("replay frontend provenance = %#v / %q", first.HIR.Provenance, first.FrontendSnapshotHash)
	}
	_, wantHIRHash, err := bingo.CanonicalHIR(first.HIR)
	if err != nil {
		t.Fatal(err)
	}
	if first.HIR.ContentHash != wantHIRHash {
		t.Fatalf("HIR content hash = %q, want %q", first.HIR.ContentHash, wantHIRHash)
	}

	if len(first.HIR.Functions) != 1 {
		t.Fatalf("HIR functions = %#v", first.HIR.Functions)
	}
	function := first.HIR.Functions[0]
	if function.Name != "add" || function.ReturnType != bingo.TypeNumber {
		t.Fatalf("HIR function = %#v", function)
	}
	if len(function.Parameters) != 2 {
		t.Fatalf("HIR parameters = %#v", function.Parameters)
	}
	for index, parameter := range function.Parameters {
		wantName := []string{"left", "right"}[index]
		if parameter.Name != wantName || parameter.Type != bingo.TypeNumber || parameter.Value != bingo.ValueID(index+1) {
			t.Fatalf("HIR parameter %d = %#v", index, parameter)
		}
	}
	if len(function.Blocks) != 1 || len(function.Blocks[0].Operations) != 1 {
		t.Fatalf("HIR blocks = %#v", function.Blocks)
	}
	operation := function.Blocks[0].Operations[0]
	if operation.Kind != "binary" || operation.Operator != "+" || operation.Type != bingo.TypeNumber || operation.Effect != bingo.EffectPure ||
		!slices.Equal(operation.Operands, []bingo.ValueID{1, 2}) {
		t.Fatalf("HIR add operation = %#v", operation)
	}
	if terminator := function.Blocks[0].Terminator; terminator.Kind != "return" || terminator.Value != operation.ID {
		t.Fatalf("HIR return terminator = %#v", terminator)
	}
	if !slices.ContainsFunc(first.Events, func(event LoweringEvent) bool {
		return event.Kind == "binary.add" && event.Operator == "+" && len(event.Inputs) == 2
	}) {
		t.Fatalf("lowering events have no add operation: %#v", first.Events)
	}
	wantEventKinds := []string{"function.begin", "parameter", "parameter", "binary.add", "return", "function.end"}
	gotEventKinds := make([]string, len(first.Events))
	for index, event := range first.Events {
		gotEventKinds[index] = event.Kind
	}
	if !slices.Equal(gotEventKinds, wantEventKinds) {
		t.Fatalf("lowering events are not in evaluation order: got %v, want %v", gotEventKinds, wantEventKinds)
	}
}

func TestReplayApplicationMainBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		numberBits string
	}{
		{name: "zero", source: `export function main(): number { return 0; }`, numberBits: "0000000000000000"},
		{name: "one", source: `export function main(): number { return 1; }`, numberBits: "3ff0000000000000"},
		{name: "maximum", source: `export function main(): number { return 255; }`, numberBits: "406fe00000000000"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request, frontend := replayTestRequest(map[string]string{
				"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
				"/project/main.ts":       test.source,
			})
			snapshot, diagnostics := frontend.Build(context.Background(), request)
			if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
				t.Fatalf("application snapshot diagnostics = %#v", diagnostics)
			}
			result, err := ReplaySnapshot(*snapshot, testCompilerIdentity(t, *snapshot))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.HIR.Functions) != 1 {
				t.Fatalf("application HIR functions = %#v", result.HIR.Functions)
			}
			function := result.HIR.Functions[0]
			if function.Name != "main" || !function.Exported || len(function.Parameters) != 0 || function.ReturnType != bingo.TypeNumber || len(function.Blocks) != 1 || len(function.Blocks[0].Operations) != 1 {
				t.Fatalf("application main HIR = %#v", function)
			}
			if operation := function.Blocks[0].Operations[0]; operation.Kind != "number.constant" || operation.NumberBits != test.numberBits {
				t.Fatalf("application main constant = %#v", operation)
			}
		})
	}
}

func TestReplayRejectsParameterlessNonApplicationFunction(t *testing.T) {
	request, frontend := replayTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function compute(): number { return 0; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot diagnostics = %#v", diagnostics)
	}
	if _, err := ReplaySnapshot(*snapshot, testCompilerIdentity(t, *snapshot)); err == nil || !strings.Contains(err.Error(), "only exported application main may be parameterless") {
		t.Fatalf("parameterless non-application function error = %v", err)
	}
}

func TestReplayApplicationMainRejectsUnsupportedSourceShapes(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "non-exported", source: `function main(): number { return 0; }`, want: "type is not lowerable"},
		{name: "parameterized", source: `export function main(value: number): number { return value; }`, want: "parameterless main"},
		{name: "negative", source: `export function main(): number { return -1; }`, want: "must return a numeric literal"},
		{name: "fractional", source: `export function main(): number { return 1.5; }`, want: "canonical integer from 0 through 255"},
		{name: "out of range", source: `export function main(): number { return 256; }`, want: "canonical integer from 0 through 255"},
		{name: "non-literal", source: `export function main(): number { const value = 1; return value; }`, want: "primitive variable initializer"},
		{name: "NaN", source: `export function main(): number { return NaN; }`, want: "no resolved declaration"},
		{name: "Infinity", source: `export function main(): number { return Infinity; }`, want: "no resolved declaration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, frontend := replayTestRequest(map[string]string{
				"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
				"/project/main.ts":       test.source,
			})
			snapshot, diagnostics := frontend.Build(context.Background(), request)
			if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
				t.Fatalf("unexpected frontend diagnostics = %#v", diagnostics)
			}
			if _, err := ReplaySnapshot(*snapshot, testCompilerIdentity(t, *snapshot)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("application source rejection = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReplayCommittedLocalAssignmentAndDirectCallSnapshot(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/calllocal/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity := testCompilerIdentity(t, frontend.Program)
	result, err := ReplayFrontendSnapshot(data, identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.HIR.Functions) != 2 || result.HIR.Functions[0].Name != "add" || result.HIR.Functions[0].Exported || result.HIR.Functions[1].Name != "compute" || !result.HIR.Functions[1].Exported {
		t.Fatalf("local-call HIR functions = %#v", result.HIR.Functions)
	}
	operations := result.HIR.Functions[1].Blocks[0].Operations
	if len(operations) != 2 || operations[0].Kind != "call" || operations[0].Callee != 1 || operations[0].Effect != bingo.EffectCall || operations[1].Kind != "binary" || !slices.Equal(operations[1].Operands, []bingo.ValueID{3, 2}) {
		t.Fatalf("local-call HIR operations = %#v", operations)
	}
	wantEvents := []string{"function.begin", "parameter", "parameter", "binary.add", "return", "function.end", "function.begin", "parameter", "parameter", "call.direct", "local.bind", "binary.add", "local.assign", "return", "function.end"}
	gotEvents := make([]string, len(result.Events))
	for index, event := range result.Events {
		gotEvents[index] = event.Kind
	}
	if !slices.Equal(gotEvents, wantEvents) || result.Events[9].Callee != 1 {
		t.Fatalf("local-call evaluation events = %#v", result.Events)
	}
}

func TestReplayCommittedCoalesceSnapshotProducesGuardedHIR(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/coalesce/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity := testCompilerIdentity(t, frontend.Program)
	result, err := ReplayFrontendSnapshot(data, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := bingo.VerifyCanonicalPhase2HIR(result.HIR); err != nil {
		t.Fatal(err)
	}
	if len(result.HIR.Functions) != 1 {
		t.Fatalf("coalesce HIR functions = %#v", result.HIR.Functions)
	}
	function := result.HIR.Functions[0]
	if function.Name != "coalesce" || len(function.Parameters) != 2 || function.Parameters[0].Type != bingo.TypeNullableNumber || function.Parameters[1].Type != bingo.TypeNumber || len(function.Blocks) != 4 {
		t.Fatalf("coalesce HIR function = %#v", function)
	}
	if function.Blocks[0].Operations[0].Kind != "is_nullish" || function.Blocks[2].Operations[0].Kind != "unwrap_nullable" || function.Blocks[3].Operations[0].Kind != "phi" || !slices.Equal(function.Blocks[3].Operations[0].IncomingBlocks, []bingo.BlockID{2, 3}) {
		t.Fatalf("coalesce guarded CFG = %#v", function.Blocks)
	}
	wantEvents := []string{"function.begin", "parameter", "parameter", "nullish.test", "nullish.fallback", "nullable.unwrap", "phi", "return", "function.end"}
	gotEvents := make([]string, len(result.Events))
	for index, event := range result.Events {
		gotEvents[index] = event.Kind
	}
	if !slices.Equal(gotEvents, wantEvents) {
		t.Fatalf("coalesce evaluation events = %v, want %v", gotEvents, wantEvents)
	}
}

func TestReplayCommittedCoalesceAssignSnapshotProducesSingleEvaluationHIR(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/coalesceassign/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity := testCompilerIdentity(t, frontend.Program)
	result, err := ReplayFrontendSnapshot(data, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := bingo.VerifyCanonicalPhase2HIR(result.HIR); err != nil {
		t.Fatal(err)
	}
	function := result.HIR.Functions[0]
	if function.Name != "coalesceAssign" || function.Parameters[0].Type != bingo.TypeNullableNumber || len(function.Blocks) != 4 || function.Blocks[0].Operations[0].Kind != "is_nullish" || function.Blocks[2].Operations[0].Kind != "unwrap_nullable" || function.Blocks[3].Operations[0].Kind != "phi" {
		t.Fatalf("coalesce assignment HIR = %#v", function)
	}
	wantEvents := []string{"function.begin", "parameter", "parameter", "logical.assign.test", "logical.assign.store", "nullable.unwrap", "phi", "return", "function.end"}
	gotEvents := make([]string, len(result.Events))
	for index, event := range result.Events {
		gotEvents[index] = event.Kind
	}
	if !slices.Equal(gotEvents, wantEvents) {
		t.Fatalf("coalesce assignment events = %v, want %v", gotEvents, wantEvents)
	}
}

func TestReplayCommittedClassifySnapshotProducesLiteralAndMultiReturnHIR(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/classify/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity := testCompilerIdentity(t, frontend.Program)
	first, err := ReplayFrontendSnapshot(data, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayFrontendSnapshot(data, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("classify replay is not deterministic")
	}
	if err := bingo.VerifyCanonicalPhase2HIR(first.HIR); err != nil {
		t.Fatal(err)
	}
	function := first.HIR.Functions[0]
	if function.Name != "classify" || !function.Exported || function.ReturnType != bingo.TypeNumber || len(function.Parameters) != 1 || function.Parameters[0].Name != "value" || len(function.Blocks) != 5 {
		t.Fatalf("classify HIR function = %#v", function)
	}
	if function.Blocks[0].Operations[0].Kind != "number.constant" || function.Blocks[0].Operations[0].NumberBits != "0000000000000000" || function.Blocks[0].Operations[1].Kind != "compare" || !slices.Equal(function.Blocks[0].Terminator.Successors, []bingo.BlockID{2, 3}) {
		t.Fatalf("classify first branch = %#v", function.Blocks[0])
	}
	if function.Blocks[1].Operations[0].NumberBits != "3ff0000000000000" || function.Blocks[1].Operations[1].Kind != "unary" || function.Blocks[1].Operations[1].Operator != "-" || function.Blocks[1].Terminator.Value != 5 {
		t.Fatalf("classify negative return = %#v", function.Blocks[1])
	}
	if function.Blocks[2].Operations[0].NumberBits != "3ff0000000000000" || function.Blocks[2].Operations[1].Kind != "compare" || !slices.Equal(function.Blocks[2].Terminator.Successors, []bingo.BlockID{4, 5}) || function.Blocks[3].Terminator.Value != 8 || function.Blocks[4].Terminator.Value != 9 {
		t.Fatalf("classify second branch = %#v", function.Blocks[2:])
	}
	wantEvents := []string{"function.begin", "parameter", "literal.number", "if.condition", "literal.number", "unary.negate", "return", "literal.number", "if.condition", "literal.number", "return", "literal.number", "return", "function.end"}
	gotEvents := make([]string, len(first.Events))
	for index, event := range first.Events {
		gotEvents[index] = event.Kind
	}
	if !slices.Equal(gotEvents, wantEvents) {
		t.Fatalf("classify events = %v, want %v", gotEvents, wantEvents)
	}
}

func TestReplayCommittedStringLengthSnapshotProducesUTF16HIR(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/stringlength/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity := testCompilerIdentity(t, frontend.Program)
	result, err := ReplayFrontendSnapshot(data, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := bingo.VerifyCanonicalPhase2HIR(result.HIR); err != nil {
		t.Fatal(err)
	}
	if len(result.HIR.Functions) != 1 {
		t.Fatalf("string length HIR functions = %#v", result.HIR.Functions)
	}
	function := result.HIR.Functions[0]
	if function.Name != "stringLength" || !function.Exported || function.ReturnType != bingo.TypeNumber || len(function.Parameters) != 1 || function.Parameters[0].Type != bingo.TypeString || len(function.Blocks) != 1 {
		t.Fatalf("string length HIR function = %#v", function)
	}
	operation := function.Blocks[0].Operations[0]
	if operation.ID != 2 || operation.Kind != "string.length" || operation.Type != bingo.TypeNumber || !slices.Equal(operation.Operands, []bingo.ValueID{1}) || function.Blocks[0].Terminator.Value != 2 {
		t.Fatalf("string length HIR operation = %#v", operation)
	}
	wantEvents := []string{"function.begin", "parameter", "string.length", "return", "function.end"}
	gotEvents := make([]string, len(result.Events))
	for index, event := range result.Events {
		gotEvents[index] = event.Kind
	}
	if !slices.Equal(gotEvents, wantEvents) {
		t.Fatalf("string length events = %v, want %v", gotEvents, wantEvents)
	}
}

func TestReplayCoalesceAssignRejectsRehashedReturnBindingTampering(t *testing.T) {
	base := buildReplayCoalesceAssignSnapshot(t)
	identity := testCompilerIdentity(t, *base)
	broken := cloneReplaySnapshot(t, base)
	nodeIndexes := make(map[NodeID]int, len(broken.Nodes))
	for index, node := range broken.Nodes {
		nodeIndexes[node.ID] = index
	}
	assignmentIndex := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == snapshotKindBinaryExpression && node.SyntaxPayload.Operator == snapshotKindQuestionQuestionEqualsToken
	})
	returnIndex := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindReturnStatement })
	if assignmentIndex < 0 || returnIndex < 0 {
		t.Fatal("coalesce assignment snapshot is missing its assignment or return")
	}
	rightIndex, ok := nodeIndexes[childByRole(broken.Nodes[assignmentIndex], "right")]
	if !ok {
		t.Fatal("coalesce assignment snapshot is missing its fallback")
	}
	returnValueIndex, ok := nodeIndexes[childByRole(broken.Nodes[returnIndex], "expression")]
	if !ok {
		t.Fatal("coalesce assignment snapshot is missing its return value")
	}
	broken.Nodes[returnValueIndex].Symbol = broken.Nodes[rightIndex].Symbol
	broken.Nodes[returnValueIndex].ResolvedSymbol = broken.Nodes[rightIndex].ResolvedSymbol
	if err := finalizeTestSnapshot(&broken); err != nil {
		t.Fatal(err)
	}
	result, err := ReplaySnapshot(broken, identity)
	if err == nil || !strings.Contains(err.Error(), "return does not read the assigned local exactly once") {
		t.Fatalf("rehashed coalesce assignment return tamper result/error = %#v / %v", result, err)
	}
	if len(result.Events) != 0 || len(result.HIR.Functions) != 0 {
		t.Fatalf("rejected coalesce assignment tamper emitted partial HIR: %#v", result)
	}
}

func TestReplaySerializedAddUsesResolvedSymbolFallback(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)
	copy := *snapshot
	copy.Nodes = slices.Clone(snapshot.Nodes)

	binaryIndex := slices.IndexFunc(copy.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == snapshotKindBinaryExpression
	})
	if binaryIndex < 0 {
		t.Fatal("add snapshot has no binary expression")
	}
	operandID := childByRole(copy.Nodes[binaryIndex], "left")
	operandIndex := slices.IndexFunc(copy.Nodes, func(node NodeSnapshot) bool { return node.ID == operandID })
	if operandIndex < 0 {
		t.Fatalf("left operand %q is missing", operandID)
	}
	if copy.Nodes[operandIndex].ResolvedSymbol == "" {
		t.Fatalf("left operand has no resolved symbol: %#v", copy.Nodes[operandIndex])
	}
	copy.Nodes[operandIndex].Symbol = ""
	if err := finalizeTestSnapshot(&copy); err != nil {
		t.Fatal(err)
	}
	serialized, err := copy.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	result, err := ReplaySerializedSnapshot(serialized, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := bingo.VerifyHIR(result.HIR); err != nil {
		t.Fatal(err)
	}
	if len(result.HIR.Functions) != 1 || len(result.HIR.Functions[0].Blocks) != 1 || len(result.HIR.Functions[0].Blocks[0].Operations) != 1 {
		t.Fatalf("resolved-symbol HIR = %#v", result.HIR)
	}
	if got := result.HIR.Functions[0].Blocks[0].Operations[0].Operands; !slices.Equal(got, []bingo.ValueID{1, 2}) {
		t.Fatalf("resolved-symbol operands = %v", got)
	}
}

func TestReplaySerializedChooseProducesVerifiedPhase2HIR(t *testing.T) {
	snapshot := buildReplayChooseSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)
	serialized, err := snapshot.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	first, err := ReplaySerializedSnapshot(serialized, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplaySerializedSnapshot(serialized, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("serialized choose replay is not deterministic")
	}
	if err := bingo.VerifyCanonicalPhase2HIR(first.HIR); err != nil {
		t.Fatalf("verify choose HIR: %v", err)
	}
	function := first.HIR.Functions[0]
	if function.Name != "choose" || function.ReturnType != bingo.TypeNumber || len(function.Parameters) != 3 || len(function.Blocks) != 3 {
		t.Fatalf("choose HIR function = %#v", function)
	}
	if function.Parameters[0].Type != bingo.TypeBoolean || function.Parameters[1].Type != bingo.TypeNumber || function.Parameters[2].Type != bingo.TypeNumber {
		t.Fatalf("choose parameters = %#v", function.Parameters)
	}
	entry := function.Blocks[0].Terminator
	if entry.Kind != "condbranch" || entry.Value != 1 || !slices.Equal(entry.Successors, []bingo.BlockID{2, 3}) {
		t.Fatalf("choose entry = %#v", entry)
	}
	if function.Blocks[1].Terminator.Value != 2 || function.Blocks[2].Terminator.Value != 3 {
		t.Fatalf("choose returns = %#v / %#v", function.Blocks[1].Terminator, function.Blocks[2].Terminator)
	}
	wantEvents := []string{"function.begin", "parameter", "parameter", "parameter", "if.condition", "return", "return", "function.end"}
	gotEvents := make([]string, len(first.Events))
	for index, event := range first.Events {
		gotEvents[index] = event.Kind
	}
	if !slices.Equal(gotEvents, wantEvents) {
		t.Fatalf("choose evaluation events = %v, want %v", gotEvents, wantEvents)
	}
}

func TestReplaySerializedLoopProducesVerifiedPhase2HIR(t *testing.T) {
	snapshot := buildReplayLoopSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)
	serialized, err := snapshot.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	result, err := ReplaySerializedSnapshot(serialized, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := bingo.VerifyCanonicalPhase2HIR(result.HIR); err != nil {
		t.Fatalf("verify loop HIR: %v", err)
	}
	function := result.HIR.Functions[0]
	if function.Name != "compute" || len(function.Blocks) != 4 {
		t.Fatalf("loop HIR function = %#v", function)
	}
	phi := function.Blocks[1].Operations[0]
	comparison := function.Blocks[1].Operations[1]
	if phi.Kind != "phi" || !slices.Equal(phi.Operands, []bingo.ValueID{1, 5}) || !slices.Equal(phi.IncomingBlocks, []bingo.BlockID{1, 3}) || comparison.Kind != "compare" || comparison.Operator != "<" || comparison.Type != bingo.TypeBoolean {
		t.Fatalf("loop header operations = %#v", function.Blocks[1].Operations)
	}
	if body := function.Blocks[2]; body.Operations[0].ID != 5 || body.Terminator.Kind != "branch" || !slices.Equal(body.Terminator.Successors, []bingo.BlockID{2}) {
		t.Fatalf("loop body = %#v", body)
	}
	wantEvents := []string{"function.begin", "parameter", "parameter", "local.bind", "while.condition", "phi", "binary.add", "local.assign", "return", "function.end"}
	gotEvents := make([]string, len(result.Events))
	for index, event := range result.Events {
		gotEvents[index] = event.Kind
	}
	if !slices.Equal(gotEvents, wantEvents) {
		t.Fatalf("loop evaluation events = %v, want %v", gotEvents, wantEvents)
	}
}

func TestReplayLoopRejectsRehashedConditionBindingTampering(t *testing.T) {
	base := buildReplayLoopSnapshot(t)
	identity := testCompilerIdentity(t, *base)
	broken := cloneReplaySnapshot(t, base)
	whileIndex := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindWhileStatement })
	if whileIndex < 0 {
		t.Fatal("loop snapshot has no while statement")
	}
	nodes := make(map[NodeID]int, len(broken.Nodes))
	for index, node := range broken.Nodes {
		nodes[node.ID] = index
	}
	condition := broken.Nodes[nodes[childByRole(broken.Nodes[whileIndex], "child[0]")]]
	leftIndex := nodes[childByRole(condition, "left")]
	rightIndex := nodes[childByRole(condition, "right")]
	broken.Nodes[rightIndex].Symbol = broken.Nodes[leftIndex].Symbol
	broken.Nodes[rightIndex].ResolvedSymbol = broken.Nodes[leftIndex].ResolvedSymbol
	if err := finalizeTestSnapshot(&broken); err != nil {
		t.Fatal(err)
	}
	serialized, err := broken.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	result, err := ReplaySerializedSnapshot(serialized, identity)
	if err == nil || !strings.Contains(err.Error(), "limit is not the other number parameter") {
		t.Fatalf("rehashed loop binding tamper result/error = %#v / %v", result, err)
	}
	if len(result.Events) != 0 || len(result.HIR.Functions) != 0 {
		t.Fatalf("rejected loop tamper emitted partial HIR: %#v", result)
	}
}

func TestReplayRejectsRehashedChooseSourceTampering(t *testing.T) {
	base := buildReplayChooseSnapshot(t)
	identity := testCompilerIdentity(t, *base)
	tests := []struct {
		name   string
		mutate func(*ProgramSnapshot)
	}{
		{name: "if condition child role", mutate: func(snapshot *ProgramSnapshot) {
			index := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindIfStatement })
			for childIndex := range snapshot.Nodes[index].NamedChildren {
				if snapshot.Nodes[index].NamedChildren[childIndex].Role == "child[0]" {
					snapshot.Nodes[index].NamedChildren[childIndex].Role = "condition"
					return
				}
			}
			t.Fatal("choose if statement has no condition child")
		}},
		{name: "condition type", mutate: func(snapshot *ProgramSnapshot) {
			nodes := make(map[NodeID]NodeSnapshot, len(snapshot.Nodes))
			for _, node := range snapshot.Nodes {
				nodes[node.ID] = node
			}
			ifIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindIfStatement })
			conditionID := childByRole(snapshot.Nodes[ifIndex], "child[0]")
			conditionIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.ID == conditionID })
			leftIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool {
				return node.Kind == snapshotKindParameter && childText(node, "name", nodes) == "left"
			})
			if conditionIndex < 0 || leftIndex < 0 {
				t.Fatal("choose condition or left parameter is missing")
			}
			numberType := nodeTypeID(snapshot.Nodes[leftIndex])
			snapshot.Nodes[conditionIndex].DeclaredType = numberType
			snapshot.Nodes[conditionIndex].NarrowedType = numberType
			snapshot.Nodes[conditionIndex].ContextualType = 0
			snapshot.Nodes[conditionIndex].Flow = tsfrontend.FlowFactSnapshot{}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broken := cloneReplaySnapshot(t, base)
			test.mutate(&broken)
			if err := finalizeTestSnapshot(&broken); err != nil {
				t.Fatal(err)
			}
			result, err := ReplaySnapshot(broken, identity)
			if err == nil {
				t.Fatal("rehashed choose source tamper was accepted")
			}
			if len(result.Events) != 0 || len(result.HIR.Functions) != 0 {
				t.Fatalf("rejected choose tamper emitted partial HIR: %#v", result)
			}
		})
	}
}

func TestReplaySnapshotFailsClosedForUnboundKind(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)
	copy := *snapshot
	copy.Nodes = slices.Clone(snapshot.Nodes)

	index := slices.IndexFunc(copy.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindPlusToken })
	if index < 0 {
		t.Fatal("add snapshot has no plus token")
	}
	copy.Nodes[index].Kind = "KindForStatement"
	copy.Nodes[index].KindValue = 249
	copy.Nodes[index].SyntaxPayload.Tag = "KindForStatement"
	if err := finalizeTestSnapshot(&copy); err != nil {
		t.Fatal(err)
	}
	serialized, err := copy.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	result, err := ReplaySerializedSnapshot(serialized, identity)
	if err == nil || !strings.Contains(err.Error(), `lowerer for Kind "KindForStatement" is not bound`) {
		t.Fatalf("unbound lowerer result/error = %#v / %v", result, err)
	}
	if len(result.Events) != 0 || len(result.HIR.Functions) != 0 {
		t.Fatalf("unbound Kind emitted partial HIR: %#v", result)
	}
}

func TestReplaySnapshotRejectsMultipleReturns(t *testing.T) {
	request, frontend := replayTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"allowUnreachableCode":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function add(left: number, right: number): number { return left + right; return left + right; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	identity := testCompilerIdentity(t, *snapshot)
	serialized, err := snapshot.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReplaySerializedSnapshot(serialized, identity); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("multiple return replay error = %v", err)
	}
}

func TestReplaySnapshotRejectsMultipleFunctionsInsteadOfInventingExecutionOrder(t *testing.T) {
	request, frontend := replayTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts": `
export function add(left: number, right: number): number { return left + right; }
export function sum(left: number, right: number): number { return left + right; }
`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	identity := testCompilerIdentity(t, *snapshot)
	serialized, err := snapshot.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReplaySerializedSnapshot(serialized, identity); err == nil || !strings.Contains(err.Error(), "exactly one exported function") {
		t.Fatalf("multiple function replay error = %v", err)
	}
}

func TestSnapshotLowererReadinessRegistryIsSortedAndBound(t *testing.T) {
	if err := validateSnapshotLowererReadinessRegistry(snapshotLowererReadinessRegistry); err != nil {
		t.Fatal(err)
	}
	for _, definition := range snapshotLowererReadinessRegistry {
		got, ok := lookupSnapshotLowererReadiness(definition.Kind)
		if !ok || got.Kind != definition.Kind || got.Handle == nil {
			t.Fatalf("lowerer lookup for %q = %#v / %v", definition.Kind, got, ok)
		}
	}
	unbound := slices.Clone(snapshotLowererReadinessRegistry)
	unbound[0].Handle = nil
	if err := validateSnapshotLowererReadinessRegistry(unbound); err == nil || !strings.Contains(err.Error(), "unbound") {
		t.Fatalf("unbound registry error = %v", err)
	}
}

func TestPrimitiveReadinessRequiresRegisteredSemanticFacts(t *testing.T) {
	t.Parallel()
	snapshot := buildReplayAddSnapshot(t)
	identity := testCompilerIdentity(t, *snapshot)

	t.Run("binary type", func(t *testing.T) {
		broken := cloneReplaySnapshot(t, snapshot)
		index := slices.IndexFunc(broken.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindBinaryExpression })
		broken.Nodes[index].DeclaredType = 0
		broken.Nodes[index].NarrowedType = 0
		broken.Nodes[index].ContextualType = 0
		broken.Nodes[index].Flow = tsfrontend.FlowFactSnapshot{}
		if err := finalizeTestSnapshot(&broken); err != nil {
			t.Fatal(err)
		}
		if _, err := ReplaySnapshot(broken, identity); err == nil || !strings.Contains(err.Error(), `required fact "type"`) {
			t.Fatalf("binary semantic readiness error = %v", err)
		}
	})

	t.Run("implementation signature", func(t *testing.T) {
		broken := cloneReplaySnapshot(t, snapshot)
		function := replayFunctionNode(t, &broken, "add")
		index := replaySignatureIndex(t, &broken, function.ID)
		broken.Signatures[index].Declaration = ""
		if err := finalizeTestSnapshot(&broken); err != nil {
			t.Fatal(err)
		}
		if _, err := ReplaySnapshot(broken, identity); err == nil || !strings.Contains(err.Error(), "exactly one implementation signature") {
			t.Fatalf("function signature readiness error = %v", err)
		}
	})

	t.Run("incomplete effect", func(t *testing.T) {
		broken := cloneReplaySnapshot(t, snapshot)
		function := replayFunctionNode(t, &broken, "add")
		index := replaySignatureIndex(t, &broken, function.ID)
		broken.Signatures[index].EffectProof.Complete = false
		broken.Signatures[index].Effects = []string{"unknown"}
		if err := finalizeTestSnapshot(&broken); err != nil {
			t.Fatal(err)
		}
		if _, err := ReplaySnapshot(broken, identity); err == nil || !strings.Contains(err.Error(), "effect proof completeness mismatch") {
			t.Fatalf("effect readiness error = %v", err)
		}
	})

	unbound := slices.Clone(snapshotLowererReadinessRegistry)
	unbound[0].RequiredFacts = []string{"missing-fact"}
	if err := validateSnapshotLowererReadinessRegistry(unbound); err == nil || !strings.Contains(err.Error(), "unbound semantic fact") {
		t.Fatalf("unbound semantic fact registry error = %v", err)
	}
}

func TestReplayRejectsRehashedPrimitiveFactCorruption(t *testing.T) {
	base := buildReplayAddSnapshot(t)
	identity := testCompilerIdentity(t, *base)
	tests := []struct {
		name   string
		mutate func(*ProgramSnapshot)
	}{
		{name: "bogus plus", mutate: func(snapshot *ProgramSnapshot) {
			index := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindBinaryExpression })
			snapshot.Nodes[index].SyntaxPayload.Operator = "KindPlusLikeToken"
		}},
		{name: "object scalar containing number", mutate: func(snapshot *ProgramSnapshot) {
			binary := snapshot.Nodes[slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindBinaryExpression })]
			typeID := nodeTypeID(binary)
			index := slices.IndexFunc(snapshot.Types, func(value TypeSnapshot) bool { return value.ID == typeID })
			snapshot.Types[index].Kind = "object"
			snapshot.Types[index].Flags = 1 << 20
			snapshot.Types[index].ObjectFlags = 16
			snapshot.Types[index].TypePayload.Tag = "object"
			snapshot.Types[index].TypePayload.Scalar = "object|1048576|16|||intrinsic:number"
		}},
		{name: "missing operand symbol", mutate: func(snapshot *ProgramSnapshot) {
			index := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool {
				return node.Kind == snapshotKindIdentifier && node.Parent != "" && node.Symbol != ""
			})
			snapshot.Nodes[index].Symbol = "symbol_missing"
			snapshot.Nodes[index].ResolvedSymbol = "symbol_missing"
		}},
		{name: "missing operand type", mutate: func(snapshot *ProgramSnapshot) {
			index := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindBinaryExpression })
			snapshot.Nodes[index].DeclaredType = TypeID(999999)
			snapshot.Nodes[index].NarrowedType = TypeID(999999)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broken := cloneReplaySnapshot(t, base)
			test.mutate(&broken)
			if err := finalizeTestSnapshot(&broken); err != nil {
				t.Fatal(err)
			}
			if _, err := ReplaySnapshot(broken, identity); err == nil {
				t.Fatal("rehashed primitive fact corruption was accepted")
			}
		})
	}
}

func TestReplayRejectsRehashedSubsetGateTamperingAcrossAllWrappers(t *testing.T) {
	base := buildReplayAddSnapshot(t)
	identity := testCompilerIdentity(t, *base)
	tests := []struct {
		name   string
		mutate func(*ProgramSnapshot)
	}{
		{name: "using node flag", mutate: func(snapshot *ProgramSnapshot) {
			index := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindPlusToken })
			snapshot.Nodes[index].NodeFlags |= 1 << 2
		}},
		{name: "async modifier", mutate: func(snapshot *ProgramSnapshot) {
			index := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindPlusToken })
			snapshot.Nodes[index].ModifierBits |= 1 << 10
		}},
		{name: "decorator modifier", mutate: func(snapshot *ProgramSnapshot) {
			index := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindPlusToken })
			snapshot.Nodes[index].ModifierBits |= 1 << 15
		}},
		{name: "latent any contextual type", mutate: func(snapshot *ProgramSnapshot) {
			attachPrimitiveContextType(snapshot, "any", 1, strings.Repeat("9", 64))
		}},
		{name: "latent unknown contextual type", mutate: func(snapshot *ProgramSnapshot) {
			attachPrimitiveContextType(snapshot, "unknown", 2, strings.Repeat("a", 64))
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broken := cloneReplaySnapshot(t, base)
			test.mutate(&broken)
			if err := finalizeTestSnapshot(&broken); err != nil {
				t.Fatal(err)
			}
			if err := frontendwire.ValidateProgramSnapshot(broken); err != nil {
				t.Fatalf("wire validator unexpectedly rejected the rehashed tamper: %v", err)
			}
			if diagnostics := tsfrontend.RunSubsetGate(broken); !tsfrontend.DiagnosticsHaveErrors(diagnostics) {
				t.Fatalf("source subset gate accepted rehashed tamper: %#v", diagnostics)
			}

			serialized, err := broken.CanonicalBytes()
			if err != nil {
				t.Fatal(err)
			}
			frontend, err := tsfrontend.NewFrontendSnapshot(broken)
			if err != nil {
				t.Fatal(err)
			}
			wrapped, err := frontend.CanonicalBytes()
			if err != nil {
				t.Fatal(err)
			}

			paths := []struct {
				name string
				run  func() (SnapshotReplayResult, error)
			}{
				{name: "in-memory", run: func() (SnapshotReplayResult, error) { return ReplaySnapshot(broken, identity) }},
				{name: "serialized", run: func() (SnapshotReplayResult, error) { return ReplaySerializedSnapshot(serialized, identity) }},
				{name: "frontend-wrapper", run: func() (SnapshotReplayResult, error) { return ReplayFrontendSnapshot(wrapped, identity) }},
			}
			for _, path := range paths {
				t.Run(path.name, func(t *testing.T) {
					result, err := path.run()
					if err == nil {
						t.Fatal("rehashed subset-gate tamper was accepted")
					}
					if len(result.Events) != 0 || len(result.HIR.Functions) != 0 {
						t.Fatalf("rejected tamper emitted partial HIR: %#v", result)
					}
				})
			}
		})
	}
}

func attachPrimitiveContextType(snapshot *ProgramSnapshot, kind string, flags uint32, canonicalHash string) {
	var id TypeID
	for _, record := range snapshot.Types {
		if record.ID > id {
			id = record.ID
		}
	}
	id++
	snapshot.Types = append(snapshot.Types, TypeSnapshot{
		ID:            id,
		CanonicalHash: canonicalHash,
		Kind:          kind,
		Flags:         flags,
		TypePayload: frontendwire.TypePayload{
			Tag:    kind,
			Scalar: kind,
		},
	})
	index := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindBinaryExpression })
	snapshot.Nodes[index].ContextualType = id
	snapshot.Nodes[index].Flow.ContextualTypeHash = canonicalHash
}

func TestReplayRejectsNonNumberAddFixture(t *testing.T) {
	request, frontend := replayTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function add(left: string, right: string): string { return left + right; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	identity := testCompilerIdentity(t, *snapshot)
	if _, err := ReplaySnapshot(*snapshot, identity); err == nil {
		t.Fatal("string add fixture was accepted as primitive number add")
	}
}

func buildReplayAddSnapshot(t *testing.T) *ProgramSnapshot {
	t.Helper()
	request, frontend := replayTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function add(left: number, right: number): number { return left + right; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil {
		t.Fatalf("snapshot is nil: %#v", diagnostics)
	}
	if tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("build diagnostics: %#v", diagnostics)
	}
	nodes := make(map[NodeID]NodeSnapshot, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[node.ID] = node
	}
	symbols := make(map[SymbolID]SymbolSnapshot, len(snapshot.Symbols))
	for _, symbol := range snapshot.Symbols {
		symbols[symbol.ID] = symbol
	}
	types := make(map[TypeID]TypeSnapshot, len(snapshot.Types))
	for _, typ := range snapshot.Types {
		types[typ.ID] = typ
	}
	signatures := make(map[SignatureID]SignatureSnapshot, len(snapshot.Signatures))
	for _, signature := range snapshot.Signatures {
		signatures[signature.ID] = signature
	}
	functionIndex := slices.IndexFunc(snapshot.Nodes, func(node NodeSnapshot) bool { return node.Kind == snapshotKindFunctionDeclaration })
	if functionIndex < 0 {
		t.Fatal("add snapshot has no function declaration")
	}
	returnType, resolved := resolveFunctionReturnType(snapshot.Nodes[functionIndex], nodes, symbols, types, signatures)
	if !resolved {
		t.Fatal("function return type did not resolve through its signature")
	}
	if got, err := bingoType(returnType, types); err != nil || got != bingo.TypeNumber {
		t.Fatalf("resolved return type = %q / %v", got, err)
	}
	return snapshot
}

func buildReplayChooseSnapshot(t *testing.T) *ProgramSnapshot {
	t.Helper()
	request, frontend := replayTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function choose(flag: boolean, left: number, right: number): number { if (flag) { return left; } return right; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	return snapshot
}

func buildReplayLoopSnapshot(t *testing.T) *ProgramSnapshot {
	t.Helper()
	request, frontend := replayTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function compute(step: number, limit: number): number { let value = step; while (value < limit) { value = value + step; } return value; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("loop snapshot diagnostics = %#v", diagnostics)
	}
	return snapshot
}

func buildReplayCoalesceAssignSnapshot(t *testing.T) *ProgramSnapshot {
	t.Helper()
	request, frontend := replayTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export function coalesceAssign(value: number | null | undefined, fallback: number): number { value ??= fallback; return value; }`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("coalesce assignment snapshot diagnostics = %#v", diagnostics)
	}
	return snapshot
}

func replayTestRequest(files map[string]string) (tsfrontend.BuildRequest, *tsfrontend.Frontend) {
	fs := vfstest.FromMap(files, true)
	frontend := tsfrontend.NewFrontend(bundled.WrapFS(fs), bundled.LibPath(), tsfrontend.TypeScriptGoCommit, tsfrontend.StandardLibraryHash)
	return tsfrontend.BuildRequest{
		ConfigPath:       "/project/tsconfig.json",
		CurrentDirectory: "/project",
		FileSystem:       fs,
	}, frontend
}

func cloneReplaySnapshot(t *testing.T, snapshot *ProgramSnapshot) ProgramSnapshot {
	t.Helper()
	encoded, err := jsonx.Marshal(snapshot, jsonx.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	var clone ProgramSnapshot
	if err := jsonx.Unmarshal(encoded, &clone, jsonx.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	return clone
}

func replayFunctionNode(t *testing.T, snapshot *ProgramSnapshot, name string) NodeSnapshot {
	t.Helper()
	nodes := make(map[NodeID]NodeSnapshot, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[node.ID] = node
	}
	for _, node := range snapshot.Nodes {
		if node.Kind == snapshotKindFunctionDeclaration && childText(node, "name", nodes) == name {
			return node
		}
	}
	t.Fatalf("function %q is missing", name)
	return NodeSnapshot{}
}

func replaySignatureIndex(t *testing.T, snapshot *ProgramSnapshot, declaration NodeID) int {
	t.Helper()
	for index, signature := range snapshot.Signatures {
		if signature.Declaration == declaration && signature.EffectProof.Implementation == declaration {
			return index
		}
	}
	t.Fatalf("implementation signature for %q is missing", declaration)
	return -1
}

func finalizeTestSnapshot(snapshot *ProgramSnapshot) error {
	snapshot.ContentHash = ""
	encoded, err := jsonx.Marshal(snapshot, jsonx.Deterministic(true))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	snapshot.ContentHash = hex.EncodeToString(digest[:])
	return nil
}

func isDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
