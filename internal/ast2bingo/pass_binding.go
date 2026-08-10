package ast2bingo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

const (
	primitiveSourceTypePlanSchemaVersion uint32 = 2
	primitiveTypedHIRSchemaVersion       uint32 = 5
)

// PrimitiveTypedHIRSchemaVersion is the wire version consumed by target-aware
// replay. It is kept alongside the private pass implementation constants so a
// downstream checker-free verifier cannot guess a schema number.
const PrimitiveTypedHIRSchemaVersion = primitiveTypedHIRSchemaVersion

var primitiveHIRPassPrefix = []bingo.PassID{
	bingo.PassValidateSnapshot,
	bingo.PassTypedHIR,
}

// primitiveSourceTypePlan is the narrow, target-independent plan supported by
// the first add(number, number) slice. Keeping the validated snapshot in the
// artifact makes the next handler deterministic and closure-free.
type primitiveSourceTypePlan struct {
	SchemaVersion       uint32          `json:"schemaVersion"`
	SnapshotContentHash string          `json:"snapshotContentHash"`
	Functions           []NodeID        `json:"functions"`
	Snapshot            ProgramSnapshot `json:"snapshot"`
}

// primitiveTypedHIRArtifact is the terminal production artifact for Phase
// 1.5. No MIR or target representation is claimed by this boundary.
type primitiveTypedHIRArtifact struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Events                []LoweringEvent             `json:"events"`
	HIR                   bingo.HIRModule             `json:"hir"`
}

// PrimitiveTypedHIRArtifact is the verified checker-free wire consumed by the
// target-aware first-slice lowering pipeline.
type PrimitiveTypedHIRArtifact = primitiveTypedHIRArtifact

func executePrimitiveHIRPasses(ctx context.Context, snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (primitiveTypedHIRArtifact, bingo.PassExecution, error) {
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return primitiveTypedHIRArtifact{}, bingo.PassExecution{}, err
	}
	if err := bingo.ValidatePassPrefix(primitiveHIRPassPrefix, bingo.PassTypedHIR); err != nil {
		return primitiveTypedHIRArtifact{}, bingo.PassExecution{}, fmt.Errorf("invalid primitive pass prefix: %w", err)
	}
	executor, err := bingo.NewPassExecutorThrough(primitiveHIRPassHandlers(identity), bingo.PassTypedHIR, 0)
	if err != nil {
		return primitiveTypedHIRArtifact{}, bingo.PassExecution{}, fmt.Errorf("bind primitive pass handlers: %w", err)
	}
	encoded, err := snapshot.CanonicalBytes()
	if err != nil {
		return primitiveTypedHIRArtifact{}, bingo.PassExecution{}, fmt.Errorf("encode primitive snapshot artifact: %w", err)
	}
	initial := bingo.PassState{
		Schema:   "snapshot-v2",
		Facts:    []string{"syntax", "types", "symbols", "signatures", "module-graph"},
		Artifact: encoded,
	}
	execution, err := executor.ExecuteThrough(ctx, initial, bingo.PassTypedHIR)
	if err != nil {
		return primitiveTypedHIRArtifact{}, execution, err
	}
	if len(execution.Dumps) != len(primitiveHIRPassPrefix) || execution.State.Schema != "hir-v5" {
		return primitiveTypedHIRArtifact{}, execution, fmt.Errorf(
			"primitive pass prefix ended with %d dumps and schema %q",
			len(execution.Dumps), execution.State.Schema,
		)
	}
	artifact, err := decodePrimitiveTypedHIRArtifact(execution.State.Artifact)
	if err != nil {
		return primitiveTypedHIRArtifact{}, execution, fmt.Errorf("decode terminal typed HIR artifact: %w", err)
	}
	return artifact, execution, nil
}

func primitiveHIRPassHandlers(identity bingo.CompilerBuildIdentity) map[bingo.PassID]bingo.PassHandler {
	return map[bingo.PassID]bingo.PassHandler{
		bingo.PassValidateSnapshot: {
			PreVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState) error {
				return preVerifyPrimitiveSnapshotPass(ctx, spec, iteration, state, identity)
			},
			Run: func(ctx context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState) (bingo.PassResult, error) {
				return runPrimitiveSnapshotPass(ctx, spec, iteration, state, identity)
			},
			PostVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState) (bingo.PassVerification, error) {
				return postVerifyPrimitiveSnapshotPass(ctx, spec, iteration, input, output, identity)
			},
		},
		bingo.PassTypedHIR: {
			PreVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState) error {
				return preVerifyPrimitiveTypedHIRPass(ctx, spec, iteration, state, identity)
			},
			Run: func(ctx context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState) (bingo.PassResult, error) {
				return runPrimitiveTypedHIRPass(ctx, spec, iteration, state, identity)
			},
			PostVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState) (bingo.PassVerification, error) {
				return postVerifyPrimitiveTypedHIRPass(ctx, spec, iteration, input, output, identity)
			},
		},
	}
}

// PrimitiveHIRPassHandlers returns the production handlers for the verified
// snapshot-to-HIR prefix. Callers may combine this exact prefix with later
// checker-free target passes, but cannot omit either validation stage.
func PrimitiveHIRPassHandlers(identity bingo.CompilerBuildIdentity) map[bingo.PassID]bingo.PassHandler {
	return primitiveHIRPassHandlers(identity)
}

func preVerifyPrimitiveSnapshotPass(_ context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState, identity bingo.CompilerBuildIdentity) error {
	if err := requirePrimitivePass(spec, iteration, bingo.PassValidateSnapshot, "snapshot-v2", "source-type-plan-v2"); err != nil {
		return err
	}
	snapshot, err := frontendwire.DecodeProgramSnapshot(state.Artifact)
	if err != nil {
		return err
	}
	return validateCompilerIdentityForSnapshot(identity, *snapshot)
}

func runPrimitiveSnapshotPass(_ context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState, identity bingo.CompilerBuildIdentity) (bingo.PassResult, error) {
	if err := requirePrimitivePass(spec, iteration, bingo.PassValidateSnapshot, "snapshot-v2", "source-type-plan-v2"); err != nil {
		return bingo.PassResult{}, err
	}
	snapshot, err := frontendwire.DecodeProgramSnapshot(state.Artifact)
	if err != nil {
		return bingo.PassResult{}, err
	}
	if err := validateCompilerIdentityForSnapshot(identity, *snapshot); err != nil {
		return bingo.PassResult{}, err
	}
	plan, err := buildPrimitiveSourceTypePlan(*snapshot)
	if err != nil {
		return bingo.PassResult{}, err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return bingo.PassResult{}, fmt.Errorf("encode primitive source type plan: %w", err)
	}
	return bingo.PassResult{State: advancePrimitivePassState(state, spec, encoded)}, nil
}

func postVerifyPrimitiveSnapshotPass(_ context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState, identity bingo.CompilerBuildIdentity) (bingo.PassVerification, error) {
	if err := requirePrimitivePass(spec, iteration, bingo.PassValidateSnapshot, "snapshot-v2", "source-type-plan-v2"); err != nil {
		return bingo.PassVerification{}, err
	}
	snapshot, err := frontendwire.DecodeProgramSnapshot(input.Artifact)
	if err != nil {
		return bingo.PassVerification{}, err
	}
	plan, err := decodePrimitiveSourceTypePlan(output.Artifact)
	if err != nil {
		return bingo.PassVerification{}, err
	}
	if err := validateCompilerIdentityForSnapshot(identity, *snapshot); err != nil {
		return bingo.PassVerification{}, err
	}
	if err := verifyPrimitiveSourceTypePlan(*snapshot, plan); err != nil {
		return bingo.PassVerification{}, err
	}
	return bingo.PassVerification{}, nil
}

func preVerifyPrimitiveTypedHIRPass(_ context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState, identity bingo.CompilerBuildIdentity) error {
	if err := requirePrimitivePass(spec, iteration, bingo.PassTypedHIR, "source-type-plan-v2", "hir-v5"); err != nil {
		return err
	}
	plan, err := decodePrimitiveSourceTypePlan(state.Artifact)
	if err != nil {
		return err
	}
	return validateCompilerIdentityForSnapshot(identity, plan.Snapshot)
}

func runPrimitiveTypedHIRPass(_ context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState, identity bingo.CompilerBuildIdentity) (bingo.PassResult, error) {
	if err := requirePrimitivePass(spec, iteration, bingo.PassTypedHIR, "source-type-plan-v2", "hir-v5"); err != nil {
		return bingo.PassResult{}, err
	}
	plan, err := decodePrimitiveSourceTypePlan(state.Artifact)
	if err != nil {
		return bingo.PassResult{}, err
	}
	if err := validateCompilerIdentityForSnapshot(identity, plan.Snapshot); err != nil {
		return bingo.PassResult{}, err
	}
	artifact, err := lowerPrimitiveSourceTypePlan(plan, identity)
	if err != nil {
		return bingo.PassResult{}, err
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return bingo.PassResult{}, fmt.Errorf("encode primitive typed HIR artifact: %w", err)
	}
	return bingo.PassResult{State: advancePrimitivePassState(state, spec, encoded)}, nil
}

func postVerifyPrimitiveTypedHIRPass(_ context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState, identity bingo.CompilerBuildIdentity) (bingo.PassVerification, error) {
	if err := requirePrimitivePass(spec, iteration, bingo.PassTypedHIR, "source-type-plan-v2", "hir-v5"); err != nil {
		return bingo.PassVerification{}, err
	}
	plan, err := decodePrimitiveSourceTypePlan(input.Artifact)
	if err != nil {
		return bingo.PassVerification{}, err
	}
	artifact, err := decodePrimitiveTypedHIRArtifact(output.Artifact)
	if err != nil {
		return bingo.PassVerification{}, err
	}
	if err := verifyPrimitiveTypedHIRArtifact(plan, artifact, identity); err != nil {
		return bingo.PassVerification{}, err
	}
	return bingo.PassVerification{}, nil
}

func requirePrimitivePass(spec bingo.PassSpec, iteration int, id bingo.PassID, inputSchema, outputSchema string) error {
	if spec.ID != id || spec.InputSchema != inputSchema || spec.OutputSchema != outputSchema {
		return fmt.Errorf("unexpected primitive pass contract %q (%s -> %s)", spec.ID, spec.InputSchema, spec.OutputSchema)
	}
	if iteration != 1 {
		return fmt.Errorf("primitive pass %q cannot run iteration %d", id, iteration)
	}
	return nil
}

func advancePrimitivePassState(state bingo.PassState, spec bingo.PassSpec, artifact []byte) bingo.PassState {
	state.Schema = spec.OutputSchema
	state.Artifact = artifact
	state.Facts = slices.Clone(state.Facts)
	for _, fact := range spec.WritesFacts {
		if !slices.Contains(state.Facts, fact) {
			state.Facts = append(state.Facts, fact)
		}
	}
	return state
}

func buildPrimitiveSourceTypePlan(snapshot ProgramSnapshot) (primitiveSourceTypePlan, error) {
	if err := frontendwire.ValidateProgramSnapshot(snapshot); err != nil {
		return primitiveSourceTypePlan{}, err
	}
	if frontendwire.DiagnosticsHaveErrors(snapshot.Diagnostics) {
		return primitiveSourceTypePlan{}, fmt.Errorf("snapshot contains error diagnostics")
	}
	indexes := indexPrimitiveSnapshot(snapshot)
	if err := validatePrimitiveSubsetContract(snapshot, indexes); err != nil {
		return primitiveSourceTypePlan{}, err
	}
	if err := preflightSnapshotLowerers(snapshot, indexes); err != nil {
		return primitiveSourceTypePlan{}, err
	}
	functions := make([]NodeID, 0, 2)
	for _, node := range snapshot.Nodes {
		if node.SyntaxPayload.Tag == snapshotKindFunctionDeclaration {
			functions = append(functions, node.ID)
		}
	}
	if len(functions) == 0 || len(functions) > 2 {
		return primitiveSourceTypePlan{}, fmt.Errorf("primitive replay requires one or two lowerable functions, got %d", len(functions))
	}
	slices.SortFunc(functions, func(left, right NodeID) int {
		leftNode, rightNode := indexes.Nodes[left], indexes.Nodes[right]
		if leftNode.Span.Start < rightNode.Span.Start {
			return -1
		}
		if leftNode.Span.Start > rightNode.Span.Start {
			return 1
		}
		return strings.Compare(string(left), string(right))
	})
	exported := 0
	for _, id := range functions {
		if indexes.Nodes[id].ModifierBits == snapshotModifierExport {
			exported++
		}
	}
	if exported != 1 {
		return primitiveSourceTypePlan{}, fmt.Errorf("primitive replay requires exactly one exported function, got %d", exported)
	}
	return primitiveSourceTypePlan{
		SchemaVersion:       primitiveSourceTypePlanSchemaVersion,
		SnapshotContentHash: snapshot.ContentHash,
		Functions:           functions,
		Snapshot:            snapshot,
	}, nil
}

func verifyPrimitiveSourceTypePlan(input ProgramSnapshot, plan primitiveSourceTypePlan) error {
	if plan.SchemaVersion != primitiveSourceTypePlanSchemaVersion {
		return fmt.Errorf("unsupported primitive source type plan schema %d", plan.SchemaVersion)
	}
	if plan.SnapshotContentHash == "" || plan.SnapshotContentHash != input.ContentHash || plan.Snapshot.ContentHash != input.ContentHash {
		return fmt.Errorf("primitive source type plan is not bound to its input snapshot")
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return err
	}
	planJSON, err := json.Marshal(plan.Snapshot)
	if err != nil {
		return err
	}
	if !bytes.Equal(inputJSON, planJSON) {
		return fmt.Errorf("primitive source type plan changed its input snapshot")
	}
	expected, err := buildPrimitiveSourceTypePlan(input)
	if err != nil {
		return err
	}
	if !slices.Equal(plan.Functions, expected.Functions) {
		return fmt.Errorf("primitive source type plan functions are %v, want %v", plan.Functions, expected.Functions)
	}
	return nil
}

func lowerPrimitiveSourceTypePlan(plan primitiveSourceTypePlan, identity bingo.CompilerBuildIdentity) (primitiveTypedHIRArtifact, error) {
	if err := validateCompilerIdentityForSnapshot(identity, plan.Snapshot); err != nil {
		return primitiveTypedHIRArtifact{}, err
	}
	if err := verifyPrimitiveSourceTypePlan(plan.Snapshot, plan); err != nil {
		return primitiveTypedHIRArtifact{}, err
	}
	indexes := indexPrimitiveSnapshot(plan.Snapshot)
	functionIDs := make(map[NodeID]bingo.FunctionID, len(plan.Functions))
	for index, id := range plan.Functions {
		functionIDs[id] = bingo.FunctionID(index + 1)
	}
	functions := make([]bingo.HIRFunction, 0, len(plan.Functions))
	events := make([]LoweringEvent, 0)
	for index, id := range plan.Functions {
		functionNode, ok := indexes.Nodes[id]
		if !ok {
			return primitiveTypedHIRArtifact{}, fmt.Errorf("primitive source type plan references missing function %q", id)
		}
		function, functionEvents, err := replayFunction(index+1, functionNode, indexes.Nodes, indexes.Types, indexes.Symbols, indexes.Signatures, functionIDs)
		if err != nil {
			return primitiveTypedHIRArtifact{}, err
		}
		function.Exported = functionNode.ModifierBits == snapshotModifierExport
		functions = append(functions, function)
		events = append(events, functionEvents...)
	}
	requirements := make([]bingo.RuntimeCapabilityID, 0)
	requirementsDigest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return primitiveTypedHIRArtifact{}, err
	}
	hir := bingo.HIRModule{
		SchemaVersion:                 bingo.HIRSchemaVersion,
		Provenance:                    primitiveHIRProvenance(plan.Snapshot, identity, requirementsDigest),
		LogicalCapabilityRequirements: requirements,
		Functions:                     functions,
	}
	_, hirHash, err := canonicalPrimitiveHIR(hir)
	if err != nil {
		return primitiveTypedHIRArtifact{}, err
	}
	hir.ContentHash = hirHash
	return primitiveTypedHIRArtifact{
		SchemaVersion:         primitiveTypedHIRSchemaVersion,
		FrontendSnapshotHash:  plan.SnapshotContentHash,
		CompilerBuildIdentity: identity,
		Events:                events,
		HIR:                   hir,
	}, nil
}

func verifyPrimitiveTypedHIRArtifact(plan primitiveSourceTypePlan, artifact primitiveTypedHIRArtifact, identity bingo.CompilerBuildIdentity) error {
	if err := validateCompilerIdentityForSnapshot(identity, plan.Snapshot); err != nil {
		return err
	}
	if err := verifyPrimitiveSourceTypePlan(plan.Snapshot, plan); err != nil {
		return err
	}
	if artifact.SchemaVersion != primitiveTypedHIRSchemaVersion {
		return fmt.Errorf("unsupported primitive typed HIR artifact schema %d", artifact.SchemaVersion)
	}
	if artifact.CompilerBuildIdentity != identity {
		return fmt.Errorf("primitive HIR compiler identity does not match driver input")
	}
	requirementsDigest, err := bingo.LogicalCapabilityRequirementsDigest(artifact.HIR.LogicalCapabilityRequirements)
	if err != nil {
		return fmt.Errorf("primitive HIR logical capability requirements: %w", err)
	}
	if artifact.FrontendSnapshotHash != plan.SnapshotContentHash ||
		artifact.HIR.Provenance != primitiveHIRProvenance(plan.Snapshot, identity, requirementsDigest) {
		return fmt.Errorf("primitive HIR provenance does not match its source plan")
	}
	if len(artifact.HIR.LogicalCapabilityRequirements) != 0 {
		return fmt.Errorf("first-slice HIR does not bind runtime capabilities %v", artifact.HIR.LogicalCapabilityRequirements)
	}
	if err := verifyCanonicalPrimitiveHIR(artifact.HIR); err != nil {
		return fmt.Errorf("verify canonical primitive HIR: %w", err)
	}
	if len(plan.Functions) > 1 {
		expected, err := lowerPrimitiveSourceTypePlan(plan, identity)
		if err != nil {
			return fmt.Errorf("rebuild multi-function HIR from source plan: %w", err)
		}
		expectedJSON, expectedErr := json.Marshal(expected)
		actualJSON, actualErr := json.Marshal(artifact)
		if expectedErr != nil || actualErr != nil || !bytes.Equal(expectedJSON, actualJSON) {
			return fmt.Errorf("multi-function HIR or evaluation-order events do not match source plan")
		}
		return nil
	}
	indexes := indexPrimitiveSnapshot(plan.Snapshot)
	if len(plan.Functions) != 1 {
		return fmt.Errorf("primitive source type plan has no function")
	}
	functionNode, ok := indexes.Nodes[plan.Functions[0]]
	if !ok {
		return fmt.Errorf("primitive source type plan references missing function %q", plan.Functions[0])
	}
	if len(artifact.HIR.Functions) != 1 {
		return fmt.Errorf("primitive HIR has %d functions, want 1", len(artifact.HIR.Functions))
	}
	function := artifact.HIR.Functions[0]
	expectedName := childText(functionNode, "name", indexes.Nodes)
	if function.ID != 1 || function.Name != expectedName || function.Origin != originOf(functionNode) {
		return fmt.Errorf("primitive HIR function identity does not match source plan")
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, indexes.Nodes, indexes.Symbols, indexes.Types, indexes.Signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, indexes.Nodes)
	}
	returnType, err := bingoType(returnTypeID, indexes.Types)
	if err != nil || returnType != bingo.TypeNumber || function.ReturnType != returnType {
		return fmt.Errorf("primitive HIR return type does not match canonical number source type")
	}

	parameterIDs := namedChildren(functionNode, "parameter[")
	if len(function.Parameters) != len(parameterIDs) || len(parameterIDs) < 2 || len(parameterIDs) > 3 {
		return fmt.Errorf("primitive HIR parameter count does not match source plan")
	}
	parameterValues := make(map[SymbolID]bingo.ValueID, len(parameterIDs)*2)
	expectedEvents := []LoweringEvent{{Kind: "function.begin", Node: functionNode.ID, Origin: functionNode.Origin}}
	for index, parameterID := range parameterIDs {
		parameterNode := indexes.Nodes[parameterID]
		parameterTypeID := nodeTypeID(parameterNode)
		parameterType, typeErr := bingoType(parameterTypeID, indexes.Types)
		if typeErr != nil || (parameterType != bingo.TypeNumber && parameterType != bingo.TypeBoolean && parameterType != bingo.TypeNullableNumber) {
			return fmt.Errorf("primitive source parameter %q is not canonical number, boolean, or nullable number", parameterID)
		}
		expected := bingo.HIRParameter{
			Name:   childText(parameterNode, "name", indexes.Nodes),
			Value:  bingo.ValueID(index + 1),
			Type:   parameterType,
			Origin: originOf(parameterNode),
		}
		if function.Parameters[index] != expected {
			return fmt.Errorf("primitive HIR parameter %d does not match source plan", index)
		}
		for _, symbol := range parameterSymbolIDs(parameterNode, indexes.Nodes) {
			parameterValues[symbol] = expected.Value
		}
		expectedEvents = append(expectedEvents, LoweringEvent{Kind: "parameter", Node: parameterNode.ID, Origin: parameterNode.Origin, Type: parameterTypeID})
	}

	bodyID := childByRole(functionNode, "body")
	loop, isLoop, loopErr := findPrimitiveLoop(bodyID, indexes.Nodes)
	if loopErr != nil {
		return fmt.Errorf("primitive loop source: %w", loopErr)
	}
	if isLoop {
		expectedFunction, expectedLoopEvents, err := replayLoopFunction(
			bingo.HIRFunction{ID: function.ID, Name: expectedName, Exported: function.Exported, Parameters: slices.Clone(function.Parameters), Origin: originOf(functionNode)},
			expectedEvents,
			loop,
			parameterValues,
			func() map[bingo.ValueID]bingo.TypeKind {
				result := make(map[bingo.ValueID]bingo.TypeKind, len(function.Parameters))
				for _, parameter := range function.Parameters {
					result[parameter.Value] = parameter.Type
				}
				return result
			}(),
			functionNode,
			indexes.Nodes,
			indexes.Types,
			indexes.Symbols,
			indexes.Signatures,
		)
		if err != nil {
			return fmt.Errorf("rebuild primitive loop HIR: %w", err)
		}
		expectedJSON, expectedErr := json.Marshal(expectedFunction)
		actualJSON, actualErr := json.Marshal(function)
		if expectedErr != nil || actualErr != nil || !bytes.Equal(expectedJSON, actualJSON) || !equalLoweringEvents(artifact.Events, expectedLoopEvents) {
			return fmt.Errorf("primitive loop HIR or evaluation-order events do not match source plan")
		}
		return nil
	}
	coalesceAssign, isCoalesceAssign, coalesceAssignErr := findPrimitiveCoalesceAssign(bodyID, indexes.Nodes)
	if coalesceAssignErr != nil {
		return fmt.Errorf("primitive coalesce assignment source: %w", coalesceAssignErr)
	}
	if isCoalesceAssign {
		expectedFunction, expectedEvents, err := replayCoalesceAssignFunction(
			bingo.HIRFunction{ID: function.ID, Name: expectedName, Exported: function.Exported, Parameters: slices.Clone(function.Parameters), Origin: originOf(functionNode)},
			expectedEvents,
			coalesceAssign,
			parameterValues,
			func() map[bingo.ValueID]bingo.TypeKind {
				result := make(map[bingo.ValueID]bingo.TypeKind, len(function.Parameters))
				for _, parameter := range function.Parameters {
					result[parameter.Value] = parameter.Type
				}
				return result
			}(),
			functionNode, indexes.Nodes, indexes.Types, indexes.Symbols, indexes.Signatures,
		)
		if err != nil {
			return fmt.Errorf("rebuild primitive coalesce assignment HIR: %w", err)
		}
		expectedJSON, expectedErr := json.Marshal(expectedFunction)
		actualJSON, actualErr := json.Marshal(function)
		if expectedErr != nil || actualErr != nil || !bytes.Equal(expectedJSON, actualJSON) || !equalLoweringEvents(artifact.Events, expectedEvents) {
			return fmt.Errorf("primitive coalesce assignment HIR or evaluation-order events do not match source plan")
		}
		return nil
	}
	coalesce, isCoalesce, coalesceErr := findPrimitiveCoalesce(bodyID, indexes.Nodes)
	if coalesceErr != nil {
		return fmt.Errorf("primitive coalesce source: %w", coalesceErr)
	}
	if isCoalesce {
		expectedFunction, expectedCoalesceEvents, err := replayCoalesceFunction(
			bingo.HIRFunction{ID: function.ID, Name: expectedName, Exported: function.Exported, Parameters: slices.Clone(function.Parameters), Origin: originOf(functionNode)},
			expectedEvents,
			coalesce,
			parameterValues,
			func() map[bingo.ValueID]bingo.TypeKind {
				result := make(map[bingo.ValueID]bingo.TypeKind, len(function.Parameters))
				for _, parameter := range function.Parameters {
					result[parameter.Value] = parameter.Type
				}
				return result
			}(),
			functionNode, indexes.Nodes, indexes.Types, indexes.Symbols, indexes.Signatures,
		)
		if err != nil {
			return fmt.Errorf("rebuild primitive coalesce HIR: %w", err)
		}
		expectedJSON, expectedErr := json.Marshal(expectedFunction)
		actualJSON, actualErr := json.Marshal(function)
		if expectedErr != nil || actualErr != nil || !bytes.Equal(expectedJSON, actualJSON) || !equalLoweringEvents(artifact.Events, expectedCoalesceEvents) {
			return fmt.Errorf("primitive coalesce HIR or evaluation-order events do not match source plan")
		}
		return nil
	}
	choose, isChoose, err := findPrimitiveChoose(bodyID, indexes.Nodes)
	if err != nil {
		return err
	}
	if isChoose {
		return verifyPrimitiveChooseHIR(functionNode, function, artifact.Events, parameterValues, choose, indexes, returnTypeID)
	}
	returnNode, binaryNode, err := findPrimitiveReturn(bodyID, indexes.Nodes)
	if err != nil {
		return err
	}
	inputIDs := namedChildren(binaryNode, "left")
	inputIDs = append(inputIDs, namedChildren(binaryNode, "right")...)
	if len(inputIDs) != 2 {
		return fmt.Errorf("primitive source add has %d inputs, want 2", len(inputIDs))
	}
	binaryTypeID := nodeTypeID(binaryNode)
	binaryType, typeErr := bingoType(binaryTypeID, indexes.Types)
	if typeErr != nil || binaryType != bingo.TypeNumber {
		return fmt.Errorf("primitive source add is not canonical number")
	}
	operands := make([]bingo.ValueID, 0, 2)
	for _, inputID := range inputIDs {
		inputNode := indexes.Nodes[inputID]
		inputType, inputTypeErr := bingoType(nodeTypeID(inputNode), indexes.Types)
		if inputTypeErr != nil || inputType != binaryType {
			return fmt.Errorf("primitive source input %q is not canonical number", inputID)
		}
		value, found := parameterValue(inputNode, parameterValues)
		if !found {
			return fmt.Errorf("primitive source input %q is not a parameter", inputID)
		}
		operands = append(operands, value)
	}
	if len(function.Blocks) != 1 || function.Blocks[0].ID != 1 || len(function.Blocks[0].Operations) != 1 {
		return fmt.Errorf("primitive HIR must contain exactly one block and add operation")
	}
	operation := function.Blocks[0].Operations[0]
	if operation.ID != 3 || operation.Kind != "binary" || operation.Type != bingo.TypeNumber ||
		operation.Operator != "+" || operation.Effect != bingo.EffectPure || operation.Origin != originOf(binaryNode) ||
		operation.LogicalCapabilityRequirements == nil || len(operation.LogicalCapabilityRequirements) != 0 ||
		!slices.Equal(operation.Operands, operands) {
		return fmt.Errorf("primitive HIR add operation does not match source plan")
	}
	terminator := function.Blocks[0].Terminator
	if terminator.Kind != "return" || terminator.Value != operation.ID || len(terminator.Successors) != 0 || terminator.Origin != originOf(returnNode) {
		return fmt.Errorf("primitive HIR return does not match source plan")
	}
	expectedEvents = append(expectedEvents,
		LoweringEvent{Kind: "binary.add", Node: binaryNode.ID, Origin: binaryNode.Origin, Type: binaryTypeID, Operator: "+", Inputs: inputIDs},
		LoweringEvent{Kind: "return", Node: returnNode.ID, Origin: returnNode.Origin, Type: returnTypeID},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
	if !equalLoweringEvents(artifact.Events, expectedEvents) {
		return fmt.Errorf("primitive HIR evaluation-order events do not match source plan")
	}
	return nil
}

func canonicalPrimitiveHIR(hir bingo.HIRModule) ([]byte, string, error) {
	if len(hir.Functions) > 1 || (len(hir.Functions) == 1 && len(hir.Functions[0].Blocks) > 1) {
		return bingo.CanonicalPhase2HIR(hir)
	}
	return bingo.CanonicalHIR(hir)
}

func verifyCanonicalPrimitiveHIR(hir bingo.HIRModule) error {
	if len(hir.Functions) > 1 || (len(hir.Functions) == 1 && len(hir.Functions[0].Blocks) > 1) {
		return bingo.VerifyCanonicalPhase2HIR(hir)
	}
	return bingo.VerifyCanonicalHIR(hir)
}

func verifyPrimitiveChooseHIR(
	functionNode NodeSnapshot,
	function bingo.HIRFunction,
	events []LoweringEvent,
	parameterValues map[SymbolID]bingo.ValueID,
	choose primitiveChooseSource,
	indexes snapshotSemanticFactIndexes,
	returnTypeID TypeID,
) error {
	conditionValue, ok := parameterValue(choose.Condition, parameterValues)
	if !ok {
		return fmt.Errorf("primitive choose condition is not a parameter")
	}
	conditionTypeID := nodeTypeID(choose.Condition)
	conditionType, err := bingoType(conditionTypeID, indexes.Types)
	if err != nil || conditionType != bingo.TypeBoolean {
		return fmt.Errorf("primitive choose condition is not canonical boolean")
	}
	thenValue, ok := parameterValue(choose.ThenValue, parameterValues)
	if !ok {
		return fmt.Errorf("primitive choose then value is not a parameter")
	}
	elseValue, ok := parameterValue(choose.ElseValue, parameterValues)
	if !ok {
		return fmt.Errorf("primitive choose else value is not a parameter")
	}
	if len(function.Blocks) != 3 || function.Blocks[0].ID != 1 || function.Blocks[1].ID != 2 || function.Blocks[2].ID != 3 {
		return fmt.Errorf("primitive choose HIR must contain three canonical blocks")
	}
	for _, block := range function.Blocks {
		if block.Operations == nil || len(block.Operations) != 0 {
			return fmt.Errorf("primitive choose HIR blocks must have explicit empty operations")
		}
	}
	entry := function.Blocks[0].Terminator
	if entry.Kind != "condbranch" || entry.Value != conditionValue || !slices.Equal(entry.Successors, []bingo.BlockID{2, 3}) || entry.Origin != originOf(choose.IfNode) {
		return fmt.Errorf("primitive choose condition branch does not match source plan")
	}
	if then := function.Blocks[1].Terminator; then.Kind != "return" || then.Value != thenValue || len(then.Successors) != 0 || then.Origin != originOf(choose.ThenReturn) {
		return fmt.Errorf("primitive choose true return does not match source plan")
	}
	if otherwise := function.Blocks[2].Terminator; otherwise.Kind != "return" || otherwise.Value != elseValue || len(otherwise.Successors) != 0 || otherwise.Origin != originOf(choose.ElseReturn) {
		return fmt.Errorf("primitive choose false return does not match source plan")
	}
	expectedEvents := make([]LoweringEvent, 0, len(function.Parameters)+5)
	expectedEvents = append(expectedEvents, LoweringEvent{Kind: "function.begin", Node: functionNode.ID, Origin: functionNode.Origin})
	for _, parameterID := range namedChildren(functionNode, "parameter[") {
		parameter := indexes.Nodes[parameterID]
		expectedEvents = append(expectedEvents, LoweringEvent{Kind: "parameter", Node: parameter.ID, Origin: parameter.Origin, Type: nodeTypeID(parameter)})
	}
	expectedEvents = append(expectedEvents,
		LoweringEvent{Kind: "if.condition", Node: choose.IfNode.ID, Origin: choose.IfNode.Origin, Type: conditionTypeID, Inputs: []NodeID{choose.Condition.ID}},
		LoweringEvent{Kind: "return", Node: choose.ThenReturn.ID, Origin: choose.ThenReturn.Origin, Type: nodeTypeID(choose.ThenValue), Inputs: []NodeID{choose.ThenValue.ID}},
		LoweringEvent{Kind: "return", Node: choose.ElseReturn.ID, Origin: choose.ElseReturn.Origin, Type: nodeTypeID(choose.ElseValue), Inputs: []NodeID{choose.ElseValue.ID}},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
	if returnTypeID == 0 || !equalLoweringEvents(events, expectedEvents) {
		return fmt.Errorf("primitive choose HIR evaluation-order events do not match source plan")
	}
	return nil
}

func primitiveHIRProvenance(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity, requirementsDigest string) bingo.HIRProvenance {
	return bingo.HIRProvenance{
		FrontendSnapshotSchemaVersion:       snapshot.SchemaVersion,
		FrontendSnapshotHash:                snapshot.ContentHash,
		SourceContentHash:                   primitiveSourceContentHash(snapshot),
		CompilerBuildIdentity:               identity,
		StandardLibraryHash:                 snapshot.Provenance.StandardLibraryHash,
		KindManifestHash:                    snapshot.Provenance.KindManifestHash,
		LogicalCapabilityRequirementsDigest: requirementsDigest,
	}
}

func validateCompilerIdentityForSnapshot(identity bingo.CompilerBuildIdentity, snapshot ProgramSnapshot) error {
	if err := bingo.ValidateCompilerBuildIdentity(identity); err != nil {
		return fmt.Errorf("invalid compiler build identity: %w", err)
	}
	if identity.UpstreamCommit != snapshot.Provenance.TypeScriptGoCommit {
		return fmt.Errorf(
			"compiler upstream commit %q does not match snapshot commit %q",
			identity.UpstreamCommit,
			snapshot.Provenance.TypeScriptGoCommit,
		)
	}
	return nil
}

func primitiveSourceContentHash(snapshot ProgramSnapshot) string {
	files := slices.Clone(snapshot.Files)
	slices.SortStableFunc(files, func(left, right frontendwire.FileSnapshot) int {
		if order := strings.Compare(string(left.ID), string(right.ID)); order != 0 {
			return order
		}
		return strings.Compare(left.CanonicalPath, right.CanonicalPath)
	})
	digest := sha256.New()
	for _, file := range files {
		_, _ = digest.Write([]byte(file.ID))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(file.CanonicalPath))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(file.ContentHash))
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func indexPrimitiveSnapshot(snapshot ProgramSnapshot) snapshotSemanticFactIndexes {
	indexes := snapshotSemanticFactIndexes{
		Nodes:      make(map[NodeID]NodeSnapshot, len(snapshot.Nodes)),
		Types:      make(map[TypeID]TypeSnapshot, len(snapshot.Types)),
		Symbols:    make(map[SymbolID]SymbolSnapshot, len(snapshot.Symbols)),
		Signatures: make(map[SignatureID]SignatureSnapshot, len(snapshot.Signatures)),
		Modules:    make(map[ModuleID]ModuleSnapshot, len(snapshot.Modules)),
	}
	for _, node := range snapshot.Nodes {
		indexes.Nodes[node.ID] = node
	}
	for _, typ := range snapshot.Types {
		indexes.Types[typ.ID] = typ
	}
	for _, symbol := range snapshot.Symbols {
		indexes.Symbols[symbol.ID] = symbol
	}
	for _, signature := range snapshot.Signatures {
		indexes.Signatures[signature.ID] = signature
	}
	for _, module := range snapshot.Modules {
		indexes.Modules[module.ID] = module
	}
	return indexes
}

func decodePrimitiveSourceTypePlan(raw json.RawMessage) (primitiveSourceTypePlan, error) {
	var plan primitiveSourceTypePlan
	if err := decodeStrictPassArtifact(raw, &plan); err != nil {
		return primitiveSourceTypePlan{}, fmt.Errorf("decode primitive source type plan: %w", err)
	}
	if err := verifyPrimitiveSourceTypePlan(plan.Snapshot, plan); err != nil {
		return primitiveSourceTypePlan{}, err
	}
	return plan, nil
}

func decodePrimitiveTypedHIRArtifact(raw json.RawMessage) (primitiveTypedHIRArtifact, error) {
	var artifact primitiveTypedHIRArtifact
	if err := decodeStrictPassArtifact(raw, &artifact); err != nil {
		return primitiveTypedHIRArtifact{}, fmt.Errorf("decode primitive typed HIR artifact: %w", err)
	}
	return artifact, nil
}

// DecodePrimitiveTypedHIRArtifact strictly decodes the typed-HIR pass wire.
// Target-aware consumers must additionally verify its canonical HIR and bind
// all provenance to the BuildPlan and TargetContext they consume.
func DecodePrimitiveTypedHIRArtifact(raw json.RawMessage) (PrimitiveTypedHIRArtifact, error) {
	return decodePrimitiveTypedHIRArtifact(raw)
}

func decodeStrictPassArtifact(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON contains multiple values")
		}
		return err
	}
	return nil
}

func equalLoweringEvents(left, right []LoweringEvent) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
