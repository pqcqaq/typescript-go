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
	primitiveSourceTypePlanSchemaVersion uint32 = 1
	primitiveTypedHIRSchemaVersion       uint32 = 2
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
	Function            NodeID          `json:"function"`
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
	if len(execution.Dumps) != len(primitiveHIRPassPrefix) || execution.State.Schema != "hir-v2" {
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
	if err := requirePrimitivePass(spec, iteration, bingo.PassValidateSnapshot, "snapshot-v2", "source-type-plan-v1"); err != nil {
		return err
	}
	snapshot, err := frontendwire.DecodeProgramSnapshot(state.Artifact)
	if err != nil {
		return err
	}
	return validateCompilerIdentityForSnapshot(identity, *snapshot)
}

func runPrimitiveSnapshotPass(_ context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState, identity bingo.CompilerBuildIdentity) (bingo.PassResult, error) {
	if err := requirePrimitivePass(spec, iteration, bingo.PassValidateSnapshot, "snapshot-v2", "source-type-plan-v1"); err != nil {
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
	if err := requirePrimitivePass(spec, iteration, bingo.PassValidateSnapshot, "snapshot-v2", "source-type-plan-v1"); err != nil {
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
	if err := requirePrimitivePass(spec, iteration, bingo.PassTypedHIR, "source-type-plan-v1", "hir-v2"); err != nil {
		return err
	}
	plan, err := decodePrimitiveSourceTypePlan(state.Artifact)
	if err != nil {
		return err
	}
	return validateCompilerIdentityForSnapshot(identity, plan.Snapshot)
}

func runPrimitiveTypedHIRPass(_ context.Context, spec bingo.PassSpec, iteration int, state bingo.PassState, identity bingo.CompilerBuildIdentity) (bingo.PassResult, error) {
	if err := requirePrimitivePass(spec, iteration, bingo.PassTypedHIR, "source-type-plan-v1", "hir-v2"); err != nil {
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
	if err := requirePrimitivePass(spec, iteration, bingo.PassTypedHIR, "source-type-plan-v1", "hir-v2"); err != nil {
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
	functions := make([]NodeID, 0, 1)
	for _, node := range snapshot.Nodes {
		if node.SyntaxPayload.Tag == snapshotKindFunctionDeclaration {
			functions = append(functions, node.ID)
		}
	}
	if len(functions) != 1 {
		return primitiveSourceTypePlan{}, fmt.Errorf("primitive replay requires exactly one lowerable function, got %d", len(functions))
	}
	return primitiveSourceTypePlan{
		SchemaVersion:       primitiveSourceTypePlanSchemaVersion,
		SnapshotContentHash: snapshot.ContentHash,
		Function:            functions[0],
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
	if plan.Function != expected.Function {
		return fmt.Errorf("primitive source type plan function is %q, want %q", plan.Function, expected.Function)
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
	functionNode, ok := indexes.Nodes[plan.Function]
	if !ok {
		return primitiveTypedHIRArtifact{}, fmt.Errorf("primitive source type plan references missing function %q", plan.Function)
	}
	function, events, err := replayFunction(1, functionNode, indexes.Nodes, indexes.Types, indexes.Symbols, indexes.Signatures)
	if err != nil {
		return primitiveTypedHIRArtifact{}, err
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
		Functions:                     []bingo.HIRFunction{function},
	}
	_, hirHash, err := bingo.CanonicalHIR(hir)
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
	if err := bingo.VerifyCanonicalHIR(artifact.HIR); err != nil {
		return fmt.Errorf("verify canonical primitive HIR: %w", err)
	}
	indexes := indexPrimitiveSnapshot(plan.Snapshot)
	functionNode, ok := indexes.Nodes[plan.Function]
	if !ok {
		return fmt.Errorf("primitive source type plan references missing function %q", plan.Function)
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
	if len(function.Parameters) != len(parameterIDs) || len(parameterIDs) != 2 {
		return fmt.Errorf("primitive HIR parameter count does not match source plan")
	}
	parameterValues := make(map[SymbolID]bingo.ValueID, len(parameterIDs)*2)
	expectedEvents := []LoweringEvent{{Kind: "function.begin", Node: functionNode.ID, Origin: functionNode.Origin}}
	for index, parameterID := range parameterIDs {
		parameterNode := indexes.Nodes[parameterID]
		parameterTypeID := nodeTypeID(parameterNode)
		parameterType, typeErr := bingoType(parameterTypeID, indexes.Types)
		if typeErr != nil || parameterType != bingo.TypeNumber {
			return fmt.Errorf("primitive source parameter %q is not canonical number", parameterID)
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
